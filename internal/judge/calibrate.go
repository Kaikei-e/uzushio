package judge

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

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
	// Parallel is how many items are judged at once. Zero is one.
	Parallel int
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
	// Hard says the corpus's own two readings disagreed about this item.
	Hard bool `json:"hard"`
	// Runs is what the judge answered, one entry per seed.
	Runs []RunResult `json:"runs"`
}

// RunResult is one seed's answer.
type RunResult struct {
	Seed           int    `json:"seed"`
	Outcome        string `json:"outcome"`
	Category       string `json:"category"`
	Reason         string `json:"reason,omitempty"`
	SwapConsistent int    `json:"swap_consistent_pairs"`
	InvalidRetries int    `json:"invalid_output_retries"`
	LatencyMS      int64  `json:"latency_ms"`
	RunDir         string `json:"run_dir,omitempty"`
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

	judged, err := runAll(ctx, opts, seeds)
	if err != nil {
		return Result{}, err
	}
	return assemble(opts, seeds, judged)
}

// runAll judges every item at every seed, with the requested number of items
// in flight.
func runAll(ctx context.Context, opts Options, seeds []int) ([][]Judged, error) {
	parallel := max(opts.Parallel, 1)
	out := make([][]Judged, len(opts.Suite.Tasks))
	errs := make([]error, len(opts.Suite.Tasks))

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
					errs[i] = ctx.Err()
					return
				}
				run, err := opts.Runner.Judge(ctx, opts.Suite, task, seed)
				if err != nil {
					errs[i] = err
					return
				}
				out[i] = append(out[i], run)
			}
			if opts.Log != nil {
				opts.Log(fmt.Sprintf("%s: %s", task.ID, out[i][0].Outcome))
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// assemble turns the runs into the report and the journal.
func assemble(opts Options, seeds []int, judged [][]Judged) (Result, error) {
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
		MinKappa:      opts.MinKappa,
		Strata:        map[string]int{},
		Outcomes: Outcomes{
			ByKind:              map[string]int{},
			NoCandidateByReason: map[string]int{},
		},
	}

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
	items := make([]ItemResult, 0, len(opts.Suite.Tasks))
	for i, task := range opts.Suite.Tasks {
		runs := judged[i]
		if len(runs) == 0 {
			return Result{}, fmt.Errorf("%w: item %s produced no run", ErrJudge, task.ID)
		}
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
			item.Label = given[0].Category()
			for _, label := range given {
				if label.Choice == ChoiceAllBad {
					report.AllBadLabels++
				}
			}
		}
		switch {
		case item.Label != "":
			item.Human = item.Label
		case item.Gold != "":
			item.Human = item.Gold
		}

		for _, run := range runs {
			item.Runs = append(item.Runs, RunResult{
				Seed: run.Seed, Outcome: run.Outcome, Category: run.Category(),
				Reason: run.Reason, SwapConsistent: run.SwapConsistent,
				InvalidRetries: run.InvalidRetries, LatencyMS: run.LatencyMS,
				RunDir: run.RunDir,
			})
			latencies = append(latencies, run.LatencyMS)
			report.Outcomes.ByKind[run.Outcome]++
			report.Outcomes.Total++
			report.Outcomes.InvalidRetries += run.InvalidRetries
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
				if err := swap.Observe(first, second); err != nil {
					return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
				}
				if first != Abstain && second != Abstain {
					report.Swap.Decided++
					if first != second {
						report.Swap.Flips++
					}
				}
			}
		}

		// The re-run table reads the first seed against each later one. The
		// first is the reference on purpose: pooling every unordered pair
		// would force the two marginals to be equal, which is the one thing
		// that makes kappa flatter than the data.
		for _, later := range runs[1:] {
			if err := rerun.Observe(runs[0].Category(), later.Category()); err != nil {
				return Result{}, fmt.Errorf("%w: %w", ErrJudge, err)
			}
		}

		answer := runs[0].Category()
		for reference, human := range map[string]string{
			ReferenceGold: item.Gold, ReferenceLabels: item.Label, ReferenceHuman: item.Human,
		} {
			if human == "" {
				continue
			}
			if err := validity[reference].observe(answer, human); err != nil {
				return Result{}, err
			}
		}
		items = append(items, item)
	}

	report.Swap.FlipRate = rate(report.Swap.Flips, report.Swap.Decided)
	report.Swap.FlipCI = stats.Wilson(report.Swap.Flips, report.Swap.Decided, opts.Alpha)
	report.Swap.Agreement = Agreement{
		TieHandling:  vocab.TieAbstainAsCategory.String(),
		Coefficients: stats.Agreement(swap, opts.Alpha),
	}
	report.Rerun = Rerun{
		Seeds:       len(seeds),
		Comparisons: rerun.N(),
		Agreement: Agreement{
			TieHandling:  vocab.TieAbstainAsCategory.String(),
			Coefficients: stats.Agreement(rerun, opts.Alpha),
		},
	}
	report.Validity = map[string]Validity{}
	for reference, tables := range validity {
		report.Validity[reference] = tables.report(reference, opts.Alpha)
	}
	report.HumanHuman = humanHuman(opts.Labels, categories, opts.Alpha)
	report.Outcomes.NoCandidateRate = rate(report.Outcomes.ByKind[OutcomeNoCandidate], report.Outcomes.Total)
	report.Outcomes.InvalidRetryRate = rate(report.Outcomes.InvalidRetries, report.Outcomes.Calls)
	report.Latency = latency(latencies)

	human := report.Validity[ReferenceHuman]
	report.HumanKappa = human.Primary.Kappa
	report.NHuman = human.Primary.N
	report.Verdict = verdict(human, opts.MinKappa)
	return Result{Report: report, Items: items}, nil
}

// verdict reads the calibration word off the validity coefficient. It is the
// primary handling that decides, because that is the one computed over the
// whole sample: letting the secondary decide would let a judge that abstains
// on everything hard call itself calibrated on what is left.
func verdict(v Validity, minKappa float64) vocab.Calibrated {
	if v.Primary.N == 0 || !v.Primary.Kappa.Defined() {
		return vocab.CalibratedUnmeasured
	}
	if v.Primary.Kappa.Float() >= minKappa {
		return vocab.CalibratedYes
	}
	return vocab.CalibratedNo
}

// validityTables holds one reference's two tables: every item under the
// primary handling, and the decided ones under the secondary.
type validityTables struct {
	primary   *stats.Table
	secondary *stats.Table
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
	return &validityTables{primary: primary, secondary: secondary}, nil
}

func (v *validityTables) observe(answer, human string) error {
	if err := v.primary.Observe(answer, human); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if answer == Abstain || human == Abstain {
		return nil
	}
	if err := v.secondary.Observe(answer, human); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return nil
}

func (v *validityTables) report(reference string, alpha float64) Validity {
	return Validity{
		Reference: reference,
		Primary: Agreement{
			TieHandling:  vocab.TieAbstainAsCategory.String(),
			Coefficients: stats.Agreement(v.primary, alpha),
		},
		Secondary: Agreement{
			TieHandling:  vocab.TieDecidedOnly.String(),
			Coefficients: stats.Agreement(v.secondary, alpha),
		},
	}
}

// humanHuman is the ceiling: how far two people agree with each other on the
// items they both labelled.
//
// It is the number a judge's agreement with people has to be read against. A
// judge that reaches 0.5 against labels two people only reach 0.55 on is doing
// about as well as the labels allow; the same 0.5 against labels two people
// reach 0.9 on is a judge measuring something else.
//
// Only the first two labelers who share items are compared. Averaging over
// every pair would report a number no pair of people actually produced, and
// picking the best pair would be picking the answer.
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
	for a := range people {
		for b := a + 1; b < len(people); b++ {
			shared := make([]string, 0, len(given[people[a]]))
			for item := range given[people[a]] {
				if _, both := given[people[b]][item]; both {
					shared = append(shared, item)
				}
			}
			if len(shared) == 0 {
				continue
			}
			sort.Strings(shared)
			table, err := stats.NewTable(categories)
			if err != nil {
				return nil
			}
			for _, item := range shared {
				if err := table.Observe(given[people[a]][item], given[people[b]][item]); err != nil {
					return nil
				}
			}
			return &Agreement{
				TieHandling:  vocab.TieAbstainAsCategory.String(),
				Coefficients: stats.Agreement(table, alpha),
			}
		}
	}
	return nil
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
