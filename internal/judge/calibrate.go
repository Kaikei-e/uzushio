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
)

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
	return assemble(opts, seeds, judged, failures)
}

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

// assemble turns the runs into the report and the journal.
func assemble(opts Options, seeds []int, judged [][]Judged, failures []error) (Result, error) {
	labels := byItem(opts.Labels)
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Suite:         opts.Suite.ID,
		Source:        opts.Suite.Source,
		License:       opts.Suite.License,
		Judge:         opts.Judge,
		Pool:          opts.Pool,
		Day:           opts.Day,
		Items:         len(opts.Suite.Tasks),
		Seeds:         len(seeds),
		Alpha:         opts.Alpha,
		Level:         1 - opts.Alpha,
		MinKappa:      opts.MinKappa,
		MaxUnmeasured: opts.MaxUnmeasured,
		Strata:        map[string]int{},
		Outcomes: Outcomes{
			ByKind:              map[string]int{},
			NoCandidateByReason: map[string]int{},
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
	categories := append(slices.Clone(Positions), Abstain)
	rerun, err := stats.NewTable(categories)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	validity := map[string]*validityTables{}
	for _, reference := range []string{ReferenceGold, ReferenceLabels, ReferenceHuman} {
		tables, err := newValidityTables(categories)
		if err != nil {
			return Result{}, err
		}
		validity[reference] = tables
	}

	var latencies []int64
	var flips, decidedPairs []int
	noCandidateItems, measuredItems := 0, 0
	items := make([]ItemResult, 0, len(opts.Suite.Tasks))
	for i, task := range opts.Suite.Tasks {
		gold, hasGold, err := opts.Suite.GoldOf(task)
		if err != nil {
			return Result{}, err
		}
		item := ItemResult{Item: task.ID, Stratum: stratumOf(task, gold), Hard: gold.Hard}
		report.Strata[item.Stratum]++
		if hasGold {
			item.Gold = gold.Category()
		}
		if given := labels[task.ID]; len(given) > 0 {
			item.Label = firstLabel(given)
			for _, label := range given {
				if label.Choice == ChoiceAllBad {
					report.AllBadLabels++
				}
			}
		}
		switch {
		case item.Label != "":
			item.Human = item.Label
			item.HumanFrom = ReferenceLabels
		case item.Gold != "":
			item.Human = item.Gold
			item.HumanFrom = ReferenceGold
		}

		runs := judged[i]
		if failures[i] != nil {
			item.Error = scrub(failures[i].Error(), opts.Vault, opts.Suite.Dir)
		}
		itemFlips, itemDecided := 0, 0
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
			if !run.Measured() {
				report.Outcomes.Unmeasured++
				continue
			}
			if run.Outcome == OutcomeNoCandidate {
				reason := run.Reason
				if reason == "" {
					reason = "unstated"
				}
				report.Outcomes.NoCandidateByReason[reason]++
			}
			for _, pair := range run.Pairs {
				report.Outcomes.Calls += len(pair.Orders)
				if len(pair.Orders) != 2 {
					continue
				}
				report.Swap.Pairs++
				first, second := pair.side(pair.Orders[0]), pair.side(pair.Orders[1])
				if err := swap.ObserveIn(task.ID, first, second); err != nil {
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
			if err := rerun.ObserveIn(task.ID, runs[0].Category(), later.Category()); err != nil {
				return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
			}
		}

		answer := runs[0].Category()
		for _, reference := range []string{ReferenceGold, ReferenceLabels, ReferenceHuman} {
			human := map[string]string{
				ReferenceGold: item.Gold, ReferenceLabels: item.Label, ReferenceHuman: item.Human,
			}[reference]
			if human == "" {
				continue
			}
			if err := validity[reference].observe(task.ID, answer, human); err != nil {
				return Result{}, err
			}
			if reference == ReferenceHuman {
				validity[reference].from[item.HumanFrom]++
			}
		}
		items = append(items, item)
	}

	report.Swap.Rate = stats.ClusteredProportion(flips, decidedPairs, opts.Alpha)
	report.Swap.Agreement = agreementOf(swap, opts.Alpha)
	report.Rerun = Rerun{
		Seeds:       len(seeds),
		Comparisons: rerun.N(),
		Agreement:   agreementOf(rerun, opts.Alpha),
	}
	report.Validity = map[string]Validity{}
	for reference, tables := range validity {
		report.Validity[reference] = tables.report(reference, opts.Alpha)
	}
	report.HumanHuman = humanHuman(opts.Labels, categories, opts.Alpha)
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
