package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/judge"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// CalibrationsDir is where a calibration's report is written, relative to the
// vault root. It is committed: the document names the report, and a document
// naming a file nobody can open is a document that cannot be checked.
const CalibrationsDir = "calibrations"

// newJudgeCmd builds the judge command group.
//
// It is `uzushio judge` and not `uzushio calibrate` on purpose: `uzushio task
// calibrate` already exists and re-centres a banded verifier's tolerances on
// the host that runs it. That is a different thing measured on a different
// subject, and two commands called calibrate would be one word for two jobs.
func newJudgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "judge",
		Short: "Measure a judge: its consistency with itself, and its agreement with people",
		Long: "judge builds the corpus a judge is measured on, runs the judge over it, and\n" +
			"records what it found in the vault.\n\n" +
			"A judge that picks one of three answers is an instrument, and an instrument\n" +
			"nobody has checked against a reference is a number generator. Three quantities\n" +
			"are kept apart: how often the judge gives the same answer when the candidates\n" +
			"change places, how often it gives the same answer on another seed, and how far\n" +
			"it agrees with people. Only the last is validity, and a judge can be perfectly\n" +
			"consistent and consistently wrong.",
	}
	cmd.AddCommand(newJudgeCalibrateCmd(), newJudgeStatusCmd(), newJudgeImportMTBenchCmd(),
		newJudgeTrialCmd())
	return cmd
}

// newJudgeStatusCmd builds the reading command.
func newJudgeStatusCmd() *cobra.Command {
	var (
		vault  string
		docdag string
		asOf   string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Say which calibrations bind today, and how long ago validity was measured",
		Long: "status asks the graph which calibrations are in force and prints them, with\n" +
			"the day the last comparison with people closed.\n\n" +
			"A calibration carries force for a fixed number of days after its window and\n" +
			"then stops, without anyone editing anything. That is deliberate: the failure\n" +
			"this guards against is not a wrong measurement but a stale one that nobody\n" +
			"noticed had gone stale, which is the ordinary way an automated gate ends up\n" +
			"resting on a number from last quarter.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := judge.ReadStatus(cmd.Context(), docdag, vault, asOf)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, line := range status.Lines() {
				fmt.Fprintln(out, line)
			}
			if len(status.Warnings) > 0 {
				return &exitError{code: exitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&vault, "vault", ".", "the vault root")
	cmd.Flags().StringVar(&docdag, "docdag", "docdag", "the DocDag binary that answers what binds")
	cmd.Flags().StringVar(&asOf, "as-of", "", "the day to answer for, YYYY-MM-DD (default: today, UTC)")
	return cmd
}

// newJudgeCalibrateCmd builds the measuring command.
func newJudgeCalibrateCmd() *cobra.Command {
	var (
		suitePath     string
		cmoa          string
		config        string
		reruns        int
		labelFiles    []string
		out           string
		vault         string
		parallel      int
		alpha         float64
		minKappa      float64
		maxUnmeasured float64
		asOf          string
		windowFrom    string
		pool          string
		dryRun        bool
		replayDir     string
	)
	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Run a judge over a calibration suite and record what it agreed with",
		Long: "calibrate asks the harness to judge every item of a chat suite, once per seed,\n" +
			"and turns the traces into three coefficients: swap consistency, re-run\n" +
			"consistency, and agreement with the human labels.\n\n" +
			"Every number is reported under a named tie handling. How abstentions and ties\n" +
			"are treated is not a preprocessing detail — the same verdicts scored two\n" +
			"defensible ways give two different estimands — so the handling's name travels\n" +
			"with every coefficient, in the report and in the document.\n\n" +
			"It refuses before spending anything: a suite that is not the chat face, a\n" +
			"harness with no judge command, or a configuration with no judge in it are all\n" +
			"usage failures rather than a run that discovers them halfway through.\n\n" +
			"--replay <report directory> rebuilds report.json and items.jsonl from the run\n" +
			"directories the journal already names, asking no judge anything and writing no\n" +
			"vault document. It is how a report written before a field existed gains it. It\n" +
			"refuses when a recorded run directory is not on disk, and when the numbers it\n" +
			"recomputes are not the ones the report already carries: the document that names\n" +
			"the report is append-only history, and it has to keep matching what it quotes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			errOut := cmd.ErrOrStderr()

			if replayDir != "" {
				return replayCalibration(cmd, replayDir, vault)
			}
			if err := requireFlags(cmd, "suite", "config"); err != nil {
				return &exitError{code: exitUsage, err: err}
			}

			suite, err := judge.LoadSuite(suitePath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if suite.Face != judge.FaceChat {
				return &exitError{code: exitUsage, err: fmt.Errorf(
					"suite %s is face %q; a judge is calibrated on the %q face",
					suite.ID, suite.Face, judge.FaceChat)}
			}
			if err := suite.CheckCandidates(); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := hasJudgeCommand(ctx, cmoa); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			judgeModel, err := judgeModelOf(config)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			labels, err := judge.LoadLabels(labelFiles)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}

			day := asOf
			if day == "" {
				day = time.Now().UTC().Format(vocab.DayLayout)
			}
			if windowFrom == "" {
				windowFrom = day
			}
			seq, target, err := freeSlot(vault, judgeModel, day, out)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			replaced, err := supersededBy(vault, judgeModel, day)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			fmt.Fprintf(errOut, "judging %d item(s) at %d seed(s) each with %s\n",
				len(suite.Tasks), reruns+1, judgeModel)
			if dryRun {
				fmt.Fprintf(errOut, "dry run: nothing spent (would write %s and the vault document)\n", target)
				return nil
			}

			result, err := judge.Calibrate(ctx, judge.Options{
				Suite:    suite,
				Runner:   judge.CMoARunner{Binary: cmoa, Config: config, Log: func(line string) { fmt.Fprintln(errOut, line) }},
				Reruns:   reruns,
				Labels:   labels,
				Judge:    judgeModel,
				Pool:     pool,
				Day:      day,
				Alpha:    alpha,
				MinKappa: minKappa,
				Parallel: parallel,
				// The vault is what a recorded trace path is relative to. It
				// was missing here, so a journal recorded its run directories
				// against the suite instead and `--replay --vault` could not
				// find them again — the paths were relative to a root nothing
				// named.
				Vault:         vault,
				MaxUnmeasured: maxUnmeasured,
			})
			if err != nil {
				return err
			}
			if err := result.Write(target); err != nil {
				return err
			}
			reportPath, err := filepath.Rel(vault, filepath.Join(target, judge.ReportFile))
			if err != nil {
				return err
			}
			document, err := result.Report.Document(windowFrom, filepath.ToSlash(reportPath), seq)
			if err != nil {
				return err
			}
			document.Supersedes = replaced
			written, err := writeDocument(vault, document)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(strings.TrimRight(result.Report.Summary(), "\n"), "\n") {
				fmt.Fprintln(errOut, line)
			}
			fmt.Fprintf(errOut, "wrote: %s\nwrote: %s\n", target, written)
			fmt.Fprintln(cmd.OutOrStdout(), document.ID())
			if result.Report.OverUnmeasuredBudget {
				fmt.Fprintf(errOut,
					"%d of %d item(s) measured nothing, over the %.0f%% this run stands behind\n",
					result.Report.Unmeasured, result.Report.Items, 100*result.Report.MaxUnmeasured)
				return &exitError{code: exitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&suitePath, "suite", "", "the chat calibration suite manifest (required)")
	cmd.Flags().StringVar(&cmoa, "cmoa", "cmoa", "the harness binary that runs the judge")
	cmd.Flags().StringVar(&config, "config", "", "the harness configuration, which names the judge (required)")
	cmd.Flags().IntVar(&reruns, "rerun", 2, "extra seeds per item, for the re-run coefficient")
	cmd.Flags().StringArrayVar(&labelFiles, "labels", nil,
		"a JSONL file of human labels, matched to items by identifier; repeat for more")
	cmd.Flags().StringVar(&out, "out", "",
		"where the report goes (default: <vault>/"+CalibrationsDir+"/<judge>@<day>)")
	cmd.Flags().StringVar(&vault, "vault", ".",
		"the vault the calibration document is written into, "+
			"and the root a replay's recorded run directories are relative to")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "items judged at once")
	cmd.Flags().Float64Var(&alpha, "alpha", judge.DefaultAlpha, "the level every interval is computed at")
	cmd.Flags().Float64Var(&minKappa, "min-kappa", judge.DefaultMinKappa,
		"the agreement with people a judge has to reach to be called calibrated")
	cmd.Flags().Float64Var(&maxUnmeasured, "max-unmeasured", judge.DefaultMaxUnmeasured,
		"the share of items that may fail before the calibration reaches no verdict and exits non-zero")
	cmd.Flags().StringVar(&asOf, "as-of", "", "the day the window closes, YYYY-MM-DD (default: today, UTC)")
	cmd.Flags().StringVar(&windowFrom, "window-from", "",
		"the day the window opens, YYYY-MM-DD (default: the day it closes)")
	cmd.Flags().StringVar(&pool, "pool", doc.PoolExternal,
		"what produced the candidates: a proposer pool, or external for answers read from files")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check everything and run no judge")
	cmd.Flags().StringVar(&replayDir, "replay", "",
		"rebuild this report directory from the run directories it names, judging nothing")
	// --suite and --config are required of a measurement and meaningless to a
	// replay, so the requirement is checked in the command rather than marked
	// on the flags: cobra validates a marked flag before RunE can tell which
	// of the two jobs it was asked for.
	return cmd
}

// requireFlags reports the required flags that were left off.
func requireFlags(cmd *cobra.Command, names ...string) error {
	var missing []string
	for _, name := range names {
		if !cmd.Flags().Changed(name) {
			missing = append(missing, `"`+name+`"`)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(`required flag(s) %s not set`, strings.Join(missing, ", "))
}

// replayCalibration rebuilds a report from the traces it already read.
//
// It writes no vault document, and that is the point rather than an omission.
// A calibration document is the claim somebody made on a day, it is
// append-only history, and a rebuilt report that moved a document would be a
// measurement rewriting its own record.
func replayCalibration(cmd *cobra.Command, dir, vault string) error {
	if err := refuseWithReplay(cmd, "rebuilds a report and judges nothing",
		"suite", "cmoa", "config", "rerun", "labels", "out", "parallel", "alpha",
		"min-kappa", "max-unmeasured", "as-of", "window-from", "pool", "dry-run"); err != nil {
		return &exitError{code: exitUsage, err: err}
	}
	result, err := judge.Replay(judge.ReplayOptions{Dir: dir, Vault: vault})
	if err != nil {
		return &exitError{code: exitUsage, err: err}
	}
	if err := result.Write(dir); err != nil {
		return err
	}
	errOut := cmd.ErrOrStderr()
	for _, line := range strings.Split(strings.TrimRight(result.Report.Summary(), "\n"), "\n") {
		fmt.Fprintln(errOut, line)
	}
	fmt.Fprintf(errOut, "replayed %d item(s) from their traces; no vault document was touched\n",
		result.Report.Items)
	fmt.Fprintf(errOut, "wrote: %s\n", dir)
	fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(dir, judge.ReportFile))
	return nil
}

// hasJudgeCommand asks the harness whether it can judge at all, before a run
// spends an hour finding out that it cannot.
func hasJudgeCommand(ctx context.Context, binary string) error {
	cmd := exec.CommandContext(ctx, binary, "judge", "--help") //nolint:gosec // the caller names the harness
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s has no judge command (%w); a calibration needs a harness that can judge", binary, err)
	}
	return nil
}

// judgeModelOf reads the judge's model slug out of the harness configuration.
// A configuration with no judge in it is a usage failure: there is nothing to
// calibrate, and the model slug is what the document is named after.
func judgeModelOf(name string) (string, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the configuration
	if err != nil {
		return "", err
	}
	var config struct {
		Judge *struct {
			Model string `json:"model"`
		} `json:"judge"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if config.Judge == nil || config.Judge.Model == "" {
		return "", fmt.Errorf("%s declares no judge; there is nothing to calibrate", name)
	}
	slug := slugOf(config.Judge.Model)
	if !vocab.ValidModelSlug(slug) {
		return "", fmt.Errorf("%s names the judge %q, which is not a model slug (want %s)",
			name, config.Judge.Model, "lowercase letters, digits and dots in hyphenated segments")
	}
	return slug, nil
}

// slugOf turns a model name into the slug an identifier can carry. A judge is
// configured by whatever name its server answers to, and that name is often a
// path or carries capitals or underscores; the document is named after the
// model, so the name is normalised once here rather than in every caller.
func slugOf(model string) string {
	slug := strings.ToLower(path.Base(model))
	slug = strings.TrimSuffix(slug, ".gguf")
	var b strings.Builder
	previousHyphen := false
	for _, r := range slug {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.':
			b.WriteRune(r)
			previousHyphen = false
		case !previousHyphen && b.Len() > 0:
			b.WriteByte('-')
			previousHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// freeSlot finds the sequence number and the report directory a new
// calibration takes: the first one whose vault document does not exist yet, so
// two calibrations of one judge on one day do not overwrite each other.
func freeSlot(vault, judgeModel, day, out string) (int, string, error) {
	for seq := range 100 {
		id, err := vocab.CalibrationID(judgeModel, day, seq)
		if err != nil {
			return 0, "", err
		}
		relative, err := vocab.Path(vocab.KindCalibration, id)
		if err != nil {
			return 0, "", err
		}
		if _, err := os.Stat(filepath.Join(vault, filepath.FromSlash(relative))); err == nil {
			continue
		}
		target := out
		switch {
		case target == "":
			target = filepath.Join(vault, CalibrationsDir, path.Base(id))
		case seq > 0:
			// A second calibration of one judge on one day takes a directory
			// of its own even where the caller named one. Two append-only
			// documents pointing at one report is a document whose evidence
			// is another measurement's numbers, and nothing downstream can
			// tell.
			target += "-" + strconv.Itoa(seq)
		}
		return seq, target, nil
	}
	return 0, "", errors.New("a hundred calibrations of one judge in one day is not a measurement")
}

// supersededBy returns the calibration a new one replaces: the most recent
// measurement of the same judge that is still in force on the day.
//
// It reads the documents rather than asking DocDag, because the question is
// narrower than "what binds" — same judge, still in force — and answering it
// here keeps `calibrate` runnable without the engine on the path. What retires
// the old document is the edge this declares plus the projection that reads
// it; the old document itself is never touched, being append-only history.
func supersededBy(vault, judgeModel, day string) ([]doc.Supersession, error) {
	all, err := judge.Calibrations(vault)
	if err != nil {
		return nil, err
	}
	var newest *doc.Calibration
	for i, calibration := range all {
		if calibration.Judge != judgeModel {
			continue
		}
		last, err := calibration.LastDay()
		if err != nil || last < day {
			continue
		}
		if newest == nil || calibration.WindowTo > newest.WindowTo {
			newest = &all[i]
		}
	}
	if newest == nil {
		return nil, nil
	}
	return []doc.Supersession{{Edit: newest.ID(), Reason: vocab.ReasonRemeasured}}, nil
}

// writeDocument puts a calibration in the vault and answers with where it went.
func writeDocument(vault string, document doc.Calibration) (string, error) {
	relative, err := document.Path()
	if err != nil {
		return "", err
	}
	target := filepath.Join(vault, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	body, err := document.Bytes()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, body, 0o644); err != nil { //nolint:gosec // a vault document is world-readable
		return "", err
	}
	return target, nil
}
