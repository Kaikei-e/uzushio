package judge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ReportSchemaVersion is the version of the report.json this package writes.
const ReportSchemaVersion = 1

// The two files a calibration leaves behind beside the vault document.
const (
	ReportFile = "report.json"
	ItemsFile  = "items.jsonl"
)

// Agreement is one coefficient with the tie handling it was computed under.
// The handling travels with the number rather than being stated once at the
// top, because a number quoted out of a report is quoted without the top.
type Agreement struct {
	TieHandling string `json:"tie_handling"`
	// CIMethod names how the interval was made and Level is its coverage.
	// They travel with the number for the same reason the handling does: a
	// width is only readable next to what produced it, and an interval
	// computed over rows that share an item is a different claim from one
	// computed over independent rows.
	CIMethod string  `json:"ci_method"`
	Level    float64 `json:"level"`
	// Labelers names the two people a ceiling was measured between, where the
	// agreement is a human-human one.
	Labelers []string `json:"labelers,omitempty"`
	stats.Coefficients
}

// Swap is the judge against itself with the candidates in the other order.
//
// Every run of every item contributes its three pairs, so a calibration at
// three seeds over two hundred items reads eighteen hundred pairs rather than
// six hundred. That is deliberate: position consistency is a property of the
// judge under one prompt, and a second seed is another reading of it, not a
// different question.
type Swap struct {
	// Pairs is how many pairs were judged in both orders, over every seed;
	// Decided how many of those the judge answered both times, and Flips how
	// many of the decided ones it answered differently.
	Pairs   int `json:"pairs"`
	Decided int `json:"decided"`
	Flips   int `json:"flips"`
	// Rate is Flips over Decided with a leave-one-item-out interval — not a
	// score interval, because the rows share items. It is published beside the
	// coefficient because it is the raw quantity a reader can check, and
	// because a flip rate and a kappa can move in opposite directions when the
	// marginals are lopsided. It is a `decided-only` quantity: a pair either
	// side abstained on is in neither the numerator nor the denominator.
	Rate stats.Proportion `json:"flip_rate"`
	// Agreement is the coefficient over the pair's two slots and abstention.
	//
	// It is a consistency statistic. The two readings come from one model
	// under one prompt, so they are not independent, and non-independence
	// pushes observed agreement — and so kappa — up. It must not be reported
	// as a two-rater reliability.
	Agreement Agreement `json:"agreement"`
	// Position is the same bias read off the single calls rather than the
	// pairs, and DisagreeBreakdown is which way the pairs that flipped fell.
	Position          Position          `json:"position"`
	DisagreeBreakdown DisagreeBreakdown `json:"disagree_breakdown"`
}

// Position is how often the judge named whichever answer it was shown first.
//
// The flip rate above says the judge changed its mind under a swap; this says
// which way it leans, and the two answer different questions. A judge that
// flips on nothing has no bias to report, and a judge that flips on everything
// still might have none — it would flip both ways equally. `p_first` at 0.5 is
// a judge with no position preference; the distance from it is the bias, and
// it is what a flip rate cannot tell you.
type Position struct {
	// DecidedCalls is the denominator: single calls that came back readable
	// and named one of the two answers. A tie and an unreadable answer are
	// both excluded — one is the judge declining to choose a position, the
	// other is not an answer at all.
	DecidedCalls int `json:"decided_calls"`
	// FirstChosen is how many of those named the answer shown first.
	FirstChosen int `json:"first_chosen"`
	// PFirst is FirstChosen over DecidedCalls, and Bias its distance from the
	// half a judge with no position preference would sit at. Both are null
	// where no call named an answer: a zero there would read as "the judge
	// never chose the first one", which is a finding rather than the absence
	// of one.
	PFirst stats.Coefficient `json:"p_first"`
	Bias   stats.Coefficient `json:"bias"`
	// Clusters is how many items those calls came from, and CI the interval
	// over them. The rows are calls, and six calls of one item move together —
	// a judge reading the fence on one prompt reads it on all six — so the
	// interval is the same leave-one-item-out jackknife the rest of the report
	// uses and never a score interval over the calls.
	Clusters int             `json:"clusters"`
	CI       *stats.Interval `json:"ci"`
	Level    float64         `json:"level"`
	CIMethod string          `json:"ci_method"`
}

// DisagreeBreakdown is which way the pairs whose two orders contradicted each
// other fell.
//
// A pair is a disagreement when both orders named a candidate and named
// different ones, and in a two-answer call there are only two ways for that to
// happen: the judge said `A` both times, or `B` both times. BothFirst is the
// first — the judge choosing whichever answer came first, which is the fence
// speaking rather than the answers. Other is anything the arithmetic did not
// expect and is reported rather than dropped.
type DisagreeBreakdown struct {
	Pairs      int `json:"pairs"`
	BothFirst  int `json:"both_first"`
	BothSecond int `json:"both_second"`
	Other      int `json:"other"`
}

// Rerun is the judge against itself on another seed.
type Rerun struct {
	Seeds       int       `json:"seeds"`
	Comparisons int       `json:"comparisons"`
	Agreement   Agreement `json:"agreement"`
}

// Validity is the judge against people.
type Validity struct {
	Reference string `json:"reference"`
	// Primary keeps every item and scores abstention as a category of its
	// own; Secondary drops the items either side abstained on. They are
	// different estimands rather than one number computed two ways, which is
	// why both are reported and both carry the handling's name.
	Primary   Agreement `json:"primary"`
	Secondary Agreement `json:"secondary"`
	// FromGold and FromLabels say which labeler set each item's human side
	// came from. For the `human` reference the two are a mixture, and a
	// coefficient over a mixture has to say what of.
	FromGold   int `json:"from_gold"`
	FromLabels int `json:"from_labels"`
}

// Outcomes is what the judge did, as distinct from how well it did it.
type Outcomes struct {
	Total  int            `json:"total"`
	ByKind map[string]int `json:"by_kind"`
	// Unmeasured is how many runs produced no judgement — a timeout, a failed
	// judge, a harness that would not run. They are in no coefficient.
	Unmeasured          int            `json:"unmeasured_runs"`
	NoCandidateByReason map[string]int `json:"no_candidate_by_reason"`
	// NoCandidateRate is over measured items at the first seed, which is one
	// row per item and so the one rate in this report whose rows really are
	// independent. It is the only one with a score interval.
	NoCandidateRate  stats.Proportion `json:"no_candidate_rate"`
	Calls            int              `json:"calls"`
	InvalidRetries   int              `json:"invalid_output_retries"`
	InvalidRetryRate float64          `json:"invalid_output_retry_rate"`
	// ByRule is how many runs each stage of the harness's selection rule
	// settled, and ByTieBreak and ByConsensus split the two stages that
	// have a vocabulary of their own. Judgeless counts the runs that made
	// no judge call at all, which is the cost the consensus stage saves and
	// also the set of runs no judge answered for.
	ByRule      map[string]int `json:"by_rule"`
	ByTieBreak  map[string]int `json:"by_tie_break_key,omitempty"`
	ByConsensus map[string]int `json:"by_consensus_agreement,omitempty"`
	Judgeless   int            `json:"judgeless_runs"`
}

// The stages of the harness's chat selection rule, as a calibration counts
// them. The words are CMoA's (ADR 0013): the candidates are compared with
// each other first, the judge's pairwise verdicts are settled with a
// Copeland score, and candidates the score cannot part go to a recorded
// chain of tie-break keys.
const (
	// RuleConsensus: a strict majority of the candidates said the same
	// thing and the judge was never asked.
	RuleConsensus = "consensus"
	// RuleCondorcet: one candidate won every pair under both orders.
	RuleCondorcet = "condorcet"
	// RuleCopeland: no candidate swept, and one held the highest score
	// alone.
	RuleCopeland = "copeland"
	// RuleTieBreak: the score left more than one candidate at the top and a
	// key parted them. ByTieBreak says which.
	RuleTieBreak = "copeland-tie-break"
	// RuleUnstated: the harness selected and gave a reason this build does
	// not recognise. It is a bucket rather than a guess, so a vocabulary
	// that grows is visible instead of silently folded into a neighbour.
	RuleUnstated = "unstated"
)

// RuleRows is the order the stages are reported in: the one that costs
// nothing, then the two the judge decided, then the chain, then the word for
// a sentence this build has not learned.
var RuleRows = []string{RuleConsensus, RuleCondorcet, RuleCopeland, RuleTieBreak, RuleUnstated}

// Rescored is what the calibration whose recorded calls were re-aggregated
// concluded, kept beside this one's numbers so the document can put the two
// side by side without a reader opening a second report.
//
// It is a copy of a handful of the source's fields and not a reference to
// it, for the reason the coefficients are copied into the document: the
// comparison is the claim this report makes, and a claim that has to be
// reassembled from another file to be read is a claim nobody checks.
type Rescored struct {
	Dir      string         `json:"dir"`
	Day      string         `json:"day"`
	Items    int            `json:"items"`
	Seeds    int            `json:"seeds"`
	ByKind   map[string]int `json:"outcomes_by_kind"`
	ByReason map[string]int `json:"no_candidate_by_reason"`
	ByRule   map[string]int `json:"by_rule"`
	Calls    int            `json:"calls"`
	// TotalMS is the sum of the source's run latencies: the judge time the
	// re-aggregation did not have to spend again.
	TotalMS int64 `json:"judge_time_ms"`
	// The three coefficients and the decided-only reading of the third, so
	// the table can show what the rule change moved.
	SwapKappa         float64 `json:"swap_kappa"`
	RerunKappa        float64 `json:"rerun_kappa"`
	HumanKappa        float64 `json:"human_kappa"`
	HumanDecided      float64 `json:"human_kappa_decided_only"`
	HumanDecidedItems int     `json:"n_human_decided_only"`
	// SweptAgree and SweptDiffer are the integrity check a rescoring can
	// run on itself: where the new rule found a Condorcet winner and the
	// candidates had not agreed, the old rule required exactly that winner
	// too, so the two must name the same candidate. Every difference is a
	// call that came back differently, and there should be none — the calls
	// were not made again.
	SweptAgree  int `json:"condorcet_reproduced"`
	SweptDiffer int `json:"condorcet_differed"`
}

// The two rows of the abstention table a run's outcome is not already a word
// for. Every other row is the no-candidate sub-reason the harness recorded, so
// the vocabulary stays CMoA's.
const (
	// CoverageAgrees is a run that chose the candidate the people chose.
	CoverageAgrees = "selected_agrees"
	// CoverageDiffers is a run that chose another one — or chose at all on an
	// item the people left undecided.
	CoverageDiffers = "selected_differs"
)

// CoverageRows is the order the abstention table is reported in: what the
// judge decided first, then the ways it did not, then the machine.
var CoverageRows = []string{
	CoverageAgrees, CoverageDiffers,
	"no_majority", "all_draws", "cycle", "invalid_output", "too_few_candidates",
	OutcomeJudgeTimeout, OutcomeJudgeFailed,
}

// Coverage is what the judge did against what the people decided: the
// abstention table, pooled over the seeds and again at each of them.
//
// It is here because an abstention rate on its own says nothing about what was
// lost. A judge that abstains on the items the people could not decide either
// is agreeing with them; a judge that abstains on the items the people decided
// cleanly has lost that item to its own indecision, and under a both-orders
// protocol the ordinary way that happens is the two orders contradicting each
// other. That is the coverage a position bias costs, and it is a count rather
// than an inference.
type Coverage struct {
	Pooled Coverages   `json:"pooled"`
	BySeed []Coverages `json:"by_seed"`
}

// Coverages is one cross-table: the rows against the two columns.
type Coverages struct {
	// Seed is the seed the table is of, and zero in the pooled one.
	Seed int `json:"seed,omitempty"`
	// Runs is how many runs it counts, and Ungolded how many were left out
	// because their item carries no human label at all — there is no column
	// for those, and dropping them silently would make the columns not sum.
	Runs     int `json:"runs"`
	Ungolded int `json:"runs_without_gold"`
	// GoldDecided and GoldTie are the column totals.
	GoldDecided int `json:"gold_decided"`
	GoldTie     int `json:"gold_tie"`
	// AbstainedOnDecided is the number the table is for: runs that reached no
	// candidate on an item the people had decided.
	AbstainedOnDecided int           `json:"abstained_on_gold_decided"`
	Rows               []CoverageRow `json:"rows"`
}

// CoverageRow is one row of the table: an outcome against the two columns.
type CoverageRow struct {
	Outcome     string `json:"outcome"`
	GoldDecided int    `json:"gold_decided"`
	GoldTie     int    `json:"gold_tie"`
}

// Latency is what the runs cost.
type Latency struct {
	MedianMS int64 `json:"median_ms"`
	MinMS    int64 `json:"min_ms"`
	MaxMS    int64 `json:"max_ms"`
	TotalMS  int64 `json:"total_ms"`
}

// Report is everything a calibration measured.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Suite         string `json:"suite"`
	Source        string `json:"source,omitempty"`
	License       string `json:"license,omitempty"`
	Judge         string `json:"judge"`
	Pool          string `json:"pool"`
	Day           string `json:"day"`
	// RescoredFrom names the calibration directory whose recorded judge
	// calls were re-aggregated, empty where the judge was asked. Every other
	// number in this report reads the same either way, which is why the
	// distinction is a field rather than a remark. Rescored is what that
	// calibration concluded.
	RescoredFrom string    `json:"rescored_from,omitempty"`
	Rescored     *Rescored `json:"rescored,omitempty"`
	Items        int       `json:"items"`
	Seeds        int       `json:"seeds"`
	// Unmeasured is how many items produced no usable judgement at all, and
	// UnmeasuredRate the share of the suite they are. Over MaxUnmeasured the
	// calibration reaches no verdict: the items a failing fleet drops are not
	// a random sample of the suite.
	Unmeasured           int     `json:"unmeasured_items"`
	UnmeasuredRate       float64 `json:"unmeasured_rate"`
	MaxUnmeasured        float64 `json:"max_unmeasured"`
	OverUnmeasuredBudget bool    `json:"over_unmeasured_budget"`
	// AllBadLabels is how many labels said none of the three was worth
	// choosing. It folds into abstention for the coefficient and is counted
	// here, because a corpus with many of them says something about the
	// candidates rather than about the judge.
	AllBadLabels int            `json:"all_bad_labels"`
	Alpha        float64        `json:"alpha"`
	Level        float64        `json:"level"`
	MinKappa     float64        `json:"min_kappa"`
	Strata       map[string]int `json:"strata"`

	Swap     Swap                `json:"swap"`
	Rerun    Rerun               `json:"rerun"`
	Validity map[string]Validity `json:"validity"`
	// AbstentionByGold is what the judge did against what the people decided.
	// It is the coverage half of the position numbers above: how many of the
	// abstentions fell on items the people had no trouble deciding.
	AbstentionByGold Coverage   `json:"abstention_by_gold"`
	HumanHuman       *Agreement `json:"human_human,omitempty"`
	Outcomes         Outcomes   `json:"outcomes"`
	Latency          Latency    `json:"latency"`

	// HumanKappa and NHuman are what the document carries, lifted out of the
	// validity block so that the one number the verdict rests on is not
	// something a reader has to go looking for.
	HumanKappa stats.Coefficient `json:"human_kappa"`
	NHuman     int               `json:"n_human"`
	Verdict    vocab.Calibrated  `json:"verdict"`
}

// Write materialises the report and the journal under dir.
func (r Result) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	body, err := json.MarshalIndent(r.Report, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if err := writeFile(filepath.Join(dir, ReportFile), append(body, '\n')); err != nil {
		return err
	}
	var journal bytes.Buffer
	for _, item := range r.Items {
		line, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrJudge, err)
		}
		journal.Write(line)
		journal.WriteByte('\n')
	}
	return writeFile(filepath.Join(dir, ItemsFile), journal.Bytes())
}

func writeFile(name string, body []byte) error {
	if err := os.WriteFile(name, body, 0o644); err != nil { //nolint:gosec // a report is world-readable on purpose
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return nil
}

// Document turns a report into the vault record of it. reportPath is where the
// report was written, relative to the vault root.
func (r Report) Document(windowFrom, reportPath string, seq int) (doc.Calibration, error) {
	kappa := func(c stats.Coefficient) float64 {
		if !c.Defined() {
			return doc.KappaUnmeasured
		}
		return c.Float()
	}
	human := r.Validity[ReferenceHuman]
	calibration := doc.Calibration{
		Judge:        r.Judge,
		Day:          r.Day,
		Seq:          seq,
		Title:        fmt.Sprintf("%s judged by %s: %s", r.Suite, r.Judge, r.Verdict),
		Date:         r.Day,
		Pool:         r.Pool,
		WindowFrom:   windowFrom,
		WindowTo:     r.Day,
		NItems:       r.Items,
		TieHandling:  vocab.TieAbstainAsCategory,
		SwapKappa:    kappa(r.Swap.Agreement.Kappa),
		RerunKappa:   kappa(r.Rerun.Agreement.Kappa),
		HumanKappa:   kappa(human.Primary.Kappa),
		NHuman:       human.Primary.N,
		Verdict:      r.Verdict,
		Report:       reportPath,
		RescoredFrom: r.RescoredFrom,
		Body:         r.Summary(),
	}
	if err := calibration.Validate(); err != nil {
		return doc.Calibration{}, err
	}
	return calibration, nil
}

// Summary is the human-readable body of the document: every number with the
// tie handling it was computed under, and the three claims kept apart.
func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Judge `%s` over suite `%s`, %d item(s) at %d seed(s) each.\n",
		r.Judge, r.Suite, r.Items, r.Seeds)
	if r.Source != "" {
		fmt.Fprintf(&b, "Candidates: %s, from %s (%s).\n", r.Pool, r.Source, r.License)
	}
	if r.RescoredFrom != "" {
		fmt.Fprintf(&b, "\n**The judge was not asked anything.** Every call below was read back "+
			"out of the runs `%s` recorded and re-aggregated under the harness's current "+
			"selection rule. The answers are the ones that judge gave in that window; what is "+
			"measured here is the rule over them, and a coefficient in this document is "+
			"comparable with the source's only in that light.\n", r.RescoredFrom)
	}
	b.WriteString("\n## Consistency, which is not validity\n\n")
	fmt.Fprintf(&b, "- swap: %d pair(s) judged both ways, %d decided both times, %d flipped — "+
		"flip rate %.3f under `%s`, %s. kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %s, %d row(s) over %d item(s)), %s.\n",
		r.Swap.Pairs, r.Swap.Decided, r.Swap.Flips, r.Swap.Rate.Value,
		vocab.TieDecidedOnly, interval(r.Swap.Rate.CI, r.Swap.Rate.Level, r.Swap.Rate.Method),
		coefficient(r.Swap.Agreement.Kappa), r.Swap.Agreement.TieHandling,
		r.Swap.Agreement.PO, r.Swap.Agreement.PE, coefficient(r.Swap.Agreement.PABAK),
		r.Swap.Agreement.N, r.Swap.Agreement.Clusters,
		interval(r.Swap.Agreement.CI, r.Swap.Agreement.Level, r.Swap.Agreement.CIMethod))
	fmt.Fprintf(&b, "- re-run: %d comparison(s) across %d seed(s). kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %s), %s.\n",
		r.Rerun.Comparisons, r.Rerun.Seeds, coefficient(r.Rerun.Agreement.Kappa),
		r.Rerun.Agreement.TieHandling, r.Rerun.Agreement.PO, r.Rerun.Agreement.PE,
		coefficient(r.Rerun.Agreement.PABAK),
		interval(r.Rerun.Agreement.CI, r.Rerun.Agreement.Level, r.Rerun.Agreement.CIMethod))
	b.WriteString("\nBoth are the judge against itself. The two readings are not independent,\n")
	b.WriteString("which pushes observed agreement up, so neither is a two-rater reliability\n")
	b.WriteString("and neither is evidence that the judge is measuring the right thing.\n")
	b.WriteString("\nThe re-run arm moves no candidate. Both orders of every pair are asked at\n")
	b.WriteString("every seed, so a second seed reorders nothing; what it moves is the nonce\n")
	b.WriteString("inside the candidate fences. The re-run coefficient is therefore the same\n")
	b.WriteString("decision under an irrelevant-token perturbation, together with whatever the\n")
	b.WriteString("server does differently at temperature 0, and it does not separate the two.\n")

	r.position(&b)

	b.WriteString("\n## Validity, which is the judge against people\n\n")
	for _, reference := range []string{ReferenceHuman, ReferenceGold, ReferenceLabels} {
		v, ok := r.Validity[reference]
		if !ok || v.Primary.N == 0 {
			continue
		}
		fmt.Fprintf(&b, "- against `%s` (%s): kappa %s under `%s` over %d item(s) "+
			"(p_o %.3f, p_e %.3f, PABAK %s, %s); "+
			"kappa %s under `%s` over %d item(s).\n",
			reference, labelerSet(v), coefficient(v.Primary.Kappa), v.Primary.TieHandling,
			v.Primary.N, v.Primary.PO, v.Primary.PE, coefficient(v.Primary.PABAK),
			interval(v.Primary.CI, v.Primary.Level, v.Primary.CIMethod),
			coefficient(v.Secondary.Kappa), v.Secondary.TieHandling, v.Secondary.N)
	}
	r.judgeDecided(&b)
	if r.HumanHuman != nil {
		fmt.Fprintf(&b, "- ceiling: %s agree at kappa %s under `%s` over %d shared item(s). "+
			"A judge is read against this, not against 1.0.\n",
			strings.Join(r.HumanHuman.Labelers, " and "), coefficient(r.HumanHuman.Kappa),
			r.HumanHuman.TieHandling, r.HumanHuman.N)
	} else {
		b.WriteString("- ceiling: not measured. No two people labelled the same item, so how far\n" +
			"  the labels themselves agree is unknown, and the coefficients above have no\n" +
			"  upper bound to be read against.\n")
	}
	if r.AllBadLabels > 0 {
		fmt.Fprintf(&b, "- %d label(s) said none of the three was worth choosing. "+
			"They count as abstentions in the coefficients above.\n", r.AllBadLabels)
	}

	b.WriteString("\n## What the judge did\n\n")
	for _, kind := range slices.Sorted(keysOf(r.Outcomes.ByKind)) {
		fmt.Fprintf(&b, "- `%s`: %d of %d run(s)\n", kind, r.Outcomes.ByKind[kind], r.Outcomes.Total)
	}
	for _, reason := range slices.Sorted(keysOf(r.Outcomes.NoCandidateByReason)) {
		fmt.Fprintf(&b, "  - no candidate, `%s`: %d\n", reason, r.Outcomes.NoCandidateByReason[reason])
	}
	fmt.Fprintf(&b, "  - no candidate, over measured items: %.3f, %s\n",
		r.Outcomes.NoCandidateRate.Value,
		interval(r.Outcomes.NoCandidateRate.CI, r.Outcomes.NoCandidateRate.Level,
			r.Outcomes.NoCandidateRate.Method))
	r.rules(&b)
	fmt.Fprintf(&b, "- %d judge call(s), %d retried for an unreadable answer (%.3f).\n",
		r.Outcomes.Calls, r.Outcomes.InvalidRetries, r.Outcomes.InvalidRetryRate)
	fmt.Fprintf(&b, "- %d run(s) and %d item(s) measured nothing — a timeout, a failed judge or a "+
		"harness that would not run. They are in no coefficient above: %.1f%% of the suite, "+
		"against a %.1f%% budget.\n",
		r.Outcomes.Unmeasured, r.Unmeasured, 100*r.UnmeasuredRate, 100*r.MaxUnmeasured)
	fmt.Fprintf(&b, "- median run %d ms, longest %d ms.\n", r.Latency.MedianMS, r.Latency.MaxMS)
	if len(r.Strata) > 0 {
		var bands []string
		for _, name := range slices.Sorted(keysOf(r.Strata)) {
			bands = append(bands, fmt.Sprintf("%s %d", name, r.Strata[name]))
		}
		fmt.Fprintf(&b, "- margin strata: %s. Kappa is sensitive to the mix, so it is recorded.\n",
			strings.Join(bands, ", "))
	}

	r.comparison(&b)

	fmt.Fprintf(&b, "\n## Verdict: %s\n\n", r.Verdict)
	switch {
	case r.OverUnmeasuredBudget:
		fmt.Fprintf(&b, "%.1f%% of the suite produced no judgement at all, over the %.1f%% this\n"+
			"calibration will stand behind. The numbers above are what was left, and what a\n"+
			"failing harness drops is not a random sample of a suite, so they are reported\n"+
			"rather than concluded from.\n", 100*r.UnmeasuredRate, 100*r.MaxUnmeasured)
	case r.Verdict == vocab.CalibratedUnmeasured:
		b.WriteString("Nobody has compared this judge with people on this suite, so its\n" +
			"reliability is all that was measured, and reliability is not validity.\n")
	case r.Verdict == vocab.CalibratedYes:
		fmt.Fprintf(&b, "Agreement with people reached the %.3f threshold under `%s`.\n",
			r.MinKappa, vocab.TieAbstainAsCategory)
	case r.Verdict == vocab.CalibratedNo:
		fmt.Fprintf(&b, "Agreement with people did not reach the %.3f threshold under `%s`.\n",
			r.MinKappa, vocab.TieAbstainAsCategory)
	}
	fmt.Fprintf(&b, "\nThis record carries force for %d days after the window closes, and then\n"+
		"stops on its own. A judge running on an expired calibration is a judge nobody\n"+
		"has checked lately; `uzushio judge status` is where that shows up.\n", doc.ValidityDays)
	return b.String()
}

// position is the position-bias part of the document body: the bias read off
// the single calls, which way the pairs that contradicted themselves fell, and
// what the indecision cost the suite.
//
// Every count says what it is over. The three denominators here — a decided
// call, a disagreeing pair, a run — are different populations of different
// sizes, and a number lifted out of this section against the wrong one is
// wrong by a factor rather than by a rounding.
func (r Report) position(b *strings.Builder) {
	p := r.Swap.Position
	b.WriteString("\n### Position bias\n\n")
	fmt.Fprintf(b, "- the judge named the answer it was shown first in %d of %d decided "+
		"call(s) — a call whose status was `ok` and which named one of the two answers, so "+
		"a tie and an unreadable answer are in neither the numerator nor the denominator. "+
		"p_first %s over %d item(s), %s. bias %s from the 0.500 of a judge with no position "+
		"preference.\n",
		p.FirstChosen, p.DecidedCalls, coefficient(p.PFirst), p.Clusters,
		interval(p.CI, p.Level, p.CIMethod), coefficient(p.Bias))
	d := r.Swap.DisagreeBreakdown
	fmt.Fprintf(b, "- %d of %d swap disagreement(s) — pairs whose two orders both named a "+
		"candidate and did not name the same one — were the judge choosing whichever answer "+
		"came first; %d chose whichever came second, %d were neither.\n",
		d.BothFirst, d.Pairs, d.BothSecond, d.Other)

	pooled := r.AbstentionByGold.Pooled
	if pooled.Runs == 0 {
		return
	}
	b.WriteString("- what the judge did against what the people decided, " +
		"as `gold decided`/`gold tie`:\n")
	fmt.Fprintf(b, "  - pooled, %d run(s): %s\n", pooled.Runs, coverageLine(pooled))
	for _, table := range r.AbstentionByGold.BySeed {
		fmt.Fprintf(b, "  - seed %d, %d run(s): %s\n", table.Seed, table.Runs, coverageLine(table))
	}
	if pooled.Ungolded > 0 {
		fmt.Fprintf(b, "  - %d run(s) are in no column: their item carries no human label.\n",
			pooled.Ungolded)
	}
	fmt.Fprintf(b, "- %d of the %d run(s) on an item the people had decided reached no "+
		"candidate. That is the coverage the bias above costs: those items are not missing "+
		"data, they are disagreements with the people in the validity table below.\n",
		pooled.AbstainedOnDecided, pooled.GoldDecided)
}

// coverageLine renders one cross-table's rows for prose.
func coverageLine(t Coverages) string {
	parts := make([]string, 0, len(t.Rows))
	for _, row := range t.Rows {
		parts = append(parts, fmt.Sprintf("`%s` %d/%d", row.Outcome, row.GoldDecided, row.GoldTie))
	}
	return strings.Join(parts, ", ")
}

// labelerSet says where a validity coefficient's human side came from, which
// for the mixed reference is two answers rather than one.
func labelerSet(v Validity) string {
	switch {
	case v.FromGold > 0 && v.FromLabels > 0:
		return fmt.Sprintf("%d from the corpus's annotators, %d from a labels file",
			v.FromGold, v.FromLabels)
	case v.FromLabels > 0:
		return "a labels file"
	case v.FromGold > 0:
		return "the corpus's own annotators"
	}
	return "nothing"
}

// interval renders an interval for prose, naming its level and how it was
// made, and saying plainly where there is none. A width printed without those
// is a number a reader will take on trust.
func interval(ci *stats.Interval, level float64, method string) string {
	if ci == nil {
		return "no interval (too few independent units to resample)"
	}
	return fmt.Sprintf("%.0f%% %s CI [%.3f, %.3f]", 100*level, method, ci.Lo, ci.Hi)
}

// coefficient renders a coefficient for prose, naming the undefined one.
func coefficient(c stats.Coefficient) string {
	if !c.Defined() {
		return "`" + vocab.KappaUnmeasured + "`"
	}
	return fmt.Sprintf("%.3f", c.Float())
}

// keysOf yields a map's keys for slices.Sorted, which is the one place this
// package reads one.
func keysOf(m map[string]int) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// judgeDecided is the validity line for the items the judge was asked about,
// which is a different set from the items the selector answered.
//
// It is printed apart from the three references above because it is not a
// fourth set of labels: it is the same comparison with the runs no judge
// answered for left out, and the number worth reading beside it is how many
// items that removed.
func (r Report) judgeDecided(b *strings.Builder) {
	v, ok := r.Validity[ReferenceHumanJudged]
	if !ok || v.Primary.N == 0 {
		return
	}
	human, ok := r.Validity[ReferenceHuman]
	if !ok {
		return
	}
	dropped := human.Primary.N - v.Primary.N
	if dropped == 0 {
		return
	}
	fmt.Fprintf(b, "- against `human`, over the %d item(s) the judge was actually asked about "+
		"(%d dropped, settled by the candidates agreeing with each other): kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %s, %s); kappa %s under `%s` over %d item(s). "+
		"This is the judge's own validity, and it is the reading that stays comparable with a "+
		"calibration made before the harness settled anything without asking.\n",
		v.Primary.N, dropped, coefficient(v.Primary.Kappa), v.Primary.TieHandling,
		v.Primary.PO, v.Primary.PE, coefficient(v.Primary.PABAK),
		interval(v.Primary.CI, v.Primary.Level, v.Primary.CIMethod),
		coefficient(v.Secondary.Kappa), v.Secondary.TieHandling, v.Secondary.N)
}

// rules is which stage of the selection rule settled how many runs.
//
// The distribution is the rule, read off the runs. A run the candidates
// agreed on cost no judge call; a Condorcet sweep and a score that had to be
// broken are different findings about the same six answers, and folding
// them into one `selected` count is what made the earlier report unable to
// say which.
func (r Report) rules(b *strings.Builder) {
	if len(r.Outcomes.ByRule) == 0 {
		return
	}
	for _, rule := range RuleRows {
		n, ok := r.Outcomes.ByRule[rule]
		if !ok {
			continue
		}
		fmt.Fprintf(b, "  - selected by `%s`: %d\n", rule, n)
		switch rule {
		case RuleConsensus:
			for _, kind := range slices.Sorted(keysOf(r.Outcomes.ByConsensus)) {
				fmt.Fprintf(b, "    - agreement `%s`: %d\n", kind, r.Outcomes.ByConsensus[kind])
			}
		case RuleTieBreak:
			for _, key := range slices.Sorted(keysOf(r.Outcomes.ByTieBreak)) {
				fmt.Fprintf(b, "    - key `%s`: %d\n", key, r.Outcomes.ByTieBreak[key])
			}
		}
	}
	if r.Outcomes.Judgeless > 0 {
		fmt.Fprintf(b, "  - %d run(s) asked the judge nothing at all.\n", r.Outcomes.Judgeless)
	}
}

// comparison puts a rescoring beside the calibration it re-read.
//
// Both columns are over the same four hundred recorded calls, so every
// difference in it is the rule and nothing else: not the fleet, not the day,
// not the seed. That is the one thing a rescoring can say that a fresh
// measurement cannot, and it is why the table is here rather than in a
// commit message.
func (r Report) comparison(b *strings.Builder) {
	before := r.Rescored
	if before == nil {
		return
	}
	fmt.Fprintf(b, "\n## Against %s, over the same calls\n\n", before.Day)
	fmt.Fprintf(b, "Both columns read the same %d recorded call(s) of the same %d run(s). "+
		"Every difference below is the selection rule and nothing else — not the fleet, "+
		"not the day, not the seed.\n\n", before.Calls, before.Items*before.Seeds)
	fmt.Fprintf(b, "| | %s | %s |\n|---|---|---|\n", before.Day, r.Day)
	row := func(what, was, now string) { fmt.Fprintf(b, "| %s | %s | %s |\n", what, was, now) }

	for _, kind := range slices.Sorted(keysOf(union(before.ByKind, r.Outcomes.ByKind))) {
		row("`"+kind+"`", count(before.ByKind[kind]), count(r.Outcomes.ByKind[kind]))
	}
	for _, reason := range slices.Sorted(keysOf(union(before.ByReason, r.Outcomes.NoCandidateByReason))) {
		row("no candidate, `"+reason+"`",
			count(before.ByReason[reason]), count(r.Outcomes.NoCandidateByReason[reason]))
	}
	for _, rule := range RuleRows {
		if before.ByRule[rule] == 0 && r.Outcomes.ByRule[rule] == 0 {
			continue
		}
		row("selected by `"+rule+"`", count(before.ByRule[rule]), count(r.Outcomes.ByRule[rule]))
	}
	for _, key := range slices.Sorted(keysOf(r.Outcomes.ByTieBreak)) {
		row("tie-break key `"+key+"`", count(0), count(r.Outcomes.ByTieBreak[key]))
	}
	for _, kind := range slices.Sorted(keysOf(r.Outcomes.ByConsensus)) {
		row("consensus `"+kind+"`", count(0), count(r.Outcomes.ByConsensus[kind]))
	}
	row("judge calls", count(before.Calls), count(r.Outcomes.Calls))
	row("swap kappa", doc.Kappa(before.SwapKappa), coefficient(r.Swap.Agreement.Kappa))
	row("re-run kappa", doc.Kappa(before.RerunKappa), coefficient(r.Rerun.Agreement.Kappa))
	row("human kappa, `"+string(vocab.TieAbstainAsCategory)+"`",
		doc.Kappa(before.HumanKappa), coefficient(r.HumanKappa))
	row("human kappa, `"+string(vocab.TieDecidedOnly)+"`",
		fmt.Sprintf("%s over %d", doc.Kappa(before.HumanDecided), before.HumanDecidedItems),
		decidedOnly(r.Validity[ReferenceHuman]))
	if v, ok := r.Validity[ReferenceHumanJudged]; ok && v.Primary.N > 0 {
		row("human kappa, judge-decided items only", "the same "+doc.Kappa(before.HumanKappa),
			fmt.Sprintf("%s over %d", coefficient(v.Primary.Kappa), v.Primary.N))
	}
	row("run time, summed", duration(before.TotalMS), duration(r.Latency.TotalMS))
	if n := before.SweptAgree + before.SweptDiffer; n > 0 {
		fmt.Fprintf(b, "\nThe replay checks itself where the two rules must agree. On the %d "+
			"run(s) the new rule settled with a Condorcet winner and no consensus, the old "+
			"rule required the same sweep of the same six answers and had to name the same "+
			"candidate: %d did and %d did not. A difference there would be a call that came "+
			"back differently, and no call was made again.\n",
			n, before.SweptAgree, before.SweptDiffer)
	}
	r.whatMoved(b, before)
}

// whatMoved says which of the numbers above is still about the judge.
//
// It is the paragraph the table cannot be read without. The three
// coefficients are computed the same way they always were, and they have
// stopped meaning the same thing: the harness now answers where it used to
// abstain, so `human` is the agreement of the whole selector with the
// people, over a different set of decisions taken on the same items from
// the same calls. Reading the fall in `decided-only` as a judge that got
// worse is the mistake this paragraph exists to prevent.
func (r Report) whatMoved(b *strings.Builder, before *Rescored) {
	selected := r.Outcomes.ByKind[OutcomeSelected]
	fmt.Fprintf(b, "\nThe judge answered the same %d call(s) in both columns, so nothing here "+
		"is the judge changing its mind. What changed is what the harness does with those "+
		"answers: it now returns a candidate on %d of %d run(s) where it returned one on %d, "+
		"and %d run(s) it settled without asking at all. So `human kappa` above is the "+
		"agreement of the **whole selector** with the people rather than of the judge, and "+
		"it is computed over a different set of decisions on the same items.\n",
		before.Calls, selected, r.Outcomes.Total, before.ByKind[OutcomeSelected],
		r.Outcomes.Judgeless)
	human, ok := r.Validity[ReferenceHuman]
	if !ok {
		return
	}
	fmt.Fprintf(b, "\nThe `decided-only` row is where that shows most sharply: the "+
		"denominator went from %d item(s) to %d, because the items it used to drop are the "+
		"ones the judge could not part and the rule now parts for it. A coefficient over "+
		"the hard items included is not comparable with one over the easy items alone, and "+
		"the movement from %s to %s is the price of deciding them rather than a judge that "+
		"got worse.\n",
		before.HumanDecidedItems, human.Secondary.N,
		doc.Kappa(before.HumanDecided), coefficient(human.Secondary.Kappa))
	judged, ok := r.Validity[ReferenceHumanJudged]
	if !ok || judged.Primary.N == 0 {
		return
	}
	fmt.Fprintf(b, "\nThe row that is still the judge alone is the judge-decided one: %s "+
		"over the %d item(s) it was asked about, against %s over %d. The calls are the same "+
		"calls, so the judge's own validity had little room to move, and what it moved by is "+
		"the %d item(s) the candidates settled between themselves. That is the number to "+
		"compare with %s when the question is the judge and not the harness.\n",
		coefficient(judged.Primary.Kappa), judged.Primary.N,
		doc.Kappa(before.HumanKappa), before.Items,
		human.Primary.N-judged.Primary.N, before.Day)
}

// union is the key set of two count maps.
func union(a, b map[string]int) map[string]int {
	out := map[string]int{}
	for k := range a {
		out[k] = 0
	}
	for k := range b {
		out[k] = 0
	}
	return out
}

// count writes a count the way a table column does.
func count(n int) string { return strconv.Itoa(n) }

// decidedOnly is a validity's secondary reading, with the sample size that
// makes it readable: the coefficient alone moves when the denominator does.
func decidedOnly(v Validity) string {
	return fmt.Sprintf("%s over %d", coefficient(v.Secondary.Kappa), v.Secondary.N)
}

// duration writes a span of judge time in the units a person compares.
func duration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
}
