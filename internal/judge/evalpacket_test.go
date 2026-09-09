package judge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packetFixture(t *testing.T) (*EvaluationDataset, Suite, string) {
	t.Helper()
	dir := t.TempDir()
	itemDir := filepath.Join(dir, "item-1")
	if err := os.MkdirAll(filepath.Join(itemDir, "candidates"), 0o755); err != nil {
		t.Fatal(err)
	}
	conversation := []byte(`[{"role":"user","content":"Solve the task."}]`)
	for name, body := range map[string]string{
		"task.json":                       `{"conversation":"inputs/source-conversation.json","rubric":"criteria/review.md","reference":"references/answer.md"}`,
		"inputs/source-conversation.json": string(conversation), "criteria/review.md": "Correctness and clarity.\n", "references/answer.md": "Reference context.\n",
		"candidates/c1.txt": "First answer.\n", "candidates/c2.txt": "Second answer.\n", "candidates/c3.txt": "Third answer.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(itemDir, filepath.FromSlash(name))), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(itemDir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dataset := &EvaluationDataset{SchemaVersion: EvaluationDatasetSchemaVersion, ID: "synthetic", Items: []EvaluationItem{{
		ID: "item-1", Set: "D", Source: "synthetic", SourceID: "one", License: "test", Cluster: "one",
		ConversationSHA256: digest(conversation), Reason: "test", Strata: map[string]string{"language": "en", "category": "test", "length_bin": "short"},
		Candidates: []string{"secret-alpha", "secret-beta", "secret-gamma"},
	}}}
	suite := Suite{ID: "synthetic-suite", Dir: dir, Tasks: []Task{{ID: "item-1", Dir: "item-1"}}}
	return dataset, suite, itemDir
}

func TestEvaluationPacketIsAnonymousAndImportsOneAnnotation(t *testing.T) {
	dataset, suite, _ := packetFixture(t)
	packet, mapping, err := BuildEvaluationPacket(dataset, suite, "D", "annotator-opaque", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Items) != 1 || len(packet.Items[0].Candidates) != 3 {
		t.Fatalf("packet = %+v", packet)
	}
	if packet.Items[0].Reference != "Reference context.\n" {
		t.Fatalf("packet reference = %q", packet.Items[0].Reference)
	}
	out := filepath.Join(t.TempDir(), "packet")
	if err := WriteEvaluationPacket(out, packet, mapping); err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(filepath.Join(out, "packet.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"secret-alpha", "secret-beta", "secret-gamma", "gold", "condition", "selected"} {
		if strings.Contains(string(markdown), hidden) {
			t.Fatalf("packet leaks %q:\n%s", hidden, markdown)
		}
	}
	answersPath := filepath.Join(out, "answers.json")
	body, err := os.ReadFile(answersPath)
	if err != nil {
		t.Fatal(err)
	}
	var answers EvaluationPacketAnswers
	if err := json.Unmarshal(body, &answers); err != nil {
		t.Fatal(err)
	}
	answers.Items[0].Acceptable = []string{"A", "C"}
	answers.Items[0].Rationale = "Both answer the question correctly."
	body, err = json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(answersPath, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	labels, err := ImportEvaluationPacket(out, answersPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || len(labels[0].Annotations) != 1 || labels[0].Annotations[0].Kind != "human" {
		t.Fatalf("labels = %+v", labels)
	}
	if labels[0].Adjudication != nil || labels[0].Annotations[0].Annotator != "annotator-opaque" {
		t.Fatalf("import must preserve one independent annotation: %+v", labels[0])
	}
	if len(labels[0].Annotations[0].Acceptable) != 2 {
		t.Fatalf("acceptable = %+v", labels[0].Annotations[0])
	}
	if len(labels[0].CandidateSHA256) != 3 || !validSHA256(labels[0].TaskSHA256) || !validSHA256(labels[0].ConversationSHA256) || !validSHA256(labels[0].RubricSHA256) || !validSHA256(labels[0].ReferenceSHA256) {
		t.Fatalf("import must retain every judged input digest: %+v", labels[0])
	}
}

func TestImportEvaluationPacketRefusesChangedInputAndInvalidLabel(t *testing.T) {
	dataset, suite, itemDir := packetFixture(t)
	packet, mapping, err := BuildEvaluationPacket(dataset, suite, "D", "annotator-opaque", 7)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "packet")
	if err := WriteEvaluationPacket(out, packet, mapping); err != nil {
		t.Fatal(err)
	}
	answersPath := filepath.Join(out, "answers.json")
	body, err := os.ReadFile(answersPath)
	if err != nil {
		t.Fatal(err)
	}
	var answers EvaluationPacketAnswers
	if err := json.Unmarshal(body, &answers); err != nil {
		t.Fatal(err)
	}
	answers.Items[0].Acceptable = []string{"Z"}
	answers.Items[0].Rationale = "completed"
	body, _ = json.MarshalIndent(answers, "", "  ")
	if err := os.WriteFile(answersPath, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportEvaluationPacket(out, answersPath); err == nil || !strings.Contains(err.Error(), "anonymous label") {
		t.Fatalf("invalid anonymous label error = %v", err)
	}
	answers.Items[0].Acceptable = []string{"A"}
	body, _ = json.MarshalIndent(answers, "", "  ")
	if err := os.WriteFile(answersPath, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "candidates", "c1.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportEvaluationPacket(out, answersPath); err == nil || !strings.Contains(err.Error(), "does not match mapping") {
		t.Fatalf("changed input error = %v", err)
	}
}

func TestVerifyEvaluationLabelCandidatesRefusesChangedRubricOrCandidate(t *testing.T) {
	dataset, suite, itemDir := packetFixture(t)
	packet, mapping, err := BuildEvaluationPacket(dataset, suite, "D", "annotator-opaque", 7)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "packet")
	if err := WriteEvaluationPacket(out, packet, mapping); err != nil {
		t.Fatal(err)
	}
	answersPath := filepath.Join(out, "answers.json")
	body, err := os.ReadFile(answersPath)
	if err != nil {
		t.Fatal(err)
	}
	var answers EvaluationPacketAnswers
	if err := json.Unmarshal(body, &answers); err != nil {
		t.Fatal(err)
	}
	answers.Items[0].Acceptable = []string{"A"}
	answers.Items[0].Rationale = "reviewed"
	body, _ = json.MarshalIndent(answers, "", "  ")
	if err := os.WriteFile(answersPath, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	imported, err := ImportEvaluationPacket(out, answersPath)
	if err != nil {
		t.Fatal(err)
	}
	label := imported[0]
	resolved := map[string]ResolvedEvaluationLabel{"item-1": {TaskSHA256: label.TaskSHA256, Item: label.Item, ConversationSHA256: label.ConversationSHA256, RubricSHA256: label.RubricSHA256, ReferenceSHA256: label.ReferenceSHA256, CandidateSHA256: label.CandidateSHA256}}
	if err := VerifyEvaluationLabelCandidates(resolved, suite, dataset.ItemsFor("D")); err != nil {
		t.Fatalf("matching judged inputs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "criteria", "review.md"), []byte("changed rubric\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvaluationLabelCandidates(resolved, suite, dataset.ItemsFor("D")); err == nil || !strings.Contains(err.Error(), "rubric") {
		t.Fatalf("changed rubric error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "criteria", "review.md"), []byte("Correctness and clarity.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "task.json"), []byte(`{"conversation":"inputs/source-conversation.json","rubric":"unused.md","reference":"references/answer.md"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvaluationLabelCandidates(resolved, suite, dataset.ItemsFor("D")); err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("changed task mapping error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "task.json"), []byte(`{"conversation":"inputs/source-conversation.json","rubric":"criteria/review.md","reference":"references/answer.md"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "references", "answer.md"), []byte("changed reference\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvaluationLabelCandidates(resolved, suite, dataset.ItemsFor("D")); err == nil || !strings.Contains(err.Error(), "reference") {
		t.Fatalf("changed reference error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "references", "answer.md"), []byte("Reference context.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "candidates", "c2.txt"), []byte("substituted answer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvaluationLabelCandidates(resolved, suite, dataset.ItemsFor("D")); err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("changed candidate error = %v", err)
	}
}
