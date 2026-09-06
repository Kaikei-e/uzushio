package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

// newJudgeTrialCmd builds the command that runs one experiment card.
//
// It is `judge trial` rather than a second `calibrate` because it answers a
// different question. A calibration says how far a judge agrees with people
// over a whole suite; a trial says whether one change to the judge is worth a
// calibration. The first is the reading a document quotes, the second is the
// reading somebody takes before lunch.
func newJudgeTrialCmd() *cobra.Command {
	var (
		cardPath string
		cmoa     string
		out      string
		resume   bool
		vault    string
		dryRun   bool
	)
	cmd := &cobra.Command{
		Use:   "trial",
		Short: "Compare two judge conditions on a small fixed item set, inside a time box",
		Long: "trial runs one experiment card: two judge conditions over a fixed set of items,\n" +
			"in a fixed order, at one seed, inside a time budget, and writes what it found.\n\n" +
			"It exists because measuring every change over the whole suite makes every\n" +
			"change cost an afternoon, and a change nobody can afford to try is a change\n" +
			"nobody tries. The card is written first — hypothesis, the one thing that\n" +
			"differs, the items, the thresholds, the budget — and echoed into the report, so\n" +
			"a threshold moved after the numbers are in is visible as one.\n\n" +
			"Three refusals keep it honest. A saved run stands in for the base condition\n" +
			"only when its reuse key matches: the item, the candidate texts and their\n" +
			"identifiers, the prompt version, the harness build, the judge's settings, both\n" +
			"seeds and the selection rule. A reused base contributes no wall time, so a\n" +
			"trial that read its base off disk reports no speed ratio rather than comparing\n" +
			"today against another day. And the report suggests a decision without making\n" +
			"one: `decision` is a person's, and nothing here is a statistical confirmation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "card"); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			errOut := cmd.ErrOrStderr()

			card, err := judge.LoadTrialCard(cardPath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := checkConfigs(card); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			suite, err := judge.LoadSuite(card.Path(card.Suite))
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if suite.Face != judge.FaceChat {
				return &exitError{code: exitUsage, err: fmt.Errorf(
					"suite %s is face %q; a judge is tried on the %q face",
					suite.ID, suite.Face, judge.FaceChat)}
			}
			manifests, err := loadManifests(card)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := haveItems(suite, manifests); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			labels, err := judge.LoadLabels(card.PathsOf(card.Labels))
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := hasJudgeCommand(cmd.Context(), cmoa); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if out == "" {
				out = filepath.Join(filepath.Dir(cardPath), "trial-"+card.ID)
			}

			opts := judge.TrialOptions{
				Card: card, Suite: suite, Manifests: manifests, Labels: labels,
				Runner: judge.CMoATrialRunner{
					Binary: cmoa,
					Log:    func(line string) { fmt.Fprintln(errOut, line) },
				},
				Out: out, Resume: resume, Vault: vault,
				Log: func(line string) { fmt.Fprintln(errOut, line) },
			}
			plan := opts.Plan()
			fmt.Fprintf(errOut, "card %s, stage %s: %d step(s) over %d item(s), "+
				"budget %d s, reuse %s\n",
				card.ID, card.Stage, len(plan), items(manifests), card.BudgetSeconds, card.Reuse.Kind)
			if dryRun {
				fmt.Fprintf(errOut, "dry run: nothing spent (would write %s)\n", out)
				return nil
			}

			result, err := judge.Trial(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if err := result.Write(out); err != nil {
				return err
			}
			for _, line := range strings.Split(strings.TrimRight(result.Report.Summary(), "\n"), "\n") {
				fmt.Fprintln(errOut, line)
			}
			fmt.Fprintf(errOut, "wrote: %s\n", out)
			fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(out, judge.TrialReportFile))
			// An interrupted set is not a failure of the command: the trial
			// did its job, said so, and suggested `inconclusive`. What it is
			// not allowed to do is exit zero while the caller believes the set
			// was finished.
			if result.Report.Interrupted > 0 || result.Report.StopReason == judge.StopMalfunction {
				return &exitError{code: exitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cardPath, "card", "", "the experiment card (required)")
	cmd.Flags().StringVar(&cmoa, "cmoa", "cmoa", "the harness binary that runs the judge")
	cmd.Flags().StringVar(&out, "out", "",
		"where the journal and the comparison go (default: trial-<card id> beside the card)")
	cmd.Flags().BoolVar(&resume, "resume", false,
		"continue an interrupted trial: every step already in its journal is skipped")
	cmd.Flags().StringVar(&vault, "vault", ".",
		"the root a recorded trace path is written relative to")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check everything and run no judge")
	return cmd
}

// checkConfigs refuses a card whose conditions are not the files it pinned.
//
// A condition's configuration is a local file that its owner edits between
// experiments. A trial that ran the edited one would be comparing two things
// nobody wrote down, and the failure is silent — which is why the digest is
// checked before anything is spent rather than recorded afterwards.
func checkConfigs(card judge.TrialCard) error {
	for _, condition := range []judge.TrialCondition{card.Base, card.Candidate} {
		name := card.Path(condition.Config)
		digest, err := judge.ConfigDigest(name)
		if err != nil {
			return fmt.Errorf("condition %s: %w", condition.ID, err)
		}
		if condition.ConfigSHA256 == "" {
			continue
		}
		if digest != condition.ConfigSHA256 {
			return fmt.Errorf(
				"condition %s names a configuration hashing to %s; the card pinned %s. "+
					"Either the file changed since the card was written or the card names "+
					"another one; a trial does not guess which",
				condition.ID, short(digest), short(condition.ConfigSHA256))
		}
	}
	return nil
}

func loadManifests(card judge.TrialCard) ([]judge.TrialManifest, error) {
	var out []judge.TrialManifest
	seen := map[string]string{}
	for _, name := range card.Manifests {
		manifest, err := judge.LoadTrialManifest(card.Path(name))
		if err != nil {
			return nil, err
		}
		for _, item := range manifest.Items {
			if where, ok := seen[item.ID]; ok {
				return nil, fmt.Errorf(
					"item %s is in both %s and %s; an item belongs to one set, or the "+
						"set that confirms a change is the set that tuned it",
					item.ID, where, manifest.Set)
			}
			seen[item.ID] = manifest.Set
		}
		out = append(out, manifest)
	}
	return out, nil
}

// haveItems refuses before spending anything when a manifest names an item the
// suite does not hold, or one whose candidate answers are not on disk. Model
// answers are not committed to this repository, so a fresh clone has the
// manifest and none of the text.
func haveItems(suite judge.Suite, manifests []judge.TrialManifest) error {
	known := map[string]judge.Task{}
	for _, task := range suite.Tasks {
		known[task.ID] = task
	}
	for _, manifest := range manifests {
		for _, item := range manifest.Items {
			task, ok := known[item.ID]
			if !ok {
				return fmt.Errorf("%s names item %s, which suite %s does not hold",
					filepath.Base(manifest.Path), item.ID, suite.ID)
			}
			for _, name := range suite.Candidates(task) {
				if _, err := os.Stat(name); err != nil {
					return fmt.Errorf(
						"item %s has no candidate answers (%s missing). Model responses are "+
							"not committed to this repository; fetch them with "+
							"`uzushio judge import-mtbench --candidates-only --out %s`",
						item.ID, filepath.Base(name), suite.Dir)
				}
			}
		}
	}
	return nil
}

func items(manifests []judge.TrialManifest) int {
	n := 0
	for _, manifest := range manifests {
		n += len(manifest.Items)
	}
	return n
}
