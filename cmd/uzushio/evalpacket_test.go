package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

func TestJudgeLabelPacketAndImportCommands(t *testing.T) {
	dir := t.TempDir()
	itemDir := filepath.Join(dir, "item")
	if err := os.MkdirAll(filepath.Join(itemDir, "candidates"), 0o755); err != nil {
		t.Fatal(err)
	}
	conversation := []byte(`[{"role":"user","content":"Question"}]`)
	for name, body := range map[string][]byte{
		"task.json":         []byte(`{"version":3,"face":"chat","conversation":"conversation.json","rubric":"rubric.md"}`),
		"conversation.json": conversation, "rubric.md": []byte("Correctness.\n"),
		"candidates/c1.txt": []byte("one\n"), "candidates/c2.txt": []byte("two\n"), "candidates/c3.txt": []byte("three\n"),
	} {
		if err := os.WriteFile(filepath.Join(itemDir, filepath.FromSlash(name)), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(conversation)
	dataset := judge.EvaluationDataset{SchemaVersion: judge.EvaluationDatasetSchemaVersion, ID: "d", Items: []judge.EvaluationItem{{
		ID: "item", Set: "D", Source: "test", SourceID: "1", License: "test", Cluster: "one", ConversationSHA256: hex.EncodeToString(sum[:]), Reason: "test",
		Strata: map[string]string{"language": "en", "category": "test", "length_bin": "short"}, Candidates: []string{"x", "y", "z"},
	}}}
	datasetPath := filepath.Join(dir, "dataset.json")
	writeJSON(t, datasetPath, dataset)
	suitePath := filepath.Join(dir, "suite.json")
	writeJSON(t, suitePath, judge.Suite{SchemaVersion: judge.SuiteSchemaVersion, ID: "s", Face: judge.FaceChat, Tasks: []judge.Task{{ID: "item", Dir: "item"}}})
	packetDir := filepath.Join(dir, "packets", "packet")
	packetCmd := newJudgeLabelPacketCmd()
	packetCmd.SetArgs([]string{"--dataset", datasetPath, "--suite", suitePath, "--set", "D", "--annotator", "opaque", "--out", packetDir, "--seed", "9"})
	if err := packetCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	answersPath := filepath.Join(packetDir, "answers.json")
	body, err := os.ReadFile(answersPath)
	if err != nil {
		t.Fatal(err)
	}
	var answers judge.EvaluationPacketAnswers
	if err := json.Unmarshal(body, &answers); err != nil {
		t.Fatal(err)
	}
	answers.Items[0].Acceptable, answers.Items[0].Rationale = []string{"A"}, "acceptable"
	writeJSON(t, answersPath, answers)
	out := filepath.Join(dir, "labels.json")
	importCmd := newJudgeLabelImportCmd()
	importCmd.SetArgs([]string{"--packet", packetDir, "--answers", answersPath, "--out", out})
	if err := importCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var labels []judge.EvaluationLabel
	body, err = os.ReadFile(out)
	if err != nil || json.Unmarshal(body, &labels) != nil || len(labels) != 1 || len(labels[0].Annotations) != 1 {
		t.Fatalf("labels output = %s, err = %v", body, err)
	}
	if err := importCmd.Execute(); err == nil {
		t.Fatal("label import must not overwrite output")
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
