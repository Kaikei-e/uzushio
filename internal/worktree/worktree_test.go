package worktree_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/worktree"
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

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const before = "package hello\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"

// hostileRepo is a repository configured the way a developer's own might be,
// with exactly the settings that would change the bytes of a diff: no a/ and
// b/ prefixes, four-digit index hashes, and mnemonic prefixes. Everything this
// package produces is a committed artefact, so a user's ~/.gitconfig must not
// reach it — which is what the -c overrides are for, and this is the test that
// they work.
func hostileRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	writeFile(t, filepath.Join(repo, "add.go"), before)
	git(t, repo, "init", "-q")
	git(t, repo, "config", "diff.noprefix", "true")
	git(t, repo, "config", "diff.mnemonicprefix", "true")
	git(t, repo, "config", "core.abbrev", "4")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "seed")
	return repo
}

var indexLine = regexp.MustCompile(`(?m)^index ([0-9a-f]+)\.\.([0-9a-f]+) `)

func TestDiffsIgnoreTheUsersGitConfig(t *testing.T) {
	repo := hostileRepo(t)
	tree, err := worktree.Add(t.Context(), repo, "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer tree.Close()

	if err := tree.Write("add.go", []byte(strings.Replace(before, "a - b", "a + b", 1))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	diff, err := tree.Unstaged(t.Context(), "add.go")
	if err != nil {
		t.Fatalf("Unstaged: %v", err)
	}
	for _, want := range []string{"diff --git a/add.go b/add.go", "--- a/add.go", "+++ b/add.go"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the diff does not carry %q despite diff.noprefix being set:\n%s", want, diff)
		}
	}
	match := indexLine.FindStringSubmatch(diff)
	if match == nil {
		t.Fatalf("the diff has no index line:\n%s", diff)
	}
	// core.abbrev=4 in the repository, 12 pinned here.
	if len(match[1]) != 12 || len(match[2]) != 12 {
		t.Errorf("index hashes are %d and %d characters, want 12:\n%s", len(match[1]), len(match[2]), diff)
	}
	// Three lines of context, whatever the user's diff.context says.
	if strings.Count(diff, "\n") < 8 {
		t.Errorf("the diff is shorter than three lines of context:\n%s", diff)
	}
}

// TestApplyThenStagedIsOnePatch is the property `uzushio task doctor` composes
// a mutant with: two diffs applied into the index read back out as one patch
// against the revision.
func TestApplyThenStagedIsOnePatch(t *testing.T) {
	repo := hostileRepo(t)
	ctx := t.Context()
	tree, err := worktree.Add(ctx, repo, "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer tree.Close()

	dir := t.TempDir()
	if err := tree.Write("add.go", []byte(strings.Replace(before, "a - b", "a + b", 1))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	first, err := tree.Unstaged(ctx, "add.go")
	if err != nil {
		t.Fatalf("Unstaged: %v", err)
	}
	writeFile(t, filepath.Join(dir, "reference.diff"), first)
	if err := tree.Restore(ctx, "add.go"); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if err := tree.Apply(ctx, filepath.Join(dir, "reference.diff")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := tree.Write("add.go", []byte(strings.Replace(before, "a - b", "a * b", 1))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	mutant, err := tree.Unstaged(ctx, "add.go")
	if err != nil {
		t.Fatalf("Unstaged: %v", err)
	}
	writeFile(t, filepath.Join(dir, "mutant.diff"), mutant)
	if err := tree.Restore(ctx, "add.go"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := tree.Apply(ctx, filepath.Join(dir, "mutant.diff")); err != nil {
		t.Fatalf("Apply the mutant: %v", err)
	}
	combined, err := tree.Staged(ctx)
	if err != nil {
		t.Fatalf("Staged: %v", err)
	}
	// One patch, against the revision: the seed line out, the mutant line in,
	// and no sign of the intermediate state.
	if !strings.Contains(combined, "-\treturn a - b") || !strings.Contains(combined, "+\treturn a * b") {
		t.Fatalf("the combined patch is not seed-to-mutant:\n%s", combined)
	}
	if strings.Contains(combined, "a + b") {
		t.Fatalf("the combined patch mentions the intermediate state:\n%s", combined)
	}

	// And Reset undoes both.
	if err := tree.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	back, err := tree.Staged(ctx)
	if err != nil {
		t.Fatalf("Staged after Reset: %v", err)
	}
	if strings.TrimSpace(back) != "" {
		t.Fatalf("Reset left something behind:\n%s", back)
	}
}

// TestApplyAnEmptyDiffIsANoOp records the case a caller may legitimately hand
// over: "no change at all", which git refuses as an empty patch.
func TestApplyAnEmptyDiffIsANoOp(t *testing.T) {
	repo := hostileRepo(t)
	tree, err := worktree.Add(t.Context(), repo, "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer tree.Close()
	empty := filepath.Join(t.TempDir(), "empty.diff")
	writeFile(t, empty, "")
	if err := tree.Apply(t.Context(), empty); err != nil {
		t.Fatalf("Apply an empty diff: %v", err)
	}
}

// TestErrorKeepsArgvOutOfTheDiagnostics is the split M-2 rests on: the person
// reading stderr gets the whole command, and the record gets git's own
// complaint — which is the half with no absolute path in it.
func TestErrorKeepsArgvOutOfTheDiagnostics(t *testing.T) {
	repo := hostileRepo(t)
	tree, err := worktree.Add(t.Context(), repo, "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer tree.Close()

	bogus := filepath.Join(t.TempDir(), "bogus.diff")
	writeFile(t, bogus, "--- a/gone.go\n+++ b/gone.go\n@@ -1,1 +1,1 @@\n-a\n+b\n")
	err = tree.Apply(t.Context(), bogus)
	if !errors.Is(err, worktree.ErrGit) {
		t.Fatalf("Apply error = %v, want ErrGit", err)
	}
	var gitErr *worktree.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("Apply error = %v, want a *worktree.Error", err)
	}
	if !strings.Contains(err.Error(), bogus) {
		t.Errorf("the error a person reads does not name the diff: %v", err)
	}
	diagnostics := worktree.Diagnostics(err)
	if !strings.HasPrefix(diagnostics, "error: ") {
		t.Errorf("the diagnostics are not git's own: %q", diagnostics)
	}
	if strings.Contains(diagnostics, bogus) || strings.Contains(diagnostics, tree.Dir()) {
		t.Errorf("the diagnostics carry an absolute path: %q", diagnostics)
	}
}

// TestCloseWorksUnderACancelledContext is what signal handling rests on. The
// command's context is cancelled on Ctrl-C, and the deferred Close then has to
// remove the worktree anyway — a registration left behind in the task's
// repository outlives the run and only `git worktree prune` clears it.
func TestCloseWorksUnderACancelledContext(t *testing.T) {
	repo := hostileRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	tree, err := worktree.Add(ctx, repo, "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	dir := tree.Dir()
	cancel()

	if err := tree.Close(); err != nil {
		t.Fatalf("Close under a cancelled context: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree directory is still there: %v", err)
	}
	if listed := git(t, repo, "worktree", "list"); strings.Contains(listed, dir) {
		t.Errorf("the worktree is still registered:\n%s", listed)
	}
	// Close twice is not a mistake: a deferred one beside an explicit one is
	// the ordinary shape.
	if err := tree.Close(); err != nil {
		t.Errorf("Close twice: %v", err)
	}
}

func TestResolveRev(t *testing.T) {
	repo := hostileRepo(t)
	rev, err := worktree.ResolveRev(t.Context(), repo, "HEAD")
	if err != nil {
		t.Fatalf("ResolveRev: %v", err)
	}
	if len(rev) != 40 {
		t.Fatalf("ResolveRev = %q, want a full commit", rev)
	}
	if _, err := worktree.ResolveRev(t.Context(), repo, "no-such-ref"); !errors.Is(err, worktree.ErrGit) {
		t.Fatalf("ResolveRev error = %v, want ErrGit", err)
	}
}
