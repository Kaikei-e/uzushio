package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The whole of `uzushio improve` is exercised here against fixtures: a
// directory of CMoA traces written by hand, an empty vault, a rendered harness
// directory, and a `cmoa` that answers from a canned diff. Nothing in this file
// needs a model server, a container or a network.

// improveTraces writes a trace directory in which one failure recurs often
// enough to become a pattern and another does not.
func improveTraces(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "traces")
	for i, id := range []string{
		"20260904T120000Z-0000000a",
		"20260904T121000Z-0000000b",
		"20260904T122000Z-0000000c",
	} {
		dir := filepath.Join(root, "task-hello", "runs", id)
		mkdirAll(t, filepath.Join(dir, "candidates"))
		writeJSONFile(t, filepath.Join(dir, "run.json"), map[string]any{
			"schema_version": 1,
			"run_id":         id,
			"prompt_version": "pv1",
			"task":           map[string]any{"id": "hello", "files": []string{"add.go"}},
			"proposers": []map[string]any{
				{"id": "alpha", "model": "model-a"},
				{"id": "beta", "model": "model-b"},
			},
		})
		for _, proposer := range []string{"alpha", "beta"} {
			writeJSONFile(t, filepath.Join(dir, "candidates", proposer+".json"), map[string]any{
				"proposer_id": proposer, "model": "model-" + proposer, "status": "ok",
				"diff": map[string]any{"files": []string{"add.go"}},
			})
			mkdirAll(t, filepath.Join(dir, "verify", proposer))
			writeJSONFile(t, filepath.Join(dir, "verify", proposer, "result.json"), map[string]any{
				"candidate_id": proposer, "status": "apply_failed",
				"apply_error": "error: add.go: patch does not apply\n",
			})
		}
		// One run also holds a proposer that never answered, which is the
		// failure the rule set deliberately writes nothing for.
		if i == 0 {
			writeJSONFile(t, filepath.Join(dir, "candidates", "gamma.json"), map[string]any{
				"proposer_id": "gamma", "model": "model-c", "status": "http_error",
				"error": "dial tcp: connect: connection refused",
			})
		}
	}
	return root
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeJSONFile(t *testing.T, path string, body any) {
	t.Helper()
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// improveVault is an empty vault with the two directories a pass writes into.
func improveVault(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vault")
	mkdirAll(t, filepath.Join(dir, "spec", "patterns"))
	mkdirAll(t, filepath.Join(dir, "spec", "edits"))
	mkdirAll(t, filepath.Join(dir, "spec", "topics"))
	return dir
}

func TestImproveMinesAPatternAndSaysWhatItDidNot(t *testing.T) {
	vault := improveVault(t)
	got := run(t, "improve", "--traces", improveTraces(t), "--vault", vault, "--as-of", "2026-09-05")
	if got.code != exitOK {
		t.Fatalf("improve exit = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "create") || !strings.Contains(got.stdout, "fp/context-lines-drift-hello") {
		t.Fatalf("stdout does not report the pattern:\n%s", got.stdout)
	}
	if !strings.Contains(got.stderr, "R15") {
		t.Fatalf("the failures nothing may be written for were not reported:\n%s", got.stderr)
	}
	body := readFile(t, filepath.Join(vault, "spec", "patterns", "context-lines-drift-hello.md"))
	for _, want := range []string{
		"kind: pattern", "status: open", "category: unsafe-provided", "component: memory",
		"20260904T120000Z-0000000a", "20260904T122000Z-0000000c",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the pattern does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "connection refused") {
		t.Fatalf("an infrastructure failure reached the pattern:\n%s", body)
	}
}

func TestImproveDryRunWritesNothing(t *testing.T) {
	vault := improveVault(t)
	got := run(t, "improve", "--traces", improveTraces(t), "--vault", vault,
		"--as-of", "2026-09-05", "--dry-run")
	if got.code != exitOK {
		t.Fatalf("improve exit = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "fp/context-lines-drift-hello") {
		t.Fatalf("a dry run said nothing about what it would write:\n%s", got.stdout)
	}
	if _, err := os.Stat(filepath.Join(vault, "spec", "patterns", "context-lines-drift-hello.md")); err == nil {
		t.Fatalf("a dry run wrote the pattern")
	}
}

func TestImproveIsIdempotent(t *testing.T) {
	vault := improveVault(t)
	traces := improveTraces(t)
	first := run(t, "improve", "--traces", traces, "--vault", vault, "--as-of", "2026-09-05")
	if first.code != exitOK {
		t.Fatalf("first pass exit = %d, stderr = %q", first.code, first.stderr)
	}
	path := filepath.Join(vault, "spec", "patterns", "context-lines-drift-hello.md")
	before := readFile(t, path)
	second := run(t, "improve", "--traces", traces, "--vault", vault, "--as-of", "2026-09-06")
	if second.code != exitOK {
		t.Fatalf("second pass exit = %d, stderr = %q", second.code, second.stderr)
	}
	if !strings.Contains(second.stdout, "unchanged") {
		t.Fatalf("the second pass did not report the pattern as settled:\n%s", second.stdout)
	}
	if after := readFile(t, path); after != before {
		t.Fatalf("a second pass over the same traces rewrote the document")
	}
}

func TestImproveNeedsTraces(t *testing.T) {
	got := run(t, "improve", "--vault", t.TempDir())
	if got.code != exitUsage {
		t.Fatalf("improve with no traces exit = %d, want %d", got.code, exitUsage)
	}
}

func TestImproveProposeNeedsAHarness(t *testing.T) {
	got := run(t, "improve", "--traces", improveTraces(t), "--vault", improveVault(t), "--propose")
	if got.code != exitUsage {
		t.Fatalf("propose with no harness exit = %d, want %d", got.code, exitUsage)
	}
	if !strings.Contains(got.stderr, "--harness") {
		t.Fatalf("the refusal does not name the flag: %q", got.stderr)
	}
}

// fakeCMoA writes a `cmoa` that creates a run directory holding one candidate
// per canned diff, and prints the directory the way the real one does.
func fakeCMoA(t *testing.T, diffs map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the fake cmoa is a shell script")
	}
	home := t.TempDir()
	runDir := filepath.Join(home, "run")
	mkdirAll(t, filepath.Join(runDir, "candidates"))
	for id, diff := range diffs {
		writeJSONFile(t, filepath.Join(runDir, "candidates", id+".json"), map[string]any{
			"proposer_id": id, "model": "model-" + id, "status": "ok",
		})
		if err := os.WriteFile(filepath.Join(runDir, "candidates", id+".diff"), []byte(diff), 0o644); err != nil {
			t.Fatalf("write diff: %v", err)
		}
		raw := "Root cause: the harness never told the agent to quote the file bytes.\n\n" + diff
		if err := os.WriteFile(filepath.Join(runDir, "candidates", id+".raw.txt"), []byte(raw), 0o644); err != nil {
			t.Fatalf("write raw: %v", err)
		}
	}
	bin := filepath.Join(home, "cmoa")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+runDir+"\n"), 0o755); err != nil { //nolint:gosec // a fake binary is executable
		t.Fatalf("write fake cmoa: %v", err)
	}
	return bin
}

func TestImproveProposesAnEditFromACandidate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	vault := improveVault(t)
	harness := filepath.Join(t.TempDir(), "harness")
	mkdirAll(t, filepath.Join(harness, "memory"))
	mkdirAll(t, filepath.Join(harness, "skills"))

	const note = "# Quote the file bytes\n\nA hunk's context lines are the bytes the prompt gave you.\n"
	diff := "diff --git a/memory/10-quote-bytes.md b/memory/10-quote-bytes.md\n" +
		"new file mode 100644\n--- /dev/null\n+++ b/memory/10-quote-bytes.md\n" +
		"@@ -0,0 +1,3 @@\n+# Quote the file bytes\n+\n+A hunk's context lines are the bytes the prompt gave you.\n"
	out := filepath.Join(t.TempDir(), "out")

	got := run(t, "improve",
		"--traces", improveTraces(t), "--vault", vault, "--as-of", "2026-09-05",
		"--propose", "--harness", harness, "--cmoa", fakeCMoA(t, map[string]string{"alpha": diff}),
		"--out", out)
	if got.code != exitOK {
		t.Fatalf("improve exit = %d, stdout = %q, stderr = %q", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "proposed") || !strings.Contains(got.stdout, "he-0001") {
		t.Fatalf("stdout does not report the edit:\n%s", got.stdout)
	}

	edit := readFile(t, filepath.Join(vault, "spec", "edits", "he-0001.md"))
	for _, want := range []string{
		"kind: edit", "status: proposed", "component: memory",
		"- memory/10-quote-bytes.md", "approval: auto",
		"ref: fp/context-lines-drift-hello", "expect: fix",
		"root_cause: the harness never told the agent to quote the file bytes.",
		"about:", "topic/harness-improvement",
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("the edit does not say %q:\n%s", want, edit)
		}
	}
	if !strings.Contains(edit, strings.TrimSpace(note)) {
		t.Fatalf("the edit's body is not the note it renders:\n%s", edit)
	}
	if _, err := os.Stat(filepath.Join(vault, "spec", "edits", "he-0001.diff")); err == nil {
		t.Fatalf("a memory edit wrote a sidecar diff")
	}
	// The topic an edit is about has to be in the vault, or the reference
	// dangles; the pass writes it where the vault has none.
	if _, err := os.Stat(filepath.Join(vault, "spec", "topics", "harness-improvement.md")); err != nil {
		t.Fatalf("the topic was not written: %v", err)
	}

	summary := readFile(t, filepath.Join(out, "improve.json"))
	var decoded struct {
		Runs      int `json:"runs"`
		Patterns  []struct{ ID, Action string }
		Proposals []struct{ Edit, Pattern, Component, Approval string }
	}
	if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, summary)
	}
	if decoded.Runs != 3 || len(decoded.Patterns) != 1 || len(decoded.Proposals) != 1 {
		t.Fatalf("the summary is %s", summary)
	}
	if decoded.Proposals[0].Edit != "he-0001" || decoded.Proposals[0].Approval != "auto" {
		t.Fatalf("the summary's proposal is %+v", decoded.Proposals[0])
	}
}

func TestImproveRefusesACandidateOutsideTheThreeSurfaces(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	vault := improveVault(t)
	harness := filepath.Join(t.TempDir(), "harness")
	mkdirAll(t, filepath.Join(harness, "memory"))
	mkdirAll(t, filepath.Join(harness, "skills"))
	diff := "diff --git a/hooks.json b/hooks.json\nnew file mode 100644\n--- /dev/null\n+++ b/hooks.json\n" +
		"@@ -0,0 +1 @@\n+{}\n"

	got := run(t, "improve",
		"--traces", improveTraces(t), "--vault", vault, "--as-of", "2026-09-05",
		"--propose", "--harness", harness, "--cmoa", fakeCMoA(t, map[string]string{"alpha": diff}))
	if got.code != exitOK {
		t.Fatalf("improve exit = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "refused") {
		t.Fatalf("the refusal was not reported:\n%s", got.stderr)
	}
	if _, err := os.Stat(filepath.Join(vault, "spec", "edits", "he-0001.md")); err == nil {
		t.Fatalf("an edit outside the three surfaces was written")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// twoPatternTraces writes traces in which two different failures recur, so a
// pass has more than one pattern to propose against.
func twoPatternTraces(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "traces")
	for _, id := range []string{
		"20260904T120000Z-0000000a",
		"20260904T121000Z-0000000b",
	} {
		dir := filepath.Join(root, "task-hello", "runs", id)
		mkdirAll(t, filepath.Join(dir, "candidates"))
		writeJSONFile(t, filepath.Join(dir, "run.json"), map[string]any{
			"schema_version": 1,
			"run_id":         id,
			"prompt_version": "pv1",
			"task":           map[string]any{"id": "hello", "files": []string{"add.go"}},
			"proposers": []map[string]any{
				{"id": "alpha", "model": "model-a"},
				{"id": "beta", "model": "model-b"},
			},
		})
		writeJSONFile(t, filepath.Join(dir, "candidates", "alpha.json"), map[string]any{
			"proposer_id": "alpha", "model": "model-a", "status": "ok",
			"diff": map[string]any{"files": []string{"add.go"}},
		})
		mkdirAll(t, filepath.Join(dir, "verify", "alpha"))
		writeJSONFile(t, filepath.Join(dir, "verify", "alpha", "result.json"), map[string]any{
			"candidate_id": "alpha", "status": "apply_failed",
			"apply_error": "error: add.go: patch does not apply\n",
		})
		// A second, differently-shaped failure: a completion with no diff in it.
		writeJSONFile(t, filepath.Join(dir, "candidates", "beta.json"), map[string]any{
			"proposer_id": "beta", "model": "model-b", "status": "no_diff",
		})
	}
	return root
}

// TestImproveDryRunNumbersEachPatternDistinctly covers the review's H12: under
// --dry-run nothing is written between patterns, so a plan that rescanned the
// vault each time would preview he-0001 for every one of them and the preview
// would be a lie.
func TestImproveDryRunNumbersEachPatternDistinctly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	vault := improveVault(t)
	harness := filepath.Join(t.TempDir(), "harness")
	mkdirAll(t, filepath.Join(harness, "memory"))
	mkdirAll(t, filepath.Join(harness, "skills"))
	diff := "diff --git a/memory/10-note.md b/memory/10-note.md\n" +
		"new file mode 100644\n--- /dev/null\n+++ b/memory/10-note.md\n" +
		"@@ -0,0 +1 @@\n+A note.\n"
	out := filepath.Join(t.TempDir(), "out")

	got := run(t, "improve",
		"--traces", twoPatternTraces(t), "--vault", vault, "--as-of", "2026-09-05", "--dry-run",
		"--propose", "--harness", harness, "--cmoa", fakeCMoA(t, map[string]string{"alpha": diff}),
		"--out", out)
	if got.code != exitOK {
		t.Fatalf("improve exit = %d, stdout = %q, stderr = %q", got.code, got.stdout, got.stderr)
	}

	var decoded struct {
		Patterns  []struct{ ID string }
		Proposals []struct{ Edit, Pattern string }
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(out, "improve.json"))), &decoded); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if len(decoded.Patterns) != 2 || len(decoded.Proposals) != 2 {
		t.Fatalf("the pass found %d patterns and %d proposals", len(decoded.Patterns), len(decoded.Proposals))
	}
	if decoded.Proposals[0].Edit == decoded.Proposals[1].Edit {
		t.Fatalf("both patterns previewed the same identifier %s", decoded.Proposals[0].Edit)
	}
	if decoded.Proposals[0].Edit != "he-0001" || decoded.Proposals[1].Edit != "he-0002" {
		t.Fatalf("the preview numbered %s and %s", decoded.Proposals[0].Edit, decoded.Proposals[1].Edit)
	}
	// A dry run writes nothing, previewed identifiers included.
	if _, err := os.Stat(filepath.Join(vault, "spec", "edits", "he-0001.md")); err == nil {
		t.Fatalf("a dry run wrote an edit")
	}
}

// TestImproveRefusesAClosedPattern covers L18: an edit predicting `expect: fix`
// against a claim somebody already resolved is a proposal about a closed
// question, and the vault reports it as one.
func TestImproveRefusesAClosedPattern(t *testing.T) {
	vault := improveVault(t)
	resolved := "---\nid: fp/settled\nkind: pattern\ntitle: A settled failure\n" +
		"status: resolved\ndate: 2026-09-01\ncategory: not-provided\n" +
		"context: a context\ncomponent: memory\n---\n\n# A settled failure\n"
	if err := os.WriteFile(filepath.Join(vault, "spec", "patterns", "settled.md"), []byte(resolved), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := run(t, "improve",
		"--traces", improveTraces(t), "--vault", vault, "--as-of", "2026-09-05",
		"--propose", "--harness", t.TempDir(), "--pattern", "fp/settled", "--cmoa", "true")
	if got.code != exitUsage {
		t.Fatalf("proposing against a resolved pattern exit = %d, want %d", got.code, exitUsage)
	}
	if !strings.Contains(got.stderr, "resolved") {
		t.Fatalf("the refusal does not say why: %q", got.stderr)
	}
}

// TestImproveWarnsWhenThePatternBlamesASurfaceNothingCanRender covers M20: five
// of the rules attribute to a surface the rendered harness has no place for, so
// any edit answering one of them is about a different surface from the failure.
func TestImproveWarnsWhenThePatternBlamesASurfaceNothingCanRender(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	vault := improveVault(t)
	open := "---\nid: fp/budget\nkind: pattern\ntitle: A budget failure\n" +
		"status: open\ndate: 2026-09-01\ncategory: wrong-duration\n" +
		"context: a context\ncomponent: middleware\n---\n\n# A budget failure\n"
	if err := os.WriteFile(filepath.Join(vault, "spec", "patterns", "budget.md"), []byte(open), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	harness := filepath.Join(t.TempDir(), "harness")
	mkdirAll(t, filepath.Join(harness, "memory"))
	mkdirAll(t, filepath.Join(harness, "skills"))
	diff := "diff --git a/memory/10-note.md b/memory/10-note.md\n" +
		"new file mode 100644\n--- /dev/null\n+++ b/memory/10-note.md\n" +
		"@@ -0,0 +1 @@\n+A note.\n"

	got := run(t, "improve",
		"--traces", improveTraces(t), "--vault", vault, "--as-of", "2026-09-05",
		"--propose", "--harness", harness, "--pattern", "fp/budget",
		"--cmoa", fakeCMoA(t, map[string]string{"alpha": diff}))
	if got.code != exitOK {
		t.Fatalf("improve exit = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "no injection point") {
		t.Fatalf("nothing warned that the edit answers a different surface:\n%s", got.stderr)
	}
}
