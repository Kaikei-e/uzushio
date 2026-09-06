package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

// fakeTrialCMoA writes a harness stand-in that leaves the three files a reuse
// key reads — run.json, judge.json and select.json — rather than only the
// judge record. A trial reads all three, and a fake that wrote one would test
// the arithmetic against a shape the harness does not produce.
func fakeTrialCMoA(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake harness is a shell script")
	}
	runs := filepath.Join(dir, "runs")
	script := `#!/bin/sh
set -eu
if [ "${1:-}" != "judge" ]; then echo "unknown command ${1:-}" >&2; exit 2; fi
shift
if [ "${1:-}" = "--help" ]; then echo "judge --task <dir> --candidate <file>..."; exit 0; fi
task=""; seed=0; config=""; n=0
while [ $# -gt 0 ]; do
  case "$1" in
    --task) task="$2"; shift 2 ;;
    --candidate) n=$((n+1)); shift 2 ;;
    --seed) seed="$2"; shift 2 ;;
    --judge-seed) shift 2 ;;
    --config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
gold=$(sed -n 's/.*"gold": "\([^"]*\)".*/\1/p' "$task/gold.json")
name=$(basename "$task")
id="$name-$seed-$(basename "$config" .json)"
dir="` + runs + `/$id"
mkdir -p "$dir"
ext=""
for pos in c1 c2 c3; do
  sum=$(sha256sum "$task/candidates/$pos.txt" | cut -d' ' -f1)
  ext="$ext{\"id\":\"$pos\",\"file\":\"$task/candidates/$pos.txt\",\"sha256\":\"$sum\"},"
done
conv=$(sha256sum "$task/conversation.json" | cut -d' ' -f1)
cat > "$dir/run.json" <<EOF
{"schema_version":1,"run_id":"$id","prompt_version":"p1","cmoa_version":"v0.0.0-fake",
 "face":"chat","conversation_sha256":"$conv","candidates_origin":"external",
 "external_candidates":[${ext%,}]}
EOF
cat > "$dir/select.json" <<EOF
{"schema_version":1,"run_id":"$id","rule":"consensus-then-copeland"}
EOF
cat > "$dir/judge.json" <<EOF
{"schema_version":1,"run_id":"$id",
 "judge":{"model":"gpt-oss-20b","temperature":0,"seed":7,"max_tokens":512,
          "output_format":"json_schema","parallel":3,"allow_tie":true},
 "candidates":["c1","c2","c3"],"presentation":{"seed":$seed},
 "pairs":[{"pair":["c1","c2"],"orders":[
    {"first":"c1","second":"c2","choice":"A","choice_candidate":"$gold","status":"ok"},
    {"first":"c2","second":"c1","choice":"B","choice_candidate":"$gold","status":"ok"}],
   "verdict":"$gold"}],
 "outcome":{"kind":"selected","candidate_id":"$gold","reason":"condorcet winner"},
 "swap_consistent_pairs":1,"invalid_output_retries":0,"latency_ms":1234}
EOF
echo '{"kind":"selected","candidate_id":"'"$gold"'"}'
echo "$dir"
`
	bin := filepath.Join(dir, "cmoa")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // a fake binary is executable
		t.Fatalf("write fake cmoa: %v", err)
	}
	return bin
}

// trialCard writes a card, its manifest and the two condition configurations.
func trialCard(t *testing.T, dir, suite string, items []string, mutate func(map[string]any)) string {
	t.Helper()
	var entries []map[string]any
	for _, item := range items {
		entries = append(entries, map[string]any{
			"id": item, "strata": map[string]string{"category": "writing", "language": "en"},
			"reason": "a typical item",
		})
	}
	judgeJSON(t, filepath.Join(dir, "set-d.json"), map[string]any{
		"schema_version": 1, "set": "D", "items": entries,
	})
	base := judgeConfig(t, filepath.Join(dir, "base"), "gpt-oss-20b")
	candidate := judgeConfig(t, filepath.Join(dir, "cand"), "gpt-oss-20b")
	card := map[string]any{
		"schema_version": 1, "id": "smoke",
		"hypothesis": "the two conditions agree", "change": "nothing, this is a control",
		"stage": "A", "suite": suite, "manifests": []string{"set-d.json"},
		"base":      map[string]any{"id": "base-1", "config": base},
		"candidate": map[string]any{"id": "cand-1", "config": candidate},
		"reuse":     map[string]any{"kind": "none"},
		"seed":      1, "judge_seed": 7,
		"rules": map[string]any{"kind": "speed"}, "budget_seconds": 600,
	}
	if mutate != nil {
		mutate(card)
	}
	name := filepath.Join(dir, "card.json")
	judgeJSON(t, name, card)
	return name
}

func TestJudgeTrialEndToEnd(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	cmoa := fakeTrialCMoA(t, dir)
	card := trialCard(t, dir, suite, []string{"i1", "i2"}, nil)
	out := filepath.Join(dir, "out")

	var stdout, stderr strings.Builder
	code := executeWith(context.Background(), []string{
		"judge", "trial", "--card", card, "--cmoa", cmoa, "--out", out, "--vault", dir,
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("want exit %d, got %d: %s", exitOK, code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != filepath.Join(out, judge.TrialReportFile) {
		t.Fatalf("want the report path on stdout, got %q", got)
	}

	body, err := os.ReadFile(filepath.Join(out, judge.TrialReportFile))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report judge.TrialReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Planned != 2 || report.Completed != 2 || report.Interrupted != 0 {
		t.Fatalf("want two items completed, got %d/%d/%d",
			report.Planned, report.Completed, report.Interrupted)
	}
	if report.StopReason != judge.StopCompleted {
		t.Fatalf("want stop reason %s, got %s", judge.StopCompleted, report.StopReason)
	}
	// A control condition changes nothing, which is the assertion that says
	// the two halves of the comparison were read the same way.
	if len(report.ChangedSelections) != 0 {
		t.Fatalf("a control condition changed a selection: %v", report.ChangedSelections)
	}
	if report.Quality.DeltaPoints != 0 {
		t.Fatalf("want ΔQ 0 for a control, got %v", report.Quality.DeltaPoints)
	}
	if report.Card.Hypothesis == "" {
		t.Fatal("the card travels into the report; a threshold moved afterwards has to be visible")
	}
	// The report is committed and the configurations are local files. Neither
	// the card's own directory nor the runner's absolute paths belong in it.
	if strings.Contains(string(body), dir) {
		t.Fatalf("the report carries this machine's paths:\n%s", body)
	}
	summary, err := os.ReadFile(filepath.Join(out, judge.TrialSummaryFile))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(summary), "no selection changed") {
		t.Fatalf("want the summary to say no selection changed:\n%s", summary)
	}
}

func TestJudgeTrialRefusesBeforeSpending(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	cmoa := fakeTrialCMoA(t, dir)

	for _, row := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"a configuration that changed since the card was written", func(card map[string]any) {
			card["candidate"] = map[string]any{
				"id": "cand-1", "config": filepath.Join(dir, "cand", "cmoa.json"),
				"config_sha256": strings.Repeat("0", 64),
			}
		}, "the card pinned"},
		{"an item the suite does not hold", func(card map[string]any) {
			judgeJSON(t, filepath.Join(dir, "set-d.json"), map[string]any{
				"schema_version": 1, "set": "D",
				"items": []map[string]any{{"id": "nobody"}},
			})
		}, "which suite"},
	} {
		t.Run(row.name, func(t *testing.T) {
			card := trialCard(t, dir, suite, []string{"i1"}, row.mutate)
			var stdout, stderr strings.Builder
			code := executeWith(context.Background(), []string{
				"judge", "trial", "--card", card, "--cmoa", cmoa,
				"--out", filepath.Join(t.TempDir(), "out"),
			}, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("want exit %d, got %d: %s", exitUsage, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), row.want) {
				t.Fatalf("want an error mentioning %q, got %s", row.want, stderr.String())
			}
		})
	}
}

func TestJudgeTrialDryRun(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	cmoa := fakeTrialCMoA(t, dir)
	card := trialCard(t, dir, suite, []string{"i1", "i2"}, nil)
	out := filepath.Join(dir, "dry")

	var stdout, stderr strings.Builder
	code := executeWith(context.Background(), []string{
		"judge", "trial", "--card", card, "--cmoa", cmoa, "--out", out, "--dry-run",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("want exit %d, got %d: %s", exitOK, code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "4 step(s) over 2 item(s)") {
		t.Fatalf("want the plan printed, got %s", stderr.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a dry run spent nothing and wrote nothing")
	}
}
