package judge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/stats"
)

const (
	AssessmentAdopt        = "adopt"
	AssessmentReject       = "reject"
	AssessmentInconclusive = "inconclusive"
)

// AssessmentReport is a machine recommendation.  It intentionally contains no
// human decision and excludes local configuration paths. The frozen card is
// the provenance record; callers must still apply their own data-handling
// rules to IDs and notes before sharing it.
type AssessmentReport struct {
	SchemaVersion      int                        `json:"schema_version"`
	CardID             string                     `json:"card_id"`
	CardSHA256         string                     `json:"card_sha256"`
	Finalist           string                     `json:"finalist_report_sha256"`
	Holdout            string                     `json:"holdout_sha256"`
	TrialID            string                     `json:"trial_id"`
	BaseID             string                     `json:"base_id"`
	CandidateID        string                     `json:"candidate_id"`
	Recommendation     string                     `json:"recommendation"`
	Rules              AssessmentRules            `json:"rules"`
	Planned            int                        `json:"planned_items"`
	Completed          int                        `json:"completed_items"`
	Clusters           []AssessmentCluster        `json:"clusters,omitempty"`
	Paired             *stats.FixedInterval       `json:"paired_delta,omitempty"`
	CandidateQuality   *stats.FixedInterval       `json:"candidate_quality,omitempty"`
	SeriousRegressions int                        `json:"observed_serious_regressions"`
	AllBadItems        int                        `json:"all_bad_items"`
	UnmeasuredItems    []string                   `json:"unmeasured_items,omitempty"`
	Routes             map[string]AssessmentRoute `json:"routes,omitempty"`
	Notes              []string                   `json:"notes,omitempty"`
}

// AssessmentCluster is the independent unit.  Multiple turns in a task are
// averaged before an interval sees them, so repeated turns cannot shrink it.
type AssessmentCluster struct {
	Cluster   string  `json:"cluster"`
	Items     int     `json:"items"`
	Base      float64 `json:"base_accuracy"`
	Candidate float64 `json:"candidate_accuracy"`
	Delta     float64 `json:"delta"`
}

// AssessmentRoute is a diagnostic only.  It is deliberately not a term in
// the recommendation, because a condition can change which path settles an
// item and that post-treatment grouping is not an adoption estimand.
type AssessmentRoute struct {
	Items          int      `json:"items"`
	Selected       int      `json:"selected"`
	Correct        int      `json:"correct"`
	Unmeasured     int      `json:"unmeasured"`
	WrongConsensus int      `json:"wrong_consensus"`
	Accuracy       *float64 `json:"accuracy,omitempty"`
}

type assessedItem struct {
	id, cluster                 string
	base, candidate             *float64
	baseRecord, candidateRecord TrialRecord
	allBad                      bool
}

func assessReport(card AssessmentCard, dataset *EvaluationDataset, labels map[string]ResolvedEvaluationLabel, trial TrialResult) (AssessmentReport, error) {
	report := AssessmentReport{SchemaVersion: AssessmentSchemaVersion, CardID: card.ID, CardSHA256: card.Digest,
		Finalist: card.Finalist.Report.SHA256, Holdout: card.HoldoutSHA256, TrialID: card.Trial.ID,
		BaseID: card.Trial.Base.ID, CandidateID: card.Trial.Candidate.ID, Rules: card.Rules,
		Planned:        len(dataset.ItemsFor(SetH)),
		Recommendation: AssessmentInconclusive, Routes: map[string]AssessmentRoute{}}
	byRecord := map[TrialStep]TrialRecord{}
	for _, record := range trial.Records {
		byRecord[record.Key()] = record
	}
	clusters := map[string][]assessedItem{}
	for _, item := range dataset.ItemsFor(SetH) {
		label := labels[item.ID]
		base, baseOK := byRecord[TrialStep{Item: item.ID, Set: SetH, Condition: ConditionBase}]
		candidate, candidateOK := byRecord[TrialStep{Item: item.ID, Set: SetH, Condition: ConditionCandidate}]
		row := assessedItem{id: item.ID, cluster: item.Cluster, baseRecord: base, candidateRecord: candidate, allBad: label.AllBad}
		if label.AllBad {
			report.AllBadItems++
		}
		if baseOK && base.Error == "" && base.Source == SourceMeasured && base.Measured {
			row.base = assessmentScore(base, label)
		}
		if candidateOK && candidate.Error == "" && candidate.Source == SourceMeasured && candidate.Measured {
			row.candidate = assessmentScore(candidate, label)
		}
		if row.base == nil || row.candidate == nil {
			report.UnmeasuredItems = append(report.UnmeasuredItems, item.ID)
		} else {
			report.Completed++
			if *row.base == 1 && *row.candidate == 0 {
				report.SeriousRegressions++
			}
		}
		addRoute(report.Routes, base, row.base, "base")
		addRoute(report.Routes, candidate, row.candidate, "candidate")
		clusters[item.Cluster] = append(clusters[item.Cluster], row)
	}
	for key, row := range report.Routes {
		if measured := row.Items - row.Unmeasured; measured > 0 {
			value := float64(row.Correct) / float64(measured)
			row.Accuracy = &value
		}
		report.Routes[key] = row
	}
	sort.Strings(report.UnmeasuredItems)
	for _, cluster := range sortedClusterRows(clusters) {
		if !cluster.complete() {
			continue
		}
		base, candidate := 0.0, 0.0
		for _, item := range cluster.rows {
			base += *item.base
			candidate += *item.candidate
		}
		base /= float64(len(cluster.rows))
		candidate /= float64(len(cluster.rows))
		report.Clusters = append(report.Clusters, AssessmentCluster{Cluster: cluster.name, Items: len(cluster.rows),
			Base: base, Candidate: candidate, Delta: candidate - base})
	}
	if report.SeriousRegressions > card.Rules.MaxObservedSeriousRegressions {
		report.Recommendation = AssessmentReject
		report.Notes = append(report.Notes, fmt.Sprintf("%d observed base-correct to candidate-wrong item(s), above frozen maximum %d", report.SeriousRegressions, card.Rules.MaxObservedSeriousRegressions))
		return report, nil
	}
	if len(report.UnmeasuredItems) > 0 {
		report.Notes = append(report.Notes, "one or more planned H sides were unmeasured; no missing value was scored as zero")
		return report, nil
	}
	if len(report.Clusters) < card.Rules.MinClusters {
		report.Notes = append(report.Notes, fmt.Sprintf("%d task cluster(s), below the frozen minimum %d", len(report.Clusters), card.Rules.MinClusters))
		return report, nil
	}
	delta, quality := make([]float64, 0, len(report.Clusters)), make([]float64, 0, len(report.Clusters))
	for _, cluster := range report.Clusters {
		delta = append(delta, cluster.Delta)
		quality = append(quality, cluster.Candidate)
	}
	// alpha is split between the two intervals. EmpiricalBernstein itself makes
	// its two tails simultaneous, so these two calls give joint 1-alpha coverage.
	paired, err := stats.EmpiricalBernstein(delta, -1, 1, card.Rules.Alpha/2)
	if err != nil {
		return report, err
	}
	candidate, err := stats.EmpiricalBernstein(quality, 0, 1, card.Rules.Alpha/2)
	if err != nil {
		return report, err
	}
	report.Paired, report.CandidateQuality = &paired, &candidate
	if paired.Lower >= -card.Rules.NoninferiorityMargin && candidate.Lower >= card.Rules.QualityFloor {
		report.Recommendation = AssessmentAdopt
		report.Notes = append(report.Notes, "both fixed-sample interval lower bounds clear the frozen adoption rule")
		return report, nil
	}
	if paired.Upper < -card.Rules.NoninferiorityMargin || candidate.Upper < card.Rules.QualityFloor {
		report.Recommendation = AssessmentReject
		report.Notes = append(report.Notes, "at least one fixed-sample interval upper bound misses the frozen rule")
		return report, nil
	}
	report.Notes = append(report.Notes, "the fixed-sample intervals cross a frozen decision boundary")
	return report, nil
}

func assessmentScore(record TrialRecord, label ResolvedEvaluationLabel) *float64 {
	if record.Outcome == OutcomeNoCandidate {
		if record.Reason == "invalid_output" {
			return nil
		}
		return assessmentPtr(0.0)
	}
	if record.Outcome != OutcomeSelected || record.Candidate == "" || !slices.Contains(Positions, record.Candidate) {
		return nil
	}
	if label.AllBad {
		return assessmentPtr(0.0)
	}
	if slices.Contains(label.Acceptable, record.Candidate) {
		return assessmentPtr(1.0)
	}
	return assessmentPtr(0.0)
}

func addRoute(routes map[string]AssessmentRoute, record TrialRecord, score *float64, side string) {
	key := routeOf(record)
	row := routes[side+":"+key]
	row.Items++
	if score == nil {
		row.Unmeasured++
	} else {
		if record.Outcome == OutcomeSelected {
			row.Selected++
		}
		if *score == 1 {
			row.Correct++
		}
		if record.Consensus != "" && *score == 0 {
			row.WrongConsensus++
		}
	}
	routes[side+":"+key] = row
}

func routeOf(record TrialRecord) string {
	if record.Consensus != "" {
		return "consensus/" + record.Consensus
	}
	if record.TieBreakKey != "" {
		return "tie_break/" + record.TieBreakKey
	}
	if record.Rule != "" {
		return record.Rule
	}
	if record.Measured {
		return "judge"
	}
	return "unmeasured"
}

type clusterRows struct {
	name string
	rows []assessedItem
}

func (c clusterRows) complete() bool {
	for _, row := range c.rows {
		if row.base == nil || row.candidate == nil {
			return false
		}
	}
	return true
}
func sortedClusterRows(groups map[string][]assessedItem) []clusterRows {
	out := make([]clusterRows, 0, len(groups))
	for name, rows := range groups {
		out = append(out, clusterRows{name, rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
func assessmentPtr(v float64) *float64 { return &v }

func (r AssessmentReport) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if err := writeFile(filepath.Join(dir, AssessmentReportFile), append(body, '\n')); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, AssessmentSummaryFile), []byte(r.Summary()))
}

func (r AssessmentReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Fixed-H assessment %s\n\n", r.CardID)
	fmt.Fprintf(&b, "Machine recommendation: **%s**. This is not a human final decision.\n\n", r.Recommendation)
	fmt.Fprintf(&b, "- H clusters: %d; all-bad items: %d; observed serious regressions: %d.\n", len(r.Clusters), r.AllBadItems, r.SeriousRegressions)
	if r.Paired != nil {
		fmt.Fprintf(&b, "- paired delta: %.3f, %.0f%% CI [%.3f, %.3f] over %d task clusters.\n", r.Paired.Mean, 100*(1-r.Paired.Alpha), r.Paired.Lower, r.Paired.Upper, r.Paired.N)
	}
	if r.CandidateQuality != nil {
		fmt.Fprintf(&b, "- candidate quality: %.3f, %.0f%% CI [%.3f, %.3f] over %d task clusters.\n", r.CandidateQuality.Mean, 100*(1-r.CandidateQuality.Alpha), r.CandidateQuality.Lower, r.CandidateQuality.Upper, r.CandidateQuality.N)
	}
	for _, note := range r.Notes {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	return b.String()
}
