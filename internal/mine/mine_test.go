package mine_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/mine"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// runFixture is one trace run as a test writes it: the little of CMoA's schema
// the rules read, spelled the way CMoA spells it.
type runFixture struct {
	id         string
	task       string
	taskDir    string
	taskFiles  []string
	prompt     string
	proposers  []proposerFixture
	candidates []candidateFixture
	verifies   []verifyFixture
	selection  *selectionFixture
	verifyTMO  int
	schema     *int
}

type proposerFixture struct {
	id      string
	model   string
	timeout int
}

type candidateFixture struct {
	id           string
	model        string
	status       string
	errorText    string
	finishReason string
	promptTokens int
	completion   int
	diffFiles    []string
}

type verifyFixture struct {
	id         string
	status     string
	exitCode   int
	applyError string
	bandFailed []string
	bandRows   []mine.BandRow
}

type selectionFixture struct {
	kind        string
	candidateID string
	alsoPassed  []string
}

// write lays the fixture out as a run directory under root.
func (f runFixture) write(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, f.id)
	mkdirs(t, filepath.Join(dir, "candidates"))
	schema := 1
	if f.schema != nil {
		schema = *f.schema
	}
	header := map[string]any{
		"schema_version": schema,
		"run_id":         f.id,
		"prompt_version": f.prompt,
		"task": map[string]any{
			"id": f.task, "dir": f.taskDir, "files": f.taskFiles,
		},
	}
	config := map[string]any{}
	var configProposers []map[string]any
	var proposers []map[string]any
	for _, p := range f.proposers {
		proposers = append(proposers, map[string]any{"id": p.id, "model": p.model})
		configProposers = append(configProposers, map[string]any{"id": p.id, "timeout_seconds": p.timeout})
	}
	config["proposers"] = configProposers
	config["verify"] = map[string]any{"timeout_seconds": f.verifyTMO}
	header["config"] = config
	header["proposers"] = proposers
	writeJSON(t, filepath.Join(dir, "run.json"), header)
	for _, c := range f.candidates {
		body := map[string]any{
			"proposer_id":   c.id,
			"model":         c.model,
			"status":        c.status,
			"error":         c.errorText,
			"finish_reason": c.finishReason,
			"usage": map[string]any{
				"prompt_tokens": c.promptTokens, "completion_tokens": c.completion,
			},
		}
		if len(c.diffFiles) > 0 {
			body["diff"] = map[string]any{"files": c.diffFiles, "additions": 1, "deletions": 1}
		}
		writeJSON(t, filepath.Join(dir, "candidates", c.id+".json"), body)
	}
	for _, v := range f.verifies {
		mkdirs(t, filepath.Join(dir, "verify", v.id))
		body := map[string]any{
			"candidate_id": v.id, "status": v.status, "exit_code": v.exitCode,
		}
		if v.applyError != "" {
			body["apply_error"] = v.applyError
		}
		if v.bandFailed != nil || v.bandRows != nil {
			body["band"] = map[string]any{"failed": v.bandFailed, "rows": v.bandRows}
		}
		writeJSON(t, filepath.Join(dir, "verify", v.id, "result.json"), body)
	}
	if f.selection != nil {
		writeJSON(t, filepath.Join(dir, "select.json"), map[string]any{
			"selection": map[string]any{
				"kind": f.selection.kind, "candidate_id": f.selection.candidateID,
			},
			"also_passed": f.selection.alsoPassed,
		})
	}
}

func mkdirs(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeJSON(t *testing.T, path string, body any) {
	t.Helper()
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// traces writes a directory of runs and returns it.
func traces(t *testing.T, runs ...runFixture) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runs")
	mkdirs(t, root)
	for _, run := range runs {
		run.write(t, root)
	}
	return root
}

// pool is the three proposers most fixtures use.
func pool() []proposerFixture {
	return []proposerFixture{
		{id: "alpha", model: "model-a", timeout: 600},
		{id: "beta", model: "model-b", timeout: 600},
		{id: "gamma", model: "model-c", timeout: 600},
	}
}

func mineDir(t *testing.T, dir string, opts mine.Options) mine.Result {
	t.Helper()
	runs, err := mine.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return mine.Mine(runs, opts)
}

// ids lists the pattern identifiers a result would write.
func ids(t *testing.T, buckets []mine.Bucket) []string {
	t.Helper()
	var out []string
	for _, bucket := range buckets {
		id, err := bucket.PatternID()
		if err != nil {
			t.Fatalf("pattern id: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func TestLoadReadsWhatTheRulesNeed(t *testing.T) {
	dir := traces(t, runFixture{
		id: "20260904T124640Z-90180d6b", task: "hello", prompt: "b1a71c61",
		taskFiles: []string{"add.go"}, verifyTMO: 600,
		proposers:  pool(),
		candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok", diffFiles: []string{"add.go"}}},
		verifies:   []verifyFixture{{id: "alpha", status: "pass"}},
		selection:  &selectionFixture{kind: "selected", candidateID: "alpha", alsoPassed: []string{"beta"}},
	})
	runs, err := mine.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want one run, got %d", len(runs))
	}
	run := runs[0]
	if run.ID != "20260904T124640Z-90180d6b" || run.TaskID != "hello" || run.PromptVersion != "b1a71c61" {
		t.Fatalf("header not read back: %+v", run)
	}
	if run.VerifyTimeoutSeconds != 600 || run.ProposerTimeoutSeconds("beta") != 600 {
		t.Fatalf("budgets not read back: %+v", run)
	}
	if run.Model("beta") != "model-b" {
		t.Fatalf("model not read back: %q", run.Model("beta"))
	}
	if len(run.Candidates) != 1 || run.Verifies["alpha"].Status != mine.VerifyPass {
		t.Fatalf("candidates or verifies not read back: %+v", run)
	}
	if run.Selection.Kind != mine.SelectionSelected || len(run.Selection.AlsoPassed) != 1 {
		t.Fatalf("selection not read back: %+v", run.Selection)
	}
}

func TestLoadSkipsAnUnknownSchemaVersion(t *testing.T) {
	two := 2
	dir := traces(t,
		runFixture{id: "20260904T124640Z-90180d6b", task: "hello", schema: &two, proposers: pool()},
		runFixture{id: "20260904T124641Z-90180d6c", task: "hello", proposers: pool()},
	)
	runs, err := mine.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "20260904T124641Z-90180d6c" {
		t.Fatalf("a run of another schema version was read: %+v", runs)
	}
}

func TestLoadIsOrderedAndDeduplicated(t *testing.T) {
	dir := traces(t,
		runFixture{id: "20260904T130000Z-00000002", task: "hello", proposers: pool()},
		runFixture{id: "20260904T120000Z-00000001", task: "hello", proposers: pool()},
	)
	runs, err := mine.Load(dir, dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("the same directory twice gave %d runs", len(runs))
	}
	if runs[0].ID > runs[1].ID {
		t.Fatalf("runs are not in identifier order: %s then %s", runs[0].ID, runs[1].ID)
	}
}

// TestEveryRuleFires drives each of the fourteen rules with the smallest trace
// that triggers it, and asserts the pattern it lands on. The table is the rule
// set's contract: a rule whose predicate drifts stops matching its row here
// before it starts writing the wrong document into somebody's vault.
func TestEveryRuleFires(t *testing.T) {
	cases := []struct {
		rule      string
		want      string
		category  vocab.Category
		component string
		runs      []runFixture
	}{
		{
			rule: "R1", want: "fp/no-diff-emitted",
			category: vocab.CategoryNotProvided, component: "system-prompt",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", prompt: "pv1", proposers: pool(),
					candidates: []candidateFixture{
						{id: "alpha", model: "model-a", status: "no_diff"},
						{id: "beta", model: "model-b", status: "no_diff"},
					},
				}
			}),
		},
		{
			rule: "R2", want: "fp/non-completion-response",
			category: vocab.CategoryNotProvided, component: "tool-description",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "malformed"}},
				}
			}),
		},
		{
			rule: "R3", want: "fp/context-lines-drift-hello",
			category: vocab.CategoryUnsafeProvided, component: "memory",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{
						{id: "alpha", model: "model-a", status: "ok", diffFiles: []string{"add.go"}},
					},
					verifies: []verifyFixture{{
						id: "alpha", status: "apply_failed",
						applyError: "error: patch failed: add.go:3\nerror: add.go: patch does not apply\n",
					}},
				}
			}),
		},
		{
			rule: "R4", want: "fp/malformed-hunk-hello",
			category: vocab.CategoryUnsafeProvided, component: "memory",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
					verifies: []verifyFixture{{
						id: "alpha", status: "apply_failed", applyError: "error: corrupt patch at line 12\n",
					}},
				}
			}),
		},
		{
			rule: "R5", want: "fp/out-of-scope-path-hello",
			category: vocab.CategoryUnsafeProvided, component: "memory",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
					verifies: []verifyFixture{{
						id: "alpha", status: "apply_failed",
						applyError: "error: sub/new.go: No such file or directory\n",
					}},
				}
			}),
		},
		{
			rule: "R6", want: "fp/task-specific-knowledge-gap-hello",
			category: vocab.CategoryUnsafeProvided, component: "memory",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{
						{id: "alpha", model: "model-a", status: "ok"},
						{id: "beta", model: "model-b", status: "ok"},
					},
					verifies: []verifyFixture{
						{id: "alpha", status: "fail", exitCode: 1},
						{id: "beta", status: "pass"},
					},
				}
			}),
		},
		{
			rule: "R7", want: "fp/instruction-underspecified-hello",
			category: vocab.CategoryNotProvided, component: "memory",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{
						{id: "alpha", model: "model-a", status: "ok"},
						{id: "beta", model: "model-b", status: "ok"},
					},
					verifies: []verifyFixture{
						{id: "alpha", status: "fail", exitCode: 1},
						{id: "beta", status: "fail", exitCode: 1},
					},
					selection: &selectionFixture{kind: "no_candidate"},
				}
			}),
		},
		{
			rule: "R8", want: "fp/proposer-budget-exceeded",
			category: vocab.CategoryWrongDuration, component: "middleware",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{
						{id: "alpha", model: "model-a", status: "timeout", promptTokens: 602},
					},
				}
			}),
		},
		{
			rule: "R9", want: "fp/verify-budget-exceeded-hello",
			category: vocab.CategoryWrongDuration, component: "middleware",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(), verifyTMO: 600,
					candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
					verifies:   []verifyFixture{{id: "alpha", status: "timeout"}},
				}
			}),
		},
		{
			rule: "R10", want: "fp/completion-truncated",
			category: vocab.CategoryWrongDuration, component: "system-prompt",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{{
						id: "alpha", model: "model-a", status: "ok",
						finishReason: "length", completion: 4096,
					}},
				}
			}),
		},
		{
			rule: "R11", want: "fp/edits-unlisted-file-hello",
			category: vocab.CategoryUnsafeProvided, component: "system-prompt",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(), taskFiles: []string{"add.go"},
					candidates: []candidateFixture{{
						id: "alpha", model: "model-a", status: "ok",
						diffFiles: []string{"add.go", "go.mod"},
					}},
				}
			}),
		},
		{
			rule: "R13", want: "fp/single-proposer-dependence-hello",
			category: vocab.CategoryNotProvided, component: "subagent-config",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
					verifies:   []verifyFixture{{id: "alpha", status: "pass"}},
					selection:  &selectionFixture{kind: "selected", candidateID: "alpha"},
				}
			}),
		},
		{
			rule: "R14", want: "fp/persistent-band-breach-hello-p99-latency-ms",
			category: vocab.CategoryWrongTiming, component: "middleware",
			runs: repeat(2, func(i int) runFixture {
				return runFixture{
					id: runID(i), task: "hello", proposers: pool(),
					candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
					verifies: []verifyFixture{{
						id: "alpha", status: "fail", exitCode: 1,
						bandFailed: []string{"p99_latency_ms"},
						bandRows: []mine.BandRow{{
							Invariant: "p99_latency_ms", Value: f64(21.5),
							BandLo: f64(0), BandHi: f64(15), Verdict: "fail",
						}},
					}},
				}
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			result := mineDir(t, traces(t, tc.runs...), mine.Options{})
			got := ids(t, result.Buckets)
			if !contains(got, tc.want) {
				t.Fatalf("rule %s wrote %v, want %s (below support: %v)",
					tc.rule, got, tc.want, ids(t, result.Below))
			}
			for _, bucket := range result.Buckets {
				id, _ := bucket.PatternID()
				if id != tc.want {
					continue
				}
				if bucket.Rule.Name != tc.rule {
					t.Fatalf("%s was written by %s, not %s", id, bucket.Rule.Name, tc.rule)
				}
				if bucket.Rule.Category != tc.category {
					t.Fatalf("%s is %s, want %s", id, bucket.Rule.Category, tc.category)
				}
				if bucket.Rule.Component != tc.component {
					t.Fatalf("%s attributes %s, want %s", id, bucket.Rule.Component, tc.component)
				}
				pattern, err := bucket.Pattern("2026-09-05")
				if err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				if strings.TrimSpace(pattern.Context) == "" {
					t.Fatalf("%s wrote an empty context", id)
				}
				if strings.Contains(pattern.Context, "unknown") {
					t.Fatalf("%s wrote a context with a missing fact in it: %q", id, pattern.Context)
				}
			}
		})
	}
}

// TestR12NeedsTheInstruction covers the one rule whose predicate reaches
// outside the trace: it claims the instruction did not ask for a test change,
// so it may not fire where the instruction cannot be read.
func TestR12NeedsTheInstruction(t *testing.T) {
	fixtures := repeat(2, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", taskDir: "/gone", proposers: pool(),
			taskFiles: []string{"add.go", "add_test.go"},
			candidates: []candidateFixture{{
				id: "alpha", model: "model-a", status: "ok",
				diffFiles: []string{"add.go", "add_test.go"},
			}},
		}
	})
	dir := traces(t, fixtures...)

	silent := mineDir(t, dir, mine.Options{
		Instruction: func(string) (string, bool) { return "", false },
	})
	if contains(ids(t, silent.Buckets), "fp/edits-the-tests-hello") {
		t.Fatalf("R12 fired without an instruction to read")
	}

	asked := mineDir(t, dir, mine.Options{
		Instruction: func(string) (string, bool) { return "Also update the tests.", true },
	})
	if contains(ids(t, asked.Buckets), "fp/edits-the-tests-hello") {
		t.Fatalf("R12 fired on an instruction that asked for the tests")
	}

	quiet := mineDir(t, dir, mine.Options{
		Instruction: func(string) (string, bool) { return "Make Add return the sum.", true },
	})
	if !contains(ids(t, quiet.Buckets), "fp/edits-the-tests-hello") {
		t.Fatalf("R12 did not fire: %v", ids(t, quiet.Buckets))
	}

	// The word, not the letters. An instruction reading "the latest version" or
	// "the greatest of the two" carries the substring and asks for nothing, and
	// a rule that reads them as consent is a rule that never fires again.
	for _, instruction := range []string{
		"Return the latest version.", "Return the greatest of the two.",
		"Do not protest; just fix it.", "Win the contest.",
	} {
		got := mineDir(t, dir, mine.Options{
			Instruction: func(string) (string, bool) { return instruction, true },
		})
		if !contains(ids(t, got.Buckets), "fp/edits-the-tests-hello") {
			t.Fatalf("R12 was suppressed by %q, which asks for no test change", instruction)
		}
	}
	for _, instruction := range []string{
		"Update the test too.", "Add tests.", "Change add_test.go as well.",
	} {
		got := mineDir(t, dir, mine.Options{
			Instruction: func(string) (string, bool) { return instruction, true },
		})
		if contains(ids(t, got.Buckets), "fp/edits-the-tests-hello") {
			t.Fatalf("R12 fired on %q, which does ask for a test change", instruction)
		}
	}
}

// TestOneUnreadableDocumentDoesNotCostThePassEverythingElse covers M15: a file
// somebody hand-edited badly must not take thirteen good patterns and a whole
// round of proposals with it.
func TestOneUnreadableDocumentDoesNotCostThePassEverythingElse(t *testing.T) {
	vault := t.TempDir()
	mkdirs(t, filepath.Join(vault, vocab.DirPatterns))
	write(t, filepath.Join(vault, vocab.DirPatterns, "broken.md"), []byte("not a document at all\n"))

	good := doc.Pattern{
		PatternID: "fp/fine", Title: "A pattern that reads", Date: "2026-09-05",
		Status: vocab.StatusOpen, Category: vocab.CategoryNotProvided,
		Context: "a context", Component: "memory", Evidence: []string{"20260905T000000Z-aaaaaaaa"},
	}
	bad := good
	bad.PatternID = "fp/broken"

	changes, err := mine.Plan(vault, []doc.Pattern{bad, good})
	if err != nil {
		t.Fatalf("one bad document failed the whole plan: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("plan returned %d changes", len(changes))
	}
	var sawUnreadable, sawCreate bool
	for _, change := range changes {
		switch change.Action {
		case mine.ActionUnreadable:
			sawUnreadable = true
			if change.Err == nil || !errors.Is(change.Err, mine.ErrVault) {
				t.Fatalf("the unreadable document is reported as %v, want an ErrVault", change.Err)
			}
		case mine.ActionCreate:
			sawCreate = true
		case mine.ActionAppend, mine.ActionUnchanged, mine.ActionSkipClosed:
			t.Fatalf("%s planned %s against an empty vault", change.ID, change.Action)
		}
	}
	if !sawUnreadable || !sawCreate {
		t.Fatalf("changes are %+v", changes)
	}
	if err := mine.Apply(vault, changes); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vault, vocab.DirPatterns, "fine.md")); err != nil {
		t.Fatalf("the readable pattern was not written: %v", err)
	}
}

// TestASharpenedPatternKeepsItsOwnReading covers the reporting half of M12: a
// pattern somebody re-attributed by hand outranks the rule, and the divergence
// is named rather than silently overwritten in the report.
func TestASharpenedPatternKeepsItsOwnReading(t *testing.T) {
	vault := t.TempDir()
	mkdirs(t, filepath.Join(vault, vocab.DirPatterns))
	onDisk := doc.Pattern{
		PatternID: "fp/no-diff-emitted", Title: "A completion arrives with no unified diff in it",
		Date: "2026-09-01", Status: vocab.StatusOpen, Category: vocab.CategoryNotProvided,
		Context: "a context somebody sharpened", Component: "skill",
		Evidence: []string{"20260901T000000Z-aaaaaaaa"}, Body: "a body",
	}
	write(t, filepath.Join(vault, vocab.DirPatterns, "no-diff-emitted.md"), bytesOf(t, onDisk))

	mined := onDisk
	mined.Component = "system-prompt"
	mined.Evidence = []string{"20260905T000000Z-bbbbbbbb"}
	changes, err := mine.Plan(vault, []doc.Pattern{mined})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if changes[0].Component != "skill" {
		t.Fatalf("the report names %s, but the vault holds skill", changes[0].Component)
	}
	if len(changes[0].Diverged) != 1 || changes[0].Diverged[0] != "component" {
		t.Fatalf("the divergence was not reported: %v", changes[0].Diverged)
	}
}

// TestTransportFailureIsCountedAndNotWritten holds the one deliberate non-rule.
// Six of the forty-three recorded runs behind this design were a model server
// being unreachable; a pattern for that is one no edit can fix and one the vault
// would nag about forever.
func TestTransportFailureIsCountedAndNotWritten(t *testing.T) {
	result := mineDir(t, traces(t, repeat(3, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{
				{id: "alpha", model: "model-a", status: "http_error",
					errorText: `Post "http://127.0.0.1:8083/v1/chat/completions": dial tcp 127.0.0.1:8083: connect: connection refused`},
				{id: "beta", model: "model-b", status: "http_error",
					errorText: `Post "http://127.0.0.1:8084/v1/chat/completions": EOF`},
			},
		}
	})...), mine.Options{})
	if len(result.Buckets) != 0 || len(result.Below) != 0 {
		t.Fatalf("a transport failure was mined: %v", ids(t, result.Buckets))
	}
	if len(result.NotMined) != 1 || result.NotMined[0].Name != "R15" || result.NotMined[0].Count != 6 {
		t.Fatalf("transport failures reported as %+v, want one R15 of 6", result.NotMined)
	}
	// The count says a number; the texts say what the number was. Without them
	// "count it, log it" logs nothing.
	if len(result.NotMined[0].Errors) != 2 {
		t.Fatalf("the error texts were discarded: %+v", result.NotMined[0].Errors)
	}
	if !strings.Contains(result.NotMined[0].Errors[0], "connection refused") {
		t.Fatalf("the error texts are %v", result.NotMined[0].Errors)
	}
}

// TestR16MinesARejectedRequest is the half of the old R15 that turned out to be
// mineable: a 4xx is the server reading a request the harness built and refusing
// it, which is a control action of the harness's own.
func TestR16MinesARejectedRequest(t *testing.T) {
	overflow := `llm: HTTP 400: {"error":{"message":"the request exceeds the available context size. try increasing n_ctx"}}`
	rate := `llm: HTTP 429: {"error":{"message":"rate limit reached"}}`
	server := `llm: HTTP 503: {"error":{"message":"service unavailable"}}`
	result := mineDir(t, traces(t, repeat(2, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{
				{id: "alpha", model: "model-a", status: "http_error", errorText: overflow, promptTokens: 9001},
				{id: "beta", model: "model-b", status: "http_error", errorText: rate},
				{id: "gamma", model: "model-c", status: "http_error", errorText: server},
			},
		}
	})...), mine.Options{})

	got := ids(t, result.Buckets)
	// One reason is one bucket: an edit that shortens the prompt answers the
	// overflow and does nothing about the rate limit.
	for _, want := range []string{
		"fp/request-rejected-by-server-context-length",
		"fp/request-rejected-by-server-rate-limited",
	} {
		if !contains(got, want) {
			t.Fatalf("R16 wrote %v, want %s", got, want)
		}
	}
	for _, bucket := range result.Buckets {
		id, _ := bucket.PatternID()
		if id != "fp/request-rejected-by-server-context-length" {
			continue
		}
		if bucket.Rule.Name != "R16" || bucket.Rule.Category != vocab.CategoryUnsafeProvided ||
			bucket.Rule.Component != "system-prompt" {
			t.Fatalf("R16 is %s/%s/%s", bucket.Rule.Name, bucket.Rule.Category, bucket.Rule.Component)
		}
		pattern, err := bucket.Pattern("2026-09-05")
		if err != nil {
			t.Fatalf("pattern: %v", err)
		}
		if !strings.Contains(pattern.Context, "HTTP 400") || !strings.Contains(pattern.Context, "context-length") {
			t.Fatalf("the context does not say what was refused: %q", pattern.Context)
		}
	}
	// A 5xx is the server's own failure and stays outside every surface.
	if len(result.NotMined) != 1 || result.NotMined[0].Count != 2 {
		t.Fatalf("the 5xx answers were not left with R15: %+v", result.NotMined)
	}
}

func TestSupportThreshold(t *testing.T) {
	one := traces(t, runFixture{
		id: runID(0), task: "hello", proposers: pool(),
		candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "no_diff"}},
	})
	result := mineDir(t, one, mine.Options{})
	if len(result.Buckets) != 0 {
		t.Fatalf("one run met the default threshold: %v", ids(t, result.Buckets))
	}
	if len(result.Below) != 1 {
		t.Fatalf("the below-support bucket was dropped instead of reported: %+v", result.Below)
	}
	if got := mineDir(t, one, mine.Options{MinRuns: 1}); len(got.Buckets) != 1 {
		t.Fatalf("--min-support 1 wrote %d patterns", len(got.Buckets))
	}
	two := traces(t, repeat(2, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "no_diff"}},
		}
	})...)
	if got := mineDir(t, two, mine.Options{MinProposers: 2}); len(got.Buckets) != 0 {
		t.Fatalf("one proposer met a two-proposer threshold: %v", ids(t, got.Buckets))
	}
}

// TestMiningIsDeterministic is the property the whole design rests on: the same
// traces mined twice are the same bytes, so a second pass is not a second
// opinion.
func TestMiningIsDeterministic(t *testing.T) {
	dir := traces(t, repeat(4, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", prompt: "pv1", proposers: pool(),
			candidates: []candidateFixture{
				{id: "gamma", model: "model-c", status: "no_diff"},
				{id: "alpha", model: "model-a", status: "no_diff"},
			},
		}
	})...)
	first := patternBytes(t, mineDir(t, dir, mine.Options{}))
	second := patternBytes(t, mineDir(t, dir, mine.Options{}))
	if first != second {
		t.Fatalf("two passes disagreed:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(first, "proposers model-a, model-c") {
		t.Fatalf("the context does not name the models in sorted order:\n%s", first)
	}
}

func patternBytes(t *testing.T, result mine.Result) string {
	t.Helper()
	var out strings.Builder
	for _, bucket := range result.Buckets {
		pattern, err := bucket.Pattern("2026-09-05")
		if err != nil {
			t.Fatalf("pattern: %v", err)
		}
		raw, err := pattern.Bytes()
		if err != nil {
			t.Fatalf("bytes: %v", err)
		}
		out.Write(raw)
	}
	return out.String()
}

func TestPlanCreatesAppendsAndRefusesAClosedPattern(t *testing.T) {
	vault := t.TempDir()
	mkdirs(t, filepath.Join(vault, vocab.DirPatterns))
	pattern := doc.Pattern{
		PatternID: "fp/no-diff-emitted",
		Title:     "A completion arrives with no unified diff in it",
		Date:      "2026-09-01",
		Status:    vocab.StatusOpen,
		Category:  vocab.CategoryNotProvided,
		Context:   "a context somebody sharpened by hand",
		Component: "system-prompt",
		Evidence:  []string{"20260901T000000Z-aaaaaaaa"},
		Body:      "a body somebody added to",
	}

	// A pattern the vault does not have is created.
	fresh := doc.Pattern{
		PatternID: pattern.PatternID, Title: pattern.Title, Date: "2026-09-05",
		Status: vocab.StatusOpen, Category: pattern.Category, Context: "mined context",
		Component: pattern.Component, Evidence: []string{"20260905T000000Z-bbbbbbbb"},
	}
	changes, err := mine.Plan(vault, []doc.Pattern{fresh})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if changes[0].Action != mine.ActionCreate {
		t.Fatalf("a missing pattern planned %s", changes[0].Action)
	}

	// With the hand-written one on disk, the mined evidence is appended and
	// nothing else about the document moves.
	write(t, filepath.Join(vault, vocab.DirPatterns, "no-diff-emitted.md"), bytesOf(t, pattern))
	changes, err = mine.Plan(vault, []doc.Pattern{fresh})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if changes[0].Action != mine.ActionAppend || len(changes[0].Added) != 1 {
		t.Fatalf("an open pattern planned %s adding %v", changes[0].Action, changes[0].Added)
	}
	if err := mine.Apply(vault, changes); err != nil {
		t.Fatalf("apply: %v", err)
	}
	grown, found, err := mine.ReadPattern(filepath.Join(vault, vocab.DirPatterns, "no-diff-emitted.md"))
	if err != nil || !found {
		t.Fatalf("read back: %v %v", found, err)
	}
	if grown.Date != "2026-09-01" || grown.Context != pattern.Context || grown.Body != pattern.Body {
		t.Fatalf("appending rewrote the reading: %+v", grown)
	}
	if len(grown.Evidence) != 2 {
		t.Fatalf("evidence is %v, want both runs", grown.Evidence)
	}

	// Replanning after the append is a no-op.
	changes, err = mine.Plan(vault, []doc.Pattern{fresh})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if changes[0].Action != mine.ActionUnchanged || changes[0].Bytes != nil {
		t.Fatalf("a settled pattern planned %s", changes[0].Action)
	}

	// A pattern somebody closed is never rewritten.
	closed := pattern
	closed.Status = vocab.StatusResolved
	write(t, filepath.Join(vault, vocab.DirPatterns, "no-diff-emitted.md"), bytesOf(t, closed))
	changes, err = mine.Plan(vault, []doc.Pattern{fresh})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if changes[0].Action != mine.ActionSkipClosed || changes[0].Bytes != nil {
		t.Fatalf("a resolved pattern planned %s", changes[0].Action)
	}
	if len(changes[0].Added) != 1 {
		t.Fatalf("the runs a closed pattern refused were not reported: %v", changes[0].Added)
	}
}

// TestRulesAreTheOnesTheDesignNames holds the rule set to its inventory: the
// fourteen the research derived, plus R16, which is the half of the declined
// R15 that a 4xx made mineable.
func TestRulesAreTheOnesTheDesignNames(t *testing.T) {
	want := []string{
		"R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8",
		"R9", "R10", "R11", "R12", "R13", "R14", "R16",
	}
	if len(mine.Rules()) != len(want) {
		t.Fatalf("the rule set holds %d rules, want %d", len(mine.Rules()), len(want))
	}
	for _, name := range want {
		if _, ok := mine.RuleByName(name); !ok {
			t.Fatalf("%s is not in the rule set", name)
		}
	}
	// R15 is the one deliberate non-rule and must never become a rule.
	if _, ok := mine.RuleByName("R15"); ok {
		t.Fatalf("R15 writes documents; it is the trigger that must not")
	}
	seen := map[string]bool{}
	for _, rule := range mine.Rules() {
		if seen[rule.Name] || seen[rule.Slug] {
			t.Fatalf("%s or %s is declared twice", rule.Name, rule.Slug)
		}
		seen[rule.Name], seen[rule.Slug] = true, true
		if rule.Signal == "" || rule.Title == "" {
			t.Fatalf("%s says nothing about what it reads or what it names", rule.Name)
		}
		if _, err := vocab.PatternID(rule.Slug); err != nil {
			t.Fatalf("%s has a slug no pattern identifier can carry: %v", rule.Name, err)
		}
	}
	for _, name := range []string{"R6", "R7", "R13"} {
		rule, _ := mine.RuleByName(name)
		if rule.MinPool < 2 {
			t.Fatalf("%s is a claim about the pool and carries no pool floor", name)
		}
	}
}

// TestAClaimAboutThePoolNeedsAPool covers the floor those three carry: a corpus
// run with one proposer cannot support "others passed" or "nobody else could".
func TestAClaimAboutThePoolNeedsAPool(t *testing.T) {
	solo := traces(t, repeat(3, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello",
			proposers:  []proposerFixture{{id: "alpha", model: "model-a"}},
			candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
			verifies:   []verifyFixture{{id: "alpha", status: "pass"}},
			selection:  &selectionFixture{kind: "selected", candidateID: "alpha"},
		}
	})...)
	if got := ids(t, mineDir(t, solo, mine.Options{}).Buckets); contains(got, "fp/single-proposer-dependence-hello") {
		t.Fatalf("R13 fired on a one-proposer corpus: %v", got)
	}

	pair := traces(t, repeat(3, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
			verifies:   []verifyFixture{{id: "alpha", status: "pass"}},
			selection:  &selectionFixture{kind: "selected", candidateID: "alpha"},
		}
	})...)
	if got := ids(t, mineDir(t, pair, mine.Options{}).Buckets); !contains(got, "fp/single-proposer-dependence-hello") {
		t.Fatalf("R13 did not fire on a pool of three: %v", got)
	}
}

// TestApplyFailuresAreADecisionList covers M18: `git apply` prints several
// error lines for one failure, and one failure is one reading.
func TestApplyFailuresAreADecisionList(t *testing.T) {
	both := traces(t, repeat(2, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
			verifies: []verifyFixture{{
				id: "alpha", status: "apply_failed",
				applyError: "error: sub/new.go: No such file or directory\n" +
					"error: sub/new.go: patch does not apply\n",
			}},
		}
	})...)
	result := mineDir(t, both, mine.Options{})
	got := ids(t, result.Buckets)
	if len(got) != 1 {
		t.Fatalf("one apply failure wrote %v", got)
	}
	if got[0] != "fp/context-lines-drift-hello" {
		t.Fatalf("the decision list picked %s, want the first message in order", got[0])
	}
}

// TestContextsStayShort covers M14: `context:` is a required STPA field a person
// reads, and a bucket spanning many runs must not put every value it ever saw on
// one YAML line.
func TestContextsStayShort(t *testing.T) {
	result := mineDir(t, traces(t, repeat(9, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{{
				id: "alpha", model: "model-a", status: "ok",
				finishReason: "length", completion: 4096 + i,
			}},
		}
	})...), mine.Options{})
	for _, bucket := range result.Buckets {
		pattern, err := bucket.Pattern("2026-09-05")
		if err != nil {
			t.Fatalf("pattern: %v", err)
		}
		if !strings.Contains(pattern.Context, "4096-4104") {
			t.Fatalf("nine observed ceilings were not folded into a range: %q", pattern.Context)
		}
		if strings.Count(pattern.Context, ",") > 4 {
			t.Fatalf("the context counts out every value it saw: %q", pattern.Context)
		}
	}
}

// TestAppendedPatternsCarryNoStaleCount covers the other half of M12: a document
// whose evidence a later pass grew must not keep a number that described the
// first pass.
func TestAppendedPatternsCarryNoStaleCount(t *testing.T) {
	for _, bucket := range mineDir(t, traces(t, repeat(3, func(i int) runFixture {
		return runFixture{
			id: runID(i), task: "hello", proposers: pool(),
			candidates: []candidateFixture{{id: "alpha", model: "model-a", status: "ok"}},
			verifies:   []verifyFixture{{id: "alpha", status: "timeout"}},
		}
	})...), mine.Options{}).Buckets {
		pattern, err := bucket.Pattern("2026-09-05")
		if err != nil {
			t.Fatalf("pattern: %v", err)
		}
		for _, forbidden := range []string{"3 runs", "on 3", "over 3"} {
			if strings.Contains(pattern.Context, forbidden) {
				t.Fatalf("the context carries a count that appending would make stale: %q", pattern.Context)
			}
		}
	}
}

// helpers.

func repeat(n int, of func(i int) runFixture) []runFixture {
	out := make([]runFixture, 0, n)
	for i := range n {
		out = append(out, of(i))
	}
	return out
}

// runID makes distinct, ordered CMoA run identifiers for a fixture.
func runID(i int) string {
	return "20260904T1200" + string(rune('0'+i/10%10)) + string(rune('0'+i%10)) + "Z-0000000" +
		string(rune('a'+i%6))
}

func f64(v float64) *float64 { return &v }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func bytesOf(t *testing.T, pattern doc.Pattern) []byte {
	t.Helper()
	raw, err := pattern.Bytes()
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	return raw
}

func write(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
