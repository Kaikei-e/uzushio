package judge

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/stats"
)

// This file is the other way to make a calibration: over the answers a
// previous one already got.
//
// A judge's answers are data. What CMoA changed in ADR 0013 is the function
// over them — the candidates are compared with each other first, and the
// pairwise verdicts are settled with a Copeland score and a recorded
// tie-break rather than by requiring a Condorcet winner. The six calls, the
// prompt and the swap protocol are unchanged, so the recorded calls of the
// previous calibration are the same six answers the new rule would get, and
// re-asking would spend a fleet for an afternoon to receive them again.
//
// A rescoring is therefore a measurement of the rule and not of the judge on
// the day it runs. That is a real difference and the report says so in
// words: the judge was not asked anything, the calls are the ones recorded
// in the window the source names, and the document carries `rescored_from`
// so nobody has to read the body to find out.

// Rescore is a previous calibration read as a source of recorded runs.
type Rescore struct {
	// Dir is the source report directory as it was named, which is what the
	// document records.
	Dir string
	// vault is the root the journal's run directories are relative to; the
	// journal never carries an absolute path.
	vault string
	runs  map[rescoreKey]string
	// outcomes is what the source concluded for each run, kept so a
	// rescoring can check itself where the two rules must agree.
	outcomes map[rescoreKey]recorded
	seeds    []int
}

// recorded is one source run's conclusion.
type recorded struct {
	kind      string
	candidate string
}

// rescoreKey is one recorded run: an item at a seed.
type rescoreKey struct {
	item string
	seed int
}

// OpenRescore reads a calibration's journal and indexes the run directories
// it names.
//
// Only the run's identity is taken. What each run concluded is deliberately
// not read here: the point of a rescoring is that the outcome is computed
// again from the calls, and a source outcome carried along would be a
// temptation to compare against rather than a measurement.
func OpenRescore(dir, vault string) (*Rescore, error) {
	journal, err := ReadItems(dir)
	if err != nil {
		return nil, err
	}
	r := &Rescore{
		Dir: filepath.ToSlash(dir), vault: vault,
		runs:     map[rescoreKey]string{},
		outcomes: map[rescoreKey]recorded{},
	}
	for _, item := range journal {
		for _, run := range item.Runs {
			if run.RunDir == "" {
				continue
			}
			key := rescoreKey{item.Item, run.Seed}
			r.runs[key] = filepath.Join(vault, run.RunDir)
			candidate := ""
			if run.Outcome == OutcomeSelected {
				candidate = run.Category
			}
			r.outcomes[key] = recorded{kind: run.Outcome, candidate: candidate}
			if !slices.Contains(r.seeds, run.Seed) {
				r.seeds = append(r.seeds, run.Seed)
			}
		}
	}
	if len(r.runs) == 0 {
		return nil, fmt.Errorf("%w: %s records no run directory to rescore", ErrJudge, dir)
	}
	slices.Sort(r.seeds)
	return r, nil
}

// Seeds are the seeds the source ran, ascending. A rescoring runs the seeds
// it has and no others: a seed the source never ran has no recorded calls,
// and asking the judge for them would make half the report a measurement of
// today's fleet and the other half of one from a month ago.
func (r *Rescore) Seeds() []int { return slices.Clone(r.seeds) }

// Reruns is the extra-seed count the seeds amount to.
func (r *Rescore) Reruns() int { return len(r.seeds) - 1 }

// RunDir is where the recorded run of one item at one seed is, or empty.
func (r *Rescore) RunDir(item string, seed int) string {
	return r.runs[rescoreKey{item, seed}]
}

// Check refuses before spending anything: every item of the suite must have
// a recorded run at every seed that will be asked for.
//
// A missing run would otherwise become a failed harness invocation, land in
// the unmeasured count, and be reported as a fleet that would not run —
// which is exactly the wrong diagnosis for a journal that never held the
// run in the first place.
func (r *Rescore) Check(suite Suite, seeds []int) error {
	var missing []string
	for _, task := range suite.Tasks {
		for _, seed := range seeds {
			if r.RunDir(task.ID, seed) == "" {
				missing = append(missing, fmt.Sprintf("%s seed %d", task.ID, seed))
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	shown := missing
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Errorf("%w: %s records no run for %d item-seed(s), the first being %v",
		ErrJudge, r.Dir, len(missing), shown)
}

// Recorded is what the source concluded for one run: the outcome kind and,
// where it selected, the candidate. It is how a rescoring checks itself.
func (r *Rescore) Recorded(item string, seed int) (kind, candidate string) {
	out := r.outcomes[rescoreKey{item, seed}]
	return out.kind, out.candidate
}

// Before is what the source calibration concluded, as the report keeps it.
//
// The per-stage counts are derived from the source's own journal rather than
// carried in its report, which is written before the counts existed: the
// journal records each run's outcome and the harness's reason for it, and
// that is what the stage is read off. A trace written before the harness had
// a consensus stage has neither field, so every selection it made was a
// Condorcet sweep — which is what the vocabulary of the day meant.
func (r *Rescore) Before() (*Rescored, error) {
	report, err := ReadReport(r.Dir)
	if err != nil {
		return nil, err
	}
	journal, err := ReadItems(r.Dir)
	if err != nil {
		return nil, err
	}
	out := &Rescored{
		Dir: r.Dir, Day: report.Day, Items: report.Items, Seeds: report.Seeds,
		ByKind: report.Outcomes.ByKind, ByReason: report.Outcomes.NoCandidateByReason,
		ByRule: map[string]int{}, Calls: report.Outcomes.Calls,
		TotalMS:    report.Latency.TotalMS,
		SwapKappa:  published(report.Swap.Agreement.Kappa),
		RerunKappa: published(report.Rerun.Agreement.Kappa),
		HumanKappa: published(report.HumanKappa),
	}
	if human, ok := report.Validity[ReferenceHuman]; ok {
		out.HumanDecided = published(human.Secondary.Kappa)
		out.HumanDecidedItems = human.Secondary.N
	}
	for _, item := range journal {
		for _, run := range item.Runs {
			judged := Judged{Outcome: run.Outcome, Reason: run.Reason}
			if rule := judged.Rule(); rule != "" {
				out.ByRule[rule]++
			}
		}
	}
	return out, nil
}

// published is a coefficient as a plain number, or doc.KappaUnmeasured for
// the one that was never computed — the same distinction the document draws,
// kept here so a table cell cannot print 0.000 for "nobody measured this".
func published(c stats.Coefficient) float64 {
	if !c.Defined() {
		return doc.KappaUnmeasured
	}
	return c.Float()
}
