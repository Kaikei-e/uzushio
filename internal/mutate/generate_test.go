package mutate_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/mutate"
	"github.com/Kaikei-e/uzushio/internal/task"
)

// add.go before and after the reference diff. The seed subtracts, which is the
// bug; the reference adds, which is the solution the mutants are cut from.
const (
	seed = `package hello

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a - b
}
`
	solved = `package hello

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}
`
	addTest = `package hello

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("Add is wrong")
	}
}
`
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=uzushio", "-c", "user.email=uzushio@example.com",
	}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// newTaskDir builds a task directory with a real git repository, a reference
// diff that fixes the seed bug, and a manifest that names both. The diffs are
// git's own, which is what the task carries in life.
func newTaskDir(t *testing.T, manifest string) string {
	t.Helper()
	return newTaskDirWith(t, seed, solved, manifest)
}

// newTaskDirWith is newTaskDir over a chosen pair of sources.
func newTaskDirWith(t *testing.T, seedSrc, solvedSrc, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	writeFile(t, filepath.Join(repo, "add.go"), seedSrc)
	writeFile(t, filepath.Join(repo, "add_test.go"), addTest)
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/hello\n\ngo 1.27\n")
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "seed")

	writeFile(t, filepath.Join(repo, "add.go"), solvedSrc)
	writeFile(t, filepath.Join(dir, "reference.diff"), git(t, repo, "diff"))
	git(t, repo, "reset", "--hard", "--quiet", "HEAD")

	writeFile(t, filepath.Join(dir, task.ManifestFile), manifest)
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const bareManifest = `{
  "version": 2,
  "id": "hello",
  "repo": "repo",
  "rev": "HEAD",
  "files": ["add.go", "add_test.go"],
  "reference": {"diff": "reference.diff"}
}
`

func plan(t *testing.T, dir string, opts mutate.Options) *mutate.Plan {
	t.Helper()
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := mutate.Generate(t.Context(), loaded, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return got
}

func generate(t *testing.T, dir string, opts mutate.Options) []mutate.Planned {
	t.Helper()
	return plan(t, dir, opts).Mutants
}

func TestGenerate(t *testing.T) {
	dir := newTaskDir(t, bareManifest)
	planned := generate(t, dir, mutate.Options{})
	// add_test.go is skipped, so everything comes from add.go's one line.
	want := []string{
		"mutants/0001-ret-add-L5C9.diff",
		"mutants/0002-arith-add-L5C11.diff",
	}
	got := make([]string, 0, len(planned))
	for _, mutant := range planned {
		got = append(got, mutant.Path)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("planned %v, want %v", got, want)
	}
	for _, mutant := range planned {
		if !strings.HasPrefix(mutant.Diff, "diff --git a/add.go b/add.go\n") {
			t.Fatalf("%s does not name the repository path:\n%s", mutant.Path, mutant.Diff)
		}
	}
}

// TestGeneratedDiffsApplyOnTopOfTheReference is the property the whole design
// rests on. A mutant is written against the reference-applied tree, and
// `uzushio task doctor` applies the reference and then the mutant into a
// worktree of its own before handing the result to `cmoa verify`. A diff that
// did not apply there would be reported as inconclusive for the rest of the
// task's life, and nothing else would say why.
func TestGeneratedDiffsApplyOnTopOfTheReference(t *testing.T) {
	dir := newTaskDir(t, bareManifest)
	planned := generate(t, dir, mutate.Options{})
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := mutate.Write(loaded, planned); err != nil {
		t.Fatalf("Write: %v", err)
	}
	repo := filepath.Join(dir, "repo")
	for _, mutant := range planned {
		tree := filepath.Join(t.TempDir(), "tree")
		git(t, repo, "worktree", "add", "--detach", "--quiet", tree, "HEAD")
		git(t, tree, "apply", "--index", filepath.Join(dir, "reference.diff"))
		git(t, tree, "apply", "--index", filepath.Join(dir, filepath.FromSlash(mutant.Path)))
		body, err := os.ReadFile(filepath.Join(tree, "add.go"))
		if err != nil {
			t.Fatalf("read the mutated file: %v", err)
		}
		if string(body) == solved || string(body) == seed && mutant.Operator != mutate.OpArith {
			t.Fatalf("%s changed nothing:\n%s", mutant.Path, body)
		}
		git(t, repo, "worktree", "remove", "--force", tree)
	}
}

// TestGenerateIsDeterministic runs twice against the same task and demands the
// same names and the same bytes. The mutants are committed artefacts; one that
// changed between runs would make every regeneration a diff.
func TestGenerateIsDeterministic(t *testing.T) {
	first := generate(t, newTaskDir(t, bareManifest), mutate.Options{})
	second := generate(t, newTaskDir(t, bareManifest), mutate.Options{})
	if len(first) != len(second) {
		t.Fatalf("%d mutants then %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Path != second[i].Path || first[i].Diff != second[i].Diff {
			t.Fatalf("mutant %d differs:\n%s\n%s", i, first[i].Diff, second[i].Diff)
		}
	}
}

// TestDedupeAndNumbering is the two things that make a second run safe: a
// mutant the task already carries is not written again under a new number, and
// a number the task already uses is not reused.
func TestDedupeAndNumbering(t *testing.T) {
	dir := newTaskDir(t, bareManifest)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	planned := generate(t, dir, mutate.Options{})
	entries, err := mutate.Write(loaded, planned)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	manifest, err := task.ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	manifest.Mutants = append(manifest.Mutants, entries...)
	if err := manifest.Write(dir); err != nil {
		t.Fatalf("Write manifest: %v", err)
	}

	// Everything the operators find is already there, so a second run plans
	// nothing at all.
	if again := generate(t, dir, mutate.Options{}); len(again) != 0 {
		t.Fatalf("a second run planned %d mutant(s): %+v", len(again), again)
	}

	// A mutant the manifest forgot but that is still on disk — an orphan, the
	// state an interrupted run leaves behind — is not written again. The
	// dedupe reads the directory as well as the manifest, which is the same
	// union the numbering takes, and for the same reason: a diff on disk is a
	// mutant that exists whatever the manifest says.
	manifest.Mutants = manifest.Mutants[:1]
	if err := manifest.Write(dir); err != nil {
		t.Fatalf("Write manifest: %v", err)
	}
	if again := generate(t, dir, mutate.Options{}); len(again) != 0 {
		t.Fatalf("planned %d mutant(s) that are already on disk: %+v", len(again), again)
	}

	// Delete the orphan and it comes back — under the next free number, not
	// the one it used to have, because the number is still taken by nothing.
	orphan := entries[len(entries)-1].Diff
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(orphan))); err != nil {
		t.Fatalf("remove the orphan: %v", err)
	}
	again := generate(t, dir, mutate.Options{})
	if len(again) != 1 {
		t.Fatalf("planned %d mutant(s), want the one that was deleted", len(again))
	}
	if !strings.HasPrefix(again[0].Path, "mutants/0002-") {
		t.Fatalf("planned %q, want the next free number", again[0].Path)
	}
}

func TestMax(t *testing.T) {
	dir := newTaskDir(t, bareManifest)
	if planned := generate(t, dir, mutate.Options{Max: 1}); len(planned) != 1 {
		t.Fatalf("--max 1 planned %d mutant(s)", len(planned))
	}
}

// TestGenerateNeedsAReference records the one thing a version 1 task cannot
// do: there is nothing to cut a mutant from.
func TestGenerateNeedsAReference(t *testing.T) {
	dir := newTaskDir(t, `{"version": 1, "id": "hello", "repo": "repo", "files": ["add.go"]}`)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = mutate.Generate(context.Background(), loaded, mutate.Options{})
	if err == nil || !strings.Contains(err.Error(), "reference") {
		t.Fatalf("Generate error = %v, want it to ask for a reference", err)
	}
}

// nonViableSeed has one site the syntactic rules cannot refuse: `a + b` on two
// string variables. The arith operator offers `a - b`, which is not defined on
// strings, so the mutant does not compile — and a verifier that did nothing but
// build the code would score it killed.
const nonViableSeed = `package hello

func Add(a, b int) int {
	return a - b
}

func Join(a, b string) string {
	return a + b
}
`

const nonViableSolved = `package hello

func Add(a, b int) int {
	return a + b
}

func Join(a, b string) string {
	return a + b
}
`

// TestNonViableMutantsAreDropped is the measurement fix. Every tool that
// reports honestly separates "the tests caught it" from "the compiler did";
// uzushio does it once, at generation, so a health check stays a comparison of
// exit codes and nothing else.
func TestNonViableMutantsAreDropped(t *testing.T) {
	dir := newTaskDirWith(t, nonViableSeed, nonViableSolved, bareManifest)

	dropped := plan(t, dir, mutate.Options{Operators: []mutate.Operator{mutate.OpArith}})
	if len(dropped.Mutants) != 1 {
		t.Fatalf("planned %d mutant(s), want only the one that compiles: %+v", len(dropped.Mutants), dropped.Mutants)
	}
	if !strings.Contains(dropped.Mutants[0].File, "add.go") || dropped.Mutants[0].Line != 4 {
		t.Fatalf("the surviving mutant is %+v", dropped.Mutants[0])
	}
	if dropped.NotViable[mutate.OpArith] != 1 || dropped.NotViableTotal() != 1 {
		t.Fatalf("NotViable = %v, want one arith candidate dropped", dropped.NotViable)
	}

	// --keep-nonviable is the escape hatch, and it says what it keeps.
	kept := plan(t, newTaskDirWith(t, nonViableSeed, nonViableSolved, bareManifest), mutate.Options{
		Operators: []mutate.Operator{mutate.OpArith}, KeepNonViable: true,
	})
	if len(kept.Mutants) != 2 {
		t.Fatalf("--keep-nonviable planned %d mutant(s), want both", len(kept.Mutants))
	}
	if kept.NotViableTotal() != 0 {
		t.Fatalf("--keep-nonviable counted %v as dropped", kept.NotViable)
	}
}

// TestSkippedFilesAreReported is L-8: `Skip` knows why it skipped a file, and
// a run whose files are all test files should say so rather than reporting
// nothing found.
func TestSkippedFilesAreReported(t *testing.T) {
	dir := newTaskDir(t, bareManifest)
	got := plan(t, dir, mutate.Options{})
	if len(got.Skipped) != 1 || got.Skipped[0].File != "add_test.go" ||
		got.Skipped[0].Reason != "a test file" {
		t.Fatalf("Skipped = %+v, want add_test.go as a test file", got.Skipped)
	}
}

// TestGenerateUnderACancelledContextLeavesNoWorktree is the other half of
// signal handling: the run stops, and the task's repository is not left
// carrying a registration for a directory that no longer exists.
func TestGenerateUnderACancelledContextLeavesNoWorktree(t *testing.T) {
	dir := newTaskDir(t, bareManifest)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := mutate.Generate(ctx, loaded, mutate.Options{}); err == nil {
		t.Fatal("Generate succeeded under a cancelled context")
	}
	listed := git(t, filepath.Join(dir, "repo"), "worktree", "list")
	if strings.Count(strings.TrimSpace(listed), "\n") != 0 {
		t.Fatalf("a worktree was left registered:\n%s", listed)
	}
}
