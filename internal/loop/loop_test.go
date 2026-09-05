package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/loop"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// fake answers a trial from a table, so a whole run is a unit test. It records
// what it was asked, which is how the cache and the pairing are checked.
type fake struct {
	mu   sync.Mutex
	pass func(trial loop.Trial) (bool, string)
	// pairwise answers a whole pair at once, which is what a simulation of a
	// *paired* design needs: the two arms of one pair are correlated, and a
	// per-trial coin would model two independent runs instead.
	pairwise func(task string, repeat int) (base, edit bool)
	// decorate is applied to the outcome last, for the tests that care what a
	// record ends up carrying.
	decorate func(*loop.Outcome)
	answers  map[string][2]bool
	seen     []loop.Trial
}

func (f *fake) Run(_ context.Context, trial loop.Trial) (loop.Outcome, error) {
	f.mu.Lock()
	f.seen = append(f.seen, trial)
	var pass bool
	var class string
	if f.pairwise != nil {
		if f.answers == nil {
			f.answers = map[string][2]bool{}
		}
		key := trial.Task + "#" + fmt.Sprint(trial.Repeat)
		answer, drawn := f.answers[key]
		if !drawn {
			base, edit := f.pairwise(trial.Task, trial.Repeat)
			answer = [2]bool{base, edit}
			f.answers[key] = answer
		}
		pass = answer[0]
		if trial.Arm == loop.ArmEdit {
			pass = answer[1]
		}
	}
	f.mu.Unlock()
	if f.pairwise == nil {
		pass, class = f.pass(trial)
	}
	if class == "" {
		class = loop.ErrorNone
	}
	kind := "no_candidate"
	if pass {
		kind = "selected"
	}
	outcome := loop.Outcome{Pass: pass, SelectionKind: kind, ErrorClass: class, WallMS: 1}
	if f.decorate != nil {
		f.decorate(&outcome)
	}
	return outcome, nil
}

func (f *fake) calls(arm loop.Arm) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, trial := range f.seen {
		if trial.Arm == arm {
			n++
		}
	}
	return n
}

// TestPairingUsesOneSeedForBothArms is the property the whole design rests on.
func TestPairingUsesOneSeedForBothArms(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	result, _ := mustRun(t, world(t), runner, func(o *loop.Options) {})
	if result.Header.Repeats != 3 {
		t.Errorf("screening runs %d repeats, want 3", result.Header.Repeats)
	}
	seeds := map[string]map[int64]bool{}
	for _, trial := range runner.seen {
		key := trial.Task + "#" + fmt.Sprint(trial.Repeat)
		if seeds[key] == nil {
			seeds[key] = map[int64]bool{}
		}
		seeds[key][trial.Seed] = true
	}
	for key, values := range seeds {
		if len(values) != 1 {
			t.Errorf("pair %s ran at %d different seeds, want one", key, len(values))
		}
	}
	if got := loop.Seed("a-task", 2); got != loop.Seed("a-task", 2) {
		t.Error("Seed is not a function of the task and the repeat index")
	}
	if loop.Seed("a-task", 2) == loop.Seed("a-task", 3) {
		t.Error("two repeats of one task share a seed")
	}
}

// TestImprove is an edit that fixes tasks the baseline fails: both splits show
// a gain, the pair is promoted, and a memory edit — a surface the harness
// accepts without asking anybody — has its document moved to accepted.
func TestImprove(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		return trial.Arm == loop.ArmEdit, ""
	}}
	w := world(t)
	result, options := mustRun(t, w, runner, func(*loop.Options) {})
	for _, split := range vocab.AllSplits() {
		if got := result.Splits[split].Verdict; got != vocab.VerdictImprove {
			t.Errorf("%s: verdict %s, want improve", split, got)
		}
	}
	if !result.Header.Promote {
		t.Error("both splits improved and the run did not promote")
	}
	if result.Header.Transition.Status != vocab.StatusAccepted.String() || !result.Header.Transition.Applied {
		t.Errorf("transition = %+v, want an applied acceptance", result.Header.Transition)
	}
	back := readEdit(t, w.vault, w.edit)
	if back.Status != vocab.StatusAccepted || back.Approval != vocab.ApprovalAuto {
		t.Errorf("the edit is %s/%s, want accepted/auto", back.Status, back.Approval)
	}
	if len(result.Written) != 2 {
		t.Errorf("wrote %d run documents, want one per split: %v", len(result.Written), result.Written)
	}
	for _, name := range result.Written {
		if _, err := os.Stat(filepath.Join(w.vault, name)); err != nil {
			t.Errorf("run document %s: %v", name, err)
		}
	}
	// The verdicts recomputed from the journal are the stored ones.
	assertReplay(t, options.Out)
}

// TestHold is an edit that changes nothing: every pair a tie. Non-inferiority
// is established and no gain is — but only once there are enough pairs for the
// margin to be certifiable, which at m = 0.15 is most of the screening cap.
func TestHold(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		return strings.HasSuffix(trial.Task, "1"), ""
	}}
	w := world(t)
	// Twenty tasks a split at three repeats is sixty pairs, which is the
	// screening cap and what the published margin needs.
	writeSuite(t, w.suite, 20, 20, 2)
	result, options := mustRun(t, w, runner, func(*loop.Options) {})
	for _, split := range vocab.AllSplits() {
		if got := result.Splits[split].Verdict; got != vocab.VerdictHold {
			t.Errorf("%s: verdict %s, want hold", split, got)
		}
	}
	if result.Header.Promote {
		t.Error("a run where nothing moved promoted the edit")
	}
	if readEdit(t, w.vault, w.edit).Status != vocab.StatusProposed {
		t.Error("a hold moved the edit's status")
	}
	assertReplay(t, options.Out)
}

// TestHoldNeedsTheMarginToBeCertifiable is the honest half of the same case.
// Twenty-four pairs, every one a tie, cannot certify m = 0.15 under a bound
// that does not treat the observed discordance as known — the interval is
// about ±0.25 — so the run says `inconclusive` rather than certifying a margin
// it has not earned. The margin and the cap are the owner's; what this asserts
// is that the verdict does not overstate them.
func TestHoldNeedsTheMarginToBeCertifiable(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		return strings.HasSuffix(trial.Task, "1"), ""
	}}
	result, _ := mustRun(t, world(t), runner, func(*loop.Options) {})
	for _, split := range vocab.AllSplits() {
		reading := result.Splits[split]
		if reading.Pairs != 24 {
			t.Fatalf("%s ran %d pairs, want 24", split, reading.Pairs)
		}
		if reading.Verdict != vocab.VerdictInconclusive {
			t.Errorf("%s: verdict %s at 24 all-tie pairs, want inconclusive (delta [%+.3f, %+.3f])",
				split, reading.Verdict, reading.Delta.Lo, reading.Delta.Hi)
		}
	}
}

// TestRegressByCountGate is the check the statistics cannot make: two held-out
// tasks the edit loses on every repeat, which is a regression whatever the
// e-process says about the pairs as a whole.
func TestRegressByCountGate(t *testing.T) {
	t.Parallel()
	// The edit fixes six held-out tasks and breaks two outright. The pairs
	// lean the edit's way, so the e-process sees a gain; the gate does not.
	broken := map[string]bool{"out-1": true, "out-2": true}
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		if broken[trial.Task] {
			return trial.Arm == loop.ArmBase, ""
		}
		return trial.Arm == loop.ArmEdit, ""
	}}
	w := world(t)
	result, options := mustRun(t, w, runner, func(*loop.Options) {})
	if got := result.Splits[vocab.SplitHeldOut].Verdict; got != vocab.VerdictRegress {
		t.Fatalf("held-out verdict %s, want regress; evidence %+v",
			got, result.Splits[vocab.SplitHeldOut].Evidence)
	}
	// The gate stops the split the moment the first task is complete and
	// lost, so the second broken task is never finished — which is the point:
	// one is already a regression.
	if got := result.Splits[vocab.SplitHeldOut].Evidence.LostTasks; got != 1 {
		t.Errorf("the gate counted %d tasks lost outright, want 1", got)
	}
	if result.Header.Promote {
		t.Error("a regressing edit was promoted")
	}
	if got := readEdit(t, w.vault, w.edit).Status; got != vocab.StatusRejected {
		t.Errorf("the edit is %s, want rejected", got)
	}
	assertReplay(t, options.Out)
}

// TestInconclusiveAtTheCap is the modal outcome: an effect too small for the
// budget, which the run says it could not settle rather than guessing.
func TestInconclusiveAtTheCap(t *testing.T) {
	t.Parallel()
	// One task in each split flips the edit's way on its first repeat and one
	// flips the baseline's, so the tally is even, no task is lost outright,
	// and the interval never closes inside the margin.
	runner := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	runner.pairwise = func(task string, repeat int) (base, edit bool) {
		if repeat != 0 {
			return true, true
		}
		switch {
		case strings.HasSuffix(task, "-1"):
			return false, true
		case strings.HasSuffix(task, "-2"):
			return true, false
		}
		return true, true
	}
	w := world(t)
	result, options := mustRun(t, w, runner, func(*loop.Options) {})
	for _, split := range vocab.AllSplits() {
		if got := result.Splits[split].Verdict; got != vocab.VerdictInconclusive {
			t.Errorf("%s: verdict %s, want inconclusive (evidence %+v)",
				split, got, result.Splits[split].Evidence)
		}
	}
	assertReplay(t, options.Out)
}

// TestBaselineOutcomesAreCached: the baseline harness does not change while a
// night's candidates are measured, so the second run of the same candidate
// asks the fleet for the edited arm only.
func TestBaselineOutcomesAreCached(t *testing.T) {
	t.Parallel()
	w := world(t)
	first := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	_, options := mustRun(t, w, first, func(*loop.Options) {})
	if first.calls(loop.ArmBase) == 0 {
		t.Fatal("the first run asked for no baseline trials")
	}
	second := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	// A second run of the same candidate against the same baseline, into a
	// sibling directory so the cache beside them is shared.
	_, out2 := mustRun(t, w, second, func(o *loop.Options) {
		o.Out = filepath.Join(filepath.Dir(options.Out), "second")
	})
	if got := second.calls(loop.ArmBase); got != 0 {
		t.Errorf("the second run asked for %d baseline trials, want none: the cache did not hit", got)
	}
	if second.calls(loop.ArmEdit) == 0 {
		t.Error("the second run asked for no edited trials")
	}
	hits := 0
	for _, row := range trials(t, out2.Out) {
		if row.CacheHit {
			hits++
		}
	}
	if hits == 0 {
		t.Error("no trial row records a cache hit")
	}
}

// TestAA runs the baseline against itself. The cache is off for it: a cache
// would answer both arms from one draw and report a discordance of zero it
// never measured.
func TestAA(t *testing.T) {
	t.Parallel()
	var toggle sync.Map
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		key := trial.Task + "#" + fmt.Sprint(trial.Repeat)
		_, second := toggle.LoadOrStore(key, true)
		// The second draw of a pair disagrees with the first on one task in
		// three, which is a fleet that does not quite reproduce itself.
		return !second || !strings.HasSuffix(trial.Task, "-3"), ""
	}}
	w := world(t)
	result, options := mustRun(t, w, runner, func(o *loop.Options) { o.AA = true })
	if result.AA == nil {
		t.Fatal("the run reported no calibration")
	}
	if result.AA.Pairs == 0 {
		t.Fatal("the calibration ran no pairs")
	}
	if result.AA.Discordance <= 0 {
		t.Errorf("the calibration measured a discordance of %v; the fake disagrees with itself",
			result.AA.Discordance)
	}
	if _, err := os.Stat(filepath.Join(options.Out, loop.AAName)); err != nil {
		t.Errorf("aa.json: %v", err)
	}
	// An A/A run says nothing about the edit, so it moves nothing.
	if readEdit(t, w.vault, w.edit).Status != vocab.StatusProposed {
		t.Error("an A/A calibration moved the edit's status")
	}
	if result.Header.AADiscordance == nil {
		t.Error("the header does not carry the measured discordance")
	}
}

// TestDryRun renders and plans and runs nothing.
func TestDryRun(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(loop.Trial) (bool, string) {
		t.Error("a dry run ran a trial")
		return false, ""
	}}
	w := world(t)
	result, options := mustRun(t, w, runner, func(o *loop.Options) { o.DryRun = true })
	if !result.Header.DryRun {
		t.Error("the header does not say it was a dry run")
	}
	if result.Header.Baseline.TreeSHA256 == result.Header.Candidate.TreeSHA256 {
		t.Error("the two renders are the same tree")
	}
	if result.Header.DecisionRule == "" {
		t.Error("a dry run records no decision rule")
	}
	if _, err := os.Stat(filepath.Join(options.Out, loop.HeaderName)); err != nil {
		t.Errorf("run.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(options.Out, loop.TrialsName)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a dry run wrote a trial journal")
	}
}

// TestHumanApprovalIsNotAutomatic: a system-prompt edit that measures well is
// left proposed, with the instruction a person needs.
func TestHumanApprovalIsNotAutomatic(t *testing.T) {
	t.Parallel()
	w := worldWith(t, systemPromptEdit)
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		return trial.Arm == loop.ArmEdit, ""
	}}
	result, _ := mustRun(t, w, runner, func(*loop.Options) {})
	if !result.Header.Promote {
		t.Fatalf("the run did not promote: %+v", result.Splits)
	}
	if result.Header.Transition.Applied {
		t.Error("a human-approval surface was accepted without a person")
	}
	if result.Header.Transition.Instruction == "" {
		t.Error("the run left nothing for a person to act on")
	}
	if readEdit(t, w.vault, w.edit).Status != vocab.StatusProposed {
		t.Error("the edit's status moved")
	}
}

// TestApplyFailedOnBaseline is the candidate that cannot be measured at all:
// its sidecar diff does not apply to the harness the vault describes today.
// That is a fact about the candidate rather than a failure of the run, and the
// honest record of it carries no pass rate — "not measured" must not enter the
// arithmetic that reads one.
func TestApplyFailedOnBaseline(t *testing.T) {
	t.Parallel()
	w := worldWith(t, systemPromptEdit)
	// Replace the sidecar with one that expects text the seed does not have.
	edit := readEdit(t, w.vault, w.edit)
	diff := "--- a/system-prompt.md\n+++ b/system-prompt.md\n@@ -1 +1,2 @@\n Something that was never written.\n+A second line.\n"
	relative, err := doc.DiffPath(edit.EditID)
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(w.vault, filepath.FromSlash(relative)), diff)
	edit.DiffSHA256 = doc.DiffSHA256Of([]byte(diff))
	writeEdit(t, w.vault, edit)

	runner := &fake{pass: func(loop.Trial) (bool, string) {
		t.Error("a candidate that could not be rendered was measured anyway")
		return false, ""
	}}
	result, options := mustRun(t, w, runner, func(*loop.Options) {})
	if result.Header.Unmeasurable == nil {
		t.Fatal("the header says nothing about why nothing was measured")
	}
	if result.Header.Unmeasurable.Reason != loop.ApplyFailedOnBaseline {
		t.Errorf("reason = %q, want %q", result.Header.Unmeasurable.Reason, loop.ApplyFailedOnBaseline)
	}
	for _, split := range vocab.AllSplits() {
		if got := result.Splits[split].Verdict; got != vocab.VerdictInconclusive {
			t.Errorf("%s: verdict %s, want inconclusive", split, got)
		}
	}
	if len(result.Written) != 2 {
		t.Fatalf("wrote %d run documents, want one per split", len(result.Written))
	}
	for _, name := range result.Written {
		body := readText(t, filepath.Join(w.vault, name))
		if strings.Contains(body, "pass_rate") {
			t.Errorf("%s carries a pass rate for a measurement that was never made:\n%s", name, body)
		}
		if !strings.Contains(body, loop.ApplyFailedOnBaseline) {
			t.Errorf("%s does not say why nothing was measured", name)
		}
	}
	if readEdit(t, w.vault, w.edit).Status != vocab.StatusProposed {
		t.Error("a candidate that was never measured had its status moved")
	}
	if _, err := os.Stat(filepath.Join(options.Out, loop.HeaderName)); err != nil {
		t.Errorf("run.json: %v", err)
	}
}

// TestRefusals walks the states where no budget is spent at all.
func TestRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		prepare func(t *testing.T, w scene) loop.Options
		want    string
	}{
		{
			name: "an edit that is not a proposal",
			prepare: func(t *testing.T, w scene) loop.Options {
				edit := memoryEdit(w.edit)
				edit.Status = vocab.StatusAccepted
				edit.Approval = vocab.ApprovalHuman
				edit.ApprovedBy = "somebody"
				writeEdit(t, w.vault, edit)
				return loop.Options{}
			},
			want: "measures a proposal",
		},
		{
			name: "an edit that predicts nothing",
			prepare: func(t *testing.T, w scene) loop.Options {
				edit := memoryEdit(w.edit)
				edit.Predicts = nil
				writeEdit(t, w.vault, edit)
				return loop.Options{}
			},
			want: "predicts nothing",
		},
		{
			name: "a surface the harness has no place for",
			prepare: func(t *testing.T, w scene) loop.Options {
				edit := memoryEdit(w.edit)
				edit.Component = "subagent-config"
				edit.Touches = []string{"subagent-config"}
				writeEdit(t, w.vault, edit)
				return loop.Options{}
			},
			want: "no injection point",
		},
		{
			name: "a split under the floor",
			prepare: func(t *testing.T, w scene) loop.Options {
				writeSuite(t, w.suite, 8, 2, 8)
				return loop.Options{}
			},
			want: "the floor is",
		},
		{
			name: "a mode that is neither",
			prepare: func(t *testing.T, w scene) loop.Options {
				return loop.Options{Mode: "whenever"}
			},
			want: "--mode",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := world(t)
			overrides := c.prepare(t, w)
			options := w.options(&fake{pass: func(loop.Trial) (bool, string) {
				t.Error("a refused run spent budget")
				return false, ""
			}})
			if overrides.Mode != "" {
				options.Mode = overrides.Mode
			}
			_, err := loop.Run(t.Context(), options)
			if err == nil {
				t.Fatalf("the run went ahead; want a refusal naming %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
		})
	}
}

// TestReplayNoticesATamperedHeader is the other half of the replay invariant:
// it is only worth anything if it can fail.
func TestReplayNoticesATamperedHeader(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(trial loop.Trial) (bool, string) { return trial.Arm == loop.ArmEdit, "" }}
	_, options := mustRun(t, world(t), runner, func(*loop.Options) {})
	name := filepath.Join(options.Out, loop.HeaderName)
	body, err := os.ReadFile(name) //nolint:gosec // a file the test just wrote
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(body), `"verdict": "improve"`, `"verdict": "hold"`, 1)
	if tampered == string(body) {
		t.Fatal("the header does not carry a verdict to tamper with")
	}
	if err := os.WriteFile(name, []byte(tampered), 0o644); err != nil { //nolint:gosec // a test fixture
		t.Fatal(err)
	}
	comparison, err := loop.Replay(name)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if comparison.Identical() {
		t.Error("the replay accepted a header the journal does not support")
	}
}

// TestCMoARunner drives the exec runner against a canned harness, so the two
// calls, the flags and the reading of select.json are checked without a fleet.
func TestCMoARunner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := filepath.Join(dir, "cmoa")
	writeText(t, script, `#!/bin/sh
set -e
case "$1" in
propose)
  run="$TMPDIR_RUN"
  mkdir -p "$run/candidates"
  printf '%s' "$*" > "$run/argv.txt"
  printf '{"run_id":"20260905T000000Z-abcdef01","harness":{"render":{"tree_sha256":"'"$UZ_DIGEST"'"}}}' > "$run/run.json"
  printf '{"status":"ok","usage":{"prompt_tokens":11,"completion_tokens":7}}' > "$run/candidates/a.json"
  echo "$run"
  ;;
select)
  run=""
  while [ $# -gt 0 ]; do
    if [ "$1" = "--run" ]; then run="$2"; fi
    shift
  done
  printf '{"selection":{"kind":"selected","candidate_id":"a"}}' > "$run/select.json"
  echo "selected a"
  ;;
esac
`)
	if err := os.Chmod(script, 0o755); err != nil { //nolint:gosec // an executable test fixture
		t.Fatal(err)
	}
	runDir := filepath.Join(dir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := loop.CMoARunner{
		Binary: script,
		Config: filepath.Join(dir, "cmoa.json"),
		Env:    []string{"TMPDIR_RUN=" + runDir, "UZ_DIGEST=deadbeef"},
	}
	outcome, err := runner.Run(t.Context(), loop.Trial{
		Task: "a-task", TaskDir: dir, Repeat: 1, Seed: 42,
		Arm: loop.ArmEdit, Harness: dir, HarnessSHA256: "deadbeef",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !outcome.Pass || outcome.SelectionKind != "selected" {
		t.Errorf("outcome = %+v, want a pass", outcome)
	}
	if outcome.TokensIn != 11 || outcome.TokensOut != 7 {
		t.Errorf("usage = %d/%d, want 11/7", outcome.TokensIn, outcome.TokensOut)
	}
	if outcome.RunID != "20260905T000000Z-abcdef01" {
		t.Errorf("run id = %q", outcome.RunID)
	}
	argv := readText(t, filepath.Join(runDir, "argv.txt"))
	for _, want := range []string{"--harness", "--seed 42", "--temperature 0"} {
		if !strings.Contains(argv, want) {
			t.Errorf("propose was called as %q, which does not carry %q", argv, want)
		}
	}

	// A harness that read a different directory from the one the render
	// describes invalidates the pair, not one trial in it.
	_, err = runner.Run(t.Context(), loop.Trial{
		Task: "a-task", TaskDir: dir, Arm: loop.ArmBase, Harness: dir, HarnessSHA256: "notthesame",
	})
	if err == nil {
		t.Error("a harness digest mismatch was accepted")
	}
}

// --- the world a run happens in ---

type scene struct {
	vault  string
	suite  string
	edit   string
	root   string
	config string
	docdag string
}

func world(t *testing.T) scene { return worldWith(t, memoryEdit) }

func worldWith(t *testing.T, build func(id string) doc.Edit) scene {
	t.Helper()
	root := t.TempDir()
	w := scene{
		vault:  filepath.Join(root, "vault"),
		suite:  filepath.Join(root, "suite", "suite.json"),
		edit:   "he-0001",
		root:   root,
		config: filepath.Join(root, "cmoa.json"),
		docdag: docdagFor(t),
	}
	// A run names its fleet or it is not reproducible: the configuration is
	// the model slug in every document it writes and it keys the baseline
	// cache.
	writeText(t, w.config, `{"version":1,"proposers":[`+
		`{"id":"one","model":"a-model","base_url":"http://127.0.0.1:8081/v1"},`+
		`{"id":"two","model":"another-model","base_url":"http://127.0.0.1:8082/v1"}]}`)
	copyVault(t, w.vault)
	edit := build(w.edit)
	if edit.Component == "system-prompt" {
		writeSidecar(t, w.vault, &edit)
	}
	writeEdit(t, w.vault, edit)
	writeSuite(t, w.suite, 8, 8, 2)
	return w
}

func (w scene) options(runner loop.Runner) loop.Options {
	return loop.Options{
		Vault: w.vault, Edit: w.edit, Suite: w.suite, Config: w.config,
		Out: filepath.Join(w.root, "runs", "one"), Mode: loop.ModeScreening,
		AsOf: "2026-09-05", DocDag: w.docdag, Runner: runner,
		Version: "test",
		Now:     func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) },
	}
}

func mustRun(t *testing.T, w scene, runner loop.Runner, adjust func(*loop.Options)) (loop.Result, loop.Options) {
	t.Helper()
	options := w.options(runner)
	adjust(&options)
	result, err := loop.Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, options
}

func assertReplay(t *testing.T, out string) {
	t.Helper()
	comparison, err := loop.Replay(filepath.Join(out, loop.HeaderName))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !comparison.Identical() {
		t.Errorf("the journal does not reproduce the stored verdicts: %v", comparison.Anomalies)
	}
}

func trials(t *testing.T, out string) []loop.TrialRecord {
	t.Helper()
	rows, err := loop.ReadTrials(filepath.Join(out, loop.TrialsName))
	if err != nil {
		t.Fatalf("ReadTrials: %v", err)
	}
	return rows
}

func memoryEdit(id string) doc.Edit {
	return doc.Edit{
		EditID: id, Title: "Say what the verifier runs", Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "memory", Touches: []string{"memory"},
		Paths: []string{"memory/00-verifier.md"}, Approval: vocab.ApprovalAuto,
		About:     []string{"topic/harness-improvement"},
		RootCause: "the proposer did not know what the verifier ran",
		Predicts:  []doc.Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body:      "The verifier runs the tests in a container. It does not run a formatter.",
	}
}

func systemPromptEdit(id string) doc.Edit {
	return doc.Edit{
		EditID: id, Title: "Prefer the standard library", Date: "2026-09-05",
		Status: vocab.StatusProposed, Component: "system-prompt", Touches: []string{"system-prompt"},
		Paths: []string{"system-prompt.md"}, Approval: vocab.ApprovalHuman,
		About:     []string{"topic/harness-improvement"},
		RootCause: "the proposer reached for a dependency",
		Predicts:  []doc.Prediction{{Pattern: "fp/example", Expect: vocab.ExpectFix}},
		Body:      "The seed system prompt.",
	}
}

// writeSidecar writes the unified diff a system-prompt edit's content lives in
// and fills in the digest that stands guard over it.
func writeSidecar(t *testing.T, vault string, edit *doc.Edit) {
	t.Helper()
	diff := "--- /dev/null\n+++ b/system-prompt.md\n@@ -0,0 +1 @@\n+Prefer the standard library.\n"
	relative, err := doc.DiffPath(edit.EditID)
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(vault, filepath.FromSlash(relative)), diff)
	edit.DiffSHA256 = doc.DiffSHA256Of([]byte(diff))
}

func writeEdit(t *testing.T, vault string, edit doc.Edit) {
	t.Helper()
	relative, err := edit.Path()
	if err != nil {
		t.Fatal(err)
	}
	body, err := edit.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(vault, filepath.FromSlash(relative)), string(body))
}

func readEdit(t *testing.T, vault, id string) doc.Edit {
	t.Helper()
	relative, err := vocab.Path(vocab.KindEdit, id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	edit, err := doc.ParseEdit(id, body)
	if err != nil {
		t.Fatal(err)
	}
	return edit
}

// writeSuite writes a suite of in + out tasks with a stated floor.
func writeSuite(t *testing.T, name string, in, out, floor int) {
	t.Helper()
	type task struct {
		ID    string `json:"id"`
		Dir   string `json:"dir"`
		Split string `json:"split"`
	}
	file := struct {
		SchemaVersion    int    `json:"schema_version"`
		ID               string `json:"id"`
		MinTasksPerSplit int    `json:"min_tasks_per_split"`
		Tasks            []task `json:"tasks"`
		Note             string `json:"note"`
	}{SchemaVersion: 1, ID: "suite-under-test", MinTasksPerSplit: floor,
		Note: "an unknown key a run must ignore"}
	for i := 1; i <= in; i++ {
		file.Tasks = append(file.Tasks, task{ID: fmt.Sprintf("in-%d", i), Dir: fmt.Sprintf("in-%d", i), Split: "held-in"})
	}
	for i := 1; i <= out; i++ {
		file.Tasks = append(file.Tasks, task{ID: fmt.Sprintf("out-%d", i), Dir: fmt.Sprintf("out-%d", i), Split: "held-out"})
	}
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, name, string(body))
}

// copyVault copies the repository's configuration and specification corpus.
func copyVault(t *testing.T, into string) {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(into, "docdag.yaml"), readText(t, filepath.Join(source, "docdag.yaml")))
	specRoot := filepath.Join(source, "spec")
	err = filepath.WalkDir(specRoot, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(specRoot, p)
		if relErr != nil {
			return relErr
		}
		writeText(t, filepath.Join(into, "spec", relative), readText(t, p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, target, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil { //nolint:gosec // a test fixture
		t.Fatal(err)
	}
}

func readText(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name) //nolint:gosec // a test fixture
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// docdagFor makes sure the engine that answers the binding question is there,
// and points the render at it.
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

// TestAVerifierThatCannotRunIsNotALoss is the invariant C2 exists for: the
// harness's own word for `verifier_failed` is that it "says nothing about any
// candidate", and a run that scored it as a failure would reject an edit
// because docker was missing — asymmetrically, since the baseline arm is often
// served from the cache and the edited arm is always run.
func TestAVerifierThatCannotRunIsNotALoss(t *testing.T) {
	t.Parallel()
	for _, class := range []string{loop.ErrorVerifier, loop.ErrorTimeout, loop.ErrorInfra, loop.ErrorHarnessCrash} {
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			// The edited arm never gets an answer; the baseline always passes.
			runner := &fake{pass: func(trial loop.Trial) (bool, string) {
				if trial.Arm == loop.ArmEdit {
					return false, class
				}
				return true, ""
			}}
			w := world(t)
			options := w.options(runner)
			_, err := loop.Run(t.Context(), options)
			// Nothing was measurable, so the run stops rather than reading a
			// wall of unanswered pairs as evidence. What matters is what it
			// did *not* do on the way there.
			if err == nil {
				t.Fatalf("a run in which every %s trial failed came back with a verdict", class)
			}
			if !errors.Is(err, loop.ErrRun) {
				t.Errorf("error %v does not wrap loop.ErrRun", err)
			}
			if got := readEdit(t, w.vault, w.edit).Status; got != vocab.StatusProposed {
				t.Errorf("the edit is %s; %s must not decide anything about it", got, class)
			}
			// Every trial is journalled — it cost wall-clock — and every pair
			// is pending.
			for _, row := range trials(t, options.Out) {
				if row.PairOutcome != "pending" {
					t.Errorf("a pair with a %s trial recorded outcome %q", class, row.PairOutcome)
				}
			}
			// Nothing unmeasurable reaches the cache, where it would be
			// replayed as a baseline failure by every later candidate.
			_ = filepath.WalkDir(filepath.Join(filepath.Dir(options.Out), "cache"),
				func(name string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil || entry.IsDir() {
						return nil //nolint:nilerr // a missing cache is nothing to check
					}
					var cached struct {
						Fleet   string       `json:"fleet_sha256"`
						Outcome loop.Outcome `json:"outcome"`
					}
					if err := json.Unmarshal([]byte(readText(t, name)), &cached); err != nil {
						t.Errorf("cache entry %s: %v", name, err)
						return nil
					}
					if cached.Fleet == "" {
						t.Errorf("cache entry %s names no fleet; it would be shared across every model", name)
					}
					if cached.Outcome.ErrorClass != loop.ErrorNone {
						t.Errorf("cache entry %s remembers a %s; a later run would replay it as a baseline failure",
							name, cached.Outcome.ErrorClass)
					}
					return nil
				})
		})
	}
}

// TestCalibrationRunsTheBaselineOnBothArms is C4. d̂₀ is the one number every
// sample-size statement in the design rests on, and an arm pointed at the
// candidate makes it a number about the edit instead.
func TestCalibrationRunsTheBaselineOnBothArms(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	dirs := map[string]bool{}
	shas := map[string]bool{}
	runner := &fake{pass: func(trial loop.Trial) (bool, string) {
		mu.Lock()
		dirs[trial.Harness] = true
		shas[trial.HarnessSHA256] = true
		mu.Unlock()
		return true, ""
	}}
	result, options := mustRun(t, world(t), runner, func(o *loop.Options) { o.AA = true })
	if len(dirs) != 1 {
		t.Errorf("the calibration read %d harness directories, want one: %v", len(dirs), keys(dirs))
	}
	if len(shas) != 1 {
		t.Errorf("the calibration ran against %d harness digests, want one: %v", len(shas), keys(shas))
	}
	want := filepath.Join(options.Out, "harness", "base")
	if !dirs[want] {
		t.Errorf("the calibration read %v, want the baseline at %s", keys(dirs), want)
	}
	if !shas[result.Header.Baseline.TreeSHA256] {
		t.Errorf("the calibration ran against %v, want the baseline digest %s",
			keys(shas), result.Header.Baseline.TreeSHA256)
	}
}

// TestNoAbsolutePathsAreWritten: every file a run leaves behind is meant to be
// committable. The harness's stderr and the trace directory it prints are the
// two channels that carry somebody's home directory into one.
func TestNoAbsolutePathsAreWritten(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		t.Skip("no home directory to look for")
	}
	leak := filepath.Join(home, "a-directory-nobody-should-see")
	runner := &fake{pass: func(trial loop.Trial) (bool, string) { return trial.Arm == loop.ArmEdit, "" }}
	runner.decorate = func(o *loop.Outcome) {
		o.RunDir = filepath.Join(leak, "runs", "20260905T000000Z-abcdef01")
		o.Error = "cmoa: compose file " + filepath.Join(leak, "compose.yaml") + " is unreadable"
	}
	w := world(t)
	_, options := mustRun(t, w, runner, func(*loop.Options) {})

	roots := []string{options.Out, filepath.Dir(options.Out), filepath.Join(w.vault, "spec", "runs")}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return nil //nolint:nilerr // a missing directory is nothing to check
			}
			body, readErr := os.ReadFile(name) //nolint:gosec // a file the test just wrote
			if readErr != nil {
				return nil
			}
			if strings.Contains(string(body), home) {
				t.Errorf("%s carries the home directory of the machine that ran it", name)
			}
			return nil
		})
	}
}

// TestReplayRecomputesThePairing is H7: the pairing arithmetic has to be
// inside the invariant, not beside it. A journal whose outcomes have been
// flipped must not replay clean.
func TestReplayRecomputesThePairing(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(trial loop.Trial) (bool, string) { return trial.Arm == loop.ArmEdit, "" }}
	_, options := mustRun(t, world(t), runner, func(*loop.Options) {})
	name := filepath.Join(options.Out, loop.TrialsName)
	body := readText(t, name)
	// Flip every trial's answer and leave the recorded pair outcomes alone.
	writeText(t, name, strings.ReplaceAll(body, `"pass":true`, `"pass":false`))
	comparison, err := loop.Replay(filepath.Join(options.Out, loop.HeaderName))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if comparison.Identical() {
		t.Error("the replay accepted a journal whose trial outcomes had all been flipped")
	}
}

// TestNullEditIsNotRejected drives an edit that changes nothing through
// loop.Run — the real scheduler, the real gate, the real stopping rule — and
// counts how often it is called a regression.
//
// It is the test that would have caught the count gate: under the definition
// this replaced, a harmless edit was rejected about four times in five at the
// discordance the design expects, and each rejection was written into the
// vault as `status: rejected`, which no later run will re-measure.
//
// UZUSHIO_SIM_RUNS and UZUSHIO_SIM_D0 raise the sample for a one-off sweep.
func TestNullEditIsNotRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("the simulation renders a vault per run")
	}
	t.Parallel()
	runs, discordance := simulationSize(t, 200, 0.2)
	source := rand.New(rand.NewPCG(0x6e756c6c, 0x65646974)) //nolint:gosec // a reproducible simulation

	rejected, promoted := 0, 0
	for i := range runs {
		// A pair disagrees with probability d, and when it does either arm is
		// as likely to win: an edit that changes nothing.
		runner := &fake{pass: func(trial loop.Trial) (bool, string) {
			return trial.Arm == loop.ArmEdit, ""
		}}
		runner.pairwise = func(task string, repeat int) (base, edit bool) {
			if source.Float64() >= discordance {
				return true, true
			}
			if source.Float64() < 0.5 {
				return false, true
			}
			return true, false
		}
		w := world(t)
		writeSuite(t, w.suite, 12, 12, 2)
		options := w.options(runner)
		options.Out = filepath.Join(w.root, "runs", "sim-"+itoa(i))
		result, err := loop.Run(t.Context(), options)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		for _, split := range vocab.AllSplits() {
			if result.Splits[split].Verdict == vocab.VerdictRegress {
				rejected++
				break
			}
		}
		if result.Header.Promote {
			promoted++
		}
	}
	falseRegress := float64(rejected) / float64(runs)
	t.Logf("null edit, d0=%.2f, %d runs: rejected %d (%.4f), promoted %d (%.4f)",
		discordance, runs, rejected, falseRegress, promoted, float64(promoted)/float64(runs))
	if falseRegress > 0.05 {
		t.Errorf("a harmless edit is rejected %.4f of the time at d0=%.2f, want at most 0.05",
			falseRegress, discordance)
	}
	if float64(promoted)/float64(runs) > 0.05 {
		t.Errorf("a null edit is promoted %.4f of the time, want at most 0.05", float64(promoted)/float64(runs))
	}
}

// TestACatastrophicTaskRegressionIsCaught is the other side of the gate: one
// task that the edit takes from passing every time to failing every time is
// what the count gate exists to notice, and the sequential test at this budget
// will not notice it on its own.
func TestACatastrophicTaskRegressionIsCaught(t *testing.T) {
	t.Parallel()
	runner := &fake{pass: func(trial loop.Trial) (bool, string) { return true, "" }}
	runner.pairwise = func(task string, repeat int) (base, edit bool) {
		if task == "out-3" {
			return true, false
		}
		return true, true
	}
	w := world(t)
	writeSuite(t, w.suite, 12, 12, 2)
	result, _ := mustRun(t, w, runner, func(*loop.Options) {})
	reading := result.Splits[vocab.SplitHeldOut]
	if reading.Verdict != vocab.VerdictRegress {
		t.Errorf("one task taken from always-passing to always-failing reads as %s, want regress (%+v)",
			reading.Verdict, reading.Evidence)
	}
	if reading.Evidence.LostTasks != 1 {
		t.Errorf("the gate counted %d tasks lost outright, want 1", reading.Evidence.LostTasks)
	}
	if got := readEdit(t, w.vault, w.edit).Status; got != vocab.StatusRejected {
		t.Errorf("the edit is %s, want rejected", got)
	}
}

// TestTheGateWaitsForATaskToFinish: a task that has lost the one repeat it has
// run so far has not lost every repeat, and calling it a regression mid-sweep
// is what made the old gate fire on noise.
func TestTheGateWaitsForATaskToFinish(t *testing.T) {
	t.Parallel()
	// out-1 loses its first repeat and wins the other two.
	runner := &fake{pass: func(trial loop.Trial) (bool, string) { return true, "" }}
	runner.pairwise = func(task string, repeat int) (base, edit bool) {
		if task == "out-1" && repeat == 0 {
			return true, false
		}
		return true, true
	}
	w := world(t)
	result, _ := mustRun(t, w, runner, func(*loop.Options) {})
	reading := result.Splits[vocab.SplitHeldOut]
	if reading.Evidence.LostTasks != 0 {
		t.Errorf("a task that lost one repeat of three counted as lost outright (%+v)", reading.Evidence)
	}
	if reading.Verdict == vocab.VerdictRegress {
		t.Error("one unlucky repeat was read as a regression")
	}
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// simulationSize reads the sweep's size out of the environment, so the same
// test both runs cheaply in CI and produces the published numbers.
func simulationSize(t *testing.T, runs int, discordance float64) (int, float64) {
	t.Helper()
	if named := os.Getenv("UZUSHIO_SIM_RUNS"); named != "" {
		n, err := strconv.Atoi(named)
		if err != nil {
			t.Fatalf("UZUSHIO_SIM_RUNS: %v", err)
		}
		runs = n
	}
	if named := os.Getenv("UZUSHIO_SIM_D0"); named != "" {
		d, err := strconv.ParseFloat(named, 64)
		if err != nil {
			t.Fatalf("UZUSHIO_SIM_D0: %v", err)
		}
		discordance = d
	}
	return runs, discordance
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestCatastropheCatchRate measures the other half of the gate's trade: how
// often one task taken from always passing to always failing is caught, when
// every other task is drawing the ordinary pairing noise around it.
//
// The pair statistic on its own will not find it — one task in twelve moving
// three pairs is far inside the interval — so this is the number that says
// whether the gate earns its false-positive rate.
func TestCatastropheCatchRate(t *testing.T) {
	if testing.Short() {
		t.Skip("the simulation renders a vault per run")
	}
	t.Parallel()
	runs, discordance := simulationSize(t, 100, 0.2)
	source := rand.New(rand.NewPCG(0x63617463, 0x68726174)) //nolint:gosec // a reproducible simulation

	caught := 0
	for i := range runs {
		runner := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
		runner.pairwise = func(task string, _ int) (base, edit bool) {
			if task == "out-3" {
				return true, false
			}
			if source.Float64() >= discordance {
				return true, true
			}
			if source.Float64() < 0.5 {
				return false, true
			}
			return true, false
		}
		w := world(t)
		writeSuite(t, w.suite, 12, 12, 2)
		options := w.options(runner)
		options.Out = filepath.Join(w.root, "runs", "catch-"+itoa(i))
		result, err := loop.Run(t.Context(), options)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if result.Splits[vocab.SplitHeldOut].Verdict == vocab.VerdictRegress {
			caught++
		}
	}
	rate := float64(caught) / float64(runs)
	t.Logf("one always-failing task among %d, d0=%.2f, %d runs: caught %d (%.4f)",
		12, discordance, runs, caught, rate)
	if rate < 0.99 {
		t.Errorf("a task taken from always passing to always failing is caught %.4f of the time, want at least 0.99", rate)
	}
}

// TestTheCacheIsKeyedOnTheFleet is H5. Without it, two runs whose only
// difference is a swapped model share every baseline outcome, and the second
// publishes a comparison against a baseline its own fleet never ran — a
// corrupted result with no error anywhere.
func TestTheCacheIsKeyedOnTheFleet(t *testing.T) {
	t.Parallel()
	w := world(t)
	first := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	_, options := mustRun(t, w, first, func(*loop.Options) {})
	if first.calls(loop.ArmBase) == 0 {
		t.Fatal("the first run asked for no baseline trials")
	}

	// The same everything, one model swapped.
	writeText(t, w.config, `{"version":1,"proposers":[`+
		`{"id":"one","model":"a-different-model","base_url":"http://127.0.0.1:8081/v1"},`+
		`{"id":"two","model":"another-model","base_url":"http://127.0.0.1:8082/v1"}]}`)
	second := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	_, _ = mustRun(t, w, second, func(o *loop.Options) {
		o.Out = filepath.Join(filepath.Dir(options.Out), "second")
	})
	if second.calls(loop.ArmBase) == 0 {
		t.Error("a run against a different fleet was served every baseline outcome from the cache")
	}

	// And the same fleet still hits.
	third := &fake{pass: func(loop.Trial) (bool, string) { return true, "" }}
	_, _ = mustRun(t, w, third, func(o *loop.Options) {
		o.Out = filepath.Join(filepath.Dir(options.Out), "third")
	})
	if third.calls(loop.ArmBase) != 0 {
		t.Errorf("the same fleet asked for %d baseline trials again", third.calls(loop.ArmBase))
	}
}

// TestConfigIsRequired: a run without a named fleet is not reproducible, and
// its documents would say `pool-unknown`.
func TestConfigIsRequired(t *testing.T) {
	t.Parallel()
	w := world(t)
	options := w.options(&fake{pass: func(loop.Trial) (bool, string) { return true, "" }})
	options.Config = ""
	if _, err := loop.Run(t.Context(), options); err == nil {
		t.Fatal("a run without a configuration went ahead")
	} else if !strings.Contains(err.Error(), "--config") {
		t.Errorf("error %q does not name the flag", err)
	}
}

// TestAcceptingAnEditKeepsWhatTheRunDidNotWrite is M6. A run records what it
// measured; it does not get to edit prose it did not write. Round-tripping the
// document through a closed frontmatter struct silently deleted any key
// somebody had added by hand.
func TestAcceptingAnEditKeepsWhatTheRunDidNotWrite(t *testing.T) {
	t.Parallel()
	w := world(t)
	relative, err := vocab.Path(vocab.KindEdit, w.edit)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(w.vault, filepath.FromSlash(relative))
	before := readText(t, name)
	// A key this package does not know about, and a body with spacing of its
	// own, both written by hand.
	before = strings.Replace(before, "approval: auto\n",
		"approval: auto\nx_ticket: OPS-1234\nauthor_note: raised at the Tuesday review\n", 1)
	before = strings.TrimRight(before, "\n") + "\n\n\nA paragraph after two blank lines.\n"
	writeText(t, name, before)

	runner := &fake{pass: func(trial loop.Trial) (bool, string) { return trial.Arm == loop.ArmEdit, "" }}
	result, _ := mustRun(t, w, runner, func(*loop.Options) {})
	if !result.Header.Transition.Applied {
		t.Fatalf("the run did not accept the edit: %+v", result.Header.Transition)
	}

	after := readText(t, name)
	for _, want := range []string{
		"x_ticket: OPS-1234",
		"author_note: raised at the Tuesday review",
		"A paragraph after two blank lines.",
		"status: accepted",
		"approval: auto",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("the accepted document does not carry %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, "status: proposed") {
		t.Errorf("the status was not moved:\n%s", after)
	}
	// Nothing else moved: the two edited lines are the whole difference.
	if a, b := strings.Count(before, "\n"), strings.Count(after, "\n"); a != b {
		t.Errorf("the document went from %d lines to %d", a, b)
	}
}
