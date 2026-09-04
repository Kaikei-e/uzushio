// Package worktree is the small amount of git uzushio needs: a throwaway
// checkout of a task's repository at one revision, the diffs applied into it,
// and the unified diff read back out.
//
// It shells out to git rather than reimplementing any of it. Two of the things
// this package does are exactly what git is for and nothing else is: applying a
// unified diff to a tree, and producing one that git will apply again. A diff
// git wrote is a diff git accepts, which is the property `uzushio task doctor`
// and `uzushio task mutate` both rest on — the diffs they produce are handed
// straight to `cmoa verify`, which applies them with git in a worktree of its
// own.
//
// Every diff-producing command is run with the formatting options pinned. A
// user's diff.noprefix, core.autocrlf or an external diff driver would
// otherwise change the bytes of a committed artefact from one machine to the
// next.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrGit is the sentinel every git failure wraps.
var ErrGit = errors.New("worktree: git")

// Error is a git command that failed. It keeps the argument list and git's own
// diagnostics apart, because they go to different places: the argument list
// names the task directory and the diff being applied, both absolute, and is
// for the person reading stderr — while Stderr is git's own complaint, which
// is what a report that will be committed may carry.
type Error struct {
	Args   []string
	Dir    string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s in %s: %v: %s", strings.Join(e.Args, " "), e.Dir, e.Err, e.Stderr)
}

// Unwrap returns both the sentinel and the underlying failure, so
// errors.Is(err, ErrGit) holds and the exec error is still reachable.
func (e *Error) Unwrap() []error { return []error{ErrGit, e.Err} }

// Diagnostics returns git's own message for an error, or the error's text
// where it did not come from git. It is what goes into a record.
func Diagnostics(err error) string {
	var gitErr *Error
	if errors.As(err, &gitErr) && strings.TrimSpace(gitErr.Stderr) != "" {
		return gitErr.Stderr
	}
	return err.Error()
}

// diffConfig pins the configuration a diff's bytes depend on. core.abbrev is
// pinned too: the index line's hash length otherwise depends on how many
// objects the repository holds, so the same content would diff differently in a
// repository that had grown.
var diffConfig = []string{
	"-c", "core.autocrlf=false",
	"-c", "core.abbrev=12",
	"-c", "core.quotepath=false",
	"-c", "diff.noprefix=false",
	"-c", "diff.mnemonicprefix=false",
	"-c", "diff.external=",
}

// diffFlags pins the rest of it: three lines of context, git's own a/ and b/
// prefixes, and nothing a .gitattributes driver could reach.
var diffFlags = []string{
	"--no-ext-diff", "--no-color", "--no-textconv", "--no-renames", "-U3",
	"--src-prefix=a/", "--dst-prefix=b/",
}

// Tree is a detached worktree of one repository at one revision, in a
// directory of its own. Close removes it.
type Tree struct {
	repo   string
	parent string
	dir    string
}

// Add creates a detached worktree of repo at rev under a new temporary
// directory. The caller closes it.
func Add(ctx context.Context, repo, rev string) (*Tree, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	parent, err := os.MkdirTemp("", "uzushio-worktree-")
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	dir := filepath.Join(parent, "tree")
	if _, err := run(ctx, abs, "worktree", "add", "--detach", "--quiet", dir, rev); err != nil {
		// The directory was made a moment ago and holds nothing; the failure
		// worth reporting is git's.
		_ = os.RemoveAll(parent)
		return nil, err
	}
	return &Tree{repo: abs, parent: parent, dir: dir}, nil
}

// Dir is the worktree's root.
func (t *Tree) Dir() string { return t.dir }

// Close removes the worktree and the directory it lived in. It is safe to call
// twice, so a deferred Close beside an explicit one is not a mistake.
func (t *Tree) Close() error {
	if t.dir == "" {
		return nil
	}
	_, err := run(context.WithoutCancel(context.Background()), t.repo,
		"worktree", "remove", "--force", t.dir)
	removeErr := os.RemoveAll(t.parent)
	t.dir, t.parent = "", ""
	if err != nil {
		return err
	}
	if removeErr != nil {
		return fmt.Errorf("worktree: %w", removeErr)
	}
	return nil
}

// Apply applies a unified diff into the worktree and its index. An empty file
// is a no-op rather than an error: "no change at all" is a diff a caller may
// legitimately hand over, and git refuses an empty patch.
//
// --index is what makes the result readable back out as one diff against the
// revision: a file the patch creates is in the index, so it shows up in
// Staged, which a plain worktree apply would leave untracked and invisible.
func (t *Tree) Apply(ctx context.Context, diffPath string) error {
	body, err := os.ReadFile(diffPath)
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	_, err = run(ctx, t.dir, "apply", "--index", "--whitespace=nowarn", diffPath)
	return err
}

// Reset puts the worktree and its index back to the revision it was made at,
// undoing every Apply. It is how one throwaway checkout serves a whole run of
// mutants: reset, apply the reference, apply the mutant, read the combined
// diff, and start again — rather than reverse-applying, which leaves residue
// behind the first patch that does not reverse cleanly.
func (t *Tree) Reset(ctx context.Context) error {
	if _, err := run(ctx, t.dir, "reset", "--hard", "--quiet", "HEAD"); err != nil {
		return err
	}
	_, err := run(ctx, t.dir, "clean", "-fdq")
	return err
}

// Staged returns everything applied so far as one unified diff against the
// revision the worktree was made at.
func (t *Tree) Staged(ctx context.Context) (string, error) {
	args := append([]string{}, diffConfig...)
	args = append(args, "diff", "--cached")
	args = append(args, diffFlags...)
	out, err := run(ctx, t.dir, args...)
	return out, err
}

// Unstaged returns the difference between the working tree and the index for
// the named paths — which is to say, everything written into the tree since
// the last Apply. It is how `uzushio task mutate` reads one mutant back out.
func (t *Tree) Unstaged(ctx context.Context, paths ...string) (string, error) {
	args := append([]string{}, diffConfig...)
	args = append(args, "diff")
	args = append(args, diffFlags...)
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return run(ctx, t.dir, args...)
}

// Restore puts a path back to what the index holds, undoing whatever was
// written over it.
func (t *Tree) Restore(ctx context.Context, path string) error {
	_, err := run(ctx, t.dir, "checkout", "--", path)
	return err
}

// Read returns the contents of one path in the worktree.
func (t *Tree) Read(path string) ([]byte, error) {
	body, err := os.ReadFile(filepath.Join(t.dir, filepath.FromSlash(path)))
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	return body, nil
}

// Write overwrites one path in the worktree.
func (t *Tree) Write(path string, body []byte) error {
	target := filepath.Join(t.dir, filepath.FromSlash(path))
	if err := os.WriteFile(target, body, 0o644); err != nil { //nolint:gosec // a source file in a throwaway checkout
		return fmt.Errorf("worktree: %w", err)
	}
	return nil
}

// ResolveRev turns a revision into a full commit SHA, the way CMoA does, so a
// report names the commit rather than the word HEAD.
func ResolveRev(ctx context.Context, repo, rev string) (string, error) {
	out, err := run(ctx, repo, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// run executes git in a directory and returns its standard output. A failure
// carries git's own message, because git's message is the useful one.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return out.String(), &Error{
			Args:   args,
			Dir:    dir,
			Stderr: strings.TrimSpace(errOut.String()),
			Err:    err,
		}
	}
	return out.String(), nil
}
