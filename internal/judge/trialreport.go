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

// TrialReport is the comparison.
type TrialReport struct {
	SchemaVersion int       `json:"schema_version"`
	Card          TrialCard `json:"card"`
	At            string    `json:"at"`
	// What was planned and what happened to it.
	Planned     int `json:"planned_items"`
	Completed   int `json:"completed_items"`
	Interrupted int `json:"interrupted_items"`
	SkippedStep int `json:"skipped_steps_resumed"`
	// Why the run ended, in the stable value and in the memo's word.
	StopReason      string `json:"stop_reason"`
	StopReasonLabel string `json:"stop_reason_label"`
	// The wall clock, split the way the memo asks for it.
	Phases       TrialPhases `json:"phases"`
	TEvalSeconds float64     `json:"t_eval_seconds"`
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

// TrialPhases is where the wall clock went.
type TrialPhases struct {
	// LoadSeconds is reading the card, the suite and the reuse index.
	LoadSeconds float64 `json:"load_seconds"`
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
}

// TrialQuality is the quality half of the comparison.
type TrialQuality struct {
	Metric   string `json:"metric"`
	Handling string `json:"handling"`
	// Measured says a quality number could be computed at all.
	Measured bool `json:"measured"`
	// Evaluable is the items both conditions answered and the people had
	// named a position on.
	Evaluable int `json:"evaluable_items"`
	// NoReference is items with no human position, Unmeasured items the
	// machine lost on one side or the other.
	NoReference int `json:"no_reference_items"`
	Unmeasured  int `json:"unmeasured_items"`
	// QBase and QNew are shares in 0..1, DeltaPoints their difference in
	// points of a hundred.
	QBase       float64 `json:"q_base"`
	QNew        float64 `json:"q_new"`
	DeltaPoints float64 `json:"delta_q_points"`
	// NewlyWrong and NewlyRight are the items the change moved each way.
	NewlyWrong int `json:"newly_wrong"`
	NewlyRight int `json:"newly_right"`
	// Sets is the same arithmetic per item set. D and R are never averaged
	// together: R is a set of items chosen because they already go wrong.
	Sets map[string]TrialSetQuality `json:"sets,omitempty"`
	// Weighted is D re-weighted by the manifest's target shares, absent where
	// the manifest declares none.
	Weighted *TrialWeighted `json:"weighted_d,omitempty"`
	// Note says why a number is missing, where one is.
	Note string `json:"note,omitempty"`
}

// TrialSetQuality is one set's numbers.
type TrialSetQuality struct {
	Set         string  `json:"set"`
	Evaluable   int     `json:"evaluable_items"`
	QBase       float64 `json:"q_base"`
	QNew        float64 `json:"q_new"`
	DeltaPoints float64 `json:"delta_q_points"`
}

// TrialWeighted is the D set re-weighted onto the target population.
type TrialWeighted struct {
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
		Card:          r.redactedCard(),
		At:            r.now().UTC().Format(time.RFC3339),
		SkippedStep:   r.skipped,
		Phases: TrialPhases{
			LoadSeconds:    round3(r.loadSeconds),
			MeasureSeconds: round3(r.measureSeconds),
			WaitSeconds:    round3(measured.Sub(started).Seconds() - r.loadSeconds - r.measureSeconds),
		},
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
		if record.WallSeconds != nil {
			wall = append(wall, *record.WallSeconds)
		}
		latency += float64(record.LatencyMS)
	}
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
	q := TrialQuality{Metric: MetricSelectionTop1, Handling: TrialQualityHandling}
	var base, newer []float64
	perSet := map[string][]([2]int){}
	for _, item := range items {
		switch {
		case item.Reference == "":
			q.NoReference++
			continue
		case item.QBase == nil || item.QNew == nil:
			q.Unmeasured++
			continue
		}
		q.Evaluable++
		base = append(base, float64(*item.QBase))
		newer = append(newer, float64(*item.QNew))
		perSet[item.Set] = append(perSet[item.Set], [2]int{*item.QBase, *item.QNew})
		switch {
		case *item.QBase == 1 && *item.QNew == 0:
			q.NewlyWrong++
		case *item.QBase == 0 && *item.QNew == 1:
			q.NewlyRight++
		}
	}
	if q.Evaluable == 0 {
		q.Metric = MetricPairJudge
		q.Note = "no item in this trial carries a human label naming a position, so no " +
			"selection top-1 score exists. Where the only human evidence is pair " +
			"preferences the quantity is a pair-judge agreement, which is a different " +
			"claim and does not stand in for selection top-1. This run measured " +
			"behaviour and time, and did not measure quality."
		return q
	}
	q.Measured = true
	q.QBase, q.QNew = round3(mean(base)), round3(mean(newer))
	q.DeltaPoints = round3(100 * (mean(newer) - mean(base)))
	q.Sets = map[string]TrialSetQuality{}
	for set, pairs := range perSet {
		var b, n []float64
		for _, pair := range pairs {
			b = append(b, float64(pair[0]))
			n = append(n, float64(pair[1]))
		}
		q.Sets[set] = TrialSetQuality{
			Set: set, Evaluable: len(pairs),
			QBase: round3(mean(b)), QNew: round3(mean(n)),
			DeltaPoints: round3(100 * (mean(n) - mean(b))),
		}
	}
	q.Weighted = r.weighted(items)
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
	if !report.Quality.Measured {
		out.Value = DecisionInconclusive
		out.Why = append(out.Why, "quality was not measured on these items: "+report.Quality.Note)
		if report.Speed.Comparable && report.Speed.GJudge != nil {
			out.Why = append(out.Why, fmt.Sprintf(
				"time was measured: G_judge %.3f over %d item(s)", *report.Speed.GJudge, report.Speed.Items))
		}
		return out
	}
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

	b.WriteString("## Cost\n\n")
	fmt.Fprintf(&b, "- T_eval %.1f s: load %.1f, wait %.1f, measure %.1f, aggregate %.1f.\n",
		t.TEvalSeconds, t.Phases.LoadSeconds, t.Phases.WaitSeconds,
		t.Phases.MeasureSeconds, t.Phases.AggregateSeconds)
	fmt.Fprintf(&b, "- reuse %.0f%% (%d step(s) read back, %d measured) under kind `%s`.\n",
		100*t.Reuse.Rate, t.Reuse.Reused, t.Reuse.Measured, t.Reuse.Kind)
	if len(t.Reuse.KeyMismatch) > 0 {
		fmt.Fprintf(&b, "- a saved run was rejected on: %s. A run whose key differs is not "+
			"this base condition.\n", strings.Join(t.Reuse.KeyMismatch, ", "))
	}
	fmt.Fprintf(&b, "- new inference: %d run(s), %d judge call(s), %d invalid-output retr(ies).\n\n",
		t.Inference.Runs, t.Inference.Calls, t.Inference.Retries)

	b.WriteString("## Quality\n\n")
	fmt.Fprintf(&b, "Metric `%s` under handling `%s`.\n\n", t.Quality.Metric, t.Quality.Handling)
	if t.Quality.Measured {
		fmt.Fprintf(&b, "- q(base) %.3f, q(new) %.3f, **ΔQ %+.1f pt** over %d evaluable item(s).\n",
			t.Quality.QBase, t.Quality.QNew, t.Quality.DeltaPoints, t.Quality.Evaluable)
		fmt.Fprintf(&b, "- %d item(s) newly wrong, %d newly right.\n",
			t.Quality.NewlyWrong, t.Quality.NewlyRight)
		fmt.Fprintf(&b, "- %d item(s) carried no human position and %d lost a side to the "+
			"machine; neither is in the number above.\n",
			t.Quality.NoReference, t.Quality.Unmeasured)
		for _, set := range sortedSetKeys(t.Quality.Sets) {
			s := t.Quality.Sets[set]
			fmt.Fprintf(&b, "- set %s (shown on its own, never averaged into another): "+
				"q %.3f → %.3f, ΔQ %+.1f pt over %d item(s).\n",
				s.Set, s.QBase, s.QNew, s.DeltaPoints, s.Evaluable)
		}
		if t.Quality.Weighted != nil {
			fmt.Fprintf(&b, "- D re-weighted onto the manifest's target shares: "+
				"q %.3f → %.3f, ΔQ %+.1f pt.\n", t.Quality.Weighted.QBase,
				t.Quality.Weighted.QNew, t.Quality.Weighted.DeltaPoints)
			if len(t.Quality.Weighted.Unevaluated) > 0 {
				fmt.Fprintf(&b, "- 未評価 / unevaluated strata (fewer than %d evaluable items): %s.\n",
					t.Card.MinItemsPerCategory, strings.Join(t.Quality.Weighted.Unevaluated, ", "))
			}
		}
	} else {
		fmt.Fprintf(&b, "- not measured. %s\n", t.Quality.Note)
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
