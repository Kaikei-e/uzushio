package judge

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// DefaultAlpha is the level every interval in a calibration is computed at.
const DefaultAlpha = 0.05

// DefaultMinKappa is the agreement with people a judge has to reach to be
// called calibrated.
//
// It is Krippendorff's lower threshold — rely on a coefficient at 0.800, draw
// tentative conclusions between 0.667 and 0.800, discard below 0.667 — rather
// than a band from Landis & Koch, whose authors described their own cut-points
// as arbitrary and who supply no argument for them. Krippendorff's is at least
// stated as a rule with a condition attached: raise the floor where the cost
// of a wrong conclusion is high. The number is a flag because that condition
// is the caller's to answer, not this package's.
const DefaultMinKappa = 0.667

// DefaultSeed is the presentation seed the first run of an item is made at.
// The re-runs take the seeds after it.
const DefaultSeed = 1

// DefaultMaxUnmeasured is the share of items a calibration tolerates losing to
// a failing harness before it stops standing behind its numbers.
//
// It is a fraction rather than a count because what it guards is not the
// arithmetic — a lost item is simply absent from every table — but the claim.
// Ten per cent missing is a suite with a bad afternoon; half missing is a
// measurement of whatever was left when the fleet came back, and the items a
// fleet drops are not a random sample of them.
const DefaultMaxUnmeasured = 0.1

// The three references a judge's answers can be scored against.
const (
	// ReferenceGold is the label derived from the corpus's own human
	// comparisons.
	ReferenceGold = "gold"
	// ReferenceLabels is the label a person gave on the labelling page.
	ReferenceLabels = "labels"
	// ReferenceHuman is the one the verdict reads: the person's label where
	// there is one, and the derived label otherwise. It exists because the two
	// sources answer the same question about the same items, and a judge
	// measured against a mixture of them is measured against more items than
	// either alone.
	ReferenceHuman = "human"
	// ReferenceHumanJudged is ReferenceHuman restricted to the items the
	// judge was actually asked about.
	//
	// Since the harness settles a run the candidates agree on without a
	// judge call, `human` has stopped being a coefficient about the judge
	// and become one about the whole selector. Both are worth having and
	// they are not the same claim, so the narrower one is computed beside
	// it: it is the reading that stays comparable with a calibration made
	// before the consensus stage existed.
	ReferenceHumanJudged = "human_judge_decided"
)

// references are the four the report scores, in the order it writes them.
func references() []string {
	return []string{ReferenceGold, ReferenceLabels, ReferenceHuman, ReferenceHumanJudged}
}

// Options are one calibration.
type Options struct {
	// Suite is the items, Runner what runs the judge over them.
	Suite  Suite
	Runner Runner
	// Reruns is how many extra seeds each item is judged at, for the re-run
	// coefficient. Zero measures no re-run reliability and says so.
	Reruns int
	// Labels are the human labels, matched to items by identifier.
	Labels []Label
	// Judge is the model slug of the judge, Pool what produced the
	// candidates. Both go into the document.
	Judge string
	Pool  string
	// Day is the day the window closes, as YYYY-MM-DD.
	Day string
	// RescoredFrom names the calibration whose recorded calls were
	// re-aggregated, empty where the judge was asked, and Rescored is what
	// that calibration concluded. Both travel into the report unchanged.
	RescoredFrom string
	Rescored     *Rescored
	// Recorded answers what the source calibration concluded for one run,
	// so a rescoring can check itself where the two rules must agree. Nil
	// runs no check.
	Recorded func(item string, seed int) (kind, candidate string)
	// Alpha and MinKappa are the level and the threshold. Zero is the default.
	Alpha    float64
	MinKappa float64
	// Vault is the root every path in the report is written relative to.
	// Empty is the working directory.
	Vault string
	// Parallel is how many items are judged at once. Zero is one.
	Parallel int
	// MaxUnmeasured is the share of items that may fail before the
	// calibration refuses to stand behind its numbers. Zero is
	// DefaultMaxUnmeasured. It does not stop the run: the report and the
	// journal are written either way, and what the fraction decides is
	// whether the command exits non-zero and what the document says.
	MaxUnmeasured float64
	// Log receives a line per item.
	Log func(string)
}

// Result is a finished calibration.
type Result struct {
	Report Report
	Items  []ItemResult
}

// ItemResult is one item's line of the journal.
type ItemResult struct {
	Item    string `json:"item"`
	Stratum string `json:"stratum"`
	// Gold and Label are the two human references as kappa categories, and
	// Human is the one the verdict used. An empty string is "no reference".
	Gold  string `json:"gold"`
	Label string `json:"label,omitempty"`
	Human string `json:"human,omitempty"`
	// HumanFrom names which of the two the human reference came from.
	HumanFrom string `json:"human_from,omitempty"`
	// Hard says the corpus's own two readings disagreed about this item.
	Hard bool `json:"hard"`
	// Unmeasured says the item produced no usable answer: the harness could
	// not be run, or its first run timed out or failed. Such an item is in
	// no coefficient, and Error says what happened.
	Unmeasured bool   `json:"unmeasured"`
	Error      string `json:"error,omitempty"`
	// Runs is what the judge answered, one entry per seed.
	Runs []RunResult `json:"runs"`
}

// RunResult is one seed's answer.
type RunResult struct {
	Seed     int    `json:"seed"`
	Outcome  string `json:"outcome"`
	Category string `json:"category"`
	Reason   string `json:"reason,omitempty"`
	// Measured says the run produced a judgement rather than a failure.
	Measured       bool  `json:"measured"`
	SwapConsistent int   `json:"swap_consistent_pairs"`
	InvalidRetries int   `json:"invalid_output_retries"`
	LatencyMS      int64 `json:"latency_ms"`
	// RunID is the trace's own identifier and RunDir where to find it,
	// relative to the vault. Neither is ever an absolute path: this file is
	// committed, and the harness writes its traces wherever its configuration
	// says, which is often outside the repository entirely.
	RunID  string `json:"run_id,omitempty"`
	RunDir string `json:"run_dir,omitempty"`
}

// Calibrate runs the judge over the suite and computes what it found.
func Calibrate(ctx context.Context, opts Options) (Result, error) {
	if opts.Suite.Face != FaceChat {
		return Result{}, fmt.Errorf("%w: suite %s is face %q; a judge is calibrated on the chat face",
			ErrJudge, opts.Suite.ID, opts.Suite.Face)
	}
	if opts.Alpha <= 0 {
		opts.Alpha = DefaultAlpha
	}
	if opts.MinKappa <= 0 {
		opts.MinKappa = DefaultMinKappa
	}
	seeds := make([]int, 0, opts.Reruns+1)
	for i := range opts.Reruns + 1 {
		seeds = append(seeds, DefaultSeed+i)
	}

	if opts.MaxUnmeasured <= 0 {
		opts.MaxUnmeasured = DefaultMaxUnmeasured
	}
	judged, failures, err := runAll(ctx, opts, seeds)
	if err != nil {
		return Result{}, err
	}
	measured, allBad, err := measurements(opts, judged, failures)
	if err != nil {
		return Result{}, err
	}
	return assemble(opts, assembly{
		items:   measured,
		seeds:   len(seeds),
		allBad:  allBad,
		ceiling: humanHuman(opts.Labels, kappaCategories(), opts.Alpha),
	})
}

// kappaCategories is the vocabulary every table over a judge's answer is
// scored on: the three slots and the one abstention.
func kappaCategories() []string { return append(slices.Clone(Positions), Abstain) }

// runAll judges every item at every seed, with the requested number of items
// in flight.
//
// A failing invocation loses its item and nothing else. Two hundred items at
// three seeds is six hundred subprocesses over a couple of hours, and the
// earlier shape — first error wins, nothing written — threw away every
// completed judgement because invocation four hundred and thirty-one exited
// non-zero. What a failure costs now is one item, recorded with its message;
// what it does to the claim is decided afterwards, by the unmeasured fraction.
//
// A cancelled context is the exception. That is the operator stopping the run,
// not the harness misbehaving, and finishing the remaining items to write a
// report nobody asked for is not resilience.
func runAll(ctx context.Context, opts Options, seeds []int) ([][]Judged, []error, error) {
	parallel := max(opts.Parallel, 1)
	out := make([][]Judged, len(opts.Suite.Tasks))
	failures := make([]error, len(opts.Suite.Tasks))

	var wg sync.WaitGroup
	gate := make(chan struct{}, parallel)
	for i, task := range opts.Suite.Tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			for _, seed := range seeds {
				if ctx.Err() != nil {
					return
				}
				run, err := opts.Runner.Judge(ctx, opts.Suite, task, seed)
				if err != nil {
					failures[i] = err
					if opts.Log != nil {
						opts.Log(fmt.Sprintf("%s seed %d: %v", task.ID, seed, err))
					}
					return
				}
				out[i] = append(out[i], run)
			}
			if opts.Log != nil && len(out[i]) > 0 {
				opts.Log(fmt.Sprintf("%s: %s", task.ID, out[i][0].Outcome))
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return out, failures, nil
}

// measurement is one item's identity and what the judge did on it: the two
// halves the arithmetic reads. Where they came from is not its business — a
// fleet answering now, or the traces a fleet left an hour ago and a replay
// reads back — which is the whole reason the split exists.
type measurement struct {
	// item carries the identity, the strata and the human labels. Runs,
	// Unmeasured and the outcome half of Error are assemble's to fill: those
	// are what the runs say rather than what the caller knows.
	item ItemResult
	runs []Judged
}

// assembly is everything the arithmetic needs besides the runs.
//
// The two label-side facts travel in it rather than being recomputed inside,
// because no trace records either: how many labels said none of the three was
// worth choosing, and how far two people agreed with each other. A replay has
// no labels file to read them from and takes them off the report it is
// rebuilding, which is honest — they are the one part of a calibration the
// traces cannot re-derive.
type assembly struct {
	items   []measurement
	seeds   int
	allBad  int
	ceiling *Agreement
}

// measurements resolves every item's human label and pairs it with its runs.
func measurements(opts Options, judged [][]Judged, failures []error) ([]measurement, int, error) {
	labels := byItem(opts.Labels)
	allBad := 0
	out := make([]measurement, 0, len(opts.Suite.Tasks))
	for i, task := range opts.Suite.Tasks {
		gold, hasGold, err := opts.Suite.GoldOf(task)
		if err != nil {
			return nil, 0, err
		}
		item := ItemResult{Item: task.ID, Stratum: stratumOf(task, gold), Hard: gold.Hard}
		if hasGold {
			item.Gold = gold.Category()
		}
		if given := labels[task.ID]; len(given) > 0 {
			item.Label = firstLabel(given)
			for _, label := range given {
				if label.Choice == ChoiceAllBad {
					allBad++
				}
			}
		}
		switch {
		case item.Label != "":
			item.Human, item.HumanFrom = item.Label, ReferenceLabels
		case item.Gold != "":
			item.Human, item.HumanFrom = item.Gold, ReferenceGold
		}
		if failures[i] != nil {
			item.Error = scrub(failures[i].Error(), opts.Vault, opts.Suite.Dir)
		}
		out = append(out, measurement{item: item, runs: judged[i]})
	}
	return out, allBad, nil
}

// assemble turns the runs into the report and the journal.
func assemble(opts Options, a assembly) (Result, error) {
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Suite:         opts.Suite.ID,
		Source:        opts.Suite.Source,
		License:       opts.Suite.License,
		Judge:         opts.Judge,
		Pool:          opts.Pool,
		Day:           opts.Day,
		RescoredFrom:  opts.RescoredFrom,
		Rescored:      opts.Rescored,
		Items:         len(a.items),
		Seeds:         a.seeds,
		AllBadLabels:  a.allBad,
		Alpha:         opts.Alpha,
		Level:         1 - opts.Alpha,
		MinKappa:      opts.MinKappa,
		MaxUnmeasured: opts.MaxUnmeasured,
		Strata:        map[string]int{},
		Outcomes: Outcomes{
			ByKind:              map[string]int{},
			NoCandidateByReason: map[string]int{},
			ByRule:              map[string]int{},
			ByTieBreak:          map[string]int{},
			ByConsensus:         map[string]int{},
		},
	}

	// Every table is clustered by item. One item contributes three pairs per
	// seed to the swap table and one row per later seed to the re-run table,
	// and those rows are not independent of each other: a judge that is
	// confused by one prompt is confused by all three of its pairs at once.
	// The interval is computed over items for that reason.
	swap, err := stats.NewTable([]string{"pair0", "pair1", Abstain})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	categories := kappaCategories()
	rerun, err := stats.NewTable(categories)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	validity := map[string]*validityTables{}
	for _, reference := range references() {
		tables, err := newValidityTables(categories)
		if err != nil {
			return Result{}, err
		}
		validity[reference] = tables
	}

	var latencies []int64
	var flips, decidedPairs []int
	// The position counts are per item for the same reason the flip counts
	// are: the rows are single calls, six of them come from one prompt, and
	// six calls of one item move together.
	var firstChosen, decidedCalls []int
	coverage := newCoverage()
	noCandidateItems, measuredItems := 0, 0
	items := make([]ItemResult, 0, len(a.items))
	for _, measured := range a.items {
		item := measured.item
		runs := measured.runs
		report.Strata[item.Stratum]++

		itemFlips, itemDecided := 0, 0
		itemFirst, itemCalls := 0, 0
		for _, run := range runs {
			item.Runs = append(item.Runs, RunResult{
				Seed: run.Seed, Outcome: run.Outcome, Category: category(run),
				Reason: run.Reason, SwapConsistent: run.SwapConsistent,
				InvalidRetries: run.InvalidRetries, LatencyMS: run.LatencyMS,
				RunID:    filepath.Base(run.RunDir),
				RunDir:   RecordPath(run.RunDir, opts.Vault, opts.Suite.Dir),
				Measured: run.Measured(),
			})
			latencies = append(latencies, run.LatencyMS)
			report.Outcomes.ByKind[run.Outcome]++
			report.Outcomes.Total++
			report.Outcomes.InvalidRetries += run.InvalidRetries
			if rule := run.Rule(); rule != "" {
				report.Outcomes.ByRule[rule]++
			}
			if run.TieBreak != "" {
				report.Outcomes.ByTieBreak[run.TieBreak]++
			}
			if run.Consensus != "" {
				report.Outcomes.ByConsensus[run.Consensus]++
			}
			if run.Measured() && run.Calls() == 0 {
				report.Outcomes.Judgeless++
			}
			checkSweep(opts, report.Rescored, item.Item, run)
			// The abstention table counts the run that measured nothing too:
			// what it is for is what the suite lost, and a run nobody could
			// make is as lost as a run that could decide nothing.
			coverage.observe(run, item.Gold)
			if !run.Measured() {
				report.Outcomes.Unmeasured++
				continue
			}
			if run.Outcome == OutcomeNoCandidate {
				reason := run.Reason
				if reason == "" {
					reason = ReasonUnstated
				}
				report.Outcomes.NoCandidateByReason[reason]++
			}
			for _, pair := range run.Pairs {
				report.Outcomes.Calls += len(pair.Orders)
				// The single call, which is the unit the position bias lives
				// in. The pair below is the unit the swap agreement lives in,
				// and the two denominators are different on purpose.
				for _, order := range pair.Orders {
					if !order.Decided() {
						continue
					}
					itemCalls++
					if order.ChoseFirst() {
						itemFirst++
					}
				}
				if pair.DrawReason == DrawDisagree {
					report.Swap.DisagreeBreakdown.observe(pair)
				}
				if len(pair.Orders) != 2 {
					continue
				}
				report.Swap.Pairs++
				first, second := pair.side(pair.Orders[0]), pair.side(pair.Orders[1])
				if err := swap.ObserveIn(item.Item, first, second); err != nil {
					return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
				}
				if first != Abstain && second != Abstain {
					itemDecided++
					if first != second {
						itemFlips++
					}
				}
			}
		}
		if itemDecided > 0 {
			flips = append(flips, itemFlips)
			decidedPairs = append(decidedPairs, itemDecided)
		}
		if itemCalls > 0 {
			firstChosen = append(firstChosen, itemFirst)
			decidedCalls = append(decidedCalls, itemCalls)
		}
		report.Swap.Decided += itemDecided
		report.Swap.Flips += itemFlips

		// An item whose first run produced no judgement is unmeasured: it is
		// in no coefficient, and it is counted so the claim can be refused.
		if len(runs) == 0 || !runs[0].Measured() {
			item.Unmeasured = true
			if item.Error == "" && len(runs) > 0 {
				item.Error = runs[0].Outcome
			}
			report.Unmeasured++
			items = append(items, item)
			continue
		}
		measuredItems++
		if runs[0].Outcome == OutcomeNoCandidate {
			noCandidateItems++
		}

		// The re-run table reads the first seed against each later one. The
		// first is the reference on purpose: pooling every unordered pair
		// would force the two marginals to be equal, which is the one thing
		// that makes kappa flatter than the data.
		for _, later := range runs[1:] {
			if !later.Measured() {
				continue
			}
			if err := rerun.ObserveIn(item.Item, runs[0].Category(), later.Category()); err != nil {
				return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
			}
		}

		answer := runs[0].Category()
		for _, reference := range references() {
			human := map[string]string{
				ReferenceGold: item.Gold, ReferenceLabels: item.Label,
				ReferenceHuman: item.Human, ReferenceHumanJudged: item.Human,
			}[reference]
			if human == "" {
				continue
			}
			// The restricted reading drops the items no judge answered for.
			// It is the same comparison over fewer items, not a different
			// one, and the count it reports is how many are left.
			if reference == ReferenceHumanJudged && runs[0].Calls() == 0 {
				continue
			}
			if err := validity[reference].observe(item.Item, answer, human); err != nil {
				return Result{}, err
			}
			if reference == ReferenceHuman {
				validity[reference].from[item.HumanFrom]++
			}
		}
		items = append(items, item)
	}

	report.Swap.Rate = stats.ClusteredProportion(flips, decidedPairs, opts.Alpha)
	report.Swap.Position = positionOf(firstChosen, decidedCalls, opts.Alpha)
	report.Swap.Agreement = agreementOf(swap, opts.Alpha)
	report.AbstentionByGold = coverage.report()
	report.Rerun = Rerun{
		Seeds:       a.seeds,
		Comparisons: rerun.N(),
		Agreement:   agreementOf(rerun, opts.Alpha),
	}
	report.Validity = map[string]Validity{}
	for reference, tables := range validity {
		report.Validity[reference] = tables.report(reference, opts.Alpha)
	}
	report.HumanHuman = a.ceiling
	// One row per measured item, so this is the one rate in the report whose
	// rows really are independent and the one that gets a Wilson interval.
	report.Outcomes.NoCandidateRate = stats.IndependentProportion(noCandidateItems, measuredItems, opts.Alpha)
	report.Outcomes.InvalidRetryRate = rate(report.Outcomes.InvalidRetries, report.Outcomes.Calls)
	report.Latency = latency(latencies)
	report.UnmeasuredRate = rate(report.Unmeasured, report.Items)
	report.OverUnmeasuredBudget = report.UnmeasuredRate > opts.MaxUnmeasured

	human := report.Validity[ReferenceHuman]
	report.HumanKappa = human.Primary.Kappa
	report.NHuman = human.Primary.N
	report.Verdict = verdict(human, opts.MinKappa, report.OverUnmeasuredBudget)
	return Result{Report: report, Items: items}, nil
}

// category is a run's kappa category, or the empty string where the run
// measured nothing.
func category(run Judged) string {
	if !run.Measured() {
		return ""
	}
	return run.Category()
}

// agreementOf wraps a table's coefficients with the two things a number quoted
// out of a report needs: the tie handling it was computed under, and how its
// interval was made.
func agreementOf(t *stats.Table, alpha float64) Agreement {
	return Agreement{
		TieHandling:  vocab.TieAbstainAsCategory.String(),
		CIMethod:     stats.MethodClusterJackknife,
		Level:        1 - alpha,
		Coefficients: stats.Agreement(t, alpha),
	}
}

// firstLabel is one item's human label where several people gave one: the
// label of the labeler whose name sorts first.
//
// It is a rule rather than "whichever line came first" because the file is a
// log and its order is an accident of when people clicked. Reordering two
// lines used to move kappa.
func firstLabel(given []Label) string {
	chosen := given[0]
	for _, label := range given[1:] {
		if label.Labeler < chosen.Labeler {
			chosen = label
		}
	}
	return chosen.Category()
}

// verdict reads the calibration word off the validity coefficient. It is the
// primary handling that decides, because that is the one computed over the
// whole sample: letting the secondary decide would let a judge that abstains
// on everything hard call itself calibrated on what is left.
//
// The comparison is against the coefficient as the document publishes it —
// three decimal places — so that a reader cannot find `human_kappa: "0.667"`
// beside `verdict: uncalibrated` against a threshold of 0.667 and conclude the
// document contradicts itself. The rounding is the published fact; the verdict
// follows it.
//
// A run that lost too many items to a failing harness reaches no verdict at
// all. The items a fleet drops are not a random sample of the suite, so what
// is left is a measurement of the wrong thing rather than a smaller
// measurement of the right one.
func verdict(v Validity, minKappa float64, overBudget bool) vocab.Calibrated {
	if overBudget || v.Primary.N == 0 || !v.Primary.Kappa.Defined() {
		return vocab.CalibratedUnmeasured
	}
	if published, err := doc.ParseKappa(doc.Kappa(v.Primary.Kappa.Float())); err == nil &&
		published >= minKappa {
		return vocab.CalibratedYes
	}
	return vocab.CalibratedNo
}

// validityTables holds one reference's two tables: every item under the
// primary handling, and the decided ones under the secondary. `from` counts
// which labeler set each item's human label came from, so a coefficient over a
// mixture says what it is a mixture of.
type validityTables struct {
	primary   *stats.Table
	secondary *stats.Table
	from      map[string]int
}

func newValidityTables(categories []string) (*validityTables, error) {
	primary, err := stats.NewTable(categories)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	secondary, err := stats.NewTable(slices.Clone(Positions))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return &validityTables{primary: primary, secondary: secondary, from: map[string]int{}}, nil
}

// observe records one item. The item is the cluster, which here is also the
// row: validity has one comparison per item, so its interval is the ordinary
// jackknife and the clustering costs nothing.
func (v *validityTables) observe(item, answer, human string) error {
	if err := v.primary.ObserveIn(item, answer, human); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if answer == Abstain || human == Abstain {
		return nil
	}
	if err := v.secondary.ObserveIn(item, answer, human); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return nil
}

func (v *validityTables) report(reference string, alpha float64) Validity {
	out := Validity{
		Reference: reference,
		Primary: Agreement{
			TieHandling:  vocab.TieAbstainAsCategory.String(),
			CIMethod:     stats.MethodClusterJackknife,
			Level:        1 - alpha,
			Coefficients: stats.Agreement(v.primary, alpha),
		},
		Secondary: Agreement{
			TieHandling:  vocab.TieDecidedOnly.String(),
			CIMethod:     stats.MethodClusterJackknife,
			Level:        1 - alpha,
			Coefficients: stats.Agreement(v.secondary, alpha),
		},
		FromGold:   v.from[ReferenceGold],
		FromLabels: v.from[ReferenceLabels],
	}
	switch reference {
	case ReferenceGold:
		out.FromGold, out.FromLabels = out.Primary.N, 0
	case ReferenceLabels:
		out.FromGold, out.FromLabels = 0, out.Primary.N
	}
	return out
}

// humanHuman is the ceiling: how far two people agree with each other on the
// items they both labelled.
//
// It is the number a judge's agreement with people has to be read against. A
// judge that reaches 0.5 against labels two people only reach 0.55 on is doing
// about as well as the labels allow; the same 0.5 against labels two people
// reach 0.9 on is a judge measuring something else.
//
// The pair compared is the one that shares the most items, which is the pair
// with the most to say. Averaging over every pair would report a number no two
// people actually produced, and taking the first pair with anything in common
// used to report a ceiling over one shared item while another pair shared two
// hundred.
func humanHuman(labels []Label, categories []string, alpha float64) *Agreement {
	people := labelers(labels)
	if len(people) < 2 {
		return nil
	}
	given := map[string]map[string]string{}
	for _, label := range labels {
		if given[label.Labeler] == nil {
			given[label.Labeler] = map[string]string{}
		}
		if _, already := given[label.Labeler][label.Item]; !already {
			given[label.Labeler][label.Item] = label.Category()
		}
	}
	var best []string
	var bestPair [2]string
	for a := range people {
		for b := a + 1; b < len(people); b++ {
			shared := make([]string, 0, len(given[people[a]]))
			for item := range given[people[a]] {
				if _, both := given[people[b]][item]; both {
					shared = append(shared, item)
				}
			}
			if len(shared) > len(best) {
				sort.Strings(shared)
				best, bestPair = shared, [2]string{people[a], people[b]}
			}
		}
	}
	if len(best) == 0 {
		return nil
	}
	table, err := stats.NewTable(categories)
	if err != nil {
		return nil
	}
	for _, item := range best {
		if err := table.ObserveIn(item, given[bestPair[0]][item], given[bestPair[1]][item]); err != nil {
			return nil
		}
	}
	return &Agreement{
		TieHandling:  vocab.TieAbstainAsCategory.String(),
		CIMethod:     stats.MethodClusterJackknife,
		Level:        1 - alpha,
		Labelers:     bestPair[:],
		Coefficients: stats.Agreement(table, alpha),
	}
}

// stratumOf prefers the manifest's band and falls back to the gold file's, so
// a suite that does not carry the band is still readable.
func stratumOf(task Task, gold Gold) string {
	switch {
	case task.Stratum != "":
		return task.Stratum
	case gold.MarginStratum != "":
		return gold.MarginStratum
	}
	return "unbanded"
}

func rate(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

// latency summarises what the runs cost. The median rather than the mean: one
// item that hit a retry storm should not move the number a person plans the
// next run against.
func latency(all []int64) Latency {
	if len(all) == 0 {
		return Latency{}
	}
	sorted := slices.Clone(all)
	slices.Sort(sorted)
	out := Latency{
		MedianMS: sorted[len(sorted)/2],
		MinMS:    sorted[0],
		MaxMS:    sorted[len(sorted)-1],
	}
	for _, value := range sorted {
		out.TotalMS += value
	}
	return out
}

// checkSweep is the one comparison a rescoring makes against the calibration
// it re-read.
//
// A Condorcet winner is a candidate that won every pair under both orders,
// and that is the same finding under either rule: the older rule selected
// exactly such a candidate and nothing else. So on a run the new rule
// settled that way — and where the candidates had not agreed with each
// other, which the older rule could not see — the two must name the same
// candidate. They are reading the same six recorded answers. A difference
// is not a judge that changed its mind; it is a replay that did not replay,
// and the count is put in the report rather than in a log nobody keeps.
func checkSweep(opts Options, before *Rescored, item string, run Judged) {
	if opts.Recorded == nil || before == nil || run.Rule() != RuleCondorcet {
		return
	}
	kind, candidate := opts.Recorded(item, run.Seed)
	if kind == OutcomeSelected && candidate == run.Candidate {
		before.SweptAgree++
		return
	}
	before.SweptDiffer++
}
