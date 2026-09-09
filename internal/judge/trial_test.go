package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// trialFixture is a two-or-three item chat suite with the files a reuse key
// reads, plus a clock the fake runner moves so a time budget is testable in
// nanoseconds rather than minutes.
type trialFixture struct {
	t     *testing.T
	root  string
	suite Suite
	// clock is what TrialOptions.Now answers, advanced by every measured call.
	clock time.Time
	// runner is the harness stand-in.
	runner *fakeTrialRunner
}

const (
	fixturePrompt   = "prompt-v1"
	fixtureCMoA     = "v0.0.0-test"
	fixtureRule     = "consensus-then-copeland"
	fixtureCallCost = 4 * time.Second
)

func newTrialFixture(t *testing.T, gold map[string]string) *trialFixture {
	t.Helper()
	root := t.TempDir()
	suiteDir := filepath.Join(root, "suite")
	var tasks []map[string]string
	for _, id := range sortedGoldIDs(gold) {
		base := filepath.Join(suiteDir, id)
		trialJSON(t, filepath.Join(base, "task.json"), map[string]any{
			"version": 3, "id": id, "face": "chat", "conversation": "conversation.json",
			"rubric": "rubric.md", "judge": map[string]any{"allow_tie": true},
		})
		trialJSON(t, filepath.Join(base, "conversation.json"),
			[]map[string]string{{"role": "user", "content": "question " + id}})
		trialWrite(t, filepath.Join(base, "rubric.md"), "be right\n")
		trialJSON(t, filepath.Join(base, "gold.json"), map[string]any{
			"schema_version": 1, "gold": gold[id], "margin_stratum": "mid",
			"method": "acyclic-majority",
		})
		for _, position := range Positions {
			trialWrite(t, filepath.Join(base, "candidates", position+".txt"),
				"answer "+position+" of "+id+"\n")
		}
		tasks = append(tasks, map[string]string{"id": id, "dir": id, "stratum": "mid"})
	}
	manifest := filepath.Join(suiteDir, "suite.json")
	trialJSON(t, manifest, map[string]any{
		"schema_version": 1, "id": "suite-trial", "face": "chat", "split": "calibration",
		"source": "a corpus", "license": "CC-BY-4.0", "tasks": tasks,
	})
	suite, err := LoadSuite(manifest)
	if err != nil {
		t.Fatalf("load suite: %v", err)
	}
	f := &trialFixture{t: t, root: root, suite: suite, clock: time.Unix(1e9, 0).UTC()}
	f.runner = &fakeTrialRunner{fixture: f, runs: filepath.Join(root, "runs")}
	return f
}

func sortedGoldIDs(gold map[string]string) []string {
	out := make([]string, 0, len(gold))
	for id := range gold {
		out = append(out, id)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (f *trialFixture) now() time.Time { return f.clock }

// judgeBlock is the judge settings both a configuration file and a written
// trace carry, so a saved run and a condition that has not run yet hash alike.
func judgeBlock(extra map[string]any) map[string]any {
	block := map[string]any{
		"model": "gpt-oss-20b", "base_url": "http://127.0.0.1:8090/v1",
		"temperature": 0, "max_tokens": 512, "output_format": "json_schema",
		"parallel": 3, "seed": 7,
	}
	for k, v := range extra {
		block[k] = v
	}
	return block
}

// config writes a harness configuration naming a judge.
func (f *trialFixture) config(name string, extra map[string]any) string {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	trialJSON(f.t, path, map[string]any{"version": 2, "judge": judgeBlock(extra)})
	return path
}

// savedRun writes a trace beside an item, the way the harness leaves one.
func (f *trialFixture) savedRun(item, runID, outcome, chosen string, opts savedRunOpts) string {
	f.t.Helper()
	dir := filepath.Join(f.suite.Dir, item, "runs", runID)
	prompt, cmoa, rule := fixturePrompt, fixtureCMoA, fixtureRule
	if opts.PromptVersion != "" {
		prompt = opts.PromptVersion
	}
	if opts.CMoAVersion != "" {
		cmoa = opts.CMoAVersion
	}
	if opts.Rule != "" {
		rule = opts.Rule
	}
	// The harness digests the decoded conversation rather than the bytes of
	// the file, so this fixture writes a digest the file could never hash to.
	// A trial that computed the conversation digest itself would reject every
	// saved run, and this is the fixture that would catch it.
	conversation := "canonical-" + item
	if opts.Conversation != "" {
		conversation = opts.Conversation
	}
	var external []map[string]string
	for i, name := range f.suite.Candidates(taskOf(f.suite, item)) {
		external = append(external, map[string]string{
			"id": Positions[i], "file": name, "sha256": digestOf(f.t, name),
		})
	}
	trialJSON(f.t, filepath.Join(dir, "run.json"), map[string]any{
		"schema_version": 1, "run_id": runID, "prompt_version": prompt,
		"cmoa_version": cmoa, "face": "chat",
		"conversation_sha256": conversation,
		"candidates_origin":   "external",
		"external_candidates": external,
	})
	block := judgeBlock(opts.Judge)
	block["allow_tie"] = true
	block["prompt_version"] = prompt
	judgeBody := map[string]any{
		"schema_version": 1, "run_id": runID, "judge": block,
		"candidates":   Positions,
		"presentation": map[string]any{"seed": 1, "nonce": "abcd"},
		"pairs": []map[string]any{{
			"pair": []string{"c1", "c2"},
			"orders": []map[string]any{
				{"first": "c1", "second": "c2", "choice": "A", "choice_candidate": "c1", "status": "ok"},
				{"first": "c2", "second": "c1", "choice": "B", "choice_candidate": "c1", "status": "ok"},
			},
			"verdict": "c1",
		}},
		"outcome":               map[string]any{"kind": outcome, "candidate_id": chosen, "reason": opts.Reason},
		"swap_consistent_pairs": 1, "invalid_output_retries": 0, "latency_ms": 20000,
	}
	// The two structured fields, written the way the harness writes them: the
	// agreement inside `consensus`, the key inside `tie_break`. A report that
	// counted `outcome.reason` instead would miss the consensus group a hash
	// parted, which is the difference the 2026-09-06 review resolved by hand.
	if opts.Consensus != "" {
		judgeBody["consensus"] = map[string]any{
			"normalisation": "trim-lower", "chosen": chosen,
			"groups": [][]string{{"c1", "c2"}, {"c3"}}, "agreement": opts.Consensus,
		}
	}
	if opts.TieBreak != "" {
		judgeBody["tie_break"] = map[string]any{
			"among": []string{"c1", "c2"}, "key": opts.TieBreak, "chosen": chosen,
		}
	}
	trialJSON(f.t, filepath.Join(dir, JudgeFile), judgeBody)
	trialJSON(f.t, filepath.Join(dir, "select.json"), map[string]any{
		"schema_version": 1, "run_id": runID, "rule": rule,
	})
	return dir
}

type savedRunOpts struct {
	Conversation  string
	PromptVersion string
	CMoAVersion   string
	Rule          string
	Reason        string
	Consensus     string
	TieBreak      string
	Judge         map[string]any
}

func taskOf(suite Suite, id string) Task {
	for _, task := range suite.Tasks {
		if task.ID == id {
			return task
		}
	}
	return Task{ID: id, Dir: id}
}

// fakeTrialRunner answers like the harness: it writes a trace and moves the
// fixture's clock, so a budget can be spent without spending any seconds.
type fakeTrialRunner struct {
	fixture *trialFixture
	runs    string
	seq     int
	// Calls records every (item, config) the trial asked for, which is how a
	// resume is checked: the assertion is not that the answers are right but
	// that the question was never asked twice.
	Calls []string
	// Answer decides the outcome per item and configuration.
	Answer func(item, config string) (outcome, chosen string)
	// Cost is the wall clock one call takes.
	Cost func(item, config string) time.Duration
	// Fail makes one call fail.
	Fail func(item, config string) error
	// Before runs at the top of a call, which is where a test checks what the
	// runner had already written to disk before it asked anything.
	Before func(item, config string)
	// Settle decides how the trace says the answer was reached: the consensus
	// agreement and the tie-break key, both structured fields.
	Settle func(item, config string) (consensus, tieBreak string)
}

func (r *fakeTrialRunner) Judge(_ context.Context, req TrialRequest,
) (Judged, time.Duration, error) {
	taskDir, config, seed := req.TaskDir, req.Config, req.Seed
	item := filepath.Base(taskDir)
	call := item + "|" + filepath.Base(config)
	if req.Binary != "" {
		call += "@" + filepath.Base(req.Binary)
	}
	r.Calls = append(r.Calls, call)
	if r.Before != nil {
		r.Before(item, config)
	}
	cost := fixtureCallCost
	if r.Cost != nil {
		cost = r.Cost(item, config)
	}
	r.fixture.clock = r.fixture.clock.Add(cost)
	if r.Fail != nil {
		if err := r.Fail(item, config); err != nil {
			return Judged{}, cost, err
		}
	}
	outcome, chosen := OutcomeSelected, "c1"
	if r.Answer != nil {
		outcome, chosen = r.Answer(item, config)
	}
	r.seq++
	runID := fmt.Sprintf("run-%03d", r.seq)
	dir := filepath.Join(r.runs, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Judged{}, cost, err
	}
	// The trace goes under the item so the reuse key reads the same corpus.
	var settled savedRunOpts
	if r.Settle != nil {
		settled.Consensus, settled.TieBreak = r.Settle(item, config)
	}
	saved := r.fixture.savedRun(item, runID, outcome, chosen, settled)
	judged, err := ReadJudged(saved)
	if err != nil {
		return Judged{}, cost, err
	}
	judged.Seed = seed
	return judged, cost, nil
}

func digestOf(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func trialWrite(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func trialJSON(t *testing.T, name string, value any) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	trialWrite(t, name, string(body)+"\n")
}

// card writes an experiment card and the manifests it names.
func (f *trialFixture) card(id string, items []string, mutate func(*map[string]any)) string {
	f.t.Helper()
	var entries []map[string]any
	for _, item := range items {
		entries = append(entries, map[string]any{
			"id": item, "strata": map[string]string{"category": "writing", "language": "en"},
			"reason": "typical",
		})
	}
	manifest := filepath.Join(f.root, "set-d.json")
	trialJSON(f.t, manifest, map[string]any{
		"schema_version": 1, "set": "D", "items": entries,
	})
	body := map[string]any{
		"schema_version": 1, "id": id,
		"hypothesis": "the smaller budget does not cost quality",
		"change":     "judge max_tokens 512 -> 256",
		"stage":      "A", "suite": f.suite.Dir + "/suite.json",
		"manifests": []string{manifest},
		"base": map[string]any{
			"id": "base-1", "config": f.config("base.json", nil),
		},
		"candidate": map[string]any{
			"id": "cand-1", "config": f.config("cand.json", nil),
		},
		"reuse":          map[string]any{"kind": "none"},
		"seed":           1,
		"judge_seed":     7,
		"rules":          map[string]any{"kind": "speed"},
		"budget_seconds": 600,
	}
	if mutate != nil {
		mutate(&body)
	}
	path := filepath.Join(f.root, "card.json")
	trialJSON(f.t, path, body)
	return path
}

func (f *trialFixture) options(cardPath, out string) TrialOptions {
	f.t.Helper()
	card, err := LoadTrialCard(cardPath)
	if err != nil {
		f.t.Fatalf("load card: %v", err)
	}
	var manifests []TrialManifest
	for _, name := range card.Manifests {
		manifest, err := LoadTrialManifest(card.Path(name))
		if err != nil {
			f.t.Fatalf("load manifest: %v", err)
		}
		manifests = append(manifests, manifest)
	}
	return TrialOptions{
		Card: card, Suite: f.suite, Manifests: manifests, Runner: f.runner,
		Out: out, Vault: f.root, Now: f.now,
	}
}

func TestTrialManifestValidation(t *testing.T) {
	dir := t.TempDir()
	for _, row := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"a good manifest", map[string]any{
			"schema_version": 1, "set": "D",
			"items":   []map[string]any{{"id": "a", "strata": map[string]string{"category": "math"}}},
			"weights": map[string]float64{"math": 0.5},
		}, ""},
		{"weights are optional", map[string]any{
			"schema_version": 1, "set": "R",
			"items": []map[string]any{{"id": "a"}},
		}, ""},
		{"another schema version", map[string]any{
			"schema_version": 2, "set": "D", "items": []map[string]any{{"id": "a"}},
		}, "schema version 2"},
		{"a set that is not a role", map[string]any{
			"schema_version": 1, "set": "X", "items": []map[string]any{{"id": "a"}},
		}, "which is not D, R or H"},
		{"no items", map[string]any{
			"schema_version": 1, "set": "D", "items": []map[string]any{},
		}, "holds no items"},
		{"an item named twice", map[string]any{
			"schema_version": 1, "set": "D",
			"items": []map[string]any{{"id": "a"}, {"id": "a"}},
		}, "names item a twice"},
		{"an item with no id", map[string]any{
			"schema_version": 1, "set": "D", "items": []map[string]any{{"reason": "why"}},
		}, "has no id"},
		{"a negative share", map[string]any{
			"schema_version": 1, "set": "D", "items": []map[string]any{{"id": "a"}},
			"weights": map[string]float64{"math": -1},
		}, "weights stratum"},
		{"an item in two weighted buckets", map[string]any{
			"schema_version": 1, "set": "D",
			"items": []map[string]any{{"id": "a", "strata": map[string]string{
				"category": "math", "language": "ja"}}},
			"weights": map[string]float64{"math": 0.5, "ja": 0.5},
		}, "carries 2 weighted strata"},
	} {
		t.Run(row.name, func(t *testing.T) {
			name := filepath.Join(dir, strings.ReplaceAll(row.name, " ", "-")+".json")
			trialJSON(t, name, row.body)
			_, err := LoadTrialManifest(name)
			switch {
			case row.want == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case row.want == "":
			case err == nil:
				t.Fatalf("want an error mentioning %q, got none", row.want)
			case !strings.Contains(err.Error(), row.want):
				t.Fatalf("want an error mentioning %q, got %v", row.want, err)
			}
		})
	}
}

func TestTrialCardValidation(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1"})
	dir := t.TempDir()
	good := func() map[string]any {
		return map[string]any{
			"schema_version": 1, "id": "e1", "hypothesis": "h", "change": "c", "stage": "A",
			"suite": f.suite.Dir + "/suite.json", "manifests": []string{"m.json"},
			"base":      map[string]any{"id": "b", "config": "base.json"},
			"candidate": map[string]any{"id": "n", "config": "cand.json"},
			"rules":     map[string]any{"kind": "speed"},
		}
	}
	for _, row := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"a good card", func(map[string]any) {}, ""},
		{"no hypothesis", func(m map[string]any) { delete(m, "hypothesis") }, "states no hypothesis"},
		{"a stage that is not a stage", func(m map[string]any) { m["stage"] = "D" }, "not A, B or C"},
		{"one condition named twice", func(m map[string]any) {
			m["candidate"] = map[string]any{"id": "b", "config": "cand.json"}
		}, "calls both conditions"},
		{"a rule with no purpose", func(m map[string]any) {
			m["rules"] = map[string]any{"kind": "vibes"}
		}, "has rule kind"},
		{"reuse with no source", func(m map[string]any) {
			m["reuse"] = map[string]any{"kind": "saved_runs"}
		}, "names no source"},
	} {
		t.Run(row.name, func(t *testing.T) {
			body := good()
			row.mutate(body)
			name := filepath.Join(dir, strings.ReplaceAll(row.name, " ", "-")+".json")
			trialJSON(t, name, body)
			card, err := LoadTrialCard(name)
			switch {
			case row.want == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case row.want == "":
				if card.BudgetSeconds != BudgetA {
					t.Fatalf("stage A defaults to %d seconds, got %d", BudgetA, card.BudgetSeconds)
				}
				if card.MinItemsPerCategory != DefaultMinItemsPerCategory {
					t.Fatalf("want the default category floor, got %d", card.MinItemsPerCategory)
				}
			case err == nil:
				t.Fatalf("want an error mentioning %q, got none", row.want)
			case !strings.Contains(err.Error(), row.want):
				t.Fatalf("want an error mentioning %q, got %v", row.want, err)
			}
		})
	}
}

func TestTrialPlanIsFixed(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c2", "i3": "c3"})
	card := f.card("e1", []string{"i1", "i2", "i3"}, nil)
	opts := f.options(card, filepath.Join(t.TempDir(), "out"))
	var got []string
	for _, step := range opts.Plan() {
		got = append(got, step.Item+":"+step.Condition)
	}
	want := "i1:candidate i1:base i2:candidate i2:base i3:candidate i3:base"
	if strings.Join(got, " ") != want {
		t.Fatalf("plan\n got %s\nwant %s", strings.Join(got, " "), want)
	}

	// The alternating order is the one for a runtime change: blocks, with the
	// leading condition swapping, so a whole condition is never measured in
	// one stretch of the afternoon and the other in another.
	alternating := f.card("e2", []string{"i1", "i2", "i3"}, func(m *map[string]any) {
		(*m)["alternating_blocks"] = true
		(*m)["block_size"] = 2
	})
	got = nil
	for _, step := range f.options(alternating, filepath.Join(t.TempDir(), "out")).Plan() {
		got = append(got, step.Item+":"+step.Condition)
	}
	want = "i1:candidate i2:candidate i1:base i2:base i3:base i3:candidate"
	if strings.Join(got, " ") != want {
		t.Fatalf("alternating plan\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestTrialBudgetLeavesTheIncompleteItemsVisible(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1"})
	card := f.card("budget", []string{"i1", "i2", "i3"}, func(m *map[string]any) {
		// Two calls of four seconds fit; the third item's first call does not.
		(*m)["budget_seconds"] = 5
	})
	out := filepath.Join(t.TempDir(), "out")
	result, err := Trial(context.Background(), f.options(card, out))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	report := result.Report
	if report.Planned != 3 || report.Completed != 1 || report.Interrupted != 2 {
		t.Fatalf("want 3 planned, 1 completed, 2 interrupted; got %d/%d/%d",
			report.Planned, report.Completed, report.Interrupted)
	}
	if report.StopReason != StopOutOfBudget {
		t.Fatalf("want stop reason %s, got %s", StopOutOfBudget, report.StopReason)
	}
	if report.StopReasonLabel != "時間・資源切れ" {
		t.Fatalf("the Japanese label travels with the value, got %q", report.StopReasonLabel)
	}
	if report.Suggested.Value != DecisionInconclusive {
		t.Fatalf("a run its own clock stopped decides nothing, got %q", report.Suggested.Value)
	}
	// The unfinished items are in the report by name rather than absent from
	// it: a comparison over whatever finished is a comparison over the fast
	// ones, and the only defence is being able to see which were dropped.
	incomplete := map[string]bool{}
	for _, item := range report.Items {
		if !item.Complete {
			incomplete[item.Item] = true
		}
	}
	if !incomplete["i2"] || !incomplete["i3"] {
		t.Fatalf("want i2 and i3 shown as incomplete, got %v", incomplete)
	}
	if len(report.Notes) == 0 || !strings.Contains(report.Notes[0], "not completed") {
		t.Fatalf("want a note saying items were not completed, got %v", report.Notes)
	}
}

func TestTrialResumeNeverAsksTwice(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1"})
	out := filepath.Join(t.TempDir(), "out")
	first := f.card("resume", []string{"i1", "i2", "i3"}, func(m *map[string]any) {
		(*m)["budget_seconds"] = 5
	})
	if _, err := Trial(context.Background(), f.options(first, out)); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	done := len(f.runner.Calls)
	if done != 2 {
		t.Fatalf("want the first pass to spend two calls, got %d", done)
	}

	// Without --resume the second pass refuses rather than overwriting a
	// journal that records work somebody paid for.
	opts := f.options(first, out)
	if _, err := Trial(context.Background(), opts); err == nil ||
		!strings.Contains(err.Error(), "--resume") {
		t.Fatalf("want a refusal naming --resume, got %v", err)
	}

	second := f.card("resume", []string{"i1", "i2", "i3"}, func(m *map[string]any) {
		(*m)["budget_seconds"] = 600
	})
	opts = f.options(second, out)
	opts.Resume = true
	result, err := Trial(context.Background(), opts)
	if err != nil {
		t.Fatalf("resumed pass: %v", err)
	}
	for _, call := range f.runner.Calls[done:] {
		if strings.HasPrefix(call, "i1|") {
			t.Fatalf("the resumed pass asked about i1 again: %v", f.runner.Calls)
		}
	}
	if result.Report.Completed != 3 || result.Report.Interrupted != 0 {
		t.Fatalf("want all three completed after the resume, got %d completed and %d interrupted",
			result.Report.Completed, result.Report.Interrupted)
	}
	if result.Report.SkippedStep != 2 {
		t.Fatalf("want two skipped steps recorded, got %d", result.Report.SkippedStep)
	}
	records, err := ReadTrialRecords(filepath.Join(out, TrialResultsFile))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(records) != 6 {
		t.Fatalf("want six records in the journal, got %d", len(records))
	}
}

func TestTrialResumeRefusesChangedMeasurementInputs(t *testing.T) {
	for _, row := range []struct {
		name       string
		withLabels bool
		mutate     func(*trialFixture, *TrialOptions)
	}{
		{"suite input", false, func(f *trialFixture, _ *TrialOptions) {
			trialWrite(f.t, filepath.Join(f.suite.Dir, "suite.json"), "{}\n")
		}},
		{"task declaration", false, func(f *trialFixture, _ *TrialOptions) {
			trialWrite(f.t, filepath.Join(f.suite.TaskDir(taskOf(f.suite, "i1")), "task.json"), "{}\n")
		}},
		{"configuration bytes", false, func(f *trialFixture, opts *TrialOptions) {
			trialJSON(f.t, opts.Card.Candidate.Config, map[string]any{"version": 2, "judge": judgeBlock(map[string]any{"max_tokens": 256})})
		}},
		{"candidate bytes", false, func(f *trialFixture, opts *TrialOptions) {
			trialWrite(f.t, filepath.Join(f.suite.TaskDir(taskOf(f.suite, "i1")), "candidates", "c1.txt"), "changed answer\n")
		}},
		{"conversation bytes", false, func(f *trialFixture, opts *TrialOptions) {
			trialWrite(f.t, filepath.Join(f.suite.TaskDir(taskOf(f.suite, "i1")), "conversation.json"), "[]\n")
		}},
		{"rubric bytes", false, func(f *trialFixture, opts *TrialOptions) {
			trialWrite(f.t, filepath.Join(f.suite.TaskDir(taskOf(f.suite, "i1")), "rubric.md"), "changed rubric\n")
		}},
		{"gold bytes", false, func(f *trialFixture, _ *TrialOptions) {
			trialJSON(f.t, filepath.Join(f.suite.TaskDir(taskOf(f.suite, "i1")), "gold.json"), map[string]any{"schema_version": 1, "gold": "c2"})
		}},
		{"label bytes", true, func(f *trialFixture, _ *TrialOptions) {
			trialWrite(f.t, filepath.Join(f.root, "labels.jsonl"), "{\"schema_version\":1,\"item\":\"i1\",\"labeler\":\"owner\",\"choice\":\"c2\"}\n")
		}},
		{"plan", false, func(_ *trialFixture, opts *TrialOptions) {
			opts.Manifests[0].Items[0], opts.Manifests[0].Items[1] = opts.Manifests[0].Items[1], opts.Manifests[0].Items[0]
		}},
		{"condition", false, func(_ *trialFixture, opts *TrialOptions) { opts.Card.Candidate.ID = "cand-2" }},
		{"seed", false, func(_ *trialFixture, opts *TrialOptions) { opts.Card.Seed = 2 }},
		{"build", false, func(_ *trialFixture, opts *TrialOptions) { opts.Card.Candidate.CMoA = "other-cmoa" }},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1"})
			if row.withLabels {
				trialWrite(t, filepath.Join(f.root, "labels.jsonl"), "{\"schema_version\":1,\"item\":\"i1\",\"labeler\":\"owner\",\"choice\":\"c1\"}\n")
			}
			card := f.card("resume-inputs", []string{"i1", "i2"}, func(m *map[string]any) {
				(*m)["budget_seconds"] = 5
				if row.withLabels {
					(*m)["labels"] = []string{filepath.Join(f.root, "labels.jsonl")}
				}
			})
			out := filepath.Join(t.TempDir(), "out")
			opts := f.options(card, out)
			if _, err := Trial(context.Background(), opts); err != nil {
				t.Fatalf("first pass: %v", err)
			}
			calls := len(f.runner.Calls)
			row.mutate(f, &opts)
			opts.Resume = true
			opts.Card.BudgetSeconds = 600 // execution budget may be extended.
			if _, err := Trial(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "different trial inputs") {
				t.Fatalf("want changed-input refusal, got %v", err)
			}
			if len(f.runner.Calls) != calls {
				t.Fatalf("changed resume asked a new question: %v", f.runner.Calls)
			}
		})
	}
}

func TestTrialResumeWritesIdentityBeforeTheFirstInference(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1"})
	card := f.card("resume-identity-first", []string{"i1"}, nil)
	out := filepath.Join(t.TempDir(), "out")
	f.runner.Before = func(_, _ string) {
		if _, err := os.Stat(filepath.Join(out, trialResumeFile)); err != nil {
			t.Fatalf("resume identity is absent before inference: %v", err)
		}
	}
	if _, err := Trial(context.Background(), f.options(card, out)); err != nil {
		t.Fatalf("trial: %v", err)
	}
}

func TestTrialResumeRefusesJournalWithoutIdentity(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1"})
	card := f.card("resume-missing-identity", []string{"i1", "i2"}, func(m *map[string]any) { (*m)["budget_seconds"] = 5 })
	out := filepath.Join(t.TempDir(), "out")
	opts := f.options(card, out)
	if _, err := Trial(context.Background(), opts); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if err := os.Remove(filepath.Join(out, trialResumeFile)); err != nil {
		t.Fatalf("remove identity: %v", err)
	}
	calls := len(f.runner.Calls)
	opts.Resume = true
	if _, err := Trial(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "no resume identity") {
		t.Fatalf("want missing-identity refusal, got %v", err)
	}
	if len(f.runner.Calls) != calls {
		t.Fatalf("missing identity asked a new question: %v", f.runner.Calls)
	}
}

func TestTrialResumeFingerprintHashesDefaultBinary(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1"})
	card := f.card("resume-binary", []string{"i1"}, nil)
	opts := f.options(card, filepath.Join(t.TempDir(), "out"))
	binary := filepath.Join(f.root, "bin", "cmoa")
	trialWrite(t, binary, "first build\n")
	opts.Runner = CMoATrialRunner{Binary: binary}
	first, err := resumeFingerprint(opts, opts.Plan())
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	trialWrite(t, binary, "second build\n")
	second, err := resumeFingerprint(opts, opts.Plan())
	if err != nil {
		t.Fatalf("second fingerprint: %v", err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("changing the default CMoA binary did not change the resume fingerprint")
	}
}

func TestTrialResumeRefusesChangedCMoABinary(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1"})
	card := f.card("resume-real-binary", []string{"i1"}, nil)
	out := filepath.Join(t.TempDir(), "out")
	opts := f.options(card, out)
	binary := filepath.Join(f.root, "bin", "cmoa")
	trialWrite(t, binary, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatalf("make fake binary executable: %v", err)
	}
	opts.Runner = CMoATrialRunner{Binary: binary}
	plan := opts.Plan()
	if err := ensureTrialResumeState(opts, plan, false); err != nil {
		t.Fatalf("write resume identity: %v", err)
	}
	record := TrialRecord{Item: "i1", Set: SetD, Condition: ConditionCandidate}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal journal record: %v", err)
	}
	trialWrite(t, filepath.Join(out, TrialResultsFile), string(body)+"\n")
	trialWrite(t, binary, "#!/bin/sh\necho changed\n")
	opts.Resume = true
	if _, err := Trial(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "different trial inputs") {
		t.Fatalf("want changed-binary refusal, got %v", err)
	}
}

func TestTrialReusesOnlyAMatchingKey(t *testing.T) {
	for _, row := range []struct {
		name     string
		opts     savedRunOpts
		reused   bool
		mismatch string
	}{
		{"the same question", savedRunOpts{}, true, ""},
		{"another conversation", savedRunOpts{Conversation: "canonical-somethingelse"}, false, "conversation"},
		{"another prompt version", savedRunOpts{PromptVersion: "prompt-v2"}, false, "prompt_version"},
		{"another harness build", savedRunOpts{CMoAVersion: "v0.0.0-other"}, false, "cmoa_version"},
		{"another selection rule", savedRunOpts{Rule: "judge-pairwise"}, false, "selection_rule"},
		{"another token budget", savedRunOpts{
			Judge: map[string]any{"max_tokens": 256},
		}, false, "judge"},
		{"another sampling seed", savedRunOpts{
			Judge: map[string]any{"seed": 11},
		}, false, "judge_seed"},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1"})
			for _, item := range []string{"i1", "i2"} {
				f.savedRun(item, "saved-"+item, OutcomeSelected, "c1", row.opts)
			}
			card := f.card("reuse", []string{"i1", "i2"}, func(m *map[string]any) {
				(*m)["reuse"] = map[string]any{"kind": "saved_runs", "source": f.suite.Dir}
			})
			out := filepath.Join(t.TempDir(), "out")
			result, err := Trial(context.Background(), f.options(card, out))
			if err != nil {
				t.Fatalf("trial: %v", err)
			}
			base := 0
			for _, call := range f.runner.Calls {
				if strings.HasSuffix(call, "|base.json") {
					base++
				}
			}
			if row.reused {
				if base != 0 {
					t.Fatalf("a matching key needs no base call, got %d", base)
				}
				if result.Report.Reuse.Reused != 2 {
					t.Fatalf("want two reused steps, got %d", result.Report.Reuse.Reused)
				}
				// A reused base brings no wall time with it, so there is no
				// speed comparison to report. That refusal is the point.
				if result.Report.Speed.Comparable {
					t.Fatalf("a reused base is not a measured base; speed must not be comparable")
				}
				return
			}
			if base != 2 {
				t.Fatalf("a mismatched key forces the base to be measured; got %d base call(s)", base)
			}
			if result.Report.Reuse.Reused != 0 {
				t.Fatalf("want nothing reused, got %d", result.Report.Reuse.Reused)
			}
			found := false
			for _, field := range result.Report.Reuse.KeyMismatch {
				if field == row.mismatch {
					found = true
				}
			}
			if !found {
				t.Fatalf("want the mismatch to name %q, got %v",
					row.mismatch, result.Report.Reuse.KeyMismatch)
			}
			// Both conditions measured in one session: now there is a speed
			// comparison, and it is named G_judge rather than G_T.
			if !result.Report.Speed.Comparable || result.Report.Speed.Basis != "judge" {
				t.Fatalf("want a comparable judge-stage speed, got %+v", result.Report.Speed)
			}
		})
	}
}

func TestTrialReportArithmetic(t *testing.T) {
	// Four items, people chose c1 on all of them. The candidate condition
	// answers c1 on three and c2 on one; the base answers c1 on two, c2 on
	// one, and reaches no candidate on one — a defined failure that scores
	// zero rather than being excused.
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1", "i4": "c1"})
	answers := map[string][2]string{
		"i1": {"c1", "c1"},
		"i2": {"c1", "c2"},
		"i3": {"c2", "c1"},
		"i4": {"", "c1"},
	}
	f.runner.Answer = func(item, config string) (string, string) {
		pair := answers[item]
		which := 1
		if strings.Contains(filepath.Base(config), "base") {
			which = 0
		}
		if pair[which] == "" {
			return OutcomeNoCandidate, ""
		}
		return OutcomeSelected, pair[which]
	}
	f.runner.Cost = func(_, config string) time.Duration {
		if strings.Contains(filepath.Base(config), "base") {
			return 10 * time.Second
		}
		return 8 * time.Second
	}
	// The floor on evaluable items is lowered to what this fixture has: the
	// arithmetic under test is the arithmetic, and the floor is tested where
	// it belongs, in TestTrialQualityFloor.
	card := f.card("arith", []string{"i1", "i2", "i3", "i4"}, func(m *map[string]any) {
		(*m)["min_evaluable_items"] = 4
	})
	out := filepath.Join(t.TempDir(), "out")
	result, err := Trial(context.Background(), f.options(card, out))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if q.Metric != MetricSelectionTop1 || !q.Measured {
		t.Fatalf("want a measured selection top-1 metric, got %+v", q)
	}
	if q.Evaluable != 4 {
		t.Fatalf("want four evaluable items, got %d", q.Evaluable)
	}
	if q.QBase != 0.5 || q.QNew != 0.75 {
		t.Fatalf("want q(base) 0.5 and q(new) 0.75, got %v and %v", q.QBase, q.QNew)
	}
	if q.DeltaPoints != 25 {
		t.Fatalf("want ΔQ +25 points, got %v", q.DeltaPoints)
	}
	if q.NewlyWrong != 1 || q.NewlyRight != 2 {
		t.Fatalf("want 1 newly wrong and 2 newly right, got %d and %d", q.NewlyWrong, q.NewlyRight)
	}
	speed := result.Report.Speed
	if !speed.Comparable || speed.GJudge == nil || *speed.GJudge != 0.2 {
		t.Fatalf("want G_judge 0.2 from 8 s against 10 s, got %+v", speed)
	}
	if got := result.Report.Conditions[ConditionBase].Coverage; got != 0.75 {
		t.Fatalf("want base coverage 0.75, got %v", got)
	}
	if got := len(result.Report.ChangedSelections); got != 3 {
		t.Fatalf("want three changed selections, got %d", got)
	}
	if result.Report.Inference.Runs != 8 {
		t.Fatalf("want eight new runs, got %d", result.Report.Inference.Runs)
	}
	// The report and its summary are written, and neither claims more than a
	// point estimate.
	if err := result.Write(out); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(out, TrialSummaryFile))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	summary := string(body)
	for _, want := range []string{"ΔQ +25.0 pt", "G_judge 0.200", "development reading",
		"`decision` is written", "Suggested, not decided"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("the summary does not say %q:\n%s", want, summary)
		}
	}
	for _, forbidden := range []string{"significant", "confirms", "non-inferior to", "proves"} {
		if strings.Contains(strings.ToLower(summary), forbidden) {
			t.Fatalf("the summary claims %q, which a four-item trial cannot:\n%s", forbidden, summary)
		}
	}
}

func TestTrialQualityNeedsAHumanPosition(t *testing.T) {
	// Every item's gold is `tie`, which is not a position a selector can hit.
	f := newTrialFixture(t, map[string]string{"i1": "tie", "i2": "tie"})
	card := f.card("nolabels", []string{"i1", "i2"}, nil)
	out := filepath.Join(t.TempDir(), "out")
	result, err := Trial(context.Background(), f.options(card, out))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	q := result.Report.Quality
	if q.Measured {
		t.Fatal("no item names a position; nothing here measured quality")
	}
	if q.Metric != MetricPairJudge {
		t.Fatalf("want the pair-judge naming, got %q", q.Metric)
	}
	if q.NoReference != 2 {
		t.Fatalf("want two items with no reference, got %d", q.NoReference)
	}
	if result.Report.Suggested.Value != DecisionInconclusive {
		t.Fatalf("a run that measured no quality suggests inconclusive, got %q",
			result.Report.Suggested.Value)
	}
}

func TestTrialEarlyCutIsLabelledAHeuristic(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1", "i4": "c1"})
	// The candidate loses two the base had and rescues none.
	f.runner.Answer = func(item, config string) (string, string) {
		if strings.Contains(filepath.Base(config), "base") {
			return OutcomeSelected, "c1"
		}
		if item == "i1" || item == "i2" {
			return OutcomeSelected, "c2"
		}
		return OutcomeSelected, "c1"
	}
	card := f.card("cut", []string{"i1", "i2", "i3", "i4"}, nil)
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	if result.Report.Suggested.Value != DecisionDrop {
		t.Fatalf("want a suggested drop, got %q", result.Report.Suggested.Value)
	}
	if !strings.Contains(result.Report.Suggested.Heuristic, "早い見切り") {
		t.Fatalf("the two-item rule has to name itself a heuristic, got %q",
			result.Report.Suggested.Heuristic)
	}
	if !strings.Contains(result.Report.Suggested.Heuristic, "not a demonstration") {
		t.Fatalf("the heuristic has to disclaim being a demonstration, got %q",
			result.Report.Suggested.Heuristic)
	}
}

func TestTrialWeightsAStratumWithEnoughItems(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1", "i4": "c1"})
	manifest := filepath.Join(f.root, "weighted.json")
	trialJSON(t, manifest, map[string]any{
		"schema_version": 1, "set": "D",
		"items": []map[string]any{
			{"id": "i1", "strata": map[string]string{"category": "math"}},
			{"id": "i2", "strata": map[string]string{"category": "math"}},
			{"id": "i3", "strata": map[string]string{"category": "math"}},
			{"id": "i4", "strata": map[string]string{"category": "writing"}},
		},
		"weights": map[string]float64{"math": 0.5, "writing": 0.5},
	})
	card := f.card("weighted", []string{"i1"}, func(m *map[string]any) {
		(*m)["manifests"] = []string{manifest}
		(*m)["min_items_per_category"] = 3
		(*m)["min_evaluable_items"] = 4
	})
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	weighted := result.Report.Quality.Weighted
	if weighted == nil {
		t.Fatal("a manifest with weights gets a weighted reading")
	}
	if len(weighted.Unevaluated) != 1 || weighted.Unevaluated[0] != "writing" {
		t.Fatalf("a stratum of one item is 未評価, got %v", weighted.Unevaluated)
	}
	if !weighted.Strata["math"].Enough {
		t.Fatal("three items reach the floor of three")
	}
}

func TestReuseKeyDiffNamesTheField(t *testing.T) {
	a := ReuseKey{Task: "t", Candidates: "c", PromptVersion: "p", Seed: 1, JudgeSeed: 7}
	b := a
	if got := a.Diff(b); len(got) != 0 {
		t.Fatalf("the same key differs in %v", got)
	}
	b.Candidates = "other"
	b.JudgeSeed = 9
	got := a.Diff(b)
	if len(got) != 2 || got[0] != "candidates" || got[1] != "judge_seed" {
		t.Fatalf("want candidates and judge_seed, got %v", got)
	}
	if a.Digest() == b.Digest() {
		t.Fatal("two different keys hash alike")
	}
}

// set writes one manifest of a named set beside the fixture's root.
func (f *trialFixture) set(file, kind string, items []string) string {
	f.t.Helper()
	var entries []map[string]any
	for i, item := range items {
		entries = append(entries, map[string]any{
			"id": item, "order": i + 1,
			"strata": map[string]string{"category": "writing", "language": "en"},
			"reason": "typical",
		})
	}
	path := filepath.Join(f.root, file)
	trialJSON(f.t, path, map[string]any{
		"schema_version": 1, "set": kind, "items": entries,
	})
	return path
}

// TestTrialManifestOrderIsCheckedAgainstFileOrder: the order a manifest states
// and the order it is in have to be the same statement.
func TestTrialManifestOrderIsCheckedAgainstFileOrder(t *testing.T) {
	dir := t.TempDir()
	item := func(id string, order int) map[string]any {
		out := map[string]any{"id": id, "strata": map[string]string{"category": "math"}}
		if order != 0 {
			out["order"] = order
		}
		return out
	}
	for _, row := range []struct {
		name  string
		items []map[string]any
		want  string
	}{
		{"numbered in file order", []map[string]any{item("a", 1), item("b", 2)}, ""},
		{"numbered by nobody", []map[string]any{item("a", 0), item("b", 0)}, ""},
		{"numbered out of order", []map[string]any{item("a", 2), item("b", 1)},
			"calls it order 2"},
		{"numbered halfway", []map[string]any{item("a", 1), item("b", 0)},
			"calls it order 0"},
		{"numbered from the second", []map[string]any{item("a", 0), item("b", 2)},
			"leaves the first item unnumbered"},
	} {
		t.Run(row.name, func(t *testing.T) {
			name := filepath.Join(dir, strings.ReplaceAll(row.name, " ", "-")+".json")
			trialJSON(t, name, map[string]any{
				"schema_version": 1, "set": "D", "items": row.items,
			})
			_, err := LoadTrialManifest(name)
			switch {
			case row.want == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case row.want == "":
			case err == nil:
				t.Fatalf("want an error mentioning %q, got none", row.want)
			case !strings.Contains(err.Error(), row.want):
				t.Fatalf("want an error mentioning %q, got %v", row.want, err)
			}
		})
	}
}

// TestCommittedChatSetsCarryTheirExecutionOrder reads the sets this repository
// ships and checks the property the order exists for: that a prefix is a
// representative set rather than whatever the ids happened to be.
func TestCommittedChatSetsCarryTheirExecutionOrder(t *testing.T) {
	d, err := LoadTrialManifest(filepath.Join("..", "..", "examples", "suite-chat", "sets", "D.json"))
	if err != nil {
		t.Fatalf("load D: %v", err)
	}
	if len(d.Items) != 40 {
		t.Fatalf("D holds %d items, want 40", len(d.Items))
	}
	// One item of every category in the first eight, three of every category
	// in the first twenty-four, and all three length bins by the fourth item.
	for _, row := range []struct {
		prefix     int
		categories int
		each       int
	}{{4, 4, 1}, {8, 8, 1}, {24, 8, 3}} {
		counts := map[string]int{}
		for _, item := range d.Items[:row.prefix] {
			counts[item.Strata["category"]]++
		}
		if len(counts) != row.categories {
			t.Errorf("the first %d items cover %d categories, want %d: %v",
				row.prefix, len(counts), row.categories, counts)
		}
		for category, n := range counts {
			if n != row.each {
				t.Errorf("the first %d items hold %d of %s, want %d",
					row.prefix, n, category, row.each)
			}
		}
	}
	bins := map[string]int{}
	for _, item := range d.Items[:4] {
		bins[item.Strata["length_bin"]]++
	}
	if len(bins) != 3 {
		t.Errorf("the first four items cover %d length bins, want all three: %v", len(bins), bins)
	}

	r, err := LoadTrialManifest(filepath.Join("..", "..", "examples", "suite-chat", "sets", "R.json"))
	if err != nil {
		t.Fatalf("load R: %v", err)
	}
	// The stage A take, interleaved: a clock that stops the run early still
	// leaves a known-failure item in the comparison.
	card := TrialCard{Take: map[string]int{SetD: 4, SetR: 2}}
	var got []string
	for _, step := range TrialPlan(card, []TrialManifest{d, r}) {
		if step.Condition == ConditionCandidate {
			got = append(got, step.Set)
		}
	}
	want := "D R D D R D"
	if strings.Join(got, " ") != want {
		t.Fatalf("stage A order\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

// TestTrialTakeIsAPrefixAndTheCapCoversBothSets: the take runs the first k of
// each set, and the ceiling is over D and R together.
func TestTrialTakeIsAPrefixAndTheCapCoversBothSets(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1", "i4": "c1"})
	dev := f.set("d.json", "D", []string{"i1", "i2", "i3"})
	fail := f.set("r.json", "R", []string{"i4"})
	load := func(card string) (TrialCard, []TrialManifest) {
		t.Helper()
		loaded, err := LoadTrialCard(card)
		if err != nil {
			t.Fatalf("load card: %v", err)
		}
		var manifests []TrialManifest
		for _, name := range loaded.Manifests {
			manifest, err := LoadTrialManifest(loaded.Path(name))
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			manifests = append(manifests, manifest)
		}
		return loaded, manifests
	}

	card, manifests := load(f.card("take", []string{"i1"}, func(m *map[string]any) {
		(*m)["manifests"] = []string{dev, fail}
		(*m)["take"] = map[string]int{"D": 2, "R": 1}
	}))
	if err := CheckPlan(card, manifests); err != nil {
		t.Fatalf("three items inside a cap of eight: %v", err)
	}
	taken := Taken(card, manifests)
	if len(taken[0].Items) != 2 || len(taken[1].Items) != 1 {
		t.Fatalf("take is a prefix, got %d and %d", len(taken[0].Items), len(taken[1].Items))
	}
	if taken[0].Items[0].ID != "i1" || taken[0].Items[1].ID != "i2" {
		t.Fatalf("the prefix is the file's first items, got %v", taken[0].Items)
	}

	// The cap is over the two sets together, so three plus two is over a cap
	// of four however it is split.
	card, manifests = load(f.card("cap", []string{"i1"}, func(m *map[string]any) {
		(*m)["manifests"] = []string{dev, fail}
		(*m)["max_items"] = 3
	}))
	err := CheckPlan(card, manifests)
	if err == nil || !strings.Contains(err.Error(), "D and R together") {
		t.Fatalf("want a refusal naming the combined cap, got %v", err)
	}

	// And the budget refuses on its own arithmetic: four items, both
	// conditions, at 100 s each is 800 s of a 600 s box.
	card, manifests = load(f.card("budgeted", []string{"i1"}, func(m *map[string]any) {
		(*m)["manifests"] = []string{dev, fail}
		(*m)["planning"] = map[string]any{"item_seconds": 100, "overhead_seconds": 60}
	}))
	err = CheckPlan(card, manifests)
	if err == nil || !strings.Contains(err.Error(), "affords 2") {
		t.Fatalf("want a refusal naming what the budget affords, got %v", err)
	}
	// A card that says it expects the clock is not refused; it is stopped.
	card, manifests = load(f.card("cut", []string{"i1"}, func(m *map[string]any) {
		(*m)["manifests"] = []string{dev, fail}
		(*m)["planning"] = map[string]any{
			"item_seconds": 100, "overhead_seconds": 60, "accept_cut": true,
		}
	}))
	if err := CheckPlan(card, manifests); err != nil {
		t.Fatalf("accept_cut runs the fixed order anyway: %v", err)
	}
	if got := Estimate(card, manifests).Affordable; got != 2 {
		t.Fatalf("the estimate affords %d items, want 2", got)
	}
}

// TestTrialWritesThePlanBeforeTheFirstCall: the order is on disk, with a
// timestamp, before anything is measured.
func TestTrialWritesThePlanBeforeTheFirstCall(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1"})
	out := filepath.Join(t.TempDir(), "out")
	var seen TrialReport
	f.runner.Before = func(string, string) {
		if seen.Recorded != "" {
			return
		}
		body, err := os.ReadFile(filepath.Join(out, TrialReportFile))
		if err != nil {
			t.Errorf("the plan is written before the first call: %v", err)
			return
		}
		if err := json.Unmarshal(body, &seen); err != nil {
			t.Errorf("unmarshal plan: %v", err)
		}
	}
	card := f.card("prereg", []string{"i1", "i2"}, nil)
	result, err := Trial(context.Background(), f.options(card, out))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	if seen.Recorded != RecordedPlan {
		t.Fatalf("the file before the first call is %q, want %q", seen.Recorded, RecordedPlan)
	}
	if len(seen.Plan.Items) != 2 || seen.Plan.Items[0].Item != "i1" || seen.Plan.Items[0].Position != 1 {
		t.Fatalf("the plan holds the fixed order, got %+v", seen.Plan.Items)
	}
	if seen.Plan.Sets["D"] != 2 {
		t.Fatalf("the plan counts the composition, got %v", seen.Plan.Sets)
	}
	if result.Report.Recorded != RecordedResult {
		t.Fatalf("the finished report is %q, want %q", result.Report.Recorded, RecordedResult)
	}
	if len(result.Report.Plan.Items) != 2 {
		t.Fatal("the finished report repeats the order it ran")
	}
}

// fakeSwitcher is a condition switch that costs time and restarts nothing.
// No test in this repository restarts a real judge.
type fakeSwitcher struct {
	fixture *trialFixture
	cost    time.Duration
	calls   []string
	fail    error
}

func (s *fakeSwitcher) Switch(_ context.Context, hook TrialSwitch, _ string) error {
	s.calls = append(s.calls, hook.Command)
	s.fixture.clock = s.fixture.clock.Add(s.cost)
	return s.fail
}

// TestTrialChargesTEvalForAConditionSwitch is the review's second point:
// asks for: the restart is inside the measured window, in its own phase.
func TestTrialChargesTEvalForAConditionSwitch(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1"})
	switcher := &fakeSwitcher{fixture: f, cost: 20 * time.Second}
	card := f.card("switching", []string{"i1", "i2"}, func(m *map[string]any) {
		hook := func(what string) map[string]any {
			return map[string]any{"command": "./switch.sh " + what, "timeout_seconds": 30}
		}
		base := (*m)["base"].(map[string]any)
		base["switch"] = hook("base")
		candidate := (*m)["candidate"].(map[string]any)
		candidate["switch"] = hook("candidate")
	})
	opts := f.options(card, filepath.Join(t.TempDir(), "out"))
	opts.Switcher = switcher
	result, err := Trial(context.Background(), opts)
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	// Two items, candidate then base each time: four entries into a condition
	// that costs something, the first one included.
	if len(switcher.calls) != 4 {
		t.Fatalf("%d switch(es), want 4: %v", len(switcher.calls), switcher.calls)
	}
	if got := result.Report.Phases.SwitchSeconds; got != 80 {
		t.Fatalf("switch phase %v s, want 80", got)
	}
	if len(result.Report.Switches) != 4 || result.Report.Switches[0].Condition != ConditionCandidate {
		t.Fatalf("the switches are recorded one by one, got %+v", result.Report.Switches)
	}
	// T_eval covers it, and no other phase double counts it.
	phases := result.Report.Phases
	if result.Report.TEvalSeconds < phases.SwitchSeconds+phases.MeasureSeconds {
		t.Fatalf("T_eval %v is smaller than the phases inside it (%+v)",
			result.Report.TEvalSeconds, phases)
	}
	if phases.WaitSeconds < 0 {
		t.Fatalf("wait went negative once the switch was taken out of it: %+v", phases)
	}
	if !strings.Contains(result.Report.Summary(), "switch 80.0") {
		t.Fatal("the summary names the switch phase")
	}

	// A switch that fails is a failed step rather than a silent measurement
	// of whichever condition the fleet happened to be in.
	f2 := newTrialFixture(t, map[string]string{"i1": "c1"})
	broken := &fakeSwitcher{fixture: f2, fail: errors.New("compose refused")}
	card2 := f2.card("broken-switch", []string{"i1"}, func(m *map[string]any) {
		candidate := (*m)["candidate"].(map[string]any)
		candidate["switch"] = map[string]any{"command": "./switch.sh"}
	})
	opts2 := f2.options(card2, filepath.Join(t.TempDir(), "out"))
	opts2.Switcher = broken
	result2, err := Trial(context.Background(), opts2)
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	if len(f2.runner.Calls) != 1 {
		t.Fatalf("the candidate is not asked when its switch failed, got %v", f2.runner.Calls)
	}
	if result2.Report.Switches[0].Error == "" {
		t.Fatal("a failed switch is recorded with its error")
	}
}

// TestCommandSwitcherRunsTheCommandAndWaits uses a stub command and a local
// server. It starts nothing and restarts nothing.
func TestCommandSwitcherRunsTheCommandAndWaits(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	hook := TrialSwitch{
		Command: "printf ready > switched.txt", ReadyURL: server.URL, TimeoutSeconds: 10,
	}
	if err := (CommandSwitcher{}).Switch(context.Background(), hook, dir); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "switched.txt")); err != nil || string(body) != "ready" {
		t.Fatalf("the command runs in the card's directory: %v %q", err, body)
	}

	if err := (CommandSwitcher{}).Switch(context.Background(),
		TrialSwitch{Command: "exit 3", TimeoutSeconds: 10}, dir); err == nil {
		t.Fatal("a command that fails is a failed switch")
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()
	err := (CommandSwitcher{}).Switch(context.Background(),
		TrialSwitch{Command: "true", ReadyURL: down.URL, TimeoutSeconds: 1}, dir)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("want a refusal naming the ready URL, got %v", err)
	}
}

// TestTrialCountsTieBreaksFromTheStructuredFields: the table is counted from
// judge.json's own fields, and a reason sentence that would fool a grep does
// not move it.
func TestTrialCountsTieBreaksFromTheStructuredFields(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1"})
	f.runner.Settle = func(item, _ string) (string, string) {
		switch item {
		case "i1":
			// A consensus group parted by a hash: the key is in the field and
			// the word `consensus` is in the sentence, which is the pair the
			// 2026-09-06 review had to separate by hand.
			return "numeric", "hash"
		case "i2":
			return "", "length"
		}
		return "", ""
	}
	card := f.card("keys", []string{"i1", "i2", "i3"}, nil)
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	for _, condition := range []string{ConditionBase, ConditionCandidate} {
		stats := result.Report.Conditions[condition]
		if stats.Consensus != 1 {
			t.Fatalf("%s settled %d step(s) by consensus, want 1", condition, stats.Consensus)
		}
		want := []TrialTieBreakRow{
			{Stage: TieBreakStageConsensus, Key: "hash", Steps: 1},
			{Stage: TieBreakStageCopeland, Key: "length", Steps: 1},
		}
		if len(stats.TieBreaks) != len(want) {
			t.Fatalf("%s: %+v, want %+v", condition, stats.TieBreaks, want)
		}
		for i, row := range want {
			if stats.TieBreaks[i] != row {
				t.Fatalf("%s row %d is %+v, want %+v", condition, i, stats.TieBreaks[i], row)
			}
		}
	}
	summary := result.Report.Summary()
	if !strings.Contains(summary, "| consensus | `hash` | 1 |") {
		t.Fatalf("the summary carries the stage-by-key table:\n%s", summary)
	}
	if !strings.Contains(summary, "not from `outcome.reason`") {
		t.Fatal("the summary says where the counts came from")
	}
}

// TestTrialQualityNeedsEnoughEvaluableItems: below the floor there is no ΔQ,
// and what is printed instead is 未評価 and the count.
func TestTrialQualityNeedsEnoughEvaluableItems(t *testing.T) {
	f := newTrialFixture(t, map[string]string{"i1": "c1", "i2": "c1", "i3": "c1"})
	card := f.card("floor", []string{"i1", "i2", "i3"}, nil)
	result, err := Trial(context.Background(), f.options(card, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	quality := result.Report.Quality
	if quality.Measured {
		t.Fatalf("three evaluable items is under the floor of %d, got %+v",
			DefaultMinEvaluableItems, quality)
	}
	if quality.Evaluable != 3 || quality.DeltaPoints != 0 {
		t.Fatalf("the count is kept and the difference is not printed, got %+v", quality)
	}
	if !strings.Contains(quality.Note, "未評価") || !strings.Contains(quality.Note, "33.3 points") {
		t.Fatalf("the note says what one label is worth, got %q", quality.Note)
	}
	if result.Report.Suggested.Value != DecisionInconclusive {
		t.Fatalf("an unmeasured quality suggests %q, want %q",
			result.Report.Suggested.Value, DecisionInconclusive)
	}
}

// TestCommittedTrialCardsAreRunnable reads the cards this repository ships:
// they load, they name no machine, and their plan is one the runner accepts.
func TestCommittedTrialCardsAreRunnable(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "suite-chat", "cards")
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no committed cards under %s: %v", dir, err)
	}
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			body, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			// A committed card is read by people who are not on this machine.
			for _, line := range strings.Split(string(body), "\n") {
				if strings.Contains(line, `": "/`) || strings.Contains(line, `"/home/`) {
					t.Fatalf("a committed card names an absolute path: %s", strings.TrimSpace(line))
				}
			}
			card, err := LoadTrialCard(name)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if card.Take[SetD] != 4 || card.Take[SetR] != 2 {
				t.Fatalf("the stage A take is D 4 and R 2, got %v", card.Take)
			}
			if card.BudgetSeconds != 600 || card.Reuse.Kind != ReuseNone {
				t.Fatalf("both conditions are measured in a 600 s box, got %d s and reuse %q",
					card.BudgetSeconds, card.Reuse.Kind)
			}
			var manifests []TrialManifest
			for _, manifest := range card.Manifests {
				loaded, err := LoadTrialManifest(card.Path(manifest))
				if err != nil {
					t.Fatalf("manifest: %v", err)
				}
				manifests = append(manifests, loaded)
			}
			if err := CheckPlan(card, manifests); err != nil {
				t.Fatalf("the committed card's plan is refused: %v", err)
			}
			// Six items, and the interleave keeps an R item in the first four.
			plan := TrialPlan(card, manifests)
			var sets []string
			for _, step := range plan {
				if step.Condition == ConditionCandidate {
					sets = append(sets, step.Set)
				}
			}
			if strings.Join(sets, " ") != "D R D D R D" {
				t.Fatalf("the stage A order is D R D D R D, got %s", strings.Join(sets, " "))
			}
		})
	}
}
