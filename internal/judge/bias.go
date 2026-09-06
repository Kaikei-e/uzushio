package judge

import (
	"cmp"
	"math"
	"slices"

	"github.com/Kaikei-e/uzushio/internal/stats"
)

// This file is the position half of a calibration: which answer the judge
// named when it named one, and what its indecision cost the suite.
//
// The swap arm above it already reports how often the judge changed its mind
// when the two candidates changed places. That is a flip rate, and a flip rate
// has no direction: a judge that contradicts itself on every pair could be
// contradicting itself symmetrically, which is noise, or it could be naming
// whichever answer came first every single time, which is the fence. The two
// call for different things — one is a judge that is not reading carefully,
// the other is a judge that is not reading the answers at all — and nothing in
// a flip rate tells them apart.
//
// So the single call is counted as well as the pair. P(first-shown chosen) is
// ½ for a judge with no position preference whatever else it is doing wrong,
// and the distance from ½ is the bias. Its denominator is the *decided* call:
// a tie is the judge declining to prefer a position and an unreadable answer
// is not an answer, and folding either into the denominator would pull the
// estimate towards ½ by counting non-answers as balance.

// ReasonUnstated stands in for a no-candidate that named no sub-reason, so a
// run is counted under some row rather than under an empty one.
const ReasonUnstated = "unstated"

// positionOf pools the per-item counts into the position summary.
//
// The interval is the leave-one-item-out jackknife rather than a score
// interval, because the rows are calls and six calls come from one prompt: a
// judge reading the fence on one item reads it on all six of that item's
// calls, and a Wilson interval over the calls would be about the width the
// data does not support.
func positionOf(firstChosen, decidedCalls []int, alpha float64) Position {
	p := stats.ClusteredProportion(firstChosen, decidedCalls, alpha)
	out := Position{
		DecidedCalls: p.Trials,
		FirstChosen:  p.Successes,
		PFirst:       stats.Undefined(),
		Bias:         stats.Undefined(),
		Clusters:     p.Clusters,
		CI:           p.CI,
		Level:        p.Level,
		CIMethod:     p.Method,
	}
	if p.Trials == 0 {
		// No call named an answer, so there is no share to report. Zero would
		// read as "the judge never chose the first one", which is a finding
		// rather than the absence of one.
		return out
	}
	out.PFirst = stats.Coefficient(p.Value)
	out.Bias = stats.Coefficient(math.Abs(p.Value - 0.5))
	return out
}

// observe records one pair whose two orders contradicted each other.
//
// There are only two ways for that to happen in a two-answer call, and they
// are opposite findings. Both orders answering `A` is the judge naming
// whichever answer it was shown first, twice — the position speaking. Both
// answering `B` is the same reading of the fence from the other end. Anything
// else is a pair the harness called a disagreement whose orders do not look
// like one, which is counted rather than dropped so the row still sums.
func (d *DisagreeBreakdown) observe(pair Pair) {
	d.Pairs++
	if len(pair.Orders) != 2 || !pair.Orders[0].Decided() || !pair.Orders[1].Decided() {
		d.Other++
		return
	}
	switch {
	case pair.Orders[0].ChoseFirst() && pair.Orders[1].ChoseFirst():
		d.BothFirst++
	case !pair.Orders[0].ChoseFirst() && !pair.Orders[1].ChoseFirst():
		d.BothSecond++
	default:
		d.Other++
	}
}

// coverage accumulates the abstention table: one pooled count, and one per
// seed. The per-seed tables are kept because an abstention rate that moves
// between two seeds of one suite is a different finding from one that does
// not, and pooling would hide it.
type coverage struct {
	pooled counts
	seeds  map[int]*counts
	order  []int
}

func newCoverage() *coverage {
	return &coverage{pooled: newCounts(), seeds: map[int]*counts{}}
}

// counts is one cross-table under construction: each row's two columns, and
// the totals that let a reader check them.
type counts struct {
	rows               map[string][2]int
	runs               int
	ungolded           int
	abstainedOnDecided int
}

func newCounts() counts { return counts{rows: map[string][2]int{}} }

// observe records one run against its item's human label.
func (c *coverage) observe(run Judged, gold string) {
	c.pooled.observe(run, gold)
	own, seen := c.seeds[run.Seed]
	if !seen {
		fresh := newCounts()
		own = &fresh
		c.seeds[run.Seed] = own
		c.order = append(c.order, run.Seed)
	}
	own.observe(run, gold)
}

// observe puts one run in its cell. An item with no human label at all has no
// column to go in and is counted apart, so the columns sum to the runs the
// table is over rather than to some smaller number nobody named.
func (c *counts) observe(run Judged, gold string) {
	if gold == "" {
		c.ungolded++
		return
	}
	c.runs++
	column := 0
	if gold == Abstain {
		column = 1
	}
	cell := c.rows[coverageRow(run, gold)]
	cell[column]++
	c.rows[coverageRow(run, gold)] = cell
	if column == 0 && run.Outcome == OutcomeNoCandidate {
		c.abstainedOnDecided++
	}
}

// coverageRow is the row one run belongs in: whether it agreed with the people
// where it chose at all, and by which of the harness's own words where it did
// not.
func coverageRow(run Judged, gold string) string {
	switch {
	case !run.Measured():
		return cmp.Or(run.Outcome, ReasonUnstated)
	case run.Outcome == OutcomeSelected && run.Category() == gold:
		return CoverageAgrees
	case run.Outcome == OutcomeSelected:
		return CoverageDiffers
	}
	return cmp.Or(run.Reason, ReasonUnstated)
}

// report renders the pooled table and each seed's, seeds in order.
func (c *coverage) report() Coverage {
	out := Coverage{Pooled: c.pooled.table(0)}
	for _, seed := range slices.Sorted(slices.Values(c.order)) {
		out.BySeed = append(out.BySeed, c.seeds[seed].table(seed))
	}
	return out
}

func (c counts) table(seed int) Coverages {
	out := Coverages{
		Seed: seed, Runs: c.runs, Ungolded: c.ungolded,
		AbstainedOnDecided: c.abstainedOnDecided,
	}
	for _, name := range coverageOrder(c.rows) {
		cell := c.rows[name]
		out.Rows = append(out.Rows, CoverageRow{
			Outcome: name, GoldDecided: cell[0], GoldTie: cell[1],
		})
		out.GoldDecided += cell[0]
		out.GoldTie += cell[1]
	}
	return out
}

// coverageOrder is the rows this build knows in the order it reports them,
// then anything else the harness recorded, sorted.
//
// A row nobody anticipated is reported rather than dropped. The columns have
// to sum to the runs, and a harness that starts writing a new sub-reason must
// show up as a row a reader can ask about rather than as a table that quietly
// stops adding up.
func coverageOrder(rows map[string][2]int) []string {
	out := make([]string, 0, len(rows))
	for _, name := range CoverageRows {
		if _, seen := rows[name]; seen {
			out = append(out, name)
		}
	}
	extra := make([]string, 0, len(rows))
	for name := range rows {
		if !slices.Contains(CoverageRows, name) {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	return append(out, extra...)
}
