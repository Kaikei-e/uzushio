package judge

// The comparison a trial returns.
//
// Every number here is a point estimate over a handful of items, and the
// wording is chosen so that it cannot be read as anything else. There is no
// confidence interval, no significance and no non-inferiority claim: at forty
// items one item is 2.5 points, and a stage that can move its own headline
// number by moving one item has not confirmed anything. What the report is for
// is deciding which change is worth measuring properly.

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// The two things a quality number over these items can be.
const (
	// MetricSelectionTop1 is the judge's chosen candidate against the one the
	// people chose. It needs a human label naming a position.
	MetricSelectionTop1 = "selection_top1"
	// MetricPairJudge is what is left when the only human evidence is pair
	// preferences. It is named separately because it is a different quantity,
	// and it does not stand in for selection top-1.
	MetricPairJudge = "pair_judge"
)

// TrialQualityHandling names what this report did with the answers that are
// not a choice of candidate. It travels with every quality number for the same
// reason it travels with a calibration's: the same verdicts scored two
// defensible ways are two different estimands.
//
// Under this handling an item counts only where the people named a position.
// A judge that reached `no_candidate` there scores zero — an unreadable answer
// is a defined failure of the selector, not an abstention that excuses it —
// and a run the machine lost (`judge_timeout`, `judge_failed`) is in no score
// at all and is counted on its own line.
const TrialQualityHandling = "human-position-only, failure-as-zero"

// What a trial.json holds: the plan, written before the first call, or the
// result, which overwrites it.
const (
	RecordedPlan   = "plan"
	RecordedResult = "result"
)

// TrialReport is the comparison.
type TrialReport struct {
	SchemaVersion int `json:"schema_version"`
	// Recorded says whether this file is the plan or the result.
	Recorded string    `json:"recorded"`
	Card     TrialCard `json:"card"`
	At       string    `json:"at"`
	// Plan is the fixed execution order and the composition it comes out at,
	// written to disk before the first call and repeated here afterwards.
	Plan TrialPlanRecord `json:"plan"`
	// What was planned and what happened to it.
	Planned     int `json:"planned_items"`
	Completed   int `json:"completed_items"`
	Interrupted int `json:"interrupted_items"`
	SkippedStep int `json:"skipped_steps_resumed"`
	// Why the run ended, in the stable value and in the memo's word.
	StopReason      string `json:"stop_reason"`
	StopReasonLabel string `json:"stop_reason_label"`
	// The wall clock, split the way the memo asks for it.
	Phases       TrialPhases         `json:"phases"`
	TEvalSeconds float64             `json:"t_eval_seconds"`
	Switches     []TrialSwitchRecord `json:"switches,omitempty"`
	// What was reused and what was newly inferred.
	Reuse     TrialReuseStats `json:"reuse"`
	Inference TrialInference  `json:"inference"`
	// Per condition.
	Conditions map[string]TrialConditionStats `json:"conditions"`
	// The comparison itself.
	Quality           TrialQuality  `json:"quality"`
	Speed             TrialSpeed    `json:"speed"`
	ChangedSelections []TrialChange `json:"changed_selections"`
	Items             []TrialItem   `json:"items"`
	// Suggested is what the rules point at. `decision` is not written here:
	// it is a person's, and it belongs in the card of the next experiment.
	Suggested TrialSuggestion `json:"suggested"`
	Notes     []string        `json:"notes,omitempty"`
}

// TrialPlanRecord is what was fixed before the first call: which items, in
// which order, and what the prefix is made of.
//
// It is in the file for one reason. A stage runs a prefix of a set, and a
// prefix whose order was decided after somebody saw a result is a prefix
// chosen for its answer. The order here is the manifests' own; the
// composition is counted from it, so a prefix that is four items of one
// category says so in the same file as the numbers.
type TrialPlanRecord struct {
	// Take is how many items of each set the card asked for, and Items the
	// order that produced.
	Take  map[string]int     `json:"take,omitempty"`
	Items []TrialPlannedItem `json:"items"`
	// Sets, Categories and LengthBins are the composition of the prefix.
	Sets       map[string]int `json:"sets"`
	Categories map[string]int `json:"categories,omitempty"`
	LengthBins map[string]int `json:"length_bins,omitempty"`
	// Estimate is the card's own pre-run arithmetic, a 見積り throughout.
	Estimate TrialEstimate `json:"estimate"`
}

// TrialPlannedItem is one item of the fixed execution order.
type TrialPlannedItem struct {
	Position  int    `json:"position"`
	Item      string `json:"item"`
	Set       string `json:"set"`
	Category  string `json:"category,omitempty"`
	LengthBin string `json:"length_bin,omitempty"`
}

// TrialSwitchRecord is one condition switch: what it cost and whether it
// worked.
type TrialSwitchRecord struct {
	Condition   string  `json:"condition"`
	ConditionID string  `json:"condition_id"`
	Seconds     float64 `json:"seconds"`
	At          string  `json:"at"`
	Error       string  `json:"error,omitempty"`
}

// TrialPhases is where the wall clock went.
type TrialPhases struct {
	// LoadSeconds is reading the card, the suite and the reuse index.
	LoadSeconds float64 `json:"load_seconds"`
	// SwitchSeconds is the restarts and ready waits that put the fleet into
	// each condition. It is inside T_eval because the memo's T_eval is load,
	// wait, measure and aggregate: an experiment whose inference fits the box
	// only when the two restarts around it are left out has not fitted the
	// box. First-time preparation — a download, a compile, a second binary —
	// is not here and is not in T_eval; it is a preparation cost of its own.
	SwitchSeconds float64 `json:"switch_seconds"`
	// MeasureSeconds is the sum of the harness calls.
	MeasureSeconds float64 `json:"measure_seconds"`
	// WaitSeconds is what is left over inside the run: process start-up,
	// journal writes, and whatever the trial was doing that was not a call.
	WaitSeconds float64 `json:"wait_seconds"`
	// AggregateSeconds is building this report.
	AggregateSeconds float64 `json:"aggregate_seconds"`
}

// TrialReuseStats is how much of the base was read rather than asked.
type TrialReuseStats struct {
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"`
	Reused   int    `json:"reused_steps"`
	Measured int    `json:"measured_steps"`
	// Rate is reused over all completed steps.
	Rate float64 `json:"rate"`
	// KeyMismatch names the reuse-key fields that rejected the first saved
	// run the trial looked at, where one was rejected.
	KeyMismatch []string `json:"key_mismatch,omitempty"`
}

// TrialInference is what the run cost the fleet.
type TrialInference struct {
	Runs    int `json:"new_runs"`
	Calls   int `json:"new_judge_calls"`
	Retries int `json:"invalid_output_retries"`
}

// TrialConditionStats is one condition's own summary.
type TrialConditionStats struct {
	ID       string `json:"id"`
	Config   string `json:"config"`
	Steps    int    `json:"steps"`
	Selected int    `json:"selected"`
	// NoCandidate and Unmeasured are the two failures kept apart: the first is
	// the selector reaching no answer, the second is the machine.
	NoCandidate int `json:"no_candidate"`
	Unmeasured  int `json:"unmeasured"`
	Errors      int `json:"errors"`
	// Coverage is selected over steps.
	Coverage float64 `json:"coverage"`
	// MeanWallSeconds and MedianWallSeconds are over the steps this trial
	// measured itself; nil where none were.
	MeanWallSeconds   *float64 `json:"mean_wall_seconds"`
	MedianWallSeconds *float64 `json:"median_wall_seconds"`
	// RecordedLatencyMS is the mean latency the traces carry, reused ones
	// included. It is a diagnostic and not a term in any speed comparison.
	RecordedLatencyMS float64 `json:"recorded_latency_ms_mean"`
	// Consensus is the steps the candidates settled between themselves, and
	// TieBreaks the steps a key had to part, by stage and key.
	Consensus int `json:"consensus_steps"`
	// TieBreaks counts (stage, key) pairs from judge.json's own fields —
	// `consensus` and `tie_break.key` — and never from `outcome.reason`. The
	// two are not the same count: a consensus group parted by a hash carries
	// the key in the structured field and the word `consensus` in the
	// sentence, so counting sentences finds 87 where counting fields finds
	// 91, and the 2026-09-06 review had to resolve that difference by hand.
	TieBreaks []TrialTieBreakRow `json:"tie_breaks,omitempty"`
}

// The stage a tie-break happened in: before the judge was asked, or at the top
// of the score after it was.
const (
	TieBreakStageConsensus = "consensus"
	TieBreakStageCopeland  = "copeland"
)

// TrialTieBreakRow is one (stage, key) count.
type TrialTieBreakRow struct {
	Stage string `json:"stage"`
	Key   string `json:"key"`
	Steps int    `json:"steps"`
}

// TrialQuality is the quality half of the comparison.
type TrialQuality struct {
	Metric   string `json:"metric"`
	Handling string `json:"handling"`
	// Basis is D for development stages A/B, and H for confirmation stage C.
	Basis string `json:"basis"`
	// Measured requires the representative set's item and stratum floors.
	Measured bool `json:"measured"`
	// These counts cover Basis only. Other sets keep their own diagnostics.
	Evaluable   int `json:"evaluable_items"`
	NoReference int `json:"no_reference_items"`
	Unmeasured  int `json:"unmeasured_items"`
	// The representative quality, weighted when D declares target shares.
	QBase       float64 `json:"q_base"`
	QNew        float64 `json:"q_new"`
	DeltaPoints float64 `json:"delta_q_points"`
	// D+R changes for the stage-A heuristic, independent of representative quality.
	NewlyWrong int `json:"newly_wrong"`
	NewlyRight int `json:"newly_right"`
	// Set-level diagnostics never fill another set's quality denominator.
	Sets map[string]TrialSetQuality `json:"sets,omitempty"`
	// Weighted contains D's target-stratum evidence, including missing strata.
	Weighted *TrialWeighted `json:"weighted_d,omitempty"`
	Note     string         `json:"note,omitempty"`
}

// TrialSetQuality is one set's numbers.
type TrialSetQuality struct {
	Set string `json:"set"`
	// Measured says this set has at least one human-position comparison. It is
	// independent of the trial-wide floor, so an R diagnostic remains useful
	// when D has not yet measured representative quality.
	Measured    bool    `json:"measured"`
	Evaluable   int     `json:"evaluable_items"`
	NoReference int     `json:"no_reference_items"`
	Unmeasured  int     `json:"unmeasured_items"`
	QBase       float64 `json:"q_base"`
	QNew        float64 `json:"q_new"`
	DeltaPoints float64 `json:"delta_q_points"`
	NewlyWrong  int     `json:"newly_wrong"`
	NewlyRight  int     `json:"newly_right"`
}

// TrialWeighted is the D set re-weighted onto the target population.
type TrialWeighted struct {
	// Measured is true only when every declared target stratum has enough D
	// items. A partial re-weighting would silently change the target population.
	Measured    bool    `json:"measured"`
	QBase       float64 `json:"q_base"`
	QNew        float64 `json:"q_new"`
	DeltaPoints float64 `json:"delta_q_points"`
	// Strata is each bucket's contribution, and Unevaluated names the buckets
	// with too few items to give one.
	Strata      map[string]TrialStratum `json:"strata"`
	Unevaluated []string                `json:"unevaluated"`
}

// TrialStratum is one weight bucket.
type TrialStratum struct {
	Items  int     `json:"items"`
	Share  float64 `json:"share"`
	QBase  float64 `json:"q_base"`
	QNew   float64 `json:"q_new"`
	Enough bool    `json:"enough_items"`
}

// TrialSpeed is the time half of the comparison.
type TrialSpeed struct {
	// Basis says which quantity was compared. `judge` is the only one a trial
	// can produce: it re-runs the judge over saved candidates, so the number
	// is G_judge and it is not the round's G_T.
	Basis string `json:"basis"`
	// Comparable says both sides were measured in this run.
	Comparable bool     `json:"comparable"`
	Items      int      `json:"items"`
	MeanBase   *float64 `json:"mean_base_seconds"`
	MeanNew    *float64 `json:"mean_new_seconds"`
	MedianBase *float64 `json:"median_base_seconds"`
	MedianNew  *float64 `json:"median_new_seconds"`
	// GJudge is 1 − mean(new)/mean(base) over the retry-inclusive wall times.
	GJudge *float64 `json:"g_judge"`
	Note   string   `json:"note,omitempty"`
}

// TrialChange is one item whose selection moved.
type TrialChange struct {
	Item      string `json:"item"`
	Set       string `json:"set"`
	Base      string `json:"base"`
	New       string `json:"new"`
	Reference string `json:"reference,omitempty"`
}

// TrialItem is one item's line of the comparison.
type TrialItem struct {
	Item          string   `json:"item"`
	Set           string   `json:"set"`
	Reference     string   `json:"reference,omitempty"`
	ReferenceFrom string   `json:"reference_from,omitempty"`
	Bucket        string   `json:"bucket,omitempty"`
	BaseOutcome   string   `json:"base_outcome,omitempty"`
	NewOutcome    string   `json:"new_outcome,omitempty"`
	BaseChoice    string   `json:"base_choice,omitempty"`
	NewChoice     string   `json:"new_choice,omitempty"`
	QBase         *int     `json:"q_base"`
	QNew          *int     `json:"q_new"`
	D             *int     `json:"d"`
	BaseSeconds   *float64 `json:"base_seconds"`
	NewSeconds    *float64 `json:"new_seconds"`
	BaseSource    string   `json:"base_source,omitempty"`
	NewSource     string   `json:"new_source,omitempty"`
	Complete      bool     `json:"complete"`
}

// TrialSuggestion is what the card's own rules point at.
type TrialSuggestion struct {
	Value string `json:"value"`
	// Why lists the rules that fired, in the order they were checked.
	Why []string `json:"why"`
	// Heuristic is set where the suggestion rests on the two-item development
	// rule, which is a way of ordering work and not a demonstration of
	// anything about the change.
	Heuristic string `json:"heuristic,omitempty"`
	// MinDeltaQPoints and MaxTimeRatio are the thresholds it was read against.
	MinDeltaQPoints float64 `json:"min_delta_q_points"`
	MaxTimeRatio    float64 `json:"max_time_ratio"`
}

// assemble turns the journal into the comparison.
func (r *trialRun) assemble(plan []TrialStep, started, measured time.Time) TrialReport {
	card := r.opts.Card
	report := TrialReport{
		SchemaVersion: TrialSchemaVersion,
		Recorded:      RecordedResult,
		Card:          r.redactedCard(),
		At:            r.now().UTC().Format(time.RFC3339),
		SkippedStep:   r.skipped,
		Phases: TrialPhases{
			LoadSeconds:    round3(r.loadSeconds),
			SwitchSeconds:  round3(r.switchSeconds),
			MeasureSeconds: round3(r.measureSeconds),
			WaitSeconds: round3(measured.Sub(started).Seconds() -
				r.loadSeconds - r.measureSeconds - r.switchSeconds),
		},
		Switches:          r.switches,
		Conditions:        map[string]TrialConditionStats{},
		ChangedSelections: []TrialChange{},
		Items:             []TrialItem{},
	}
	if report.Phases.WaitSeconds < 0 {
		report.Phases.WaitSeconds = 0
	}

	byStep := map[TrialStep]TrialRecord{}
	for _, record := range r.records {
		byStep[record.Key()] = record
	}
	planned := plannedItems(plan)
	report.Planned = len(planned)

	report.Conditions[ConditionBase] = r.conditionStats(ConditionBase)
	report.Conditions[ConditionCandidate] = r.conditionStats(ConditionCandidate)
	report.Reuse = r.reuseStats()
	report.Inference = r.inference()

	items, complete := r.items(planned, byStep)
	report.Items = items
	report.Completed = complete
	report.Interrupted = report.Planned - complete
	report.Quality = r.quality(items)
	report.Speed = speedOf(items)
	report.ChangedSelections = changedSelections(items)
	report.StopReason, report.Notes = r.stop(report)
	report.StopReasonLabel = TrialStopLabel(report.StopReason)
	report.Suggested = suggest(card, report)
	return report
}

// redactedCard is the card as a committed report may carry it.
//
// A card names a suite, its manifests and two harness configurations by
// whatever path its author typed, and those paths are that machine's layout:
// a home directory, a checkout, a local configuration nobody else has. The
// report is a file somebody commits. So every path in the echoed card is
// rewritten relative to the vault, or reduced to its own name where it lies
// outside it. Nothing a reader needs is lost — the name is what identifies a
// condition — and the machine does not travel with the numbers.
func (r *trialRun) redactedCard() TrialCard {
	card := r.opts.Card
	relative := func(name string) string {
		if name == "" {
			return ""
		}
		return RecordPath(name, r.opts.Vault, r.opts.Suite.Dir, card.Dir)
	}
	card.Suite = relative(card.Path(card.Suite))
	manifests := make([]string, 0, len(card.Manifests))
	for _, name := range card.Manifests {
		manifests = append(manifests, relative(card.Path(name)))
	}
	card.Manifests = manifests
	labels := make([]string, 0, len(card.Labels))
	for _, name := range card.Labels {
		labels = append(labels, relative(card.Path(name)))
	}
	card.Labels = labels
	card.Base.Config = relative(card.Path(card.Base.Config))
	card.Candidate.Config = relative(card.Path(card.Candidate.Config))
	card.Reuse.Source = relative(card.Path(card.Reuse.Source))
	return card
}

// planRecord is the fixed order and its composition.
func (r *trialRun) planRecord(plan []TrialStep) TrialPlanRecord {
	strata := map[string]map[string]string{}
	for _, manifest := range Taken(r.opts.Card, r.opts.Manifests) {
		for _, item := range manifest.Items {
			strata[item.ID] = item.Strata
		}
	}
	out := TrialPlanRecord{
		Take: r.opts.Card.Take, Items: []TrialPlannedItem{},
		Sets: map[string]int{}, Categories: map[string]int{}, LengthBins: map[string]int{},
		Estimate: Estimate(r.opts.Card, r.opts.Manifests),
	}
	for _, step := range plannedItems(plan) {
		entry := TrialPlannedItem{
			Position: len(out.Items) + 1, Item: step.Item, Set: step.Set,
			Category: strata[step.Item]["category"], LengthBin: strata[step.Item]["length_bin"],
		}
		out.Items = append(out.Items, entry)
		out.Sets[entry.Set]++
		if entry.Category != "" {
			out.Categories[entry.Category]++
		}
		if entry.LengthBin != "" {
			out.LengthBins[entry.LengthBin]++
		}
	}
	return out
}

func plannedItems(plan []TrialStep) []TrialStep {
	var out []TrialStep
	seen := map[string]bool{}
	for _, step := range plan {
		if seen[step.Item] {
			continue
		}
		seen[step.Item] = true
		out = append(out, TrialStep{Item: step.Item, Set: step.Set})
	}
	return out
}

func (r *trialRun) conditionStats(condition string) TrialConditionStats {
	stats := TrialConditionStats{ID: r.conditionID(condition), Config: r.configName(condition)}
	var wall []float64
	var latency float64
	keys := map[[2]string]int{}
	for _, record := range r.records {
		if record.Condition != condition {
			continue
		}
		stats.Steps++
		switch {
		case record.Error != "":
			stats.Errors++
			stats.Unmeasured++
		case record.Outcome == OutcomeSelected:
			stats.Selected++
		case record.Outcome == OutcomeNoCandidate:
			stats.NoCandidate++
		default:
			stats.Unmeasured++
		}
		if record.Consensus != "" {
			stats.Consensus++
		}
		if record.TieBreakKey != "" {
			stage := TieBreakStageCopeland
			if record.Consensus != "" {
				stage = TieBreakStageConsensus
			}
			keys[[2]string{stage, record.TieBreakKey}]++
		}
		if record.WallSeconds != nil {
			wall = append(wall, *record.WallSeconds)
		}
		latency += float64(record.LatencyMS)
	}
	stats.TieBreaks = tieBreakRows(keys)
	if stats.Steps > 0 {
		stats.Coverage = round3(float64(stats.Selected) / float64(stats.Steps))
		stats.RecordedLatencyMS = round3(latency / float64(stats.Steps))
	}
	if len(wall) > 0 {
		stats.MeanWallSeconds = ptr(round3(mean(wall)))
		stats.MedianWallSeconds = ptr(round3(median(wall)))
	}
	return stats
}

// configName is the condition's configuration file as a report may carry it:
// the base name only. A local configuration lives wherever its owner keeps it,
// and a committed report has no business naming that place.
func (r *trialRun) configName(condition string) string {
	return filepath.Base(r.config(condition))
}

// tieBreakRows orders the (stage, key) counts: consensus before copeland,
// then the key alphabetically, so two reports of the same run print the same
// table.
func tieBreakRows(counts map[[2]string]int) []TrialTieBreakRow {
	if len(counts) == 0 {
		return nil
	}
	out := make([]TrialTieBreakRow, 0, len(counts))
	for pair, n := range counts {
		out = append(out, TrialTieBreakRow{Stage: pair[0], Key: pair[1], Steps: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (r *trialRun) reuseStats() TrialReuseStats {
	stats := TrialReuseStats{
		Kind: r.opts.Card.Reuse.Kind,
		Source: RecordPath(r.opts.Card.Path(r.opts.Card.Reuse.Source),
			r.opts.Vault, r.opts.Suite.Dir, r.opts.Card.Dir),
		KeyMismatch: r.mismatch,
	}
	for _, record := range r.records {
		if record.Source == SourceReused {
			stats.Reused++
		} else {
			stats.Measured++
		}
	}
	if total := stats.Reused + stats.Measured; total > 0 {
		stats.Rate = round3(float64(stats.Reused) / float64(total))
	}
	return stats
}

func (r *trialRun) inference() TrialInference {
	var out TrialInference
	for _, record := range r.records {
		if record.Source != SourceMeasured || record.Error != "" {
			continue
		}
		out.Runs++
		out.Calls += record.Calls
		out.Retries += record.InvalidRetries
	}
	return out
}

// items joins the two conditions per item and scores them.
func (r *trialRun) items(planned []TrialStep, byStep map[TrialStep]TrialRecord) ([]TrialItem, int) {
	labels := byItem(r.opts.Labels)
	buckets := r.buckets()
	var out []TrialItem
	complete := 0
	for _, step := range planned {
		item := TrialItem{Item: step.Item, Set: step.Set, Bucket: buckets[step.Item]}
		reference, from := r.reference(step.Item, labels)
		item.Reference, item.ReferenceFrom = reference, from
		base, hasBase := byStep[TrialStep{Item: step.Item, Set: step.Set, Condition: ConditionBase}]
		newer, hasNew := byStep[TrialStep{Item: step.Item, Set: step.Set, Condition: ConditionCandidate}]
		if hasBase {
			item.BaseOutcome, item.BaseChoice = base.Outcome, base.Candidate
			item.BaseSeconds, item.BaseSource = base.WallSeconds, base.Source
			item.QBase = score(base, reference)
		}
		if hasNew {
			item.NewOutcome, item.NewChoice = newer.Outcome, newer.Candidate
			item.NewSeconds, item.NewSource = newer.WallSeconds, newer.Source
			item.QNew = score(newer, reference)
		}
		item.Complete = hasBase && hasNew && base.Error == "" && newer.Error == ""
		if item.Complete {
			complete++
		}
		if item.QBase != nil && item.QNew != nil {
			item.D = ptr(*item.QNew - *item.QBase)
		}
		out = append(out, item)
	}
	return out, complete
}

// buckets is each item's weight bucket, from whichever manifest holds it.
func (r *trialRun) buckets() map[string]string {
	out := map[string]string{}
	for _, manifest := range r.opts.Manifests {
		for _, item := range manifest.Items {
			bucket, err := manifest.Bucket(item)
			if err != nil {
				continue
			}
			out[item.ID] = bucket
		}
	}
	return out
}

// reference is the human answer for one item: the person's label where there
// is one, and the corpus's derived label otherwise. Only a position counts;
// `tie` and `all_bad` are not a target a selector can hit.
func (r *trialRun) reference(item string, labels map[string][]Label) (string, string) {
	if given := labels[item]; len(given) > 0 {
		if category := given[0].Category(); slices.Contains(Positions, category) {
			return category, ReferenceLabels
		}
		return "", ""
	}
	task, ok := r.tasks[item]
	if !ok {
		return "", ""
	}
	gold, found, err := r.opts.Suite.GoldOf(task)
	if err != nil || !found {
		return "", ""
	}
	if category := gold.Category(); slices.Contains(Positions, category) {
		return category, ReferenceGold
	}
	return "", ""
}

// score is one condition's answer on one item under TrialQualityHandling.
func score(record TrialRecord, reference string) *int {
	if reference == "" {
		return nil
	}
	if record.Error != "" {
		return nil
	}
	switch record.Outcome {
	case OutcomeSelected:
		if record.Candidate == reference {
			return ptr(1)
		}
		return ptr(0)
	case OutcomeNoCandidate:
		// A defined failure. The selector was asked and reached nothing; that
		// is a zero rather than an excused item, because excusing it is how a
		// change that abstains more often comes out looking better.
		return ptr(0)
	}
	// judge_timeout and judge_failed are the machine, not the judgement.
	return nil
}

func (r *trialRun) quality(items []TrialItem) TrialQuality {
	basis := SetD
	if r.opts.Card.Stage == StageC {
		basis = SetH
	}
	q := TrialQuality{
		Metric: MetricSelectionTop1, Handling: TrialQualityHandling, Basis: basis,
		Sets: map[string]TrialSetQuality{},
	}
	type totals struct {
		base, newer             []float64
		noReference, unmeasured int
		newlyWrong, newlyRight  int
	}
	perSet := map[string]*totals{}
	anyEvaluable := 0
	for _, item := range items {
		set := perSet[item.Set]
		if set == nil {
			set = &totals{}
			perSet[item.Set] = set
		}
		switch {
		case item.Reference == "":
			set.noReference++
			continue
		case item.QBase == nil || item.QNew == nil:
			set.unmeasured++
			continue
		}
		anyEvaluable++
		set.base = append(set.base, float64(*item.QBase))
		set.newer = append(set.newer, float64(*item.QNew))
		switch {
		case *item.QBase == 1 && *item.QNew == 0:
			set.newlyWrong++
		case *item.QBase == 0 && *item.QNew == 1:
			set.newlyRight++
		}
	}
	for name, set := range perSet {
		out := TrialSetQuality{
			Set: name, Evaluable: len(set.base), NoReference: set.noReference,
			Unmeasured: set.unmeasured, NewlyWrong: set.newlyWrong, NewlyRight: set.newlyRight,
		}
		if out.Evaluable > 0 {
			out.Measured = true
			out.QBase, out.QNew = round3(mean(set.base)), round3(mean(set.newer))
			out.DeltaPoints = round3(100 * (mean(set.newer) - mean(set.base)))
		}
		q.Sets[name] = out
	}
	// A's two-item heuristic remains a development count across D and R.
	for _, name := range []string{SetD, SetR} {
		q.NewlyWrong += q.Sets[name].NewlyWrong
		q.NewlyRight += q.Sets[name].NewlyRight
	}
	representative := q.Sets[basis]
	q.Evaluable = representative.Evaluable
	q.NoReference, q.Unmeasured = representative.NoReference, representative.Unmeasured
	if basis == SetD {
		q.Weighted = r.weighted(items)
	}
	if anyEvaluable == 0 {
		q.Metric = MetricPairJudge
		q.Note = "no evaluable human-position comparison exists. Pair-judge agreement " +
			"is a different quantity and does not establish selection top-1 quality."
		return q
	}
	if q.Evaluable == 0 {
		q.Note = fmt.Sprintf("未評価 / not evaluated: representative set %s has no evaluable items. "+
			"Other sets are diagnostics and cannot supply its quality denominator.", basis)
		return q
	}
	if q.Evaluable < r.opts.Card.MinEvaluableItems {
		q.Note = fmt.Sprintf("未評価 / not evaluated: representative set %s has %d evaluable item(s), "+
			"below the card's floor of %d. One item is %.1f points in the raw set. "+
			"No representative quality difference is reported; set diagnostics and individual changes remain available.",
			basis, q.Evaluable, r.opts.Card.MinEvaluableItems, 100/float64(q.Evaluable))
		return q
	}
	if q.Weighted != nil {
		if !q.Weighted.Measured {
			q.Note = "未評価 / not evaluated: D's target strata do not all reach min_items_per_category. " +
				"A partial re-weighting would change the target population; raw set diagnostics remain available."
			return q
		}
		q.QBase, q.QNew, q.DeltaPoints = q.Weighted.QBase, q.Weighted.QNew, q.Weighted.DeltaPoints
	} else {
		q.QBase, q.QNew, q.DeltaPoints = representative.QBase, representative.QNew, representative.DeltaPoints
	}
	q.Measured = true
	return q
}

// weighted re-weights the D set onto the target population the manifest
// declares. A bucket with too few evaluable items gets no number: printing a
// mean over two items invites somebody to read it as one.
func (r *trialRun) weighted(items []TrialItem) *TrialWeighted {
	shares := map[string]float64{}
	for _, manifest := range r.opts.Manifests {
		if manifest.Set != SetD {
			continue
		}
		for stratum, share := range manifest.Weights {
			shares[stratum] = share
		}
	}
	if len(shares) == 0 {
		return nil
	}
	base := map[string][]float64{}
	newer := map[string][]float64{}
	for _, item := range items {
		if item.Set != SetD || item.Bucket == "" || item.QBase == nil || item.QNew == nil {
			continue
		}
		base[item.Bucket] = append(base[item.Bucket], float64(*item.QBase))
		newer[item.Bucket] = append(newer[item.Bucket], float64(*item.QNew))
	}
	out := TrialWeighted{Strata: map[string]TrialStratum{}, Unevaluated: []string{}}
	var total, weightedBase, weightedNew float64
	for _, stratum := range sortedFloatKeys(shares) {
		enough := len(base[stratum]) >= r.opts.Card.MinItemsPerCategory
		out.Strata[stratum] = TrialStratum{
			Items: len(base[stratum]), Share: shares[stratum],
			QBase: round3(mean(base[stratum])), QNew: round3(mean(newer[stratum])),
			Enough: enough,
		}
		if !enough {
			out.Unevaluated = append(out.Unevaluated, stratum)
			continue
		}
		total += shares[stratum]
		weightedBase += shares[stratum] * mean(base[stratum])
		weightedNew += shares[stratum] * mean(newer[stratum])
	}
	if total == 0 {
		return &out
	}
	if len(out.Unevaluated) > 0 {
		return &out
	}
	out.Measured = true
	out.QBase = round3(weightedBase / total)
	out.QNew = round3(weightedNew / total)
	out.DeltaPoints = round3(100 * (weightedNew/total - weightedBase/total))
	return &out
}

func sortedFloatKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// speedOf compares the two conditions' wall times, and refuses to where one
// side was read off disk.
func speedOf(items []TrialItem) TrialSpeed {
	speed := TrialSpeed{Basis: "judge"}
	var base, newer []float64
	for _, item := range items {
		if item.BaseSeconds == nil || item.NewSeconds == nil {
			continue
		}
		base = append(base, *item.BaseSeconds)
		newer = append(newer, *item.NewSeconds)
	}
	speed.Items = len(base)
	if len(base) == 0 {
		speed.Note = "the base condition was read back from saved runs, and a wall time " +
			"recorded on another day under another load is not this trial's base. A speed " +
			"claim needs both conditions measured in the same session; run the card with " +
			"reuse kind `none` to get one."
		return speed
	}
	speed.Comparable = true
	speed.MeanBase, speed.MeanNew = ptr(round3(mean(base))), ptr(round3(mean(newer)))
	speed.MedianBase, speed.MedianNew = ptr(round3(median(base))), ptr(round3(median(newer)))
	if mean(base) > 0 {
		speed.GJudge = ptr(round3(1 - mean(newer)/mean(base)))
	}
	speed.Note = "G_judge, from retry-inclusive mean wall time over the items both " +
		"conditions were measured on. It is the judge stage only and is not the " +
		"round's G_T. The median is beside it; p95 is not reported, because a tail " +
		"read off this many items is not a tail."
	return speed
}

func changedSelections(items []TrialItem) []TrialChange {
	out := []TrialChange{}
	for _, item := range items {
		if !item.Complete {
			continue
		}
		if item.BaseChoice == item.NewChoice && item.BaseOutcome == item.NewOutcome {
			continue
		}
		out = append(out, TrialChange{
			Item: item.Item, Set: item.Set, Reference: item.Reference,
			Base: outcomeWord(item.BaseOutcome, item.BaseChoice),
			New:  outcomeWord(item.NewOutcome, item.NewChoice),
		})
	}
	return out
}

func outcomeWord(outcome, candidate string) string {
	if outcome == OutcomeSelected && candidate != "" {
		return candidate
	}
	return outcome
}

// stop decides which of the memo's stopping words this run ended under.
func (r *trialRun) stop(report TrialReport) (string, []string) {
	var notes []string
	broken := 0
	for _, record := range r.records {
		if record.Condition == ConditionCandidate &&
			(record.Error != "" || record.Reason == "invalid_output") {
			broken++
		}
	}
	if broken > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d candidate step(s) produced no readable judgement. Under the memo's first "+
				"stopping rule that is a malfunction: fix it and re-try under a new "+
				"condition identifier rather than reading the numbers below.", broken))
		return StopMalfunction, notes
	}
	if r.overBudget || report.Interrupted > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d of %d planned item(s) were not completed. The budget stopped new work; "+
				"this is the evaluation running out of time and not the model failing, "+
				"and the comparison below is over the items that finished — which are "+
				"the fast ones.", report.Interrupted, report.Planned))
		return StopOutOfBudget, notes
	}
	if report.Completed == 0 {
		return StopUndetermined, notes
	}
	return StopCompleted, notes
}

// suggest reads the card's rules against the numbers. It suggests; it does not
// decide, and it never says `adopt`.
func suggest(card TrialCard, report TrialReport) TrialSuggestion {
	minDeltaQ, maxTimeRatio := card.Rules.Thresholds()
	out := TrialSuggestion{MinDeltaQPoints: minDeltaQ, MaxTimeRatio: maxTimeRatio}
	advance := DecisionAdvance
	if card.Stage != StageA {
		advance = DecisionFinalist
	}
	switch report.StopReason {
	case StopMalfunction:
		out.Value = DecisionDrop
		out.Why = append(out.Why, "動作不良 / malfunction: a candidate step produced no readable "+
			"judgement, so this condition is not measurable as written")
		return out
	case StopOutOfBudget:
		out.Value = DecisionInconclusive
		out.Why = append(out.Why, "時間・資源切れ / out of budget: an interrupted set is a biased "+
			"set, and a run stopped by its own clock decides nothing about the change")
		return out
	}
	// The early cut is read before the quality gate, not after it. It counts
	// items that moved rather than dividing by them, it is the memo's rule for
	// the stage whose sets are too small to divide by, and a floor that
	// switched it off would switch off the one rule stage A has.
	if card.Stage == StageA && report.Quality.NewlyWrong-report.Quality.NewlyRight >= 2 {
		out.Value = DecisionDrop
		out.Heuristic = "早い見切り / early cut: at stage A a change that newly loses two more " +
			"items than it rescues drops down the queue whatever it does for time. This is a " +
			"development heuristic for ordering work, not a demonstration that the change is " +
			"worse, and a good change may be missed by it."
		out.Why = append(out.Why, fmt.Sprintf("%d newly wrong against %d newly right",
			report.Quality.NewlyWrong, report.Quality.NewlyRight))
		return out
	}
	if !report.Quality.Measured {
		out.Value = DecisionInconclusive
		out.Why = append(out.Why, "quality was not measured on these items: "+report.Quality.Note)
		if report.Speed.Comparable && report.Speed.GJudge != nil {
			out.Why = append(out.Why, fmt.Sprintf(
				"time was measured: G_judge %.3f over %d item(s)", *report.Speed.GJudge, report.Speed.Items))
		}
		return out
	}
	qualityOK := report.Quality.DeltaPoints >= minDeltaQ
	out.Why = append(out.Why, fmt.Sprintf("ΔQ %+.1f pt against a threshold of %+.1f pt over %d "+
		"evaluable item(s) — a point estimate, with no interval and no claim of "+
		"non-inferiority", report.Quality.DeltaPoints, minDeltaQ, report.Quality.Evaluable))
	if card.Rules.Kind == RuleOutputFix {
		if qualityOK {
			out.Value = advance
			out.Why = append(out.Why, "an output-fix change is read on whether the named failures "+
				"stopped and whether quality fell; check the targets by hand")
		} else {
			out.Value = DecisionDrop
			out.Why = append(out.Why, "見込みなし / no prospect: the repair cost quality on D")
		}
		return out
	}
	if !report.Speed.Comparable {
		out.Value = DecisionInconclusive
		out.Why = append(out.Why, "no comparable time — "+strings.TrimSuffix(report.Speed.Note, "."))
		return out
	}
	ratio := math.NaN()
	if report.Speed.MeanBase != nil && *report.Speed.MeanBase > 0 && report.Speed.MeanNew != nil {
		ratio = *report.Speed.MeanNew / *report.Speed.MeanBase
	}
	timeOK := !math.IsNaN(ratio) && ratio <= maxTimeRatio
	out.Why = append(out.Why, fmt.Sprintf("mean time ratio %.3f against a ceiling of %.3f over "+
		"%d item(s)", ratio, maxTimeRatio, report.Speed.Items))
	switch {
	case qualityOK && timeOK:
		out.Value = advance
	case !qualityOK && !timeOK:
		out.Value = DecisionDrop
		out.Why = append(out.Why, "見込みなし / no prospect: neither axis moved the way the card "+
			"said it would. A near difference is not `the same`; it is a change with no "+
			"measured reason to spend more on it")
	default:
		out.Value = DecisionInconclusive
		out.Why = append(out.Why, "one axis met its threshold and the other did not")
	}
	return out
}

// Summary is the short Markdown note beside the JSON.
func (t TrialReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Trial `%s` — stage %s\n\n", t.Card.ID, t.Card.Stage)
	fmt.Fprintf(&b, "**Hypothesis.** %s\n\n", t.Card.Hypothesis)
	fmt.Fprintf(&b, "**The one change.** %s\n\n", t.Card.Change)
	fmt.Fprintf(&b, "Base `%s` against candidate `%s`, seed %d, judge seed %d.\n",
		t.Card.Base.ID, t.Card.Candidate.ID, t.Card.Seed, t.Card.JudgeSeed)
	fmt.Fprintf(&b, "%d item(s) planned, %d completed, %d interrupted; stopped as `%s` (%s).\n\n",
		t.Planned, t.Completed, t.Interrupted, t.StopReason, t.StopReasonLabel)

	b.WriteString("## The order, fixed before the first call\n\n")
	fmt.Fprintf(&b, "- %d item(s): %s.\n", len(t.Plan.Items), countWords(t.Plan.Sets))
	if len(t.Plan.Categories) > 0 {
		fmt.Fprintf(&b, "- categories: %s.\n", countWords(t.Plan.Categories))
	}
	if len(t.Plan.LengthBins) > 0 {
		fmt.Fprintf(&b, "- length bins: %s.\n", countWords(t.Plan.LengthBins))
	}
	if t.Plan.Estimate.ItemSeconds > 0 {
		fmt.Fprintf(&b, "- %s: %.0f s against a %d s budget; the cap is %d item(s) and "+
			"the budget affords %d.\n", t.Plan.Estimate.Label, t.Plan.Estimate.Seconds,
			t.Plan.Estimate.Budget, t.Plan.Estimate.Cap, t.Plan.Estimate.Affordable)
	}
	b.WriteString("\n")

	b.WriteString("## Cost\n\n")
	fmt.Fprintf(&b, "- T_eval %.1f s: load %.1f, switch %.1f, wait %.1f, measure %.1f, "+
		"aggregate %.1f.\n", t.TEvalSeconds, t.Phases.LoadSeconds, t.Phases.SwitchSeconds,
		t.Phases.WaitSeconds, t.Phases.MeasureSeconds, t.Phases.AggregateSeconds)
	if len(t.Switches) > 0 {
		fmt.Fprintf(&b, "- %d condition switch(es), inside T_eval. First-time preparation "+
			"— a download, a compile, a second binary — is not in this number and is not "+
			"in T_eval.\n", len(t.Switches))
	}
	fmt.Fprintf(&b, "- reuse %.0f%% (%d step(s) read back, %d measured) under kind `%s`.\n",
		100*t.Reuse.Rate, t.Reuse.Reused, t.Reuse.Measured, t.Reuse.Kind)
	if len(t.Reuse.KeyMismatch) > 0 {
		fmt.Fprintf(&b, "- a saved run was rejected on: %s. A run whose key differs is not "+
			"this base condition.\n", strings.Join(t.Reuse.KeyMismatch, ", "))
	}
	fmt.Fprintf(&b, "- new inference: %d run(s), %d judge call(s), %d invalid-output retr(ies).\n\n",
		t.Inference.Runs, t.Inference.Calls, t.Inference.Retries)

	b.WriteString("## Quality\n\n")
	fmt.Fprintf(&b, "Metric `%s` under handling `%s`; representative set **%s**.\n\n",
		t.Quality.Metric, t.Quality.Handling, t.Quality.Basis)
	if t.Quality.Measured {
		fmt.Fprintf(&b, "- representative q(base) %.3f, q(new) %.3f, **ΔQ %+.1f pt** over %d evaluable item(s).\n",
			t.Quality.QBase, t.Quality.QNew, t.Quality.DeltaPoints, t.Quality.Evaluable)
	} else {
		fmt.Fprintf(&b, "- representative quality not measured. %s\n", t.Quality.Note)
	}
	fmt.Fprintf(&b, "- representative set: %d without a human position, %d with an unmeasured side.\n",
		t.Quality.NoReference, t.Quality.Unmeasured)
	if t.Card.Stage == StageA {
		fmt.Fprintf(&b, "- D+R early-cut counts: %d newly wrong, %d newly right (development heuristic).\n",
			t.Quality.NewlyWrong, t.Quality.NewlyRight)
	}
	for _, set := range sortedSetKeys(t.Quality.Sets) {
		s := t.Quality.Sets[set]
		if !s.Measured {
			fmt.Fprintf(&b, "- set %s diagnostic: no scored items; %d without a human position, %d unmeasured.\n",
				s.Set, s.NoReference, s.Unmeasured)
			continue
		}
		role := "diagnostic (raw, not the representative threshold)"
		if s.Set == t.Quality.Basis && t.Quality.Measured && t.Quality.Weighted == nil {
			role = "representative (unweighted)"
		}
		fmt.Fprintf(&b, "- set %s %s: q %.3f → %.3f, ΔQ %+.1f pt over %d item(s); "+
			"%d newly wrong, %d newly right.\n",
			s.Set, role, s.QBase, s.QNew, s.DeltaPoints, s.Evaluable, s.NewlyWrong, s.NewlyRight)
	}
	if w := t.Quality.Weighted; w != nil {
		if w.Measured && t.Quality.Measured {
			fmt.Fprintf(&b, "- representative quality uses complete weighted D: q %.3f → %.3f, ΔQ %+.1f pt.\n",
				w.QBase, w.QNew, w.DeltaPoints)
		}
		if len(w.Unevaluated) > 0 {
			fmt.Fprintf(&b, "- 未評価 / unevaluated strata (fewer than %d evaluable items): %s.\n",
				t.Card.MinItemsPerCategory, strings.Join(w.Unevaluated, ", "))
		}
	}
	b.WriteString("\n## Time\n\n")
	if t.Speed.Comparable && t.Speed.GJudge != nil {
		fmt.Fprintf(&b, "- mean %.2f s → %.2f s, median %.2f s → %.2f s, **G_judge %.3f** "+
			"over %d item(s).\n", *t.Speed.MeanBase, *t.Speed.MeanNew,
			*t.Speed.MedianBase, *t.Speed.MedianNew, *t.Speed.GJudge, t.Speed.Items)
		fmt.Fprintf(&b, "- %s\n", t.Speed.Note)
	} else {
		fmt.Fprintf(&b, "- **no speed comparison.** %s\n", t.Speed.Note)
	}

	b.WriteString("\n## Coverage and changed selections\n\n")
	for _, name := range []string{ConditionBase, ConditionCandidate} {
		c := t.Conditions[name]
		fmt.Fprintf(&b, "- %s `%s`: %d step(s), coverage %.3f, %d no-candidate, %d unmeasured.\n",
			name, c.ID, c.Steps, c.Coverage, c.NoCandidate, c.Unmeasured)
	}

	if len(t.ChangedSelections) == 0 {
		b.WriteString("- no selection changed.\n")
	}
	for _, change := range t.ChangedSelections {
		fmt.Fprintf(&b, "- `%s` (%s): %s → %s", change.Item, change.Set, change.Base, change.New)
		if change.Reference != "" {
			fmt.Fprintf(&b, ", people said %s", change.Reference)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## How the answers were settled\n\n")
	b.WriteString("Counted from `judge.json`'s own fields — `consensus` and " +
		"`tie_break.key` — and not from `outcome.reason`. A consensus group parted by a " +
		"hash carries the key in the field and the word `consensus` in the sentence, so " +
		"the two counts differ and only one of them is a count of anything.\n\n")
	b.WriteString("| condition | stage | key | steps |\n|---|---|---|---:|\n")
	rows := 0
	for _, name := range []string{ConditionBase, ConditionCandidate} {
		c := t.Conditions[name]
		for _, row := range c.TieBreaks {
			fmt.Fprintf(&b, "| %s `%s` | %s | `%s` | %d |\n", name, c.ID, row.Stage, row.Key, row.Steps)
			rows++
		}
	}
	if rows == 0 {
		b.WriteString("| — | — | no tie-break was recorded | 0 |\n")
	}
	for _, name := range []string{ConditionBase, ConditionCandidate} {
		c := t.Conditions[name]
		fmt.Fprintf(&b, "\n- %s `%s`: %d step(s) settled by consensus before the judge was asked.",
			name, c.ID, c.Consensus)
	}
	b.WriteString("\n")
	b.WriteString("\n## Suggested, not decided\n\n")
	fmt.Fprintf(&b, "**%s.** ", t.Suggested.Value)
	b.WriteString(strings.TrimSuffix(strings.Join(t.Suggested.Why, "; "), "."))
	b.WriteString(".\n")
	if t.Suggested.Heuristic != "" {
		fmt.Fprintf(&b, "\n%s\n", t.Suggested.Heuristic)
	}
	for _, note := range t.Notes {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	b.WriteString("\nThis is a development reading over a small fixed set at one seed. " +
		"It carries no interval, no significance and no non-inferiority claim, and it " +
		"is not evidence that the two conditions are the same. `decision` is written " +
		"by a person, in the card of whatever happens next.\n")
	return b.String()
}

// countWords is a small count map as one line, in a fixed order.
func countWords(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func sortedSetKeys(m map[string]TrialSetQuality) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Write materialises the report, the summary and the journal under dir.
func (t TrialResult) Write(dir string) error {
	body, err := json.MarshalIndent(t.Report, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if err := writeFile(filepath.Join(dir, TrialReportFile), append(body, '\n')); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, TrialSummaryFile), []byte(t.Report.Summary()))
}

func ptr[T any](v T) *T { return &v }

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	half := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[half]
	}
	return (sorted[half-1] + sorted[half]) / 2
}

func round3(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1000) / 1000
}
