package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

func TestJudgeLabelMergeWritesUnresolvedReviewFile(t *testing.T) {
	dir := t.TempDir()
	conversation := []byte("[]")
	sum := sha256.Sum256(conversation)
	dataset := judge.EvaluationDataset{SchemaVersion: judge.EvaluationDatasetSchemaVersion, ID: "d", Items: []judge.EvaluationItem{{
		ID: "item", Set: "D", Source: "synthetic", SourceID: "1", License: "test", Cluster: "one", ConversationSHA256: hex.EncodeToString(sum[:]), Reason: "test",
		Strata: map[string]string{"language": "en", "category": "test", "length_bin": "short"}, Candidates: []string{"a", "b"},
	}}}
	datasetPath := filepath.Join(dir, "dataset.json")
	writeJSON(t, datasetPath, dataset)
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	provenance := map[string]string{"a": testDigest("a"), "b": testDigest("b")}
	firstLabel := judge.EvaluationLabel{TaskSHA256: testDigest("task"), Item: "item", ConversationSHA256: testDigest("conversation"), RubricSHA256: testDigest("rubric"), CandidateSHA256: provenance, Annotations: []judge.EvaluationAnnotation{{Annotator: "one", Kind: "human", Acceptable: []string{"a"}, Rationale: "r"}}}
	secondLabel := firstLabel
	secondLabel.CandidateSHA256 = map[string]string{"a": provenance["a"], "b": provenance["b"]}
	secondLabel.Annotations = []judge.EvaluationAnnotation{{Annotator: "two", Kind: "human", Acceptable: []string{"b"}, Rationale: "r"}}
	writeJSON(t, first, []judge.EvaluationLabel{firstLabel})
	writeJSON(t, second, []judge.EvaluationLabel{secondLabel})
	out := filepath.Join(dir, "merged.json")
	cmd := newJudgeLabelMergeCmd()
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--dataset", datasetPath, "--set", "D", "--input", first, "--input", second, "--out", out})
	if err := cmd.Execute(); err == nil {
		t.Fatal("unresolved disagreement must return failure after writing")
	}
	if !strings.Contains(errOut.String(), "unresolved human disagreements: item") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	merged, err := judge.LoadEvaluationLabelFile(out)
	if err != nil || len(merged) != 1 || len(merged[0].Annotations) != 2 || merged[0].Adjudication != nil {
		t.Fatalf("review output = %+v, err = %v", merged, err)
	}
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
