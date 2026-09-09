package judge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAssessRunsFrozenHFromRealStageBArtifacts exercises the public path:
// Trial writes the D/R report, resume identity and journal; Assess pins those
// files, claims H, writes its own trial/report, then resumes without asking
// the fake fleet again.
func TestAssessRunsFrozenHFromRealStageBArtifacts(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"d1": "c1", "r1": "c1", "h1": "c1", "h2": "c1"})
	base := f.config("base.json", nil)
	candidate := f.config("candidate.json", map[string]any{"max_tokens": 256})
	manifest := func(set string, ids ...string) TrialManifest {
		path := filepath.Join(f.root, set+".json")
		items := make([]TrialManifestItem, 0, len(ids))
		for _, id := range ids {
			items = append(items, TrialManifestItem{ID: id, Strata: map[string]string{"category": "writing", "language": "en"}, Reason: "fixture"})
		}
		trialJSON(t, path, TrialManifest{SchemaVersion: TrialSchemaVersion, Set: set, Items: items})
		loaded, err := LoadTrialManifest(path)
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}
	d, r, h := manifest(SetD, "d1"), manifest(SetR, "r1"), manifest(SetH, "h1", "h2")
	condition := func(id, config string) TrialCondition {
		return TrialCondition{ID: id, Config: config, ConfigSHA256: digestOf(t, config)}
	}
	stageB := TrialCard{SchemaVersion: TrialSchemaVersion, ID: "stage-b", Hypothesis: "faster", Change: "tokens", Stage: StageB,
		Suite: filepath.Join(f.suite.Dir, "suite.json"), Manifests: []string{d.Path, r.Path}, Take: map[string]int{SetD: 1, SetR: 1}, MaxItems: 2,
		Base: condition("base", base), Candidate: condition("candidate", candidate), Reuse: TrialReuse{Kind: ReuseNone}, Seed: 1, JudgeSeed: 7,
		Rules: TrialRules{Kind: RuleSpeed}, BudgetSeconds: 600, AlternatingBlocks: true, BlockSize: 1, Dir: f.root}
	f.runner.Cost = func(_ string, config string) time.Duration {
		if config == candidate {
			return time.Second
		}
		return 2 * time.Second
	}
	stageBOut := filepath.Join(f.root, "stage-b-out")
	final, err := Trial(context.Background(), TrialOptions{Card: stageB, Suite: f.suite, Manifests: []TrialManifest{d, r}, Runner: f.runner, Out: stageBOut, Vault: f.root, Now: f.now})
	if err != nil {
		t.Fatalf("stage B trial: %v", err)
	}
	if final.Report.Suggested.Value != DecisionFinalist {
		t.Fatalf("stage B did not produce a finalist: %+v", final.Report.Suggested)
	}
	if err := final.Write(stageBOut); err != nil {
		t.Fatal(err)
	}

	items := []EvaluationItem{}
	labels := []EvaluationLabel{}
	for _, id := range []string{"h1", "h2"} {
		task := taskOf(f.suite, id)
		dir := f.suite.TaskDir(task)
		conversation := digestOf(t, filepath.Join(dir, "conversation.json"))
		item := EvaluationItem{ID: id, Set: SetH, Source: "synthetic", SourceID: id, License: "CC0", Cluster: id, ConversationSHA256: conversation, Reason: "fixture", Strata: map[string]string{"category": "writing", "language": "en", "length_bin": "short"}, Candidates: append([]string(nil), Positions...)}
		items = append(items, item)
		candidateHashes := map[string]string{}
		for i, path := range f.suite.Candidates(task) {
			candidateHashes[Positions[i]] = digestOf(t, path)
		}
		labels = append(labels, EvaluationLabel{TaskSHA256: digestOf(t, filepath.Join(dir, "task.json")), Item: id, ConversationSHA256: conversation, RubricSHA256: digestOf(t, filepath.Join(dir, "rubric.md")), CandidateSHA256: candidateHashes,
			Annotations: []EvaluationAnnotation{{Annotator: "a", Kind: "human", Acceptable: []string{"c1"}}, {Annotator: "b", Kind: "human", Acceptable: []string{"c1"}}}})
	}
	dataset := EvaluationDataset{SchemaVersion: EvaluationDatasetSchemaVersion, ID: "fixed-h", Items: items}
	datasetPath, labelsPath := filepath.Join(f.root, "dataset.json"), filepath.Join(f.root, "labels.json")
	trialJSON(t, datasetPath, dataset)
	trialJSON(t, labelsPath, labels)
	stageC := stageB
	stageC.ID, stageC.Stage, stageC.Manifests, stageC.Take, stageC.MaxItems = "stage-c", StageC, []string{h.Path}, map[string]int{SetH: 2}, 2
	assessmentPath := filepath.Join(f.root, "assessment.json")
	card := AssessmentCard{SchemaVersion: AssessmentSchemaVersion, ID: "fixed-h", Trial: stageC,
		Finalist: FinalistArtifacts{
			Report:  Artifact{Path: filepath.Join(stageBOut, TrialReportFile), SHA256: digestOf(t, filepath.Join(stageBOut, TrialReportFile))},
			Resume:  Artifact{Path: filepath.Join(stageBOut, trialResumeFile), SHA256: digestOf(t, filepath.Join(stageBOut, trialResumeFile))},
			Results: Artifact{Path: filepath.Join(stageBOut, TrialResultsFile), SHA256: digestOf(t, filepath.Join(stageBOut, TrialResultsFile))},
		},
		Dataset: Artifact{Path: datasetPath, SHA256: digestOf(t, datasetPath)}, HoldoutSHA256: dataset.HoldoutDigest(), Labels: Artifact{Path: labelsPath, SHA256: digestOf(t, labelsPath)}, Rules: AssessmentRules{Alpha: 0.1, NoninferiorityMargin: 1, QualityFloor: 0, MinClusters: 2}}
	trialJSON(t, assessmentPath, card)
	out, registry := filepath.Join(f.root, "assessment-out"), filepath.Join(f.root, "registry")
	calls := len(f.runner.Calls)
	originalConfig, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	trialWrite(t, candidate, "{\"changed\":true}\n")
	if _, err := Assess(context.Background(), AssessmentOptions{CardPath: assessmentPath, Out: filepath.Join(f.root, "rejected-out"), Registry: registry, Runner: f.runner, Now: f.now}); err == nil {
		t.Fatal("changed condition configuration must reject before H")
	}
	if len(f.runner.Calls) != calls {
		t.Fatal("changed condition opened H")
	}
	if err := os.WriteFile(candidate, originalConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	dry, err := Assess(context.Background(), AssessmentOptions{CardPath: assessmentPath, Out: out, Registry: registry, Runner: f.runner, DryRun: true, Now: f.now})
	if err != nil || dry.Recommendation != AssessmentInconclusive || len(f.runner.Calls) != calls {
		t.Fatalf("dry run = %#v, %v, calls %d", dry, err, len(f.runner.Calls))
	}
	if _, err := os.Stat(registry); !os.IsNotExist(err) {
		t.Fatalf("dry run claimed registry: %v", err)
	}
	report, err := Assess(context.Background(), AssessmentOptions{CardPath: assessmentPath, Out: out, Registry: registry, Runner: f.runner, Now: f.now})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if report.Paired == nil || report.CandidateQuality == nil {
		t.Fatalf("assessment lacks intervals: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(out, AssessmentReportFile)); err != nil {
		t.Fatalf("assessment report: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, AssessmentSummaryFile)); err != nil {
		t.Fatalf("assessment summary: %v", err)
	}
	after := len(f.runner.Calls)
	if _, err := Assess(context.Background(), AssessmentOptions{CardPath: assessmentPath, Out: out, Registry: registry, Runner: f.runner, Resume: true, Now: f.now}); err != nil {
		t.Fatalf("assessment resume: %v", err)
	}
	if len(f.runner.Calls) != after {
		t.Fatalf("resume made %d extra calls", len(f.runner.Calls)-after)
	}
}
