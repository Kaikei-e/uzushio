package judge_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

func TestLoadEvaluationDataset(t *testing.T) {
	dataset := evaluationDataset()
	path := writeEvaluationJSON(t, dataset)
	got, err := judge.LoadEvaluationDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != dataset.ID || len(got.ItemsFor("D")) != 1 || len(got.ItemsFor("R")) != 1 || len(got.ItemsFor("H")) != 2 {
		t.Fatalf("dataset = %+v", got)
	}
	if got.HoldoutDigest() == "" || len(got.HoldoutDigest()) != 64 {
		t.Fatalf("holdout digest = %q", got.HoldoutDigest())
	}

	// The H digest identifies provenance, not a local label used by a caller.
	renamed := evaluationDataset()
	renamed.Items[2].ID = "renamed-h-one"
	renamed.Items[3].ID = "renamed-h-two"
	loaded, err := judge.LoadEvaluationDataset(writeEvaluationJSON(t, renamed))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HoldoutDigest() != got.HoldoutDigest() {
		t.Fatalf("renaming H items changed digest: %s then %s", got.HoldoutDigest(), loaded.HoldoutDigest())
	}
}

func TestLoadEvaluationDatasetRefusesInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*judge.EvaluationDataset)
	}{
		{"unknown field", func(d *judge.EvaluationDataset) { d.ID = "" }},
		{"duplicate item", func(d *judge.EvaluationDataset) { d.Items[1].ID = d.Items[0].ID }},
		{"duplicate source", func(d *judge.EvaluationDataset) {
			d.Items[1].Source, d.Items[1].SourceID = d.Items[0].Source, d.Items[0].SourceID
		}},
		{"duplicate conversation", func(d *judge.EvaluationDataset) { d.Items[1].ConversationSHA256 = d.Items[0].ConversationSHA256 }},
		{"cluster crosses split", func(d *judge.EvaluationDataset) { d.Items[1].Cluster = d.Items[0].Cluster }},
		{"missing stratum", func(d *judge.EvaluationDataset) { delete(d.Items[0].Strata, "language") }},
		{"invalid set", func(d *judge.EvaluationDataset) { d.Items[0].Set = "X" }},
		{"one candidate", func(d *judge.EvaluationDataset) { d.Items[0].Candidates = d.Items[0].Candidates[:1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataset := evaluationDataset()
			tc.change(&dataset)
			_, err := judge.LoadEvaluationDataset(writeEvaluationJSON(t, dataset))
			if !errors.Is(err, judge.ErrJudge) {
				t.Fatalf("LoadEvaluationDataset error = %v, want ErrJudge", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"id":"x","items":[],"unexpected":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := judge.LoadEvaluationDataset(path); !errors.Is(err, judge.ErrJudge) {
		t.Fatalf("unknown JSON field error = %v, want ErrJudge", err)
	}
}

func TestLoadEvaluationLabelsResolvesAgreementAndAdjudication(t *testing.T) {
	items := evaluationDataset().Items[:2]
	labels := []judge.EvaluationLabel{
		{
			TaskSHA256: labelDigest("task " + items[0].ID), Item: items[0].ID, ConversationSHA256: labelDigest("conversation " + items[0].ID), RubricSHA256: labelDigest("rubric " + items[0].ID), CandidateSHA256: labelCandidates(items[0]),
			Annotations: []judge.EvaluationAnnotation{
				{Annotator: "two", Kind: "human", Acceptable: []string{"c2", "c1"}},
				{Annotator: "one", Kind: "human", Acceptable: []string{"c1", "c2"}},
			},
		},
		{
			TaskSHA256: labelDigest("task " + items[1].ID), Item: items[1].ID, ConversationSHA256: labelDigest("conversation " + items[1].ID), RubricSHA256: labelDigest("rubric " + items[1].ID), CandidateSHA256: labelCandidates(items[1]),
			Annotations: []judge.EvaluationAnnotation{
				{Annotator: "one", Kind: "human", Acceptable: []string{"c1"}},
				{Annotator: "two", Kind: "human", AllBad: true},
			},
			Adjudication: &judge.EvaluationAnnotation{Annotator: "lead", Kind: "human", Acceptable: []string{"c2"}, Rationale: "The second answer meets the rubric."},
		},
	}
	resolved, err := judge.LoadEvaluationLabels(writeEvaluationJSON(t, labels), items, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved[items[0].ID]; strings.Join(got.Acceptable, ",") != "c1,c2" || strings.Join(got.Annotators, ",") != "one,two" || got.Adjudicator != "" {
		t.Errorf("agreement = %+v", got)
	}
	if got := resolved[items[1].ID]; strings.Join(got.Acceptable, ",") != "c2" || got.Adjudicator != "lead" {
		t.Errorf("adjudication = %+v", got)
	}
}

func labelDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func labelCandidates(item judge.EvaluationItem) map[string]string {
	values := make(map[string]string, len(item.Candidates))
	for _, candidate := range item.Candidates {
		values[candidate] = labelDigest(item.ID + ":" + candidate)
	}
	return values
}

func TestLoadEvaluationLabelsRefusesIncompleteOrUnresolvedLabels(t *testing.T) {
	items := evaluationDataset().Items[:2]
	agree := []judge.EvaluationAnnotation{
		{Annotator: "one", Kind: "human", Acceptable: []string{"c1"}},
		{Annotator: "two", Kind: "human", Acceptable: []string{"c1"}},
	}
	for _, tc := range []struct {
		name          string
		labels        []judge.EvaluationLabel
		minAnnotators int
	}{
		{"omits item", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree}}, 2},
		{"unknown item", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree}, {Item: "other", Annotations: agree}}, 2},
		{"duplicate item", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree}, {Item: items[0].ID, Annotations: agree}}, 2},
		{"one annotator", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree[:1]}, {Item: items[1].ID, Annotations: agree}}, 2},
		{"disagreement without adjudication", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree}, {Item: items[1].ID, Annotations: []judge.EvaluationAnnotation{agree[0], {Annotator: "two", Kind: "human", Acceptable: []string{"c2"}}}}}, 2},
		{"adjudication without rationale", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree}, {Item: items[1].ID, Annotations: []judge.EvaluationAnnotation{agree[0], {Annotator: "two", Kind: "human", Acceptable: []string{"c2"}}}, Adjudication: &judge.EvaluationAnnotation{Annotator: "lead", Kind: "human", Acceptable: []string{"c1"}}}}, 2},
		{"bad candidate", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: []judge.EvaluationAnnotation{{Annotator: "one", Kind: "human", Acceptable: []string{"c9"}}, agree[1]}}, {Item: items[1].ID, Annotations: agree}}, 2},
		{"invalid minimum", []judge.EvaluationLabel{{Item: items[0].ID, Annotations: agree}, {Item: items[1].ID, Annotations: agree}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := judge.LoadEvaluationLabels(writeEvaluationJSON(t, tc.labels), items, tc.minAnnotators)
			if !errors.Is(err, judge.ErrJudge) {
				t.Fatalf("LoadEvaluationLabels error = %v, want ErrJudge", err)
			}
		})
	}
}

func evaluationDataset() judge.EvaluationDataset {
	return judge.EvaluationDataset{
		SchemaVersion: 1,
		ID:            "p1-chat-fixture",
		Items: []judge.EvaluationItem{
			evaluationItem("d-1", "D", "source-d", "one", "cluster-d"),
			evaluationItem("r-1", "R", "source-r", "one", "cluster-r"),
			evaluationItem("h-1", "H", "source-h", "one", "cluster-h"),
			evaluationItem("h-2", "H", "source-h", "two", "cluster-h"),
		},
	}
}

func evaluationItem(id, set, source, sourceID, cluster string) judge.EvaluationItem {
	sum := sha256.Sum256([]byte("conversation " + id))
	return judge.EvaluationItem{
		ID: id, Set: set, Source: source, SourceID: sourceID, License: "CC0-1.0", Cluster: cluster,
		ConversationSHA256: hex.EncodeToString(sum[:]), Reason: "covers a fixed fixture stratum",
		Strata:     map[string]string{"language": "ja", "category": "reasoning", "length_bin": "short"},
		Candidates: []string{"c1", "c2"},
	}
}

func writeEvaluationJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
