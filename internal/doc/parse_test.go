package doc_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// sample is an edit with every key the writer writes, so a round trip that
// dropped one shows up here rather than in a render three steps later.
func sample() doc.Edit {
	return doc.Edit{
		EditID: "he-0042", Title: "Say what the verifier runs", Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "memory", Touches: []string{"memory"},
		Paths:     []string{"memory/00-verifier.md"},
		RootCause: "the proposer did not know what the verifier ran",
		Approval:  vocab.ApprovalAuto,
		About:     []string{"topic/harness-improvement"},
		Premise:   []string{"premise/the-verifier-is-honest"},
		Predicts:  []doc.Prediction{{Pattern: "fp/missing-context", Expect: vocab.ExpectFix}},
		Body:      "The verifier runs the tests in a container.\n\nIt does not run a formatter.",
	}
}

// TestParseEditRoundTrip: what the writer wrote is what the reader reads. The
// body matters most — for a memory or a skill edit it is the file content, so
// a reader that trimmed one character too many would change the harness.
func TestParseEditRoundTrip(t *testing.T) {
	t.Parallel()
	want := sample()
	raw, err := want.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	got, err := doc.ParseEdit(want.EditID, raw)
	if err != nil {
		t.Fatalf("ParseEdit: %v", err)
	}
	again, err := got.Bytes()
	if err != nil {
		t.Fatalf("Bytes after a round trip: %v", err)
	}
	if string(again) != string(raw) {
		t.Errorf("a round trip changed the document:\n--- written ---\n%s\n--- read back ---\n%s", raw, again)
	}
	if got.Body != want.Body {
		t.Errorf("body = %q, want %q", got.Body, want.Body)
	}
	// The content is the body with exactly one trailing newline.
	content, err := got.Content()
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if string(content) != want.Body+"\n" {
		t.Errorf("content = %q, want the body and one newline", content)
	}
}

// TestParseEditRefuses walks the documents that are not edits.
func TestParseEditRefuses(t *testing.T) {
	t.Parallel()
	valid, err := sample().Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		id   string
		raw  string
		want string
	}{
		{"an identifier of the wrong shape", "he-42", string(valid), "not an edit identifier"},
		{"no frontmatter at all", "he-0042", "# A heading\n\nSome prose.\n", "does not open with"},
		{"frontmatter that is never closed", "he-0042", "---\nkind: edit\n", "not closed"},
		{"another kind", "he-0042", "---\nkind: pattern\ntitle: t\n---\n\n# t\n", "not an edit"},
		{
			"a heading that has drifted from the title", "he-0042",
			strings.Replace(string(valid), "# Say what", "# Say something else about what", 1),
			"does not match its title",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := doc.ParseEdit(c.id, []byte(c.raw))
			if err == nil {
				t.Fatalf("ParseEdit succeeded; want a refusal naming %q", c.want)
			}
			if !errors.Is(err, doc.ErrDocument) {
				t.Errorf("error %v does not wrap ErrDocument", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
		})
	}
}

// TestCheckPaths is the rule DocDag cannot carry: it has no path arithmetic,
// so nothing in the vault can say that memory/note.md belongs to the memory
// surface. This is where that is said.
func TestCheckPaths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		adjust func(e *doc.Edit)
		want   string // empty means the edit is fine
	}{
		{"the ordinary memory edit", func(*doc.Edit) {}, ""},
		{
			"no paths at all",
			func(e *doc.Edit) { e.Paths = nil },
			"names no paths",
		},
		{
			"a surface with no injection point",
			func(e *doc.Edit) { e.Component = "middleware"; e.Touches = []string{"middleware"} },
			"no injection point",
		},
		{
			"a path that belongs to another surface",
			func(e *doc.Edit) { e.Paths = []string{"skills/a/SKILL.md"} },
			`is about "memory" but owns`,
		},
		{
			"a path shape nothing renders",
			func(e *doc.Edit) { e.Paths = []string{"memory/notes/deep.md"} },
			"not a path the rendered harness has a place for",
		},
		{
			"touches that does not match paths",
			func(e *doc.Edit) { e.Touches = []string{"memory", "skill"} },
			"touches is derived from paths",
		},
		{
			"the same path twice",
			func(e *doc.Edit) { e.Paths = []string{"memory/a.md", "memory/a.md"} },
			"twice",
		},
		{
			"two files for one memory note",
			func(e *doc.Edit) { e.Paths = []string{"memory/a.md", "memory/b.md"} },
			"owns 2 files",
		},
		{
			"a memory edit with a sidecar digest",
			func(e *doc.Edit) { e.DiffSHA256 = doc.DiffSHA256Of([]byte("x")) },
			"only a system-prompt edit carries a sidecar diff",
		},
		{
			"a memory edit with an empty body",
			func(e *doc.Edit) { e.Body = "  \n" },
			"empty body",
		},
		{
			"a system-prompt edit with no sidecar digest",
			func(e *doc.Edit) {
				e.Component = "system-prompt"
				e.Touches = []string{"system-prompt"}
				e.Paths = []string{"system-prompt.md"}
			},
			"writes no diff_sha256",
		},
		{
			"a system-prompt edit with one",
			func(e *doc.Edit) {
				e.Component = "system-prompt"
				e.Touches = []string{"system-prompt"}
				e.Paths = []string{"system-prompt.md"}
				e.DiffSHA256 = doc.DiffSHA256Of([]byte("a diff"))
			},
			"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edit := sample()
			c.adjust(&edit)
			err := edit.CheckPaths()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("CheckPaths: %v", err)
			case c.want == "":
				return
			case err == nil:
				t.Fatalf("CheckPaths accepted the edit; want a refusal naming %q", c.want)
			case !strings.Contains(err.Error(), c.want):
				t.Errorf("error %q does not name %q", err, c.want)
			}
		})
	}
}

// TestValidateAcceptsAnEditWithNoPaths is the boundary between the two checks.
// A proposal DocDag accepts is not the same thing as one a run will accept,
// and collapsing them would make a legal corpus unwritable.
func TestValidateAcceptsAnEditWithNoPaths(t *testing.T) {
	t.Parallel()
	edit := sample()
	edit.Paths = nil
	if err := edit.Validate(); err != nil {
		t.Errorf("Validate refused an edit with no paths: %v", err)
	}
	if err := edit.CheckPaths(); err == nil {
		t.Error("CheckPaths accepted an edit with no paths")
	}
}

// TestValidateChecksPathShape: a path that could not name a file inside the
// rendered harness whatever surface it claimed is malformed, not merely
// unrendered, so Validate is where it is refused.
func TestValidateChecksPathShape(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/etc/passwd", "../escape.md", "memory/./a.md", "memory\\a.md", ""} {
		edit := sample()
		edit.Paths = []string{path}
		if err := edit.Validate(); err == nil {
			t.Errorf("Validate accepted the path %q", path)
		}
	}
	edit := sample()
	edit.DiffSHA256 = "NOTAHEXDIGEST"
	if err := edit.Validate(); err == nil {
		t.Error("Validate accepted a diff_sha256 that is not a digest")
	}
}

// TestDiffPath and the digest are the two halves of the sidecar contract.
func TestDiffPath(t *testing.T) {
	t.Parallel()
	got, err := doc.DiffPath("he-0007")
	if err != nil {
		t.Fatalf("DiffPath: %v", err)
	}
	if got != vocab.DirEdits+"/he-0007.diff" {
		t.Errorf("DiffPath = %q", got)
	}
	if _, err := doc.DiffPath("he-7"); err == nil {
		t.Error("DiffPath accepted an identifier of the wrong shape")
	}
	if a, b := doc.DiffSHA256Of([]byte("x")), doc.DiffSHA256Of([]byte("x")); a != b {
		t.Error("DiffSHA256Of is not a function of its bytes")
	}
	if a, b := doc.DiffSHA256Of([]byte("x")), doc.DiffSHA256Of([]byte("y")); a == b {
		t.Error("two different diffs hash the same")
	}
}

// TestContentKeepsTheNotesOwnHeading is the regression the reviewer asked for.
// The body of a memory or skill edit *is* the file, and the split has to strip
// exactly the one heading the writer wrote — the document's title — and not a
// heading the note itself opens with. Stripping two would silently rewrite the
// harness; stripping none would put the edit's title into every note.
func TestContentKeepsTheNotesOwnHeading(t *testing.T) {
	t.Parallel()
	edit := sample()
	edit.Title = "Add memory/style.md to answer fp/missing-context"
	edit.Body = "# House style\n\nUse tabs.\n\n## Imports\n\nStandard library first."
	raw, err := edit.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !strings.Contains(string(raw), "# "+edit.Title) {
		t.Fatalf("the document does not carry its title as a heading:\n%s", raw)
	}
	back, err := doc.ParseEdit(edit.EditID, raw)
	if err != nil {
		t.Fatalf("ParseEdit: %v", err)
	}
	content, err := back.Content()
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	want := "# House style\n\nUse tabs.\n\n## Imports\n\nStandard library first.\n"
	if string(content) != want {
		t.Errorf("content = %q, want %q", content, want)
	}
	if strings.Contains(string(content), edit.Title) {
		t.Errorf("the edit's own title leaked into the rendered note:\n%s", content)
	}
}
