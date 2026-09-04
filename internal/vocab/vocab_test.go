package vocab

import (
	"slices"
	"testing"
)

// TestVocabulariesAreDistinct refuses a vocabulary that repeats a word: a
// duplicate in a one_of makes two spellings of one value, and a duplicate rule
// name makes two findings DocDag will not deduplicate for us.
func TestVocabulariesAreDistinct(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{"kinds", Strings(AllKinds())},
		{"edit statuses", Strings(EditStatuses())},
		{"pattern statuses", Strings(PatternStatuses())},
		{"categories", Strings(AllCategories())},
		{"verdicts", Strings(AllVerdicts())},
		{"splits", Strings(AllSplits())},
		{"expects", Strings(AllExpects())},
		{"outcomes", Strings(AllOutcomes())},
		{"approvals", Strings(AllApprovals())},
		{"edges", Strings(AllEdges())},
		{"projections", Strings(AllProjections())},
		{"rules", Strings(AllRules())},
		{"edit fields", Strings(EditFields())},
		{"pattern fields", Strings(PatternFields())},
		{"run fields", Strings(RunFields())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]bool{}
			for _, v := range tt.values {
				if v == "" {
					t.Fatalf("%s holds an empty word", tt.name)
				}
				if seen[v] {
					t.Fatalf("%s holds %q twice", tt.name, v)
				}
				seen[v] = true
			}
		})
	}
}

func TestKindDirs(t *testing.T) {
	dirs := map[string]bool{}
	for _, k := range AllKinds() {
		dir, ok := Dir(k)
		if !ok {
			t.Fatalf("Dir(%s) is undeclared", k)
		}
		if dirs[dir] {
			t.Fatalf("two kinds share the directory %q", dir)
		}
		dirs[dir] = true
	}
	if _, ok := Dir(Kind("clause")); ok {
		t.Fatal("Dir answered for a kind uzushio does not declare")
	}
}

func TestKindStatuses(t *testing.T) {
	if got := KindStatuses(KindRun); got != nil {
		t.Fatalf("KindStatuses(run) = %v, want nothing: a run is a measurement", got)
	}
	if got := KindStatuses(KindEdit); !slices.Equal(got, EditStatuses()) {
		t.Fatalf("KindStatuses(edit) = %v", got)
	}
	if got := KindStatuses(KindPattern); !slices.Equal(got, PatternStatuses()) {
		t.Fatalf("KindStatuses(pattern) = %v", got)
	}
}

func TestFieldsAreSorted(t *testing.T) {
	for _, k := range AllKinds() {
		fields := Strings(KindFields(k))
		if !slices.IsSorted(fields) {
			t.Fatalf("%s fields are not sorted: %v", k, fields)
		}
	}
}

// TestFieldsAvoidEngineKeys keeps the declared fields clear of the keys DocDag
// answers itself. A field named after one of them would be shadowed, or would
// shadow, depending on the reader.
func TestFieldsAvoidEngineKeys(t *testing.T) {
	engine := []string{"id", "kind", "title", "date", "status", "in_force"}
	for _, k := range AllKinds() {
		for _, f := range KindFields(k) {
			if slices.Contains(engine, f.String()) {
				t.Fatalf("%s declares the engine key %q as a field", k, f)
			}
		}
	}
}

func TestStringsAndValid(t *testing.T) {
	if got := Strings(AllSplits()); !slices.Equal(got, []string{"held-in", "held-out"}) {
		t.Fatalf("Strings(AllSplits()) = %v", got)
	}
	if !Valid(VerdictHold, AllVerdicts()) {
		t.Fatal("hold is not a verdict")
	}
	if Valid(Verdict("maybe"), AllVerdicts()) {
		t.Fatal("maybe is a verdict")
	}
}
