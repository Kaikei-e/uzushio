// Package loop measures one candidate harness edit against the harness the
// vault already describes, and writes down what it found.
//
// The shape of the measurement is fixed by what it can afford. Each trial is
// an expensive, noisy Bernoulli outcome; the suite is tens of tasks, not
// thousands; and the effect an edit has is small. Three things follow, and all
// three are load-bearing rather than tuning:
//
//   - Every trial is paired. The same task at the same repeat index and the
//     same seed is run against both harnesses, and the unit of observation is
//     the pair. Pairing is the difference between a usable gate and a useless
//     one at this budget, not a refinement of one.
//   - The test is anytime-valid, so the run may look after every pair and stop
//     the moment a split is decided, or at its cap, with no correction. See
//     internal/stats.
//   - The modal verdict is `inconclusive`, and that is the design working. A
//     ten-point difference is not reliably detectable here. The interval on
//     the pass-rate difference is the honest output; the verdict is a
//     convenience built on top of it.
//
// What the run writes is meant to be enough to re-derive every decision
// offline: one JSONL line per trial, a header naming every parameter and the
// decision rule as a literal string, and one vault document per split. The
// invariant that makes that claim testable is `--replay`: feeding the trials
// back through the decision function must reproduce the stored verdicts.
package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/render"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/surfaces"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ErrRun is the sentinel every failure to make a measurement wraps. It means
// there is no measurement; a measurement that happened and came out badly is a
// verdict, not an error.
var ErrRun = errors.New("run")

// ErrRefused is what a run answers when the thing it was asked to measure is
// not measurable: an edit that is not a proposal, a surface the harness has no
// place for, a split too small for the arithmetic to mean anything. It is its
// own sentinel because the caller answers it with a usage exit code rather
// than with a verdict.
var ErrRefused = errors.New("run: refused")

// SchemaVersion is the version of the run.json and trials.jsonl this package
// writes.
const SchemaVersion = 1

// Mode is how much budget a run spends.
type Mode string

// The two modes.
const (
	// ModeScreening is the nightly gate: three repeats a task, a cap of sixty
	// pairs a split, and a margin of 0.15 — which is what sixty pairs can
	// certify and nothing tighter.
	ModeScreening Mode = "screening"
	// ModeConfirm is the run made of a candidate that has already screened
	// well: five repeats, a cap of a hundred and fifty pairs, and a margin of
	// 0.10.
	ModeConfirm Mode = "confirm"
)

// String returns the mode as the record writes it.
func (m Mode) String() string { return string(m) }

// Repeats is K: how many times each task is run per arm.
func (m Mode) Repeats() int {
	if m == ModeConfirm {
		return 5
	}
	return 3
}

// Params returns the gate's numbers for one split.
//
// The count gate is on the held-out split only, and its threshold is zero: one
// task the edit lost on every one of its repeats is a regression. It is a raw
// count of tasks, independent of the statistics, and it is there because a
// sequential test at this budget will not catch a single catastrophic task
// regression while a count will — and because held-out is where a regression
// means the edit does not generalise, which is the thing the split exists to
// find out.
//
// Zero rather than one, and "lost every repeat" rather than "came out net
// worse", because the looser reading is not a catastrophe detector: at a
// twelve-task split and the discordance the design expects it fires on an edit
// that changes nothing about four times in five, and each firing writes
// `status: rejected` into a vault that will not re-measure it.
func (m Mode) Params(split vocab.Split) stats.Params {
	p := stats.Params{Alpha: 0.05, Margin: 0.15, Cap: 60, Prior: stats.DefaultPrior, CountGate: stats.CountGateOff}
	if m == ModeConfirm {
		p.Margin = 0.10
		p.Cap = 150
	}
	if split == vocab.SplitHeldOut {
		p.CountGate = 0
	}
	return p
}

// Valid reports whether m is one of the two modes.
func (m Mode) Valid() bool { return m == ModeScreening || m == ModeConfirm }

// AAPairs is how many pairs the A/A calibration runs. Thirty is enough to tell
// a fleet that reproduces itself from one that does not, and it is cheap
// enough to run before every measurement rather than once and then trusted.
const AAPairs = 30

// AAWarnAbove is the discordance at which no verdict but `inconclusive` is
// meaningful. Above it the fleet's own nondeterminism is the dominant effect
// and no statistics fix that.
const AAWarnAbove = 0.4

// Options is one run.
type Options struct {
	// Vault is the vault root.
	Vault string
	// Edit is the candidate's identifier.
	Edit string
	// Suite names the suite file.
	Suite string
	// CMoA is the harness binary, Config the harness configuration file.
	CMoA   string
	Config string
	// Mode is screening or confirm.
	Mode Mode
	// Parallel is how many pairs are in flight at once. The statistic is read
	// after each batch, so a larger number buys wall-clock at the cost of
	// overshooting the stopping point by up to that many pairs.
	Parallel int
	// Out is the run directory. The baseline cache lives beside it, at
	// <Out>/../cache, so sibling runs share it.
	Out string
	// AA runs the baseline against itself instead of measuring the edit.
	AA bool
	// DryRun renders, checks and plans, and runs no trials.
	DryRun bool
	// AsOf is the day the baseline is read for. Empty is today.
	AsOf string
	// DocDag is the engine binary.
	DocDag string
	// Runner performs a trial. Nil builds a CMoARunner from CMoA and Config.
	Runner Runner
	// NoCache turns the baseline cache off. The A/A calibration turns it off
	// for itself, because a cache would answer both arms from one draw and
	// report a discordance of zero it never measured.
	NoCache bool
	// Version is uzushio's version, for the record.
	Version string
	// Now is the clock.
	Now func() time.Time
	// Log receives progress lines.
	Log func(string)
}

// Result is what a run concluded.
type Result struct {
	Header  Header
	Splits  map[vocab.Split]stats.Reading
	Rates   map[vocab.Split]Rates
	AA      *AAResult
	Written []string
}

// Rates is what each arm scored on one split, in the ordinary sense of a pass
// rate: trials passed over trials run. The pair statistic cannot answer this —
// a tie is a pair both arms passed or a pair both failed, and those are the
// same pair outcome and very different rates — so it is counted separately.
type Rates struct {
	Pairs    int     `json:"pairs"`
	Baseline float64 `json:"baseline_pass_rate"`
	Edited   float64 `json:"pass_rate"`
}

// AAResult is the calibration: the baseline against itself.
type AAResult struct {
	Pairs       int     `json:"pairs"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Ties        int     `json:"ties"`
	Discordance float64 `json:"discordance"`
	Harness     string  `json:"harness_sha256"`
	Warning     string  `json:"warning,omitempty"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  string  `json:"finished_at"`
}

// Run makes the measurement.
func Run(ctx context.Context, o Options) (Result, error) {
	// The check runs before the defaults, because one of the things it checks
	// is whether the caller supplied a way to run a trial at all, and filling
	// one in would answer its own question.
	if err := o.check(); err != nil {
		return Result{}, err
	}
	o = defaults(o)
	suite, err := LoadSuite(o.Suite)
	if err != nil {
		return Result{}, err
	}
	edit, err := readEdit(o.Vault, o.Edit)
	if err != nil {
		return Result{}, err
	}
	pool, err := poolOf(o.Config)
	if err != nil {
		return Result{}, err
	}
	if err := refuse(edit, suite, pool.IDs); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrRun, err)
	}
	baseline, candidate, err := renders(ctx, o)
	if err != nil {
		// A candidate whose sidecar diff will not apply to the harness the
		// vault describes today is not a failure of the run: it is a fact
		// about the candidate, and the honest record of it is a measurement
		// that could not be taken. A *binding* edit that will not apply is a
		// different thing — the vault and the seed have diverged and a person
		// has to reconcile them — so it stays an error.
		var applyErr *render.ApplyError
		if errors.As(err, &applyErr) && !applyErr.Binding {
			return unmeasurable(o, suite, edit, baseline, pool, applyErr)
		}
		return Result{}, err
	}

	header := Header{
		SchemaVersion:   SchemaVersion,
		UzushioVersion:  o.Version,
		RunID:           filepath.Base(filepath.Clean(o.Out)),
		Edit:            o.Edit,
		EditStatus:      edit.Status.String(),
		Component:       edit.Component,
		Mode:            o.Mode,
		AsOf:            baseline.AsOf,
		Suite:           SuiteRef{ID: suite.ID, Digest: suite.Digest(), Floor: suite.Floor()},
		Repeats:         o.Mode.Repeats(),
		Parallel:        o.Parallel,
		Pool:            poolSlug(pool.IDs),
		Proposers:       pool.IDs,
		FleetSHA256:     pool.Digest,
		Params:          map[string]stats.Params{},
		Baseline:        summarise(baseline),
		Candidate:       summarise(candidate),
		PreRegistration: preRegistration(ctx, o.Vault, edit),
		StartedAt:       o.Now().UTC().Format(time.RFC3339),
	}
	for _, split := range vocab.AllSplits() {
		header.Params[split.String()] = o.Mode.Params(split)
		header.Suite.Sizes = append(header.Suite.Sizes, SplitSize{
			Split: split.String(), Tasks: len(suite.Of(split)),
		})
	}
	header.DecisionRule = stats.Rule(o.Mode.Params(vocab.SplitHeldIn), o.Mode.Params(vocab.SplitHeldOut))

	result := Result{
		Header: header,
		Splits: map[vocab.Split]stats.Reading{},
		Rates:  map[vocab.Split]Rates{},
	}
	if o.DryRun {
		result.Header.DryRun = true
		result.Header.FinishedAt = o.Now().UTC().Format(time.RFC3339)
		if err := writeHeader(o.Out, result.Header); err != nil {
			return Result{}, err
		}
		o.logf("dry run: %d held-in and %d held-out tasks, %d repeats, caps %d/%d; nothing was run",
			len(suite.Of(vocab.SplitHeldIn)), len(suite.Of(vocab.SplitHeldOut)), o.Mode.Repeats(),
			o.Mode.Params(vocab.SplitHeldIn).Cap, o.Mode.Params(vocab.SplitHeldOut).Cap)
		return result, nil
	}

	journal, err := openJournal(filepath.Join(o.Out, TrialsName))
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = journal.Close() }()

	// The calibration is a mode rather than a preamble. It measures the
	// baseline against itself and says nothing whatever about the edit, so
	// there is nothing for the splits to do afterwards and no vault document
	// to write; spending the budget on both would be spending it twice.
	if o.AA {
		aa, err := calibrate(ctx, o, suite, baseline, journal, &result.Header)
		if err != nil {
			return Result{}, err
		}
		result.AA = aa
		result.Header.AADiscordance = &aa.Discordance
		result.Header.AAWarning = aa.Warning
		result.Header.FinishedAt = o.Now().UTC().Format(time.RFC3339)
		result.Header.Transition = Transition{
			Status: edit.Status.String(),
			Reason: "an A/A calibration measures the baseline against itself and says nothing about the edit",
		}
		if err := writeAA(o.Out, *aa); err != nil {
			return Result{}, err
		}
		if err := writeHeader(o.Out, result.Header); err != nil {
			return Result{}, err
		}
		return result, nil
	}

	// Held-in runs to a decision first: a candidate that cannot show a gain
	// there never deserves held-out budget.
	for _, split := range []vocab.Split{vocab.SplitHeldIn, vocab.SplitHeldOut} {
		reading, counts, err := measure(ctx, o, suite, split, baseline, candidate, pool.Digest, journal, &result.Header)
		if err != nil {
			return Result{}, err
		}
		result.Splits[split] = reading
		result.Rates[split] = Rates{
			Pairs:    counts.pairs,
			Baseline: counts.rate(counts.basePass),
			Edited:   counts.rate(counts.editPass),
		}
		result.Header.Verdicts = append(result.Header.Verdicts, SplitVerdict{
			Split: split.String(), Reading: reading,
		})
		o.logf("%s: %s after %d pairs (b=%d c=%d ties=%d, delta in [%+.3f, %+.3f])",
			split, reading.Verdict, reading.Pairs,
			reading.Evidence.Wins, reading.Evidence.Losses, reading.Evidence.Ties,
			reading.Delta.Lo, reading.Delta.Hi)
		if reading.Verdict == vocab.VerdictRegress {
			// A regression on either split settles the edit. Spending the
			// other split's budget would buy a number nobody will read.
			o.logf("%s regressed; the remaining budget is not spent", split)
			break
		}
	}
	result.Header.Promote = stats.Promote(
		verdictOf(result.Splits, vocab.SplitHeldIn), verdictOf(result.Splits, vocab.SplitHeldOut))
	result.Header.FinishedAt = o.Now().UTC().Format(time.RFC3339)

	// The record is written before the vault is touched, and the status
	// transition is the last thing the run does. The failure this orders
	// against is a real one: an edit flipped to accepted or rejected with no
	// run.json and no run document behind it is exactly the
	// accepted_unvalidated / rejected_without_run state the vocabulary exists
	// to forbid, and it is unrecoverable except from the journal. In this
	// order, a failure anywhere leaves the edit proposed and the measurement
	// on disk.
	if err := writeHeader(o.Out, result.Header); err != nil {
		return Result{}, err
	}
	written, err := record(o, suite, edit, &result)
	if err != nil {
		return Result{}, err
	}
	result.Written = written

	transition, err := apply(o, edit, result)
	if err != nil {
		return Result{}, err
	}
	result.Header.Transition = transition
	if err := writeHeader(o.Out, result.Header); err != nil {
		return Result{}, err
	}
	return result, nil
}

// verdictOf reads one split's verdict, answering `inconclusive` for a split
// that never ran — which is what a split nobody measured has concluded.
func verdictOf(splits map[vocab.Split]stats.Reading, split vocab.Split) vocab.Verdict {
	if reading, ok := splits[split]; ok {
		return reading.Verdict
	}
	return vocab.VerdictInconclusive
}

// defaults fills in what the caller left out.
func defaults(o Options) Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Mode == "" {
		o.Mode = ModeScreening
	}
	if o.Parallel < 1 {
		o.Parallel = 1
	}
	if o.Runner == nil {
		o.Runner = CMoARunner{Binary: o.CMoA, Config: o.Config, Log: o.Log}
	}
	return o
}

func (o Options) logf(format string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, a...))
	}
}

// check holds the invocation to what a run needs before it reads anything.
func (o Options) check() error {
	for _, required := range []struct{ what, value string }{
		{"--vault", o.Vault},
		{"--edit", o.Edit},
		{"--suite", o.Suite},
		{"--out", o.Out},
		// The configuration names the fleet, and the fleet is half of what a
		// measurement is about: it keys the baseline cache and it is the model
		// slug in every run document. A run without one is not reproducible
		// and its record would say `pool-unknown`.
		{"--config", o.Config},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("%w: %s is required", ErrRefused, required.what)
		}
	}
	if !o.Mode.Valid() {
		return fmt.Errorf("%w: --mode %q is neither %s nor %s", ErrRefused, o.Mode, ModeScreening, ModeConfirm)
	}
	if !o.DryRun && o.Runner == nil && strings.TrimSpace(o.CMoA) == "" {
		return fmt.Errorf("%w: --cmoa is required unless the run is a dry one", ErrRefused)
	}
	return nil
}

// refuse is the gate before any budget is spent. Every one of these is a state
// somebody can get a vault into, and each of them would otherwise produce a
// number that looks like a measurement and is not one.
func refuse(edit doc.Edit, suite Suite, proposers []string) error {
	if edit.Status != vocab.StatusProposed {
		return fmt.Errorf("%w: edit %s is %s; a run measures a proposal, and a decision that has already been made is not one",
			ErrRefused, edit.EditID, edit.Status)
	}
	if !surfaces.HasInjectionPoint(edit.Component) {
		return fmt.Errorf("%w: edit %s is about %q, which the rendered harness has no injection point for (%v), so there is nothing to measure",
			ErrRefused, edit.EditID, edit.Component, surfaces.Injectable())
	}
	if err := edit.CheckPaths(); err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	// An edit that predicts nothing cannot be wrong about anything, and a run
	// of it can confirm nothing. The prediction is the falsifiable half of the
	// proposal and it is written before the run, not after.
	if len(edit.Predicts) == 0 {
		return fmt.Errorf("%w: edit %s predicts nothing; a run scores predictions against outcomes and there is nothing to score",
			ErrRefused, edit.EditID)
	}
	for _, split := range vocab.AllSplits() {
		if n := len(suite.Of(split)); n < suite.Floor() {
			return fmt.Errorf(
				"%w: the %s split of suite %s holds %d task(s) and the floor is %d; below it one task flipping moves the pass rate more than any effect worth arguing about",
				ErrRefused, split, suite.ID, n, suite.Floor())
		}
	}
	// The run document's identifier carries the pool as a model slug, and a
	// proposer id with an underscore or a capital — an ordinary model-tag
	// shape — does not fit that grammar. Discovering it at the end would waste
	// the whole budget, so it is discovered here.
	if slug := poolSlug(proposers); !vocab.ValidModelSlug(slug) {
		return fmt.Errorf(
			"%w: the proposers make the pool slug %q, which is not the shape a run identifier's model is (%s); rename the proposers in the configuration",
			ErrRefused, slug, "lowercase letters, digits and dots in hyphenated segments")
	}
	return nil
}

// readEdit reads the candidate out of the vault.
func readEdit(vault, id string) (doc.Edit, error) {
	relative, err := vocab.Path(vocab.KindEdit, id)
	if err != nil {
		return doc.Edit{}, fmt.Errorf("%w: %w", ErrRun, err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(relative)))
	if err != nil {
		return doc.Edit{}, fmt.Errorf("%w: %w", ErrRun, err)
	}
	edit, err := doc.ParseEdit(id, body)
	if err != nil {
		return doc.Edit{}, fmt.Errorf("%w: %w", ErrRun, err)
	}
	return edit, nil
}

// renders builds the two harnesses: the baseline is the binding set as of the
// day, the candidate is that plus the edit. Both manifests are kept, because
// a comparison that cannot say what the two sides were is not a comparison.
func renders(ctx context.Context, o Options) (baseline, candidate render.Manifest, err error) {
	base := render.Options{
		Vault: o.Vault, AsOf: o.AsOf, DocDag: o.DocDag, Force: true,
		Out: filepath.Join(o.Out, "harness", "base"),
	}
	baseline, err = render.Render(ctx, base)
	if err != nil {
		return render.Manifest{}, render.Manifest{}, err
	}
	edited := base
	edited.Out = filepath.Join(o.Out, "harness", "edit")
	edited.WithEdits = []string{o.Edit}
	edited.AsOf = baseline.AsOf
	candidate, err = render.Render(ctx, edited)
	if err != nil {
		// The baseline is handed back even so: a candidate whose sidecar will
		// not apply is a measurement that cannot be taken, and the record of
		// that has to say what baseline it could not be taken against.
		return baseline, render.Manifest{}, err
	}
	if baseline.TreeSHA256 == candidate.TreeSHA256 {
		return baseline, candidate, fmt.Errorf(
			"%w: adding edit %s left the rendered harness unchanged (%s); there is no difference to measure",
			ErrRefused, o.Edit, baseline.TreeSHA256)
	}
	return baseline, candidate, nil
}

// preRegistration records where the prediction was written down, so a reader
// can check that it was written before the run rather than after it. An
// uncommitted edit says so rather than pretending to a commit.
func preRegistration(ctx context.Context, vault string, edit doc.Edit) PreRegistration {
	pre := PreRegistration{Commit: "uncommitted"}
	relative, err := edit.Path()
	if err != nil {
		return pre
	}
	pre.EditPath = relative
	cmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%H", "--", relative)
	cmd.Dir = vault
	out, err := cmd.Output()
	if sha := strings.TrimSpace(string(out)); err == nil && sha != "" {
		pre.Commit = sha
	}
	for _, prediction := range edit.Predicts {
		pre.Predicts = append(pre.Predicts, prediction.Pattern+" "+prediction.Expect.String())
	}
	sort.Strings(pre.Predicts)
	return pre
}

// fleet is who answered: the proposer identifiers, and a digest over what they
// were pointed at.
type fleet struct {
	// IDs are the proposer identifiers in configured order. They are what a
	// run document's model slug is built from: the measurement is of a pool of
	// proposers rather than of one model, and naming one of them would be a
	// record that says something the run did not measure.
	IDs []string
	// Digest identifies the pool by what it is rather than by what it is
	// called: identifier, model and endpoint of each proposer, in configured
	// order. It keys the baseline cache, so a swapped model is a cache miss
	// rather than a silent comparison against a baseline this fleet never ran.
	//
	// Nothing secret goes into it. The configuration names environment
	// variables for API keys; it never carries their values, and this reads
	// only the three fields above.
	Digest string
}

// poolOf reads the fleet out of the harness configuration.
func poolOf(config string) (fleet, error) {
	var file struct {
		Proposers []struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			BaseURL string `json:"base_url"`
		} `json:"proposers"`
	}
	if err := readJSON(config, &file); err != nil {
		return fleet{}, err
	}
	if len(file.Proposers) == 0 {
		return fleet{}, fmt.Errorf("%w: %s declares no proposers", ErrRefused, config)
	}
	f := fleet{IDs: make([]string, 0, len(file.Proposers))}
	lines := make([]string, 0, len(file.Proposers))
	for _, proposer := range file.Proposers {
		f.IDs = append(f.IDs, proposer.ID)
		lines = append(lines, proposer.ID+"\t"+proposer.Model+"\t"+proposer.BaseURL)
	}
	f.Digest = digestLines(lines)
	return f, nil
}

// poolSlug is the model slug a run document carries: `pool-` and the proposer
// identifiers, joined by hyphens, in configured order.
func poolSlug(proposers []string) string {
	if len(proposers) == 0 {
		return "pool-unknown"
	}
	return "pool-" + strings.Join(proposers, "-")
}

// ApplyFailedOnBaseline is the reason a candidate could not be measured at
// all: its sidecar diff does not apply to the harness the vault describes
// today, because an edit accepted since the candidate was written has moved
// the text underneath it.
const ApplyFailedOnBaseline = "apply_failed_on_baseline"

// Unmeasurable is what the record says instead of a pass rate.
type Unmeasurable struct {
	Reason string `json:"reason"`
	Edit   string `json:"edit"`
	// Baseline is the tree the diff was tried against, and Edits the ordered
	// edits that built it — which is what a person needs to work out which of
	// them moved the text.
	Baseline string   `json:"baseline_tree_sha256"`
	Edits    []string `json:"baseline_edits"`
	Stderr   string   `json:"stderr"`
}

// unmeasurable writes the record of a candidate that could not be measured.
//
// It writes no pass rate, and that is the point: "not measured" must not enter
// the arithmetic that reads a pass rate later. The run documents carry the
// verdict `inconclusive` and no validates edge at all, so nothing downstream
// can mistake the absence of a number for a number.
//
// This is a deliberate divergence from the research, which says a candidate
// that "fails to produce a valid evaluation result" is regress-by-construction
// (§G.13). That rule is about a candidate the *harness* choked on, which is a
// fact about the candidate. A diff that no longer applies is a fact about the
// vault: an edit accepted since the candidate was written moved the text under
// it, and the candidate may be perfectly good once rebased. Calling that a
// regression would write `status: rejected` into a vault that then refuses to
// re-measure it, and the edit would be dead because somebody else's edit
// landed first. So it is recorded as what it is — nothing was measured — with
// the baseline it could not be applied to and the instruction to rebase.
func unmeasurable(
	o Options,
	suite Suite,
	edit doc.Edit,
	baseline render.Manifest,
	pool fleet,
	applyErr *render.ApplyError,
) (Result, error) {
	now := o.Now().UTC().Format(time.RFC3339)
	header := Header{
		SchemaVersion:  SchemaVersion,
		UzushioVersion: o.Version,
		RunID:          filepath.Base(filepath.Clean(o.Out)),
		Edit:           o.Edit,
		EditStatus:     edit.Status.String(),
		Component:      edit.Component,
		Mode:           o.Mode,
		AsOf:           baseline.AsOf,
		Suite:          SuiteRef{ID: suite.ID, Digest: suite.Digest(), Floor: suite.Floor()},
		Repeats:        o.Mode.Repeats(),
		Parallel:       o.Parallel,
		Pool:           poolSlug(pool.IDs),
		Proposers:      pool.IDs,
		FleetSHA256:    pool.Digest,
		Params:         map[string]stats.Params{},
		Baseline:       summarise(baseline),
		StartedAt:      now,
		FinishedAt:     now,
		Unmeasurable: &Unmeasurable{
			Reason:   ApplyFailedOnBaseline,
			Edit:     applyErr.EditID,
			Baseline: baseline.TreeSHA256,
			Edits:    summarise(baseline).Edits,
			Stderr:   applyErr.Stderr,
		},
	}
	for _, split := range vocab.AllSplits() {
		header.Params[split.String()] = o.Mode.Params(split)
		header.Suite.Sizes = append(header.Suite.Sizes, SplitSize{
			Split: split.String(), Tasks: len(suite.Of(split)),
		})
	}
	header.DecisionRule = stats.Rule(o.Mode.Params(vocab.SplitHeldIn), o.Mode.Params(vocab.SplitHeldOut))
	header.Transition = Transition{
		Status: edit.Status.String(),
		Reason: "the candidate could not be applied to the baseline, so nothing was measured",
		Instruction: fmt.Sprintf(
			"rebase %s onto the harness as of %s and run it again", o.Edit, baseline.AsOf),
	}

	result := Result{
		Header: header,
		Splits: map[vocab.Split]stats.Reading{},
		Rates:  map[vocab.Split]Rates{},
	}
	for _, split := range vocab.AllSplits() {
		reading := o.Mode.Params(split).Read(stats.Evidence{})
		result.Splits[split] = reading
		result.Header.Verdicts = append(result.Header.Verdicts,
			SplitVerdict{Split: split.String(), Reading: reading})
	}
	written, err := recordUnmeasurable(o, suite, edit, &result)
	if err != nil {
		return Result{}, err
	}
	result.Written = written
	if err := writeHeader(o.Out, result.Header); err != nil {
		return Result{}, err
	}
	o.logf("%s could not be applied to the baseline (%s); nothing was measured",
		o.Edit, ApplyFailedOnBaseline)
	return result, nil
}
