package proposeedit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/proposeedit"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// pattern is the failure every fixture here proposes against.
func pattern() doc.Pattern {
	return doc.Pattern{
		PatternID: "fp/context-lines-drift-hello",
		Title:     "The diff's context lines do not match the file bytes the prompt supplied",
		Date:      "2026-09-05",
		Status:    vocab.StatusOpen,
		Category:  vocab.CategoryUnsafeProvided,
		Context:   "task hello, file add.go; the diff's context lines drift",
		Component: "memory",
		Evidence:  []string{"20260904T125241Z-8f6a746f", "20260904T130106Z-17dd2834"},
		Body:      "seen eight times",
	}
}

// harness writes a rendered harness directory holding the files named.
func harness(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "harness")
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, sub := range []string{"memory", "skills"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	return dir
}

// requireGit skips a test that cannot run without git.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
}

func TestBuildTaskWritesATaskCMoACanLoad(t *testing.T) {
	requireGit(t)
	dir := harness(t, map[string]string{
		"system-prompt.md":      "Be careful.\n",
		"memory/00-existing.md": "An existing note.\n",
		"render.json":           `{"schema_version":1}`,
	})
	work := filepath.Join(t.TempDir(), "task")
	task, err := proposeedit.BuildTask(context.Background(), dir, work, pattern())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if task.ID != "improve-fp-context-lines-drift-hello" {
		t.Fatalf("task id is %q", task.ID)
	}

	var manifest map[string]any
	raw, err := os.ReadFile(filepath.Join(task.Dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode task.json: %v", err)
	}
	if manifest["version"].(float64) != 2 || manifest["repo"] != "repo" || manifest["rev"] != "HEAD" {
		t.Fatalf("manifest is %v", manifest)
	}
	files := manifest["files"].([]any)
	if len(files) == 0 {
		t.Fatalf("files is empty; the proposers would see nothing")
	}
	want := map[string]bool{
		"system-prompt.md": true, "memory/00-existing.md": true, "skills/.gitkeep": true,
	}
	for _, f := range files {
		delete(want, f.(string))
		if f.(string) == "render.json" {
			t.Fatalf("the renderer's own manifest was put in front of the proposers")
		}
	}
	if len(want) != 0 {
		t.Fatalf("files %v is missing %v", files, want)
	}

	// The instruction and the compose file are what CMoA insists on.
	instruction, err := os.ReadFile(filepath.Join(task.Dir, "instruction.md"))
	if err != nil {
		t.Fatalf("read instruction: %v", err)
	}
	if !strings.Contains(string(instruction), pattern().PatternID) {
		t.Fatalf("the instruction does not name the pattern:\n%s", instruction)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, "compose.yaml")); err != nil {
		t.Fatalf("no compose file: %v", err)
	}

	// The repository has a revision to resolve.
	rev := git(t, filepath.Join(task.Dir, "repo"), "rev-parse", "HEAD")
	if len(strings.TrimSpace(rev)) != 40 {
		t.Fatalf("HEAD is %q", rev)
	}

	// Re-running --propose into the same --out is the ordinary way to retry
	// one; the half-built task the last attempt left must not make git refuse.
	if _, err := proposeedit.BuildTask(context.Background(), dir, work, pattern()); err != nil {
		t.Fatalf("rebuild into the same directory: %v", err)
	}
}

// TestTaskIDStaysDistinctWhenItHasToBeTruncated covers L17: two long pattern
// identifiers must not collide onto one CMoA task, or two proposal rounds share
// one runs/ directory and each looks like the other's history.
func TestTaskIDStaysDistinctWhenItHasToBeTruncated(t *testing.T) {
	requireGit(t)
	long := func(suffix string) doc.Pattern {
		p := pattern()
		p.PatternID = "fp/persistent-band-breach-a-very-long-task-name-indeed-" + suffix
		return p
	}
	seen := map[string]bool{}
	for _, suffix := range []string{"first-invariant-name", "second-invariant-name"} {
		task, err := proposeedit.BuildTask(
			context.Background(), harness(t, nil), filepath.Join(t.TempDir(), "task"), long(suffix))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if len(task.ID) > 64 {
			t.Fatalf("task id %q is %d characters", task.ID, len(task.ID))
		}
		if seen[task.ID] {
			t.Fatalf("two patterns collided onto task id %q", task.ID)
		}
		seen[task.ID] = true
	}
}

// TestTheGitEnvironmentIsBuiltNotInherited covers the proposeedit half of the
// review's H9: an inherited GIT_DIR makes this stage the harness into somebody
// else's repository, and an inherited attributes file rewrites the bytes of the
// very file a memory edit's body is read from.
func TestTheGitEnvironmentIsBuiltNotInherited(t *testing.T) {
	requireGit(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	git(t, elsewhere, "init", "--quiet")
	t.Setenv("GIT_DIR", filepath.Join(elsewhere, ".git"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")

	dir := harness(t, map[string]string{"memory/00-note.md": "a note\n"})
	task, err := proposeedit.BuildTask(
		context.Background(), dir, filepath.Join(t.TempDir(), "task"), pattern())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if tracked := git(t, elsewhere, "ls-files"); strings.TrimSpace(tracked) != "" {
		t.Fatalf("the harness was staged into an unrelated repository: %q", tracked)
	}
	body, err := os.ReadFile(filepath.Join(task.Dir, "repo", "memory", "00-note.md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if strings.Contains(string(body), "\r") {
		t.Fatalf("an inherited git configuration rewrote the note's bytes: %q", body)
	}
}

func TestInstructionRefusesToPrescribeTheEdit(t *testing.T) {
	text := proposeedit.Instruction(pattern())
	for _, want := range []string{
		"memory/<name>.md", "skills/<name>/SKILL.md", "system-prompt.md",
		"exactly one", "Root cause:", pattern().Context,
		// The three things a proposer cannot work out from the tree: what the
		// placeholder file is, what shape a note or a skill has to be, and that
		// a deletion or a tab makes the answer unrepresentable.
		".gitkeep", "description:", "Do not delete",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the instruction never says %q:\n%s", want, text)
		}
	}
}

// fakeRunner answers with a run directory it wrote itself, which is what a
// proposal round leaves behind.
type fakeRunner struct {
	dir        string
	candidates map[string]string // proposer id -> diff, empty for no diff
	raw        map[string]string
}

func (f fakeRunner) Propose(_ context.Context, _ string) (string, error) {
	return f.dir, nil
}

func newFakeRunner(t *testing.T, candidates, raw map[string]string) fakeRunner {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(filepath.Join(dir, "candidates"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for id, diff := range candidates {
		status := "ok"
		if diff == "" {
			status = "no_diff"
		}
		body, err := json.Marshal(map[string]any{
			"proposer_id": id, "model": "model-" + id, "status": status,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "candidates", id+".json"), body, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if diff != "" {
			if err := os.WriteFile(filepath.Join(dir, "candidates", id+".diff"), []byte(diff), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		if text, ok := raw[id]; ok {
			if err := os.WriteFile(filepath.Join(dir, "candidates", id+".raw.txt"), []byte(text), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
	return fakeRunner{dir: dir, candidates: candidates, raw: raw}
}

// newFileDiff is a unified diff that creates one file, the way a proposer's
// answer reaches CMoA's patch extractor.
func newFileDiff(path, body string) string {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	out := "diff --git a/" + path + " b/" + path + "\n" +
		"new file mode 100644\n--- /dev/null\n+++ b/" + path + "\n" +
		"@@ -0,0 +1," + itoa(len(lines)) + " @@\n"
	for _, line := range lines {
		out += "+" + line + "\n"
	}
	return out
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

func convert(t *testing.T, dir string, candidates map[string]string, raw map[string]string) ([]proposeedit.Proposal, []proposeedit.Refusal) {
	t.Helper()
	runner := newFakeRunner(t, candidates, raw)
	read, err := proposeedit.Candidates(runner.dir)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	proposals, refusals, err := proposeedit.Convert(context.Background(), read, proposeedit.ConvertOptions{
		Harness: dir,
		Pattern: pattern(),
		Day:     "2026-09-05",
		Work:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	return proposals, refusals
}

func TestConvertsAMemoryDiffIntoAnEditWhoseBodyIsTheFile(t *testing.T) {
	requireGit(t)
	dir := harness(t, nil)
	const note = "# Read the file bytes before you patch\n\nThe context lines of a hunk must be the\nbytes the prompt gave you.\n"
	proposals, refusals := convert(t, dir,
		map[string]string{"alpha": newFileDiff("memory/10-context-lines.md", note)},
		map[string]string{"alpha": "Root cause: the model reconstructs context lines from memory.\n\n```diff\n...\n```\n"})
	if len(refusals) != 0 {
		t.Fatalf("refused: %+v", refusals)
	}
	if len(proposals) != 1 {
		t.Fatalf("got %d proposals", len(proposals))
	}
	edit := proposals[0].Edit
	if edit.Component != "memory" || len(edit.Paths) != 1 || edit.Paths[0] != "memory/10-context-lines.md" {
		t.Fatalf("component %q paths %v", edit.Component, edit.Paths)
	}
	if edit.Approval != vocab.ApprovalAuto {
		t.Fatalf("a memory edit is %s, want auto: CMoA declares memory auto-accept", edit.Approval)
	}
	if edit.DiffSHA256 != "" || proposals[0].Diff != nil {
		t.Fatalf("a memory edit carries a sidecar diff")
	}
	if strings.TrimSpace(edit.Body) != strings.TrimSpace(note) {
		t.Fatalf("the body is not the file:\n%q\nwant\n%q", edit.Body, note)
	}
	if len(edit.Predicts) != 1 || edit.Predicts[0].Pattern != pattern().PatternID ||
		edit.Predicts[0].Expect != vocab.ExpectFix {
		t.Fatalf("predicts is %+v", edit.Predicts)
	}
	if !strings.HasPrefix(edit.RootCause, "the model reconstructs context lines") {
		t.Fatalf("root cause is %q", edit.RootCause)
	}
	if err := edit.CheckPaths(); err != nil {
		t.Fatalf("the edit is one a render refuses: %v", err)
	}
}

func TestConvertsAMemoryDiffWithRelativeWork(t *testing.T) {
	requireGit(t)
	dir := harness(t, nil)
	work := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeWork, err := filepath.Rel(wd, work)
	if err != nil {
		t.Skipf("no relative path to work: %v", err)
	}
	runner := newFakeRunner(t, map[string]string{"alpha": newFileDiff("memory/relative-work.md", "relative work is valid\n")}, nil)
	candidates, err := proposeedit.Candidates(runner.dir)
	if err != nil {
		t.Fatal(err)
	}
	proposals, refusals, err := proposeedit.Convert(context.Background(), candidates, proposeedit.ConvertOptions{
		Harness: dir, Pattern: pattern(), Day: "2026-09-05", Work: relativeWork,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refusals) != 0 || len(proposals) != 1 {
		t.Fatalf("proposals %d refusals %+v", len(proposals), refusals)
	}
}

func TestConvertsASystemPromptDiffIntoASidecar(t *testing.T) {
	requireGit(t)
	dir := harness(t, map[string]string{"system-prompt.md": "Be careful.\n"})
	diff := "diff --git a/system-prompt.md b/system-prompt.md\n" +
		"--- a/system-prompt.md\n+++ b/system-prompt.md\n" +
		"@@ -1 +1,2 @@\n Be careful.\n+Quote the file bytes exactly.\n"
	proposals, refusals := convert(t, dir, map[string]string{"alpha": diff}, nil)
	if len(refusals) != 0 || len(proposals) != 1 {
		t.Fatalf("proposals %d refusals %+v", len(proposals), refusals)
	}
	edit := proposals[0].Edit
	if edit.Component != "system-prompt" || edit.Approval != vocab.ApprovalHuman {
		t.Fatalf("component %q approval %s", edit.Component, edit.Approval)
	}
	if string(proposals[0].Diff) != diff {
		t.Fatalf("the sidecar is not the candidate's diff")
	}
	sum := sha256.Sum256([]byte(diff))
	if edit.DiffSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("diff_sha256 is %q", edit.DiffSHA256)
	}
	if err := edit.CheckPaths(); err != nil {
		t.Fatalf("the edit is one a render refuses: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	requireGit(t)
	dir := harness(t, map[string]string{"system-prompt.md": "Be careful.\n"})
	cases := []struct {
		name string
		diff string
		want string
	}{
		{
			name: "two surfaces",
			diff: newFileDiff("memory/10-a.md", "a note\n") + newFileDiff("skills/emit/SKILL.md", "a skill\n"),
			want: "touches 2 surfaces",
		},
		{
			name: "outside the three",
			diff: newFileDiff("hooks.json", "{}\n"),
			want: "not a path the rendered harness has a place for",
		},
		{
			name: "a nested memory note",
			diff: newFileDiff("memory/deep/note.md", "a note\n"),
			want: "not a path the rendered harness has a place for",
		},
		{
			name: "a second file in a skill",
			diff: newFileDiff("skills/emit/NOTES.md", "notes\n"),
			want: "not a path the rendered harness has a place for",
		},
		{
			name: "a diff that does not apply",
			diff: "diff --git a/system-prompt.md b/system-prompt.md\n" +
				"--- a/system-prompt.md\n+++ b/system-prompt.md\n" +
				"@@ -1 +1,2 @@\n Something else entirely.\n+And more.\n",
			want: "does not apply",
		},
		{
			name: "no diff at all",
			diff: "",
			want: `status is "no_diff"`,
		},
		{
			// A deletion has to be a refusal here rather than an unreadable
			// file three steps later, where it would take every other
			// proposer's answer down with it.
			name: "a deletion",
			diff: "diff --git a/system-prompt.md b/system-prompt.md\ndeleted file mode 100644\n" +
				"--- a/system-prompt.md\n+++ /dev/null\n@@ -1 +0,0 @@\n-Be careful.\n",
			want: "deletes system-prompt.md",
		},
		{
			name: "a raw tab in a note",
			diff: newFileDiff("memory/10-tabbed.md", "a note\n\twith a tab\n"),
			want: "writes a tab into memory/10-tabbed.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proposals, refusals := convert(t, dir, map[string]string{"alpha": tc.diff}, nil)
			if len(proposals) != 0 {
				t.Fatalf("an edit was written: %+v", proposals[0].Edit)
			}
			if len(refusals) != 1 || !strings.Contains(refusals[0].Reason, tc.want) {
				t.Fatalf("refusals are %+v, want one saying %q", refusals, tc.want)
			}
		})
	}
}

func TestOneBadCandidateDoesNotLoseAGoodOne(t *testing.T) {
	requireGit(t)
	dir := harness(t, nil)
	proposals, refusals := convert(t, dir, map[string]string{
		"alpha": newFileDiff("hooks.json", "{}\n"),
		"beta":  newFileDiff("memory/10-note.md", "a note\n"),
	}, nil)
	if len(proposals) != 1 || proposals[0].Candidate.ProposerID != "beta" {
		t.Fatalf("proposals are %+v", proposals)
	}
	if len(refusals) != 1 || refusals[0].Candidate.ProposerID != "alpha" {
		t.Fatalf("refusals are %+v", refusals)
	}
}

func TestPlanNumbersFromTheVaultAndWritesTheSidecar(t *testing.T) {
	requireGit(t)
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, vocab.DirEdits), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, vocab.DirEdits, "he-0007.md"), []byte("---\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if next, err := proposeedit.NextEditNumber(vault); err != nil || next != 8 {
		t.Fatalf("next is %d (%v), want 8", next, err)
	}

	dir := harness(t, map[string]string{"system-prompt.md": "Be careful.\n"})
	sidecar := "diff --git a/system-prompt.md b/system-prompt.md\n" +
		"--- a/system-prompt.md\n+++ b/system-prompt.md\n" +
		"@@ -1 +1,2 @@\n Be careful.\n+Quote the bytes.\n"
	proposals, _ := convert(t, dir, map[string]string{
		"alpha": sidecar,
		"beta":  newFileDiff("memory/10-note.md", "a note\n"),
	}, nil)
	if len(proposals) != 2 {
		t.Fatalf("got %d proposals", len(proposals))
	}
	numbering, err := proposeedit.NewNumbering(vault)
	if err != nil {
		t.Fatalf("numbering: %v", err)
	}
	writes, err := proposeedit.Plan(numbering, proposals)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if writes[0].ID != "he-0008" || writes[1].ID != "he-0009" {
		t.Fatalf("identifiers are %s and %s", writes[0].ID, writes[1].ID)
	}
	if writes[0].DiffPath != "spec/edits/he-0008.diff" {
		t.Fatalf("the sidecar is at %q", writes[0].DiffPath)
	}
	if writes[1].DiffPath != "" {
		t.Fatalf("a memory edit planned a sidecar at %q", writes[1].DiffPath)
	}
	if err := proposeedit.Apply(vault, writes); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, want := range []string{
		"spec/edits/he-0008.md", "spec/edits/he-0008.diff", "spec/edits/he-0009.md",
	} {
		if _, err := os.Stat(filepath.Join(vault, filepath.FromSlash(want))); err != nil {
			t.Fatalf("%s: %v", want, err)
		}
	}
	// Two proposals in one pass must not land on one number, and a second pass
	// must not land on a number the first used.
	if next, err := proposeedit.NextEditNumber(vault); err != nil || next != 10 {
		t.Fatalf("next is %d (%v), want 10", next, err)
	}
	// Apply refuses to write over a document that is already there, whoever put
	// it there: the numbering is a claim about the vault and O_EXCL is what
	// catches the claim having gone stale.
	if err := proposeedit.Apply(vault, writes); err == nil {
		t.Fatalf("writing over an existing edit was allowed")
	}
	// The same Numbering keeps counting, so a second Plan in one process — one
	// per pattern, and none of them written yet under --dry-run — does not
	// re-issue he-0008.
	again, err := proposeedit.Plan(numbering, proposals)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if again[0].ID != "he-0010" || again[1].ID != "he-0011" {
		t.Fatalf("a second plan in one process re-issued %s and %s", again[0].ID, again[1].ID)
	}
}

func TestTopicIsWrittenOnlyWhenItIsMissing(t *testing.T) {
	vault := t.TempDir()
	written, err := proposeedit.WriteTopic(vault, proposeedit.DefaultTopic, "2026-09-05")
	if err != nil || !written {
		t.Fatalf("write topic: %v %v", written, err)
	}
	path, err := proposeedit.TopicPath(proposeedit.DefaultTopic)
	if err != nil {
		t.Fatalf("topic path: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read topic: %v", err)
	}
	for _, want := range []string{"id: " + proposeedit.DefaultTopic, "kind: topic", "date: 2026-09-05"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("the topic does not say %q:\n%s", want, body)
		}
	}
	again, err := proposeedit.WriteTopic(vault, proposeedit.DefaultTopic, "2026-09-06")
	if err != nil || again {
		t.Fatalf("an existing topic was rewritten: %v %v", again, err)
	}
}

func TestExecRunnerReadsTheRunDirectoryOffStdout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the fake cmoa is a shell script")
	}
	dir := t.TempDir()
	runDir := filepath.Join(dir, "runs", "20260905T000000Z-deadbeef")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bin := filepath.Join(dir, "cmoa")
	script := "#!/bin/sh\necho " + runDir + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // a fake binary is executable
		t.Fatalf("write fake cmoa: %v", err)
	}
	got, err := proposeedit.ExecRunner{Bin: bin}.Propose(context.Background(), dir)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if got != runDir {
		t.Fatalf("run directory is %q", got)
	}

	failing := filepath.Join(dir, "cmoa-fails")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil { //nolint:gosec // a fake binary is executable
		t.Fatalf("write fake cmoa: %v", err)
	}
	if _, err := (proposeedit.ExecRunner{Bin: failing}).Propose(context.Background(), dir); err == nil {
		t.Fatalf("a failing cmoa was read as a run")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
