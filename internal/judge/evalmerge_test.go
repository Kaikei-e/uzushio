package judge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mergeItems() []EvaluationItem {
	return []EvaluationItem{{ID: "one", Candidates: []string{"a", "b"}}, {ID: "two", Candidates: []string{"a", "b"}}}
}

func annotation(item, annotator string, acceptable ...string) EvaluationLabel {
	return EvaluationLabel{TaskSHA256: digest([]byte("task-" + item)), Item: item, ConversationSHA256: digest([]byte("conversation-" + item)), RubricSHA256: digest([]byte("rubric-" + item)), CandidateSHA256: map[string]string{"a": digest([]byte("a-" + item)), "b": digest([]byte("b-" + item))}, Annotations: []EvaluationAnnotation{{Annotator: annotator, Kind: "human", Acceptable: acceptable, Rationale: "reviewed"}}}
}

func TestMergeEvaluationLabelsPreservesDisagreementAndAdjudication(t *testing.T) {
	inputs := [][]EvaluationLabel{
		{annotation("one", "rater-a", "a"), annotation("two", "rater-a", "a")},
		{annotation("one", "rater-b", "a"), annotation("two", "rater-b", "b")},
	}
	merge, err := MergeEvaluationLabels(mergeItems(), inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(merge.Unresolved, ",") != "two" || len(merge.Labels[1].Annotations) != 2 || merge.Labels[1].Adjudication != nil {
		t.Fatalf("merge = %+v", merge)
	}
	adjudication := EvaluationLabel{Item: "two", Adjudication: &EvaluationAnnotation{Annotator: "arbiter", Kind: "human", Acceptable: []string{"a"}, Rationale: "Answer a is complete."}}
	merge, err = MergeEvaluationLabels(mergeItems(), inputs, []EvaluationLabel{adjudication})
	if err != nil {
		t.Fatal(err)
	}
	if len(merge.Unresolved) != 0 || merge.Labels[1].Adjudication == nil {
		t.Fatalf("adjudicated merge = %+v", merge)
	}
	body, err := json.Marshal(merge.Labels)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEvaluationLabels(path, mergeItems(), 2); err != nil {
		t.Fatalf("merged output must be consumable by LoadEvaluationLabels: %v", err)
	}
}

func TestMergeEvaluationLabelsRefusesNonIndependentInputs(t *testing.T) {
	inputs := [][]EvaluationLabel{
		{annotation("one", "same", "a"), annotation("two", "same", "a")},
		{annotation("one", "same", "a"), annotation("two", "same", "a")},
	}
	if _, err := MergeEvaluationLabels(mergeItems(), inputs, nil); err == nil || !strings.Contains(err.Error(), "repeats annotator") {
		t.Fatalf("duplicate annotator error = %v", err)
	}
	inputs[1][1].Annotations[0].Annotator = "other"
	if _, err := MergeEvaluationLabels(mergeItems(), inputs, nil); err == nil || !strings.Contains(err.Error(), "mixes annotators") {
		t.Fatalf("mixed input error = %v", err)
	}
}

func TestMergeEvaluationLabelsRefusesDifferentAnnotatedContent(t *testing.T) {
	inputs := [][]EvaluationLabel{
		{annotation("one", "rater-a", "a"), annotation("two", "rater-a", "a")},
		{annotation("one", "rater-b", "a"), annotation("two", "rater-b", "a")},
	}
	inputs[1][0].CandidateSHA256["a"] = digest([]byte("substituted"))
	if _, err := MergeEvaluationLabels(mergeItems(), inputs, nil); err == nil || !strings.Contains(err.Error(), "different task content") {
		t.Fatalf("different candidate content error = %v", err)
	}
}
