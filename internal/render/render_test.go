package render_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/render"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// TestTreeDigestMatchesTheHarness is the interoperation test that matters
// most: the digest uzushio writes into render.json and the digest the harness
// computes from the directory it was handed have to be the same number, or
// every paired comparison is between two things nobody checked were the same.
//
// The fixture is the harness's own demo render, copied byte for byte, and the
// expected value is what the harness computed for it.
func TestTreeDigestMatchesTheHarness(t *testing.T) {
	t.Parallel()
	const want = "433d75fe25fa1df9266fe6f228bf8c51dc4f3a0c3fd18bf3a82e7498f9256fb4"
	got, err := render.Digest("testdata/demo")
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if got != want {
		t.Errorf("tree digest of the harness's demo render = %s, want %s", got, want)
	}
	files, err := render.Files("testdata/demo")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 5 {
		t.Errorf("the demo render has %d files, want 5: %+v", len(files), files)
	}
	for i := 1; i < len(files); i++ {
		if files[i-1].Path >= files[i].Path {
			t.Errorf("the file list is not sorted by path: %q then %q", files[i-1].Path, files[i].Path)
		}
	}
}

// TestManifestNameIsExcluded holds the other half of that agreement: the
// manifest is a statement about the tree and cannot be inside its own digest,
// and a .git directory is scaffolding rather than harness.
func TestManifestNameIsExcluded(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeText(t, filepath.Join(root, "system-prompt.md"), "Prefer the standard library.\n")
	before, err := render.Digest(root)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	writeText(t, filepath.Join(root, render.ManifestName), `{"schema_version":1}`)
	writeText(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	after, err := render.Digest(root)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if before != after {
		t.Errorf("the manifest or the git directory changed the tree digest: %s then %s", before, after)
	}
}

// TestRenderTheSeed is the empty case: no edits in force, so the harness is
// the two declared-but-empty surfaces and nothing else. The empty directories
// are part of the contract — they are what tells a proposer where it may act —
// so they are asserted rather than left to happen.
func TestRenderTheSeed(t *testing.T) {
	t.Parallel()
	vault := tempVault(t)
	out := filepath.Join(t.TempDir(), "harness")
	manifest, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(manifest.Edits) != 0 {
		t.Errorf("the seed render carries %d edits, want none: %+v", len(manifest.Edits), manifest.Edits)
	}
	want := []string{"memory/.gitkeep", "skills/.gitkeep"}
	if got := paths(manifest); !equal(got, want) {
		t.Errorf("the seed render holds %v, want %v", got, want)
	}
	if manifest.SeedSHA256 != render.TreeDigest(nil) {
		t.Errorf("seed_sha256 = %q, want the digest of the empty tree %q", manifest.SeedSHA256, render.TreeDigest(nil))
	}
	if manifest.OrderKey != "edit_id_asc" {
		t.Errorf("order_key = %q, want edit_id_asc", manifest.OrderKey)
	}
	if manifest.RendererVersion != render.Version() {
		t.Errorf("renderer_version = %q, want %q", manifest.RendererVersion, render.Version())
	}
	// The manifest on disk reads back as the manifest returned.
	read, err := render.ReadManifest(filepath.Join(out, render.ManifestName))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if read.TreeSHA256 != manifest.TreeSHA256 {
		t.Errorf("render.json says %s, Render returned %s", read.TreeSHA256, manifest.TreeSHA256)
	}
}

// TestRenderACandidate renders the case `uzushio run` spends its whole budget
// on: the binding set plus one non-binding edit.
func TestRenderACandidate(t *testing.T) {
	t.Parallel()
	vault := tempVault(t)
	writeEdit(t, vault, memoryEdit("he-0101", "memory/00-conventions.md", "Tabs, not spaces."))
	writeEdit(t, vault, skillEdit("he-0102", "skills/emit-diff/SKILL.md"))

	out := filepath.Join(t.TempDir(), "harness")
	manifest, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0101", "he-0102"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{
		"memory/.gitkeep", "memory/00-conventions.md",
		"skills/.gitkeep", "skills/emit-diff/SKILL.md",
	}
	if got := paths(manifest); !equal(got, want) {
		t.Errorf("the render holds %v, want %v", got, want)
	}
	// The document body is the file content, with one trailing newline.
	body := readText(t, filepath.Join(out, "memory", "00-conventions.md"))
	if body != "Tabs, not spaces.\n" {
		t.Errorf("the rendered note is %q, want the edit's body", body)
	}
	for _, record := range manifest.Edits {
		if record.Binding {
			t.Errorf("edit %s is recorded as binding, but it was added with --with-edit", record.ID)
		}
	}
	// The edits are recorded in ascending id order, which is the order they
	// were applied in.
	if len(manifest.Edits) != 2 || manifest.Edits[0].ID != "he-0101" || manifest.Edits[1].ID != "he-0102" {
		t.Errorf("edits are %+v, want he-0101 then he-0102", manifest.Edits)
	}
}

// TestRenderIsDeterministic is the Parallel Model check: two renders of one
// vault under two umasks are the same bytes. Go randomises map iteration by
// design and readdir order is not stable, so this is the test that catches a
// walk that forgot to sort.
func TestRenderIsDeterministic(t *testing.T) {
	vault := tempVault(t)
	writeEdit(t, vault, memoryEdit("he-0201", "memory/10-verifier.md", "The verifier runs in a container."))
	writeEdit(t, vault, memoryEdit("he-0202", "memory/00-conventions.md", "Tabs, not spaces."))
	writeEdit(t, vault, systemPromptEdit(t, vault, "he-0203", "Prefer the standard library.\n"))

	var digests []string
	for _, umask := range []int{0o022, 0o077} {
		previous := setUmask(umask)
		out := filepath.Join(t.TempDir(), "harness")
		manifest, err := render.Render(t.Context(), render.Options{
			Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t),
			WithEdits: []string{"he-0201", "he-0202", "he-0203"},
		})
		setUmask(previous)
		if err != nil {
			t.Fatalf("Render at umask %o: %v", umask, err)
		}
		digests = append(digests, manifest.TreeSHA256)
		if got := readText(t, filepath.Join(out, "system-prompt.md")); got != "Prefer the standard library.\n" {
			t.Errorf("the sidecar diff did not create the system prompt: %q", got)
		}
	}
	if digests[0] != digests[1] {
		t.Errorf("two renders of one vault differ: %s and %s", digests[0], digests[1])
	}
}

// TestRenderRefuses walks the states a render will not produce a tree for.
// Each of them is a proposal somebody wrote; none of them is a harness.
func TestRenderRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		prepare func(t *testing.T, vault string) render.Options
		want    string
	}{
		{
			name: "an edit that names no paths",
			prepare: func(t *testing.T, vault string) render.Options {
				edit := memoryEdit("he-0301", "memory/a.md", "A note.")
				edit.Paths = nil
				writeEdit(t, vault, edit)
				return render.Options{WithEdits: []string{"he-0301"}}
			},
			want: "names no paths",
		},
		{
			name: "an edit about a surface the tree has no place for",
			prepare: func(t *testing.T, vault string) render.Options {
				edit := memoryEdit("he-0302", "memory/a.md", "A note.")
				edit.Component = "middleware"
				edit.Touches = []string{"middleware"}
				writeEdit(t, vault, edit)
				return render.Options{WithEdits: []string{"he-0302"}}
			},
			want: "no injection point",
		},
		{
			name: "touches that does not match paths",
			prepare: func(t *testing.T, vault string) render.Options {
				edit := memoryEdit("he-0303", "memory/a.md", "A note.")
				edit.Touches = []string{"skill"}
				writeEdit(t, vault, edit)
				return render.Options{WithEdits: []string{"he-0303"}}
			},
			want: "touches is derived from paths",
		},
		{
			name: "a skill with no description to render",
			prepare: func(t *testing.T, vault string) render.Options {
				edit := skillEdit("he-0304", "skills/silent/SKILL.md")
				edit.Body = "# Silent\n\n## Also a heading"
				writeEdit(t, vault, edit)
				return render.Options{WithEdits: []string{"he-0304"}}
			},
			want: "no description",
		},
		{
			name: "two edits owning one file",
			prepare: func(t *testing.T, vault string) render.Options {
				writeEdit(t, vault, memoryEdit("he-0305", "memory/a.md", "One note."))
				writeEdit(t, vault, memoryEdit("he-0306", "memory/a.md", "Another note."))
				return render.Options{WithEdits: []string{"he-0305", "he-0306"}}
			},
			want: "both own",
		},
		{
			name: "an ablation of an edit that is not binding",
			prepare: func(t *testing.T, vault string) render.Options {
				writeEdit(t, vault, memoryEdit("he-0307", "memory/a.md", "A note."))
				return render.Options{WithoutEdits: []string{"he-0307"}}
			},
			want: "is not binding",
		},
		{
			name: "a sidecar whose digest has moved",
			prepare: func(t *testing.T, vault string) render.Options {
				edit := systemPromptEdit(t, vault, "he-0308", "Prefer the standard library.\n")
				writeEdit(t, vault, edit)
				writeText(t, filepath.Join(vault, vocab.DirEdits, "he-0308.diff"), "not the diff that was hashed\n")
				return render.Options{WithEdits: []string{"he-0308"}}
			},
			want: "has been rewritten",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			vault := tempVault(t)
			options := c.prepare(t, vault)
			options.Vault = vault
			options.AsOf = "2026-09-05"
			options.DocDag = docdagFor(t)
			options.Out = filepath.Join(t.TempDir(), "harness")
			_, err := render.Render(t.Context(), options)
			if err == nil {
				t.Fatalf("Render succeeded; want a refusal naming %q", c.want)
			}
			if !errors.Is(err, render.ErrRender) {
				t.Errorf("error %v does not wrap render.ErrRender", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
			if _, statErr := os.Stat(options.Out); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("a refused render left %s behind", options.Out)
			}
		})
	}
}

// TestApplyFailureIsItsOwnError is what lets `uzushio run` tell a candidate
// that cannot be measured from a vault that has diverged from its seed.
func TestApplyFailureIsItsOwnError(t *testing.T) {
	t.Parallel()
	vault := tempVault(t)
	edit := systemPromptEdit(t, vault, "he-0401", "Prefer the standard library.\n")
	writeEdit(t, vault, edit)
	// A second edit whose diff expects text the first one did not write.
	second := systemPromptEditFromDiff(t, vault, "he-0402", `--- a/system-prompt.md
+++ b/system-prompt.md
@@ -1 +1,2 @@
 Something else entirely.
+And a second line.
`)
	writeEdit(t, vault, second)

	_, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: filepath.Join(t.TempDir(), "harness"),
		AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0401", "he-0402"},
	})
	var applyErr *render.ApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("error = %v, want a *render.ApplyError", err)
	}
	if applyErr.EditID != "he-0402" {
		t.Errorf("the failure names %s, want he-0402", applyErr.EditID)
	}
	if applyErr.Binding {
		t.Error("the failing edit is recorded as binding, but it was a candidate")
	}
	if !errors.Is(err, render.ErrRender) {
		t.Error("an apply failure does not wrap render.ErrRender")
	}
}

// TestOutIsNotOverwrittenByAccident: a render into a directory that already
// holds something would produce a tree no manifest describes.
func TestOutIsNotOverwrittenByAccident(t *testing.T) {
	t.Parallel()
	vault := tempVault(t)
	out := t.TempDir()
	writeText(t, filepath.Join(out, "something.txt"), "here first\n")
	options := render.Options{Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t)}
	if _, err := render.Render(t.Context(), options); err == nil {
		t.Fatal("Render overwrote a non-empty directory")
	}
	options.Force = true
	if _, err := render.Render(t.Context(), options); err != nil {
		t.Fatalf("Render --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "something.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("--force left the old contents behind")
	}
}

// --- helpers ---

func memoryEdit(id, path, body string) doc.Edit {
	return doc.Edit{
		EditID: id, Title: "A note at " + path, Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "memory", Touches: []string{"memory"},
		Paths: []string{path}, Approval: vocab.ApprovalAuto,
		About:    []string{"topic/harness-improvement"},
		Predicts: []doc.Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body:     body,
	}
}

func skillEdit(id, path string) doc.Edit {
	return doc.Edit{
		EditID: id, Title: "A skill at " + path, Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "skill", Touches: []string{"skill"},
		Paths: []string{path}, Approval: vocab.ApprovalAuto,
		About:    []string{"topic/harness-improvement"},
		Predicts: []doc.Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body:     "---\nname: emit-diff\ndescription: Emit exactly one unified diff.\n---\n\n# Emit a diff\n\nThe body is not rendered.",
	}
}

// systemPromptEdit writes a sidecar that creates the system prompt from
// nothing, which is what the first edit to that surface always does: the seed
// has no system-prompt.md at all.
func systemPromptEdit(t *testing.T, vault, id, content string) doc.Edit {
	t.Helper()
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var diff strings.Builder
	diff.WriteString("--- /dev/null\n+++ b/system-prompt.md\n")
	diff.WriteString("@@ -0,0 +1," + itoa(len(lines)) + " @@\n")
	for _, line := range lines {
		diff.WriteString("+" + line + "\n")
	}
	return systemPromptEditFromDiff(t, vault, id, diff.String())
}

func systemPromptEditFromDiff(t *testing.T, vault, id, diff string) doc.Edit {
	t.Helper()
	writeText(t, filepath.Join(vault, vocab.DirEdits, id+".diff"), diff)
	return doc.Edit{
		EditID: id, Title: "A change to the system prompt", Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "system-prompt", Touches: []string{"system-prompt"},
		Paths: []string{"system-prompt.md"}, DiffSHA256: doc.DiffSHA256Of([]byte(diff)),
		Approval: vocab.ApprovalHuman,
		About:    []string{"topic/harness-improvement"},
		Predicts: []doc.Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body:     "The seed system prompt.",
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func writeEdit(t *testing.T, vault string, edit doc.Edit) {
	t.Helper()
	relative, err := edit.Path()
	if err != nil {
		t.Fatalf("%s.Path(): %v", edit.EditID, err)
	}
	body, err := edit.Bytes()
	if err != nil {
		t.Fatalf("%s.Bytes(): %v", edit.EditID, err)
	}
	writeText(t, filepath.Join(vault, filepath.FromSlash(relative)), string(body))
}

func paths(m render.Manifest) []string {
	out := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		out = append(out, f.Path)
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// tempVault copies the repository's configuration and specification corpus
// into a directory of the test's own, so an edit can be written beside real
// documents without touching the repository.
func tempVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	copyFile(t, filepath.Join(source, "docdag.yaml"), filepath.Join(root, "docdag.yaml"))
	specRoot := filepath.Join(source, "spec")
	err = filepath.WalkDir(specRoot, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(specRoot, p)
		if relErr != nil {
			return relErr
		}
		copyFile(t, p, filepath.Join(root, "spec", relative))
		return nil
	})
	if err != nil {
		t.Fatalf("copy spec: %v", err)
	}
	return root
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	writeText(t, to, string(body))
}

func writeText(t *testing.T, target, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

func readText(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// docdagFor names the engine to answer the binding question, or skips: the
// binding set is DocDag's answer and there is nothing to substitute for it.
func docdagFor(t *testing.T) string {
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

// setUmask changes the process umask and returns the previous one. The
// rendered files' permissions are not in the digest — only their paths and
// their contents are — and this test is what says so.
func setUmask(mask int) int { return syscall.Umask(mask) }

// TestRenderRefusesAPatchThatIsNotWhatTheEditSays walks the diffs a clean
// `git apply` would accept and the harness must not. These are model-generated
// bytes, so none of it is hypothetical.
func TestRenderRefusesAPatchThatIsNotWhatTheEditSays(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		diff string
		want string
	}{
		{
			// The reviewer's repro: hashing the tree reads each file, which
			// dereferences a link, so tree_sha256 would become a fact about
			// the machine — and the copy path writes the target's bytes into
			// the harness the fleet is given.
			name: "a symlink",
			diff: "diff --git a/system-prompt.md b/system-prompt.md\n" +
				"new file mode 120000\n--- /dev/null\n+++ b/system-prompt.md\n" +
				"@@ -0,0 +1 @@\n+/etc/hostname\n\\ No newline at end of file\n",
			want: "symlink",
		},
		{
			name: "a path the edit does not own",
			diff: "diff --git a/memory/planted.md b/memory/planted.md\n" +
				"new file mode 100644\n--- /dev/null\n+++ b/memory/planted.md\n" +
				"@@ -0,0 +1 @@\n+A note nobody reviewed as a note.\n",
			want: "is about \"system-prompt\" but writes",
		},
		{
			name: "a path outside the three surfaces",
			diff: "diff --git a/hooks.json b/hooks.json\n" +
				"new file mode 100644\n--- /dev/null\n+++ b/hooks.json\n" +
				"@@ -0,0 +1 @@\n+{}\n",
			want: "not a path the rendered harness has a place for",
		},
		{
			name: "an executable",
			diff: "diff --git a/system-prompt.md b/system-prompt.md\n" +
				"new file mode 100755\n--- /dev/null\n+++ b/system-prompt.md\n" +
				"@@ -0,0 +1 @@\n+Prefer the standard library.\n",
			want: "mode 100755",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			vault := tempVault(t)
			edit := systemPromptEditFromDiff(t, vault, "he-0501", c.diff)
			writeEdit(t, vault, edit)
			out := filepath.Join(t.TempDir(), "harness")
			_, err := render.Render(t.Context(), render.Options{
				Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t),
				WithEdits: []string{"he-0501"},
			})
			if err == nil {
				t.Fatalf("Render accepted the patch; want a refusal naming %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
			if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("a refused render left %s behind", out)
			}
		})
	}
}

// TestRenderAcceptsTheOrdinarySystemPromptPatch is the other half: the check
// above must not refuse the thing it exists to permit.
func TestRenderAcceptsTheOrdinarySystemPromptPatch(t *testing.T) {
	t.Parallel()
	vault := tempVault(t)
	writeEdit(t, vault, systemPromptEdit(t, vault, "he-0502", "Prefer the standard library.\n"))
	out := filepath.Join(t.TempDir(), "harness")
	manifest, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0502"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := readText(t, filepath.Join(out, "system-prompt.md")); got != "Prefer the standard library.\n" {
		t.Errorf("the system prompt is %q", got)
	}
	if len(manifest.Files) != 3 {
		t.Errorf("the render holds %v, want the prompt and the two placeholders", paths(manifest))
	}
}

// TestForceKeepsTheOldHarnessWhenTheRenderFails is H8. The whole
// build-in-a-temp-directory-then-install design promises that a failed render
// leaves no half-written harness behind; removing the target up front made
// `--force` mean "delete this whether or not I can replace it".
func TestForceKeepsTheOldHarnessWhenTheRenderFails(t *testing.T) {
	t.Parallel()
	vault := tempVault(t)
	writeEdit(t, vault, memoryEdit("he-0601", "memory/00-note.md", "The note that works."))
	live := filepath.Join(t.TempDir(), "live")
	before, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: live, AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0601"},
	})
	if err != nil {
		t.Fatalf("the first render: %v", err)
	}

	// A second edit whose sidecar cannot apply to the seed.
	writeEdit(t, vault, systemPromptEditFromDiff(t, vault, "he-0602",
		"--- a/system-prompt.md\n+++ b/system-prompt.md\n@@ -1 +1,2 @@\n Text that was never written.\n+And more.\n"))
	if _, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: live, Force: true, AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0601", "he-0602"},
	}); err == nil {
		t.Fatal("the second render succeeded; it was supposed to fail to apply")
	}

	after, err := render.Digest(live)
	if err != nil {
		t.Fatalf("the harness that was working is gone: %v", err)
	}
	if after != before.TreeSHA256 {
		t.Errorf("the live harness is now %s, was %s", after, before.TreeSHA256)
	}
	if got := readText(t, filepath.Join(live, "memory", "00-note.md")); got != "The note that works.\n" {
		t.Errorf("the live note is %q", got)
	}
}

// TestTheGitEnvironmentIsBuiltNotInherited is H9. Three channels change the
// bytes of the rendered tree without touching the vault, and a fourth makes
// the render stage its files into somebody else's repository.
func TestTheGitEnvironmentIsBuiltNotInherited(t *testing.T) {
	vault := tempVault(t)
	writeEdit(t, vault, systemPromptEdit(t, vault, "he-0701", "Prefer the standard library.\n"))
	options := render.Options{
		Vault: vault, AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0701"},
	}

	options.Out = filepath.Join(t.TempDir(), "clean")
	clean, err := render.Render(t.Context(), options)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// A repository the render must not touch, and an environment that would
	// otherwise rewrite every line ending.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	writeText(t, filepath.Join(elsewhere, ".keep"), "")
	if out, err := exec.Command("git", "-C", elsewhere, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	xdg := t.TempDir()
	writeText(t, filepath.Join(xdg, "git", "attributes"), "* text=auto eol=crlf\n")
	t.Setenv("GIT_DIR", filepath.Join(elsewhere, ".git"))
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")

	options.Out = filepath.Join(t.TempDir(), "hostile")
	hostile, err := render.Render(t.Context(), options)
	if err != nil {
		t.Fatalf("Render under a hostile environment: %v", err)
	}
	if hostile.TreeSHA256 != clean.TreeSHA256 {
		t.Errorf("the environment changed the tree digest: %s clean, %s hostile",
			clean.TreeSHA256, hostile.TreeSHA256)
	}
	if got := readText(t, filepath.Join(options.Out, "system-prompt.md")); got != "Prefer the standard library.\n" {
		t.Errorf("the rendered prompt is %q; a line ending was rewritten", got)
	}
	// Nothing was staged into the repository GIT_DIR pointed at.
	staged, err := exec.Command("git", "--git-dir="+filepath.Join(elsewhere, ".git"), "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	if strings.TrimSpace(string(staged)) != "" {
		t.Errorf("the render staged files into an unrelated repository: %q", staged)
	}
}

// TestARenderedHarnessIsReadable is M11: the mode passed to WriteFile is
// masked by the umask, and the harness is read by a container that may run as
// another uid.
func TestARenderedHarnessIsReadable(t *testing.T) {
	vault := tempVault(t)
	writeEdit(t, vault, memoryEdit("he-0801", "memory/00-note.md", "A note."))
	previous := setUmask(0o077)
	out := filepath.Join(t.TempDir(), "harness")
	_, err := render.Render(t.Context(), render.Options{
		Vault: vault, Out: out, AsOf: "2026-09-05", DocDag: docdagFor(t),
		WithEdits: []string{"he-0801"},
	})
	setUmask(previous)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	_ = filepath.WalkDir(out, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		want := fs.FileMode(0o644)
		if entry.IsDir() {
			want = 0o755
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s is %o, want %o", name, info.Mode().Perm(), want)
		}
		return nil
	})
}
