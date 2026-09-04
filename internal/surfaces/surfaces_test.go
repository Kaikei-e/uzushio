package surfaces

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestAll(t *testing.T) {
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	want := []string{
		"system-prompt", "tool-description", "tool-implementation",
		"middleware", "skill", "subagent-config", "memory",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}
	if len(got) != Count {
		t.Fatalf("All() has %d surfaces, Count is %d", len(got), Count)
	}
}

func TestByAutonomy(t *testing.T) {
	tests := []struct {
		autonomy string
		want     []string
	}{
		{AutonomyHumanApproval, []string{"system-prompt", "tool-description", "middleware", "subagent-config"}},
		{AutonomyProposeOnly, []string{"tool-implementation"}},
		{AutonomyAutoAccept, []string{"skill", "memory"}},
	}
	for _, tt := range tests {
		t.Run(tt.autonomy, func(t *testing.T) {
			got, err := ByAutonomy(tt.autonomy)
			if err != nil {
				t.Fatalf("ByAutonomy(%q): %v", tt.autonomy, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ByAutonomy(%q) = %v, want %v", tt.autonomy, got, tt.want)
			}
		})
	}
}

// TestByAutonomyPartitions holds the three autonomy lists to a partition of
// All(): a surface missing from every list would silently escape the rules
// that are built by enumerating one.
func TestByAutonomyPartitions(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	var union []string
	for _, autonomy := range Autonomies() {
		names, err := ByAutonomy(autonomy)
		if err != nil {
			t.Fatalf("ByAutonomy(%q): %v", autonomy, err)
		}
		union = append(union, names...)
	}
	slices.Sort(union)
	sorted := slices.Sorted(slices.Values(all))
	if !slices.Equal(union, sorted) {
		t.Fatalf("the autonomy lists cover %v, want %v", union, sorted)
	}
}

func TestByAutonomyUnknown(t *testing.T) {
	if _, err := ByAutonomy("no-such-autonomy"); err == nil {
		t.Fatal("ByAutonomy(unknown) = nil error, want one")
	}
}

func TestHumanApprovalAndProposeOnly(t *testing.T) {
	human, err := HumanApproval()
	if err != nil {
		t.Fatalf("HumanApproval: %v", err)
	}
	if len(human) != 4 {
		t.Fatalf("HumanApproval() = %v, want four surfaces", human)
	}
	propose, err := ProposeOnly()
	if err != nil {
		t.Fatalf("ProposeOnly: %v", err)
	}
	if !slices.Equal(propose, []string{"tool-implementation"}) {
		t.Fatalf("ProposeOnly() = %v, want [tool-implementation]", propose)
	}
}

func TestReadOnlyIsDisjoint(t *testing.T) {
	readOnly, err := ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly: %v", err)
	}
	if !slices.Equal(readOnly, []string{"verifier", "tracer", "model-config"}) {
		t.Fatalf("ReadOnly() = %v", readOnly)
	}
	all, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, name := range readOnly {
		if slices.Contains(all, name) {
			t.Fatalf("%q is both editable and read-only", name)
		}
	}
}

// TestReadOnlyIsACopy proves a caller cannot edit the vocabulary out from
// under the configuration generator by writing into the slice it was handed.
func TestReadOnlyIsACopy(t *testing.T) {
	first, err := ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly: %v", err)
	}
	first[0] = "tampered"
	second, err := ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly again: %v", err)
	}
	if second[0] == "tampered" {
		t.Fatal("ReadOnly returns the package's own slice")
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"not json", `{`},
		{"unknown field", `{"surfaces":[],"read_only":[],"extra":1}`},
		{"too few surfaces", `{"surfaces":[{"surface":"skill","autonomy":"auto-accept"}],"read_only":[]}`},
		{
			name: "unknown autonomy",
			raw: `{"surfaces":[` +
				`{"surface":"a","autonomy":"auto-accept"},{"surface":"b","autonomy":"auto-accept"},` +
				`{"surface":"c","autonomy":"auto-accept"},{"surface":"d","autonomy":"auto-accept"},` +
				`{"surface":"e","autonomy":"auto-accept"},{"surface":"f","autonomy":"auto-accept"},` +
				`{"surface":"g","autonomy":"whenever"}],"read_only":[]}`,
		},
		{
			name: "duplicate surface",
			raw: `{"surfaces":[` +
				`{"surface":"a","autonomy":"auto-accept"},{"surface":"a","autonomy":"auto-accept"},` +
				`{"surface":"c","autonomy":"auto-accept"},{"surface":"d","autonomy":"auto-accept"},` +
				`{"surface":"e","autonomy":"auto-accept"},{"surface":"f","autonomy":"auto-accept"},` +
				`{"surface":"g","autonomy":"auto-accept"}],"read_only":[]}`,
		},
		{
			name: "read-only names a surface",
			raw: `{"surfaces":[` +
				`{"surface":"a","autonomy":"auto-accept"},{"surface":"b","autonomy":"auto-accept"},` +
				`{"surface":"c","autonomy":"auto-accept"},{"surface":"d","autonomy":"auto-accept"},` +
				`{"surface":"e","autonomy":"auto-accept"},{"surface":"f","autonomy":"auto-accept"},` +
				`{"surface":"g","autonomy":"auto-accept"}],"read_only":["a"]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parse([]byte(tt.raw)); err == nil {
				t.Fatalf("parse(%s) = nil error, want one", tt.raw)
			}
		})
	}
}

// TestEmbeddedMatchesLiveCMoA compares the committed vocabulary against the
// binary that owns it. It is skipped where no cmoa is reachable, which is the
// normal case on a machine that has only this repository checked out; CI sets
// UZUSHIO_CMOA_BIN or installs cmoa on PATH so the comparison actually runs.
func TestEmbeddedMatchesLiveCMoA(t *testing.T) {
	bin := os.Getenv("UZUSHIO_CMOA_BIN")
	if bin == "" {
		found, err := exec.LookPath("cmoa")
		if err != nil {
			t.Skip("no cmoa on PATH and UZUSHIO_CMOA_BIN is unset; skipping the live comparison")
		}
		bin = found
	}
	out, err := exec.CommandContext(t.Context(), bin, "surfaces", "--format", "json").Output()
	if err != nil {
		t.Fatalf("%s surfaces --format json: %v", bin, err)
	}
	if string(out) != string(Embedded()) {
		t.Fatalf("cmoa-surfaces.json is stale; run go generate ./internal/surfaces\n--- live ---\n%s\n--- embedded ---\n%s", out, Embedded())
	}
}

// TestEmbeddedIsIndentedJSON keeps the committed file in the shape cmoa
// writes it, so `go generate` produces no diff beyond a real vocabulary change.
func TestEmbeddedIsIndentedJSON(t *testing.T) {
	var doc document
	if err := json.Unmarshal(Embedded(), &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	again, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if string(append(again, '\n')) != string(Embedded()) {
		t.Fatalf("the embedded file is not two-space indented JSON with a trailing newline:\n%s", Embedded())
	}
}
