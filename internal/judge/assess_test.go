package judge

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assessmentFixture(n int) (*EvaluationDataset, map[string]ResolvedEvaluationLabel, TrialResult) {
	dataset := &EvaluationDataset{SchemaVersion: EvaluationDatasetSchemaVersion, ID: "h"}
	labels := map[string]ResolvedEvaluationLabel{}
	result := TrialResult{}
	for i := 0; i < n; i++ {
		id := "h" + strconv.Itoa(i)
		dataset.Items = append(dataset.Items, EvaluationItem{ID: id, Set: SetH, Source: "synthetic", SourceID: id,
			License: "CC0", Cluster: "task-" + strconv.Itoa(i), ConversationSHA256: strings.Repeat("a", 64),
			Reason: "fixture", Strata: map[string]string{"language": "en", "category": "math", "length_bin": "short"}, Candidates: []string{"c1", "c2"}})
		labels[id] = ResolvedEvaluationLabel{Item: id, Acceptable: []string{"c1"}, Annotators: []string{"a", "b"}}
		for _, condition := range []string{ConditionBase, ConditionCandidate} {
			result.Records = append(result.Records, TrialRecord{Item: id, Set: SetH, Condition: condition, Source: SourceMeasured, Outcome: OutcomeSelected, Candidate: "c1", Measured: true})
		}
	}
	result.Report.Planned, result.Report.Completed = n, n
	return dataset, labels, result
}

func assessmentCardForReport() AssessmentCard {
	return AssessmentCard{ID: "a", Digest: strings.Repeat("b", 64), Finalist: FinalistArtifacts{Report: Artifact{SHA256: strings.Repeat("c", 64)}},
		Dataset: Artifact{SHA256: strings.Repeat("d", 64)}, HoldoutSHA256: strings.Repeat("e", 64), Trial: TrialCard{ID: "h", Base: TrialCondition{ID: "base"}, Candidate: TrialCondition{ID: "candidate"}},
		Rules: AssessmentRules{Alpha: 0.1, NoninferiorityMargin: 0.5, QualityFloor: 0.5, MinClusters: 2, MaxObservedSeriousRegressions: 0}}
}

func TestAssessmentUsesTaskClustersAndDoesNotCountTurnsIndependently(t *testing.T) {
	dataset, labels, result := assessmentFixture(2)
	// A second turn shares task-0. The cluster's score must average its turns,
	// not manufacture a third independent observation.
	turn := dataset.Items[0]
	turn.ID, turn.SourceID = "h0-turn2", "h0-turn2"
	dataset.Items = append(dataset.Items, turn)
	labels[turn.ID] = ResolvedEvaluationLabel{Item: turn.ID, Acceptable: []string{"c1"}, Annotators: []string{"a", "b"}}
	for _, condition := range []string{ConditionBase, ConditionCandidate} {
		result.Records = append(result.Records, TrialRecord{Item: turn.ID, Set: SetH, Condition: condition, Source: SourceMeasured, Outcome: OutcomeSelected, Candidate: "c1", Measured: true})
	}
	report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
	if err != nil {
		t.Fatalf("assess report: %v", err)
	}
	if len(report.Clusters) != 2 || report.Clusters[0].Items != 2 {
		t.Fatalf("clusters = %+v, want two task clusters and two turns in first", report.Clusters)
	}
	if report.Paired == nil || report.Paired.N != 2 {
		t.Fatalf("interval must count clusters, got %+v", report.Paired)
	}
}

func TestAssessmentUnmeasuredIsNotScoredAsZero(t *testing.T) {
	dataset, labels, result := assessmentFixture(2)
	result.Records[1].Measured = false
	result.Records[1].Outcome = OutcomeJudgeTimeout
	report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
	if err != nil {
		t.Fatalf("assess report: %v", err)
	}
	if report.Recommendation != AssessmentInconclusive || len(report.UnmeasuredItems) != 1 || report.Paired != nil {
		t.Fatalf("unmeasured side must be inconclusive without an interval: %+v", report)
	}
}

func TestAssessmentInvalidOutcomeIsUnmeasuredEvenWhenARecordSaysMeasured(t *testing.T) {
	dataset, labels, result := assessmentFixture(2)
	result.Records[1].Outcome = OutcomeJudgeFailed
	report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
	if err != nil {
		t.Fatalf("assess report: %v", err)
	}
	if len(report.UnmeasuredItems) != 1 || report.Completed != 1 {
		t.Fatalf("invalid judge outcome must not become a scored zero: %+v", report)
	}
}

func TestAssessmentInvalidNoCandidateAndUnknownSelectionAreUnmeasured(t *testing.T) {
	dataset, labels, result := assessmentFixture(2)
	result.Records[1].Outcome, result.Records[1].Reason = OutcomeNoCandidate, "invalid_output"
	result.Records[3].Candidate = "unknown"
	report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
	if err != nil {
		t.Fatalf("assess report: %v", err)
	}
	if len(report.UnmeasuredItems) != 2 || report.Completed != 0 {
		t.Fatalf("invalid no-candidate and unknown selection must be unmeasured: %+v", report)
	}
}

func TestAssessmentRecommendationHardRegressionAndAdopt(t *testing.T) {
	t.Run("hard regression rejects", func(t *testing.T) {
		dataset, labels, result := assessmentFixture(2)
		result.Records[3].Candidate = "c2" // h1 base correct -> candidate wrong
		report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
		if err != nil {
			t.Fatalf("assess report: %v", err)
		}
		if report.Recommendation != AssessmentReject || report.SeriousRegressions != 1 {
			t.Fatalf("hard regression: %+v", report)
		}
	})
	t.Run("hard regression rejects despite another missing item", func(t *testing.T) {
		dataset, labels, result := assessmentFixture(2)
		result.Records[3].Candidate = "c2"
		result.Records[1].Outcome, result.Records[1].Measured = OutcomeJudgeTimeout, false
		report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
		if err != nil {
			t.Fatalf("assess report: %v", err)
		}
		if report.Recommendation != AssessmentReject || report.SeriousRegressions != 1 {
			t.Fatalf("measured serious regression must veto despite missing data: %+v", report)
		}
	})
	t.Run("enough fixed clusters can adopt", func(t *testing.T) {
		dataset, labels, result := assessmentFixture(220)
		card := assessmentCardForReport()
		card.Rules.MinClusters = 200
		report, err := assessReport(card, dataset, labels, result)
		if err != nil {
			t.Fatalf("assess report: %v", err)
		}
		if report.Recommendation != AssessmentAdopt {
			t.Fatalf("fixed all-correct fixture should adopt: %+v", report)
		}
	})
}

func TestHoldoutClaimAllowsOnlySameResume(t *testing.T) {
	registry := t.TempDir()
	card := assessmentCardForReport()
	card.HoldoutSHA256 = strings.Repeat("f", 64)
	items := []EvaluationItem{{Source: "synthetic", SourceID: "one", ConversationSHA256: strings.Repeat("1", 64)}}
	now := time.Unix(100, 0)
	if err := claimHoldout(card, registry, "runtime", items, false, now); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := claimHoldout(card, registry, "runtime", items, true, now); err != nil {
		t.Fatalf("same resume: %v", err)
	}
	if err := claimHoldout(card, registry, "other", items, true, now); err == nil || !strings.Contains(err.Error(), "different assessment or runtime") {
		t.Fatalf("different runtime error = %v", err)
	}
	key := holdoutKeys(items[0])[0]
	if _, err := os.Stat(filepath.Join(registry, key.kind+"-"+key.name+".json")); err != nil {
		t.Fatalf("claim missing: %v", err)
	}
}

func TestHoldoutClaimSeparatelyBindsConversationAndSource(t *testing.T) {
	registry := t.TempDir()
	card := assessmentCardForReport()
	first := EvaluationItem{Source: "synthetic", SourceID: "one", Cluster: "task", ConversationSHA256: strings.Repeat("1", 64)}
	if err := claimHoldout(card, registry, "runtime", []EvaluationItem{first}, false, time.Unix(100, 0)); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	changedSource := first
	changedSource.Source = "other"
	if err := claimHoldout(AssessmentCard{Digest: strings.Repeat("2", 64)}, registry, "runtime", []EvaluationItem{changedSource}, false, time.Unix(101, 0)); err == nil {
		t.Fatal("same conversation under a changed source must collide")
	}
	changedConversation := first
	changedConversation.ConversationSHA256 = strings.Repeat("2", 64)
	if err := claimHoldout(AssessmentCard{Digest: strings.Repeat("3", 64)}, registry, "runtime", []EvaluationItem{changedConversation}, false, time.Unix(102, 0)); err == nil {
		t.Fatal("same source identity under a changed conversation must collide")
	}
}

func TestHoldoutCannotReuseFinalistConversation(t *testing.T) {
	item := EvaluationItem{ID: "h1", ConversationSHA256: strings.Repeat("4", 64)}
	resume := trialResumeState{Files: map[string]string{"stage-b/task/conversation.json": item.ConversationSHA256}}
	if err := checkHoldoutNotInFinalist([]EvaluationItem{item}, resume); err == nil {
		t.Fatal("H conversation already used by D/R finalist must be rejected")
	}
}

func TestAssessmentRuntimeFingerprintBindsOutputDirectory(t *testing.T) {
	card := assessmentCardForReport()
	first, err := assessmentRuntimeFingerprint(card, AssessmentOptions{Out: t.TempDir()})
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	second, err := assessmentRuntimeFingerprint(card, AssessmentOptions{Out: t.TempDir()})
	if err != nil {
		t.Fatalf("second fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("different output directories must not resume the same H measurement")
	}
}

func TestHoldoutClaimRejectsOverlappingSubset(t *testing.T) {
	registry := t.TempDir()
	first, second := assessmentCardForReport(), assessmentCardForReport()
	first.Digest, second.Digest = strings.Repeat("1", 64), strings.Repeat("2", 64)
	first.HoldoutSHA256, second.HoldoutSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	items := []EvaluationItem{
		{Source: "synthetic", SourceID: "one", ConversationSHA256: strings.Repeat("1", 64)},
		{Source: "synthetic", SourceID: "two", ConversationSHA256: strings.Repeat("2", 64)},
	}
	if err := claimHoldout(first, registry, "runtime", items, false, time.Unix(100, 0)); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := claimHoldout(second, registry, "runtime", items[:1], false, time.Unix(101, 0)); err == nil {
		t.Fatal("a later subset must collide with the item claim")
	}
}

func TestAssessmentRoutesKeepCondorcetAndCopelandSeparate(t *testing.T) {
	dataset, labels, result := assessmentFixture(2)
	result.Records[0].Rule, result.Records[1].Rule = RuleCondorcet, RuleCopeland
	report, err := assessReport(assessmentCardForReport(), dataset, labels, result)
	if err != nil {
		t.Fatalf("assess report: %v", err)
	}
	if report.Routes["base:"+RuleCondorcet].Items != 1 || report.Routes["candidate:"+RuleCopeland].Items != 1 {
		t.Fatalf("route diagnostics merged selection rules: %+v", report.Routes)
	}
}

func TestBindFinalistArtifactsRejectsJournalFromAnotherRun(t *testing.T) {
	card := TrialCard{ID: "stage-b", Stage: StageB, Reuse: TrialReuse{Kind: ReuseNone}, Base: TrialCondition{ID: "base"}, Candidate: TrialCondition{ID: "candidate"}}
	plan := []TrialStep{{Item: "d1", Set: SetD, Condition: ConditionBase}, {Item: "d1", Set: SetD, Condition: ConditionCandidate}}
	records := []TrialRecord{
		{Item: "d1", Set: SetD, Condition: ConditionBase, ConditionID: "base", Source: SourceMeasured, Measured: true, ReuseKey: "base", RunDir: "base", Outcome: OutcomeSelected, Candidate: "c1"},
		{Item: "d1", Set: SetD, Condition: ConditionCandidate, ConditionID: "candidate", Source: SourceMeasured, Measured: true, ReuseKey: "candidate", RunDir: "candidate", Outcome: OutcomeSelected, Candidate: "c2"},
	}
	report := TrialReport{Card: card, Planned: 1, Completed: 1, Reuse: TrialReuseStats{Kind: ReuseNone, Measured: 2}, Items: []TrialItem{{Item: "d1", Set: SetD, Complete: true, BaseOutcome: OutcomeSelected, BaseChoice: "c1", NewOutcome: OutcomeSelected, NewChoice: "c2"}}}
	resume := trialResumeState{Card: card, Plan: plan}
	if err := bindFinalistArtifacts(report, resume, records); err != nil {
		t.Fatalf("matching artifacts rejected: %v", err)
	}
	records[1].Candidate = "c1"
	if err := bindFinalistArtifacts(report, resume, records); err == nil {
		t.Fatal("journal that disagrees with the report must be rejected")
	}
}
