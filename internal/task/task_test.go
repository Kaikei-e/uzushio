package task_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/task"
)

// v1 is the manifest CMoA shipped first. It has to keep loading: uzushio is a
// reader of someone else's schema, and a reader that refuses last year's file
// is a reader nobody can upgrade to.
const v1 = `{
  "version": 1,
  "id": "hello",
  "repo": "repo",
  "rev": "HEAD",
  "files": ["add.go", "add_test.go"],
  "verify": {"compose_file": "compose.yaml", "service": "verify"}
}
`

// v2 is the manifest the two task commands need, written the way CMoA's
// example writes it.
const v2 = `{
  "version": 2,
  "id": "hello",
  "repo": "repo",
  "rev": "HEAD",
  "files": ["add.go", "add_test.go"],
  "verify": {"compose_file": "compose.yaml", "service": "verify", "kind": "exit-code", "timeout_seconds": 600},
  "reference": {"diff": "reference.diff"},
  "mutants": [
    {"diff": "mutants/0001-add-minus.diff", "expect": "killed", "origin": "hand", "operator": "", "note": "Add subtracts"}
  ],
  "doctor": {"kill_rate_min": 0.9, "reference_runs": 5}
}
`

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "repo"), 0o755); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, task.ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", task.ManifestFile, err)
	}
	return dir
}

func TestLoadV1(t *testing.T) {
	loaded, err := task.Load(write(t, v1))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != 1 || loaded.ID != "hello" || loaded.Rev != "HEAD" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Verify.Kind != task.KindExitCode {
		t.Fatalf("verify.kind = %q, want the default %q", loaded.Verify.Kind, task.KindExitCode)
	}
	if loaded.Reference != nil || len(loaded.Mutants) != 0 {
		t.Fatalf("a version 1 task carries a reference or mutants: %+v", loaded)
	}
	// A version 1 task is a task CMoA runs and a task the doctor cannot
	// measure, and it has to say which of the two it is.
	err = loaded.RequireDoctorable()
	if !errors.Is(err, task.ErrTask) {
		t.Fatalf("RequireDoctorable error = %v, want ErrTask", err)
	}
	if !strings.Contains(err.Error(), "version 2 with a reference") {
		t.Errorf("RequireDoctorable error = %q, which does not say what is missing", err)
	}
}

func TestLoadV2(t *testing.T) {
	loaded, err := task.Load(write(t, v2))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Reference == nil || loaded.Reference.Path != "reference.diff" {
		t.Fatalf("reference = %+v", loaded.Reference)
	}
	if len(loaded.Mutants) != 1 {
		t.Fatalf("mutants = %+v", loaded.Mutants)
	}
	mutant := loaded.Mutants[0]
	if mutant.Expect != task.ExpectKilled || mutant.Origin != task.OriginHand {
		t.Fatalf("mutant = %+v", mutant)
	}
	if loaded.Doctor.KillRateMin != 0.9 || loaded.Doctor.ReferenceRuns != 5 {
		t.Fatalf("doctor = %+v", loaded.Doctor)
	}
	if loaded.Verify.TimeoutSeconds != 600 {
		t.Fatalf("verify.timeout_seconds = %d, want 600", loaded.Verify.TimeoutSeconds)
	}
	if err := loaded.RequireDoctorable(); err != nil {
		t.Fatalf("RequireDoctorable: %v", err)
	}
}

// TestAVersion2TaskWithNoMutantsIsCheckable records the line between "this
// manifest is broken" and "this check has nothing to measure". A task with no
// reference cannot be checked at all; a task with no mutant can, and the answer
// is that the kill rate was not measured — which the verdict spells
// inconclusive rather than the loader spelling it as a bad manifest.
func TestAVersion2TaskWithNoMutantsIsCheckable(t *testing.T) {
	loaded, err := task.Load(write(t, `{
  "version": 2, "id": "hello", "repo": "repo", "files": ["add.go"],
  "reference": {"diff": "reference.diff"}
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := loaded.RequireDoctorable(); err != nil {
		t.Fatalf("RequireDoctorable: %v", err)
	}
	noReference, err := task.Load(write(t, `{
  "version": 2, "id": "hello", "repo": "repo", "files": ["add.go"],
  "mutants": [{"diff": "mutants/0001.diff"}]
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := noReference.RequireDoctorable(); !errors.Is(err, task.ErrTask) {
		t.Fatalf("RequireDoctorable error = %v, want ErrTask", err)
	}
}

// TestCheckRewritable is the question a command asks before it does any work.
func TestCheckRewritable(t *testing.T) {
	if err := task.CheckRewritable(write(t, v2)); err != nil {
		t.Fatalf("CheckRewritable: %v", err)
	}
	err := task.CheckRewritable(write(t, `{
  "version": 2, "id": "hello", "repo": "repo", "files": ["add.go"],
  "something_cmoa_added_later": {"a": 1}
}`))
	if !errors.Is(err, task.ErrTask) {
		t.Fatalf("CheckRewritable error = %v, want ErrTask", err)
	}
	if !strings.Contains(err.Error(), "something_cmoa_added_later") {
		t.Errorf("CheckRewritable error = %q, which does not name the key", err)
	}
	if err := task.CheckRewritable(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CheckRewritable with no manifest = %v", err)
	}
}

// TestDefaults pins what a manifest that declares nothing optional means. They
// are CMoA's defaults, repeated here because a reader that guessed differently
// would call a task broken that CMoA runs.
func TestDefaults(t *testing.T) {
	loaded, err := task.Load(write(t, `{
  "version": 2, "id": "hello", "repo": "repo", "files": ["add.go"],
  "reference": {"diff": "reference.diff"},
  "mutants": [{"diff": "mutants/0001.diff"}]
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := task.Verify{ComposeFile: "compose.yaml", Service: "verify", Kind: task.KindExitCode}
	if loaded.Verify != want {
		t.Errorf("verify = %+v, want %+v", loaded.Verify, want)
	}
	if loaded.Rev != task.DefaultRev {
		t.Errorf("rev = %q, want %q", loaded.Rev, task.DefaultRev)
	}
	if loaded.Doctor.KillRateMin != task.DefaultKillRateMin {
		t.Errorf("kill_rate_min = %v, want %v", loaded.Doctor.KillRateMin, task.DefaultKillRateMin)
	}
	if loaded.Doctor.ReferenceRuns != task.DefaultReferenceRuns {
		t.Errorf("reference_runs = %d, want %d", loaded.Doctor.ReferenceRuns, task.DefaultReferenceRuns)
	}
	mutant := loaded.Mutants[0]
	if mutant.Expect != task.ExpectKilled || mutant.Origin != task.OriginHand {
		t.Errorf("mutant defaults = %+v, want killed and hand", mutant)
	}
}

// TestUnknownFieldsAreIgnored is the decision this package is built on. CMoA
// owns the schema; a key uzushio does not know is a key CMoA added, and
// refusing it would turn every CMoA release into a uzushio bug.
func TestUnknownFieldsAreIgnored(t *testing.T) {
	loaded, err := task.Load(write(t, `{
  "version": 2, "id": "hello", "repo": "repo", "files": ["add.go"],
  "max_context_bytes": 65536,
  "something_cmoa_added_later": {"a": 1},
  "reference": {"diff": "reference.diff"},
  "mutants": [{"diff": "mutants/0001.diff"}]
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != "hello" {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		phrase string
	}{
		{"version", `{"version": 3, "id": "a", "repo": "repo", "files": ["a.go"]}`, "version"},
		{"identifier", `{"version": 2, "id": "Hello", "repo": "repo", "files": ["a.go"]}`, "id"},
		{"no repo", `{"version": 2, "id": "a", "files": ["a.go"]}`, "repo"},
		{
			"verify kind",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "verify": {"kind": "score"}}`,
			"verify.kind",
		},
		{
			"negative timeout",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "verify": {"timeout_seconds": -1}}`,
			"verify.timeout_seconds",
		},
		{
			"absolute reference",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "reference": {"diff": "/etc/passwd"}}`,
			"reference.diff",
		},
		{
			"escaping mutant",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "mutants": [{"diff": "../x.diff"}]}`,
			"mutants[0].diff",
		},
		{
			"duplicate mutant",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"],
			  "mutants": [{"diff": "m/1.diff"}, {"diff": "m/1.diff"}]}`,
			"mutants[1].diff",
		},
		{
			"mutant expect",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "mutants": [{"diff": "m/1.diff", "expect": "maybe"}]}`,
			"mutants[0].expect",
		},
		{
			"mutant origin",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "mutants": [{"diff": "m/1.diff", "origin": "llm"}]}`,
			"mutants[0].origin",
		},
		{
			"kill rate",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "doctor": {"kill_rate_min": 1.5}}`,
			"doctor.kill_rate_min",
		},
		{
			"reference runs",
			`{"version": 2, "id": "a", "repo": "repo", "files": ["a.go"], "doctor": {"reference_runs": -2}}`,
			"doctor.reference_runs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := task.Load(write(t, tt.body))
			if !errors.Is(err, task.ErrTask) {
				t.Fatalf("Load error = %v, want ErrTask", err)
			}
			if !strings.Contains(err.Error(), tt.phrase) {
				t.Errorf("Load error = %q, want it to name %q", err, tt.phrase)
			}
		})
	}
}

// TestBandIsReservedAndRefused records the one verifier kind the schema
// accepts and no command runs: a task can be written before uzushio can
// measure it, and the refusal is a sentence rather than a wrong answer.
func TestBandIsReservedAndRefused(t *testing.T) {
	loaded, err := task.Load(write(t, `{
  "version": 2, "id": "a", "repo": "repo", "files": ["a.go"],
  "verify": {"kind": "band"},
  "reference": {"diff": "reference.diff"},
  "mutants": [{"diff": "m/1.diff"}]
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Verify.Kind != task.KindBand {
		t.Fatalf("verify.kind = %q, want band", loaded.Verify.Kind)
	}
	err = loaded.RequireExitCodeVerifier()
	if !errors.Is(err, task.ErrTask) || !strings.Contains(err.Error(), "band is not implemented") {
		t.Fatalf("RequireExitCodeVerifier error = %v, want the not-implemented message", err)
	}
}

// TestWriteRoundTrips is the contract `uzushio task mutate` rewrites through:
// keys in the order CMoA writes them, values unchanged, and a mutant appended.
func TestWriteRoundTrips(t *testing.T) {
	dir := write(t, v2)
	manifest, err := task.ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	manifest.Mutants = append(manifest.Mutants, task.MutantManifest{
		Diff:     "mutants/0002-arith-add-L7C11.diff",
		Expect:   task.ExpectKilled.String(),
		Origin:   task.OriginGenerated.String(),
		Operator: "arith",
		Note:     `add.go:7:11: "+" -> "-"`,
	})
	if err := manifest.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, task.ManifestFile))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// The note reads as a person wrote it. encoding/json escapes < and > by
	// default, which would render an arrow as an entity in a file people read.
	if !bytes.Contains(body, []byte("-> ")) || bytes.Contains(body, []byte(`\u003e`)) {
		t.Errorf("the rewritten manifest escaped the note:\n%s", body)
	}
	if !bytes.HasSuffix(body, []byte("\n")) {
		t.Error("the rewritten manifest has no trailing newline")
	}
	// The keys come back in the order CMoA declares them.
	order := []string{`"version"`, `"id"`, `"repo"`, `"rev"`, `"files"`, `"verify"`,
		`"reference"`, `"mutants"`, `"doctor"`}
	at := -1
	for _, key := range order {
		next := bytes.Index(body, []byte(key))
		if next <= at {
			t.Fatalf("%s is out of order in:\n%s", key, body)
		}
		at = next
	}

	// And the file is one CMoA reads: a strict decode into the version 2
	// fields CMoA declares, which is what CMoA itself does.
	reloaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load after Write: %v", err)
	}
	if len(reloaded.Mutants) != 2 || reloaded.Mutants[1].Operator != "arith" {
		t.Fatalf("mutants after Write = %+v", reloaded.Mutants)
	}
	if err := decodeAsCMoA(body); err != nil {
		t.Fatalf("CMoA would refuse the rewritten manifest: %v", err)
	}
}

// TestWriteRefusesToDropAField is the other side of ignoring unknown keys.
// Reading leniently and writing through a typed struct would delete a key CMoA
// added, so the rewrite is the one operation that is strict.
func TestWriteRefusesToDropAField(t *testing.T) {
	dir := write(t, `{
  "version": 2, "id": "hello", "repo": "repo", "files": ["add.go"],
  "something_cmoa_added_later": {"a": 1}
}`)
	manifest, err := task.ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	err = manifest.Write(dir)
	if !errors.Is(err, task.ErrTask) {
		t.Fatalf("Write error = %v, want ErrTask", err)
	}
	if !strings.Contains(err.Error(), "something_cmoa_added_later") {
		t.Errorf("Write error = %q, which does not name the key", err)
	}
}

// cmoaManifest is CMoA's version 2 schema as its documentation declares it. It
// is written out here rather than imported, because uzushio does not import
// CMoA — the point of the test is that the bytes uzushio writes are bytes a
// strict decoder over exactly those fields accepts.
type cmoaManifest struct {
	Version         int      `json:"version"`
	ID              string   `json:"id"`
	Repo            string   `json:"repo"`
	Rev             string   `json:"rev"`
	Files           []string `json:"files"`
	MaxContextBytes int      `json:"max_context_bytes"`
	Verify          struct {
		ComposeFile    string `json:"compose_file"`
		Service        string `json:"service"`
		Kind           string `json:"kind"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	} `json:"verify"`
	Reference struct {
		Diff string `json:"diff"`
	} `json:"reference"`
	Mutants []struct {
		Diff     string `json:"diff"`
		Expect   string `json:"expect"`
		Origin   string `json:"origin"`
		Operator string `json:"operator"`
		Note     string `json:"note"`
	} `json:"mutants"`
	Doctor struct {
		KillRateMin   float64 `json:"kill_rate_min"`
		ReferenceRuns int     `json:"reference_runs"`
	} `json:"doctor"`
}

func decodeAsCMoA(body []byte) error {
	var m cmoaManifest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(&m)
}

// TestExampleTaskIsCMoAReadable holds the example this repository ships to the
// same decoder. It is the one task uzushio commits, and a task CMoA cannot
// read is an example that teaches the wrong thing.
func TestExampleTaskIsCMoAReadable(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "task-hello", task.ManifestFile)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no example task: %v", err)
	}
	if err := decodeAsCMoA(body); err != nil {
		t.Fatalf("CMoA would refuse %s: %v", path, err)
	}
}
