package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/loop"
	"github.com/Kaikei-e/uzushio/internal/render"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// TestHarnessRender drives the command end to end against a vault of the
// test's own: the seed, then the seed with a candidate in it, and the digest
// on stdout that a caller pipes into a comparison.
func TestHarnessRender(t *testing.T) {
	engine := requireDocDag(t)
	vault := harnessVault(t)
	out := filepath.Join(t.TempDir(), "harness")

	seed := run(t, "harness", "render", "--vault", vault, "--out", out,
		"--as-of", "2026-09-05", "--docdag", engine)
	if seed.code != exitOK {
		t.Fatalf("render exit = %d, stderr = %q", seed.code, seed.stderr)
	}
	digest := strings.TrimSpace(seed.stdout)
	if len(digest) != 64 {
		t.Fatalf("render printed %q, want a tree digest", seed.stdout)
	}
	if _, err := os.Stat(filepath.Join(out, render.ManifestName)); err != nil {
		t.Fatalf("render.json: %v", err)
	}

	// A second render into the same directory is refused, because a render
	// that merged into whatever was there would produce a tree no manifest
	// describes.
	again := run(t, "harness", "render", "--vault", vault, "--out", out,
		"--as-of", "2026-09-05", "--docdag", engine)
	if again.code == exitOK {
		t.Error("render overwrote a non-empty directory without --force")
	}

	withEdit := filepath.Join(t.TempDir(), "candidate")
	edited := run(t, "harness", "render", "--vault", vault, "--out", withEdit,
		"--as-of", "2026-09-05", "--docdag", engine, "--with-edit", "he-0001", "--json")
	if edited.code != exitOK {
		t.Fatalf("render --with-edit exit = %d, stderr = %q", edited.code, edited.stderr)
	}
	var manifest render.Manifest
	if err := json.Unmarshal([]byte(edited.stdout), &manifest); err != nil {
		t.Fatalf("--json did not print a manifest: %v\n%s", err, edited.stdout)
	}
	if manifest.TreeSHA256 == digest {
		t.Error("adding the candidate left the tree unchanged")
	}
	if len(manifest.Edits) != 1 || manifest.Edits[0].ID != "he-0001" {
		t.Errorf("the manifest records %+v, want the candidate", manifest.Edits)
	}
	// The digest the manifest states is the digest of the directory on disk,
	// which is the number the harness will recompute for itself.
	got, err := render.Digest(withEdit)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if got != manifest.TreeSHA256 {
		t.Errorf("the manifest says %s and the directory hashes to %s", manifest.TreeSHA256, got)
	}
}

// TestHarnessRenderRefusesWithoutAnOutput: the flag is required because there
// is no sensible default for a directory a render will overwrite.
func TestHarnessRenderRefusesWithoutAnOutput(t *testing.T) {
	got := run(t, "harness", "render", "--vault", ".")
	if got.code != exitUsage {
		t.Errorf("exit = %d, want %d", got.code, exitUsage)
	}
}

// TestRunDryRun plans a run without spending anything, and then replays the
// header it wrote — which is empty of trials and so trivially consistent.
func TestRunDryRun(t *testing.T) {
	engine := requireDocDag(t)
	vault := harnessVault(t)
	suite := filepath.Join(t.TempDir(), "suite.json")
	harnessWriteSuite(t, suite)
	out := filepath.Join(t.TempDir(), "run")

	got := run(t, "run", "--vault", vault, "--edit", "he-0001", "--suite", suite,
		"--config", harnessConfig(t, t.TempDir()),
		"--out", out, "--docdag", engine, "--as-of", "2026-09-05", "--dry-run")
	if got.code != exitOK {
		t.Fatalf("run --dry-run exit = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "dry-run he-0001 screening") {
		t.Errorf("stdout = %q", got.stdout)
	}
	header, err := loop.ReadHeader(filepath.Join(out, loop.HeaderName))
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if !header.DryRun {
		t.Error("the header does not say it was a dry run")
	}
	if header.DecisionRule == "" {
		t.Error("the header carries no decision rule")
	}
	if _, err := os.Stat(filepath.Join(out, loop.TrialsName)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a dry run wrote a trial journal")
	}
}

// TestRunWritesBesideTheSuite: a run writes rendered harness trees, a journal
// and a baseline cache it shares with sibling runs. All of it belongs beside
// the suite that was measured, under one ignore rule, rather than in whatever
// directory somebody happened to be standing in.
func TestRunWritesBesideTheSuite(t *testing.T) {
	engine := requireDocDag(t)
	vault := harnessVault(t)
	suiteDir := t.TempDir()
	suite := filepath.Join(suiteDir, "suite.json")
	harnessWriteSuite(t, suite)

	got := run(t, "run", "--vault", vault, "--edit", "he-0001", "--suite", suite,
		"--config", harnessConfig(t, suiteDir), "--docdag", engine, "--as-of", "2026-09-05", "--dry-run")
	if got.code != exitOK {
		t.Fatalf("run --dry-run exit = %d, stderr = %q", got.code, got.stderr)
	}
	out := strings.TrimSpace(lastLine(got.stdout))
	if !strings.HasPrefix(out, filepath.Join(suiteDir, "runs")+string(filepath.Separator)) {
		t.Errorf("the run wrote to %q, want a directory under %q", out, filepath.Join(suiteDir, "runs"))
	}
	if _, err := os.Stat(filepath.Join(out, loop.HeaderName)); err != nil {
		t.Errorf("run.json: %v", err)
	}
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return lines[len(lines)-1]
}

// TestRunRefusesAnEditThatIsNotAProposal answers a refusal with a usage exit
// code: the invocation was wrong, not the edit.
func TestRunRefusesAnEditThatIsNotAProposal(t *testing.T) {
	engine := requireDocDag(t)
	vault := harnessVault(t)
	edit := harnessCandidate()
	edit.Status = vocab.StatusAccepted
	edit.Approval = vocab.ApprovalHuman
	edit.ApprovedBy = "somebody"
	harnessWriteEdit(t, vault, edit)
	suite := filepath.Join(t.TempDir(), "suite.json")
	harnessWriteSuite(t, suite)

	got := run(t, "run", "--vault", vault, "--edit", "he-0001", "--suite", suite,
		"--config", harnessConfig(t, t.TempDir()),
		"--out", filepath.Join(t.TempDir(), "run"), "--docdag", engine, "--dry-run")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %q", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "measures a proposal") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

// --- helpers ---

func requireDocDag(t *testing.T) string {
	t.Helper()
	if named := os.Getenv("UZUSHIO_DOCDAG_BIN"); named != "" {
		return named
	}
	found, err := exec.LookPath("docdag")
	if err != nil {
		t.Skip("no docdag on PATH; the binding set is its answer and nothing substitutes for it")
	}
	return found
}

// harnessVault copies the repository's configuration and specification corpus
// and adds one proposed edit to measure.
func harnessVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	harnessWrite(t, filepath.Join(root, "docdag.yaml"), harnessRead(t, "../../docdag.yaml"))
	specRoot := filepath.Join("..", "..", "spec")
	err := filepath.WalkDir(specRoot, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(specRoot, p)
		if relErr != nil {
			return relErr
		}
		harnessWrite(t, filepath.Join(root, "spec", relative), harnessRead(t, p))
		return nil
	})
	if err != nil {
		t.Fatalf("copy spec: %v", err)
	}
	harnessWriteEdit(t, root, harnessCandidate())
	return root
}

func harnessCandidate() doc.Edit {
	return doc.Edit{
		EditID: "he-0001", Title: "Say what the verifier runs", Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "memory", Touches: []string{"memory"},
		Paths: []string{"memory/00-verifier.md"}, Approval: vocab.ApprovalAuto,
		About:    []string{"topic/harness-improvement"},
		Predicts: []doc.Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body:     "The verifier runs the tests in a container.",
	}
}

func harnessWriteEdit(t *testing.T, vault string, edit doc.Edit) {
	t.Helper()
	relative, err := edit.Path()
	if err != nil {
		t.Fatal(err)
	}
	body, err := edit.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	harnessWrite(t, filepath.Join(vault, filepath.FromSlash(relative)), string(body))
}

// harnessConfig writes the fleet a run names. It is required: the fleet is
// the model slug in every document a run writes and it keys the baseline
// cache, so a run without one is not reproducible.
func harnessConfig(t *testing.T, dir string) string {
	t.Helper()
	name := filepath.Join(dir, "cmoa.json")
	harnessWrite(t, name, `{"version":1,"proposers":[`+
		`{"id":"one","model":"a-model","base_url":"http://127.0.0.1:8081/v1"}]}`)
	return name
}

func harnessWriteSuite(t *testing.T, name string) {
	t.Helper()
	var tasks []string
	for i := 1; i <= 8; i++ {
		tasks = append(tasks,
			`{"id":"in-`+harnessItoa(i)+`","dir":"in-`+harnessItoa(i)+`","split":"held-in"}`,
			`{"id":"out-`+harnessItoa(i)+`","dir":"out-`+harnessItoa(i)+`","split":"held-out"}`)
	}
	harnessWrite(t, name, `{"schema_version":1,"id":"suite-under-test","min_tasks_per_split":8,"tasks":[`+
		strings.Join(tasks, ",")+`]}`)
}

func harnessItoa(n int) string { return string(rune('0' + n)) }

func harnessWrite(t *testing.T, target, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil { //nolint:gosec // a test fixture
		t.Fatal(err)
	}
}

func harnessRead(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name) //nolint:gosec // a test fixture
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
