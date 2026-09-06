package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/judge"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// judgeWrite writes a file, making the directories on the way.
func judgeWrite(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func judgeJSON(t *testing.T, name string, value any) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	judgeWrite(t, name, string(body)+"\n")
}

// judgeSuite writes a two-item chat suite whose gold labels are c1 and c2.
func judgeSuite(t *testing.T, dir string) string {
	t.Helper()
	tasks := []map[string]string{
		{"id": "i1", "dir": "i1", "stratum": "wide"},
		{"id": "i2", "dir": "i2", "stratum": "narrow"},
	}
	gold := map[string]string{"i1": "c1", "i2": "c2"}
	for _, task := range tasks {
		base := filepath.Join(dir, task["dir"])
		judgeJSON(t, filepath.Join(base, "task.json"), map[string]any{
			"version": 3, "id": task["id"], "face": "chat", "conversation": "conversation.json",
		})
		judgeJSON(t, filepath.Join(base, "conversation.json"),
			[]map[string]string{{"role": "user", "content": "the question"}})
		judgeJSON(t, filepath.Join(base, "gold.json"), map[string]any{
			"schema_version": 1, "gold": gold[task["id"]], "margin_stratum": task["stratum"],
			"method": "acyclic-majority",
		})
		for _, position := range judge.Positions {
			judgeWrite(t, filepath.Join(base, "candidates", position+".txt"), "answer "+position+"\n")
		}
	}
	manifest := filepath.Join(dir, "suite.json")
	judgeJSON(t, manifest, map[string]any{
		"schema_version": 1, "id": "suite-chat-test", "face": "chat",
		"split": "calibration", "source": "a corpus", "license": "CC-BY-4.0", "tasks": tasks,
	})
	return manifest
}

// fakeJudgeCMoA writes a shell script that answers `judge` the way the harness
// does: it makes a run directory, writes the record, prints the outcome and
// prints the directory. The judge always picks the candidate named after the
// task's gold, so the calibration comes out perfect and the arithmetic is not
// what is under test here — the wiring is.
func fakeJudgeCMoA(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake harness is a shell script")
	}
	runs := filepath.Join(dir, "runs")
	script := `#!/bin/sh
set -eu
if [ "${1:-}" != "judge" ]; then
  echo "unknown command ${1:-}" >&2
  exit 2
fi
shift
if [ "${1:-}" = "--help" ]; then
  echo "judge --task <dir> --candidate <file>..."
  exit 0
fi
task=""
seed=0
while [ $# -gt 0 ]; do
  case "$1" in
    --task) task="$2"; shift 2 ;;
    --candidate) shift 2 ;;
    --seed) seed="$2"; shift 2 ;;
    --config) shift 2 ;;
    *) shift ;;
  esac
done
gold=$(sed -n 's/.*"gold": "\([^"]*\)".*/\1/p' "$task/gold.json")
name=$(basename "$task")
dir="` + runs + `/$name-$seed"
mkdir -p "$dir"
cat > "$dir/judge.json" <<EOF
{"schema_version":1,"run_id":"$name-$seed","candidates":["c1","c2","c3"],
 "pairs":[
  {"pair":["c1","c2"],"orders":[
    {"first":"c1","second":"c2","choice":"A","choice_candidate":"$gold","status":"ok"},
    {"first":"c2","second":"c1","choice":"B","choice_candidate":"$gold","status":"ok"}],
   "verdict":"$gold"},
  {"pair":["c1","c3"],"orders":[
    {"first":"c1","second":"c3","choice":"A","choice_candidate":"c1","status":"ok"},
    {"first":"c3","second":"c1","choice":"B","choice_candidate":"c1","status":"ok"}],
   "verdict":"c1"},
  {"pair":["c2","c3"],"orders":[
    {"first":"c2","second":"c3","choice":"A","choice_candidate":"c2","status":"ok"},
    {"first":"c3","second":"c2","choice":"B","choice_candidate":"c2","status":"ok"}],
   "verdict":"c2"}],
 "wins":{"$gold":2},
 "outcome":{"kind":"selected","candidate_id":"$gold","reason":"condorcet winner"},
 "swap_consistent_pairs":3,"invalid_output_retries":0,"latency_ms":1234}
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

// judgeConfig writes a harness configuration that names a judge.
func judgeConfig(t *testing.T, dir, model string) string {
	t.Helper()
	name := filepath.Join(dir, "cmoa.json")
	body := map[string]any{"version": 2}
	if model != "" {
		body["judge"] = map[string]any{"model": model, "temperature": 0}
	}
	judgeJSON(t, name, body)
	return name
}

func TestJudgeCalibrateEndToEnd(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	bin := fakeJudgeCMoA(t, dir)
	config := judgeConfig(t, dir, "gpt-oss-20b")
	vault := filepath.Join(dir, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
		"--vault", vault, "--rerun", "1", "--as-of", "2026-09-06")
	if got.code != exitOK {
		t.Fatalf("calibrate exit = %d\nstderr:\n%s", got.code, got.stderr)
	}
	id := strings.TrimSpace(got.stdout)
	if id != "calibration/gpt-oss-20b@2026-09-06" {
		t.Fatalf("calibrate named the document %q", id)
	}

	relative, err := vocab.Path(vocab.KindCalibration, id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("the document was not written: %v", err)
	}
	for _, want := range []string{
		"kind: calibration",
		"verdict: calibrated",
		"tie_handling: abstain-as-category",
		"human_kappa: \"1.000\"",
		"in_force_until: \"2026-10-07\"",
		"report: calibrations/gpt-oss-20b@2026-09-06/report.json",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("the document does not carry %q:\n%s", want, body)
		}
	}

	// The report the document names has to be there: a record pointing at a
	// file nobody can open is a record nobody can check.
	report := filepath.Join(vault, "calibrations", "gpt-oss-20b@2026-09-06", judge.ReportFile)
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("the report the document names is missing: %v", err)
	}
	journal, err := os.ReadFile(filepath.Join(filepath.Dir(report), judge.ItemsFile))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(string(journal)), "\n") + 1; lines != 2 {
		t.Fatalf("the journal holds %d lines, want one per item", lines)
	}

	// A second calibration of one judge on one day takes the next sequence
	// number rather than overwriting the first.
	again := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
		"--vault", vault, "--rerun", "0", "--as-of", "2026-09-06")
	if again.code != exitOK {
		t.Fatalf("the second calibrate exited %d\nstderr:\n%s", again.code, again.stderr)
	}
	if strings.TrimSpace(again.stdout) != id+"-1" {
		t.Fatalf("the second calibration is called %q", strings.TrimSpace(again.stdout))
	}
}

// TestJudgeCalibrateRefusesBeforeSpending is the whole reason the checks are
// at the top of the command: a judge run costs an hour, and every one of these
// is knowable in a millisecond.
func TestJudgeCalibrateRefusesBeforeSpending(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	bin := fakeJudgeCMoA(t, dir)
	vault := t.TempDir()

	t.Run("a configuration with no judge", func(t *testing.T) {
		got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin,
			"--config", judgeConfig(t, t.TempDir(), ""), "--vault", vault)
		if got.code != exitUsage {
			t.Fatalf("exit = %d, want %d\n%s", got.code, exitUsage, got.stderr)
		}
		if !strings.Contains(got.stderr, "declares no judge") {
			t.Fatalf("stderr = %q", got.stderr)
		}
	})

	t.Run("a harness that cannot judge", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "cmoa")
		if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil { //nolint:gosec // a fake binary
			t.Fatalf("write: %v", err)
		}
		got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", other,
			"--config", judgeConfig(t, t.TempDir(), "m"), "--vault", vault)
		if got.code != exitUsage {
			t.Fatalf("exit = %d, want %d\n%s", got.code, exitUsage, got.stderr)
		}
		if !strings.Contains(got.stderr, "no judge command") {
			t.Fatalf("stderr = %q", got.stderr)
		}
	})

	t.Run("a suite of the wrong face", func(t *testing.T) {
		coding := filepath.Join(t.TempDir(), "suite.json")
		judgeJSON(t, coding, map[string]any{
			"schema_version": 1, "id": "suite-go", "face": "coding",
			"tasks": []map[string]string{{"id": "a"}},
		})
		got := run(t, "judge", "calibrate", "--suite", coding, "--cmoa", bin,
			"--config", judgeConfig(t, t.TempDir(), "m"), "--vault", vault)
		if got.code != exitUsage {
			t.Fatalf("exit = %d, want %d\n%s", got.code, exitUsage, got.stderr)
		}
	})

	// Nothing above may have written a document.
	entries, err := os.ReadDir(filepath.Join(vault, filepath.FromSlash(vocab.DirCalibrations)))
	if err == nil && len(entries) > 0 {
		t.Fatalf("a refused calibration still wrote %d document(s)", len(entries))
	}
}

func TestJudgeCalibrateDryRun(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	vault := t.TempDir()
	got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", fakeJudgeCMoA(t, dir),
		"--config", judgeConfig(t, dir, "gpt-oss-20b"), "--vault", vault, "--dry-run")
	if got.code != exitOK {
		t.Fatalf("dry run exit = %d\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "dry run") {
		t.Fatalf("stderr = %q", got.stderr)
	}
	if _, err := os.Stat(filepath.Join(vault, filepath.FromSlash(vocab.DirCalibrations))); err == nil {
		t.Fatal("a dry run wrote into the vault")
	}
}

// TestJudgeImportMTBench drives the whole import against a rows endpoint of
// the same shape as the real one, with three systems whose comparisons are
// transitive.
func TestJudgeImportMTBench(t *testing.T) {
	turn := func(question int, model string) []map[string]string {
		return []map[string]string{
			{"role": "user", "content": fmt.Sprintf("question %d", question)},
			{"role": "assistant", "content": "answer from " + model},
		}
	}
	type row struct {
		QuestionID    int                 `json:"question_id"`
		ModelA        string              `json:"model_a"`
		ModelB        string              `json:"model_b"`
		Winner        string              `json:"winner"`
		Judge         string              `json:"judge"`
		Turn          int                 `json:"turn"`
		ConversationA []map[string]string `json:"conversation_a"`
		ConversationB []map[string]string `json:"conversation_b"`
	}
	var rows []map[string]any
	for question := 81; question < 89; question++ {
		for _, pair := range [][3]string{
			{"alpha", "beta", "model_a"}, {"beta", "gamma", "model_a"}, {"alpha", "gamma", "model_a"},
		} {
			rows = append(rows, map[string]any{"row": row{
				QuestionID: question, ModelA: pair[0], ModelB: pair[1], Winner: pair[2],
				Judge: "person-1", Turn: 1,
				ConversationA: turn(question, pair[0]), ConversationB: turn(question, pair[1]),
			}})
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		length, _ := strconv.Atoi(r.URL.Query().Get("length"))
		end := min(offset+length, len(rows))
		page := []map[string]any{}
		if offset < len(rows) {
			page = rows[offset:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"num_rows_total": len(rows), "rows": page})
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "suite-chat")
	got := run(t, "judge", "import-mtbench", "--out", out, "--endpoint", server.URL+"/rows",
		"--target", "4", "--seed", "9")
	if got.code != exitOK {
		t.Fatalf("import exit = %d\nstderr:\n%s", got.code, got.stderr)
	}

	var suite struct {
		Face    string `json:"face"`
		Split   string `json:"split"`
		License string `json:"license"`
		Tasks   []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	body, err := os.ReadFile(filepath.Join(out, "suite.json"))
	if err != nil {
		t.Fatalf("read suite: %v", err)
	}
	if err := json.Unmarshal(body, &suite); err != nil {
		t.Fatalf("suite.json: %v", err)
	}
	if suite.Face != "chat" || suite.Split != "calibration" || suite.License != "CC-BY-4.0" {
		t.Fatalf("suite = %+v", suite)
	}
	if len(suite.Tasks) != 4 {
		t.Fatalf("the suite holds %d items, want 4", len(suite.Tasks))
	}
	for _, task := range suite.Tasks {
		if !vocab.ValidTaskID(task.ID) {
			t.Fatalf("%q is not a task identifier", task.ID)
		}
	}
	// The attribution has to name the licence and the paper: a derived corpus
	// that does not say where it came from is a licence breach, not a style
	// mistake.
	attribution, err := os.ReadFile(filepath.Join(out, "ATTRIBUTION.md"))
	if err != nil {
		t.Fatalf("read attribution: %v", err)
	}
	for _, want := range []string{"cc-by-4.0", "arXiv:2306.05685", "lmsys/mt_bench_human_judgments"} {
		if !strings.Contains(string(attribution), want) {
			t.Fatalf("ATTRIBUTION.md does not carry %q", want)
		}
	}

	// The suite the import wrote is one a calibration can be run over.
	dir := t.TempDir()
	calibrated := run(t, "judge", "calibrate", "--suite", filepath.Join(out, "suite.json"),
		"--cmoa", fakeJudgeCMoA(t, dir), "--config", judgeConfig(t, dir, "gpt-oss-20b"),
		"--vault", t.TempDir(), "--rerun", "0", "--as-of", "2026-09-06")
	if calibrated.code != exitOK {
		t.Fatalf("calibrating the imported suite exited %d\n%s", calibrated.code, calibrated.stderr)
	}
}

// TestJudgeStatus runs the reading against the repository's own configuration,
// which is what makes it a test of the period rather than of a fixture.
func TestJudgeStatus(t *testing.T) {
	engine := requireDocDag(t)
	vault := harnessVault(t)
	if err := os.RemoveAll(filepath.Join(vault, "spec", "calibrations")); err != nil {
		t.Fatalf("clear the calibrations: %v", err)
	}
	// A calibration whose window closed inside the period, and one whose
	// window closed long before it.
	for _, day := range []string{"2026-09-01", "2026-01-01"} {
		id := "calibration/test-judge@" + day
		relative, err := vocab.Path(vocab.KindCalibration, id)
		if err != nil {
			t.Fatal(err)
		}
		judgeWrite(t, filepath.Join(vault, filepath.FromSlash(relative)), strings.Join([]string{
			"---", "id: " + id, "kind: calibration", "title: a calibration",
			"date: \"" + day + "\"", "judge: test-judge", "pool: external",
			"window_from: \"" + day + "\"", "window_to: \"" + day + "\"",
			"in_force_until: \"" + until(t, day) + "\"", "n_items: \"10\"",
			"tie_handling: abstain-as-category", "swap_kappa: \"0.900\"",
			"rerun_kappa: \"0.950\"", "human_kappa: \"0.700\"", "n_human: \"10\"",
			"verdict: calibrated", "report: calibrations/x/report.json", "---", "",
			"# a calibration", "", "body", "",
		}, "\n"))
	}

	got := run(t, "judge", "status", "--vault", vault, "--docdag", engine, "--as-of", "2026-09-05")
	if got.code != exitOK {
		t.Fatalf("status exit = %d\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "binding calibrations as of 2026-09-05: 1") {
		t.Fatalf("stdout:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "calibration/test-judge@2026-09-01") {
		t.Fatalf("the fresh calibration is not listed:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "calibration/test-judge@2026-01-01") {
		t.Fatalf("an expired calibration is still listed as binding:\n%s", got.stdout)
	}

	// Far enough past the newest window and nothing binds, which is the
	// warning the kind exists for and a non-zero exit.
	stale := run(t, "judge", "status", "--vault", vault, "--docdag", engine, "--as-of", "2026-12-01")
	if stale.code == exitOK {
		t.Fatalf("an unbacked judge exited zero:\n%s", stale.stdout)
	}
	if !strings.Contains(stale.stdout, "validity not measured for") {
		t.Fatalf("stdout:\n%s", stale.stdout)
	}
}

// until is the day a calibration written on day stops binding.
func until(t *testing.T, day string) string {
	t.Helper()
	out, err := exec.Command("date", "-u", "-d", day+" +30 days", "+%Y-%m-%d").Output()
	if err != nil {
		t.Skipf("no date(1) to compute the period with: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestCalibrationDocumentValidatesInTheVault is the one check the unit tests
// cannot make: that what `calibrate` writes is a document the real
// configuration accepts. The body is a page of prose the report generates, and
// a summary that grew a raw tab or a wikilink nobody meant would only show up
// here.
func TestCalibrationDocumentValidatesInTheVault(t *testing.T) {
	engine := requireDocDag(t)
	// The repository's own configuration and corpus, and nothing else: the
	// point is that a generated calibration is at home in the real vault.
	vault := judgeVault(t)
	dir := t.TempDir()
	got := run(t, "judge", "calibrate",
		"--suite", judgeSuite(t, filepath.Join(dir, "suite")),
		"--cmoa", fakeJudgeCMoA(t, dir), "--config", judgeConfig(t, dir, "gpt-oss-20b"),
		"--vault", vault, "--rerun", "1", "--as-of", "2026-09-06")
	if got.code != exitOK {
		t.Fatalf("calibrate exit = %d\n%s", got.code, got.stderr)
	}
	cmd := exec.Command(engine, "validate")
	cmd.Dir = vault
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docdag validate refused the calibration:\n%s", out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Fatalf("docdag validate said:\n%s", out)
	}
}

// judgeVault copies the repository's configuration and corpus into a
// temporary vault and empties the calibration directory.
//
// The emptying is the point. The repository now carries a real calibration of
// a real judge, and a test about what `status` says has to own the documents
// it is asking about — otherwise it passes or fails on whether somebody
// re-measured the fleet this month.
func judgeVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	copyTree(t, filepath.Join("..", ".."), vault, "docdag.yaml")
	copyTree(t, filepath.Join("..", "..", "spec"), filepath.Join(vault, "spec"), "")
	if err := os.RemoveAll(filepath.Join(vault, "spec", "calibrations")); err != nil {
		t.Fatalf("clear the calibrations: %v", err)
	}
	return vault
}

// copyTree copies one file, or a directory's files, into a temporary vault.
func copyTree(t *testing.T, from, to, only string) {
	t.Helper()
	if only != "" {
		judgeWrite(t, filepath.Join(to, only), readFile(t, filepath.Join(from, only)))
		return
	}
	err := filepath.WalkDir(from, func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(from, p)
		if relErr != nil {
			return relErr
		}
		judgeWrite(t, filepath.Join(to, relative), readFile(t, p))
		return nil
	})
	if err != nil {
		t.Fatalf("copy %s: %v", from, err)
	}
}

// TestJudgeCalibrateSupersedesTheLastOne is the "a contradicted calibration
// keeps binding" defect. The second measurement of one judge declares the
// first, and the projection that reads the edge is what takes the first out of
// `binding` — the document itself is append-only and is never touched.
func TestJudgeCalibrateSupersedesTheLastOne(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	bin := fakeJudgeCMoA(t, dir)
	config := judgeConfig(t, dir, "gpt-oss-20b")
	vault := t.TempDir()

	first := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
		"--vault", vault, "--rerun", "0", "--as-of", "2026-09-06")
	if first.code != exitOK {
		t.Fatalf("the first calibrate exited %d\n%s", first.code, first.stderr)
	}
	second := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
		"--vault", vault, "--rerun", "0", "--as-of", "2026-09-20")
	if second.code != exitOK {
		t.Fatalf("the second calibrate exited %d\n%s", second.code, second.stderr)
	}
	body := judgeRead(t, vault, strings.TrimSpace(second.stdout))
	if !strings.Contains(body, "supersedes:") ||
		!strings.Contains(body, strings.TrimSpace(first.stdout)) {
		t.Fatalf("the second calibration does not replace the first:\n%s", body)
	}
	// The first document is untouched: it is history, and the vault has no way
	// to mark it retired that would not be a rewrite.
	if strings.Contains(judgeRead(t, vault, strings.TrimSpace(first.stdout)), "superseded") {
		t.Fatal("the earlier calibration was edited")
	}

	// And a third on a day past the first's period declares nothing, because
	// there is nothing still standing to replace.
	third := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
		"--vault", vault, "--rerun", "0", "--as-of", "2026-12-01")
	if third.code != exitOK {
		t.Fatalf("the third calibrate exited %d\n%s", third.code, third.stderr)
	}
	if strings.Contains(judgeRead(t, vault, strings.TrimSpace(third.stdout)), "supersedes:") {
		t.Fatal("a calibration replaced one that had already expired")
	}
}

// judgeRead reads a document out of a vault by its identifier.
func judgeRead(t *testing.T, vault, id string) string {
	t.Helper()
	relative, err := vocab.Path(vocab.KindCalibration, id)
	if err != nil {
		t.Fatalf("%q: %v", id, err)
	}
	body, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return string(body)
}

// TestJudgeCalibrateNeverOverwritesAReport is the shared-directory defect: two
// append-only documents naming one report is a document whose evidence is
// another measurement's numbers.
func TestJudgeCalibrateNeverOverwritesAReport(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	bin := fakeJudgeCMoA(t, dir)
	config := judgeConfig(t, dir, "gpt-oss-20b")
	vault := t.TempDir()
	out := filepath.Join(dir, "named")

	var reports []string
	for range 2 {
		got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
			"--vault", vault, "--out", out, "--rerun", "0", "--as-of", "2026-09-06")
		if got.code != exitOK {
			t.Fatalf("calibrate exited %d\n%s", got.code, got.stderr)
		}
		body := judgeRead(t, vault, strings.TrimSpace(got.stdout))
		for _, line := range strings.Split(body, "\n") {
			if after, found := strings.CutPrefix(line, "report: "); found {
				reports = append(reports, strings.TrimSpace(after))
			}
		}
	}
	if len(reports) != 2 {
		t.Fatalf("read %d report paths", len(reports))
	}
	if reports[0] == reports[1] {
		t.Fatalf("both calibrations name %s; the second overwrote the first's evidence", reports[0])
	}
	for _, report := range reports {
		if _, err := os.Stat(filepath.Join(vault, filepath.FromSlash(report))); err != nil {
			t.Fatalf("report %s: %v", report, err)
		}
	}
}

// TestJudgeCalibrateNeedsTheAnswers is the redistribution rule at the point it
// bites: a fresh clone has the suite and no model answers in it, and finding
// that out two hours into a run is not the same as being told now.
func TestJudgeCalibrateNeedsTheAnswers(t *testing.T) {
	dir := t.TempDir()
	suite := judgeSuite(t, filepath.Join(dir, "suite"))
	if err := os.RemoveAll(filepath.Join(dir, "suite", "i2", "candidates")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", fakeJudgeCMoA(t, dir),
		"--config", judgeConfig(t, dir, "gpt-oss-20b"), "--vault", t.TempDir(), "--rerun", "0")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d\n%s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "import-mtbench") {
		t.Fatalf("the message does not say how to fix it: %q", got.stderr)
	}
}

// TestJudgeStatusBoundaryDay pins the off-by-one. DocDag reads `period.until`
// as exclusive, so "in force for thirty days after the window" is
// window_to + 31 in the frontmatter and window_to + 30 as the last day that
// binds. The day itself is what this checks.
func TestJudgeStatusBoundaryDay(t *testing.T) {
	engine := requireDocDag(t)
	vault := judgeVault(t)
	dir := t.TempDir()
	got := run(t, "judge", "calibrate",
		"--suite", judgeSuite(t, filepath.Join(dir, "suite")),
		"--cmoa", fakeJudgeCMoA(t, dir), "--config", judgeConfig(t, dir, "gpt-oss-20b"),
		"--vault", vault, "--rerun", "0", "--as-of", "2026-09-06")
	if got.code != exitOK {
		t.Fatalf("calibrate exit = %d\n%s", got.code, got.stderr)
	}

	// The thirtieth day: still binding, and nothing to warn about.
	last := run(t, "judge", "status", "--vault", vault, "--docdag", engine, "--as-of", "2026-10-06")
	if last.code != exitOK {
		t.Fatalf("on the last day in force the status exited %d:\n%s", last.code, last.stdout)
	}
	if !strings.Contains(last.stdout, "in force through 2026-10-06") {
		t.Fatalf("stdout:\n%s", last.stdout)
	}
	if !strings.Contains(last.stdout, "binding calibrations as of 2026-10-06: 1") {
		t.Fatalf("the calibration stopped binding a day early:\n%s", last.stdout)
	}

	// The day after: nothing binds, and the warning says how long it has been.
	past := run(t, "judge", "status", "--vault", vault, "--docdag", engine, "--as-of", "2026-10-07")
	if past.code == exitOK {
		t.Fatalf("an expired calibration exited zero:\n%s", past.stdout)
	}
	if !strings.Contains(past.stdout, "binding calibrations as of 2026-10-07: 0") {
		t.Fatalf("stdout:\n%s", past.stdout)
	}
	if !strings.Contains(past.stdout, "validity not measured for 31 days") {
		t.Fatalf("stdout:\n%s", past.stdout)
	}
}

// TestCalibrateWritesNoAbsolutePath is the guard on the files that get
// committed. The fake harness writes its traces into a directory outside the
// vault, which is what a real one does when its configuration points there,
// and nothing in the report or the journal may name that directory.
func TestCalibrateWritesNoAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	vault := t.TempDir()
	suite := judgeSuite(t, filepath.Join(vault, "examples", "suite-chat"))
	// The harness's run directories live under dir, which is not under the
	// vault and not under the suite.
	bin := fakeJudgeCMoA(t, dir)

	got := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin,
		"--config", judgeConfig(t, dir, "gpt-oss-20b"), "--vault", vault,
		"--rerun", "1", "--as-of", "2026-09-06")
	if got.code != exitOK {
		t.Fatalf("calibrate exit = %d\n%s", got.code, got.stderr)
	}
	out := filepath.Join(vault, "calibrations", "gpt-oss-20b@2026-09-06")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	for _, name := range []string{judge.ReportFile, judge.ItemsFile} {
		body := readFile(t, filepath.Join(out, name))
		for _, forbidden := range []string{dir, vault, home} {
			if forbidden == "" || forbidden == "/" {
				continue
			}
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s carries the absolute path %s", name, forbidden)
			}
		}
	}
	// And the journal's own field says so rather than the test inferring it
	// from a string search.
	var item judge.ItemResult
	line, _, _ := strings.Cut(readFile(t, filepath.Join(out, judge.ItemsFile)), "\n")
	if err := json.Unmarshal([]byte(line), &item); err != nil {
		t.Fatalf("journal line: %v", err)
	}
	if len(item.Runs) == 0 {
		t.Fatal("the journal records no run")
	}
	for _, r := range item.Runs {
		if filepath.IsAbs(r.RunDir) {
			t.Fatalf("run_dir = %q, which is absolute", r.RunDir)
		}
		if r.RunID == "" {
			t.Fatal("a trace outside the vault lost its identifier as well as its path")
		}
	}
	// The document the vault keeps names its report relative to the vault too.
	body := judgeRead(t, vault, strings.TrimSpace(got.stdout))
	if strings.Contains(body, vault) || strings.Contains(body, home) {
		t.Fatalf("the calibration document carries an absolute path:\n%s", body)
	}
}

// TestJudgeStatusExitsNonZeroOnAWarning is the contract the README states: a
// pipeline can gate on this command. A binding calibration that says the judge
// does not agree with people is the case that used to print a line and exit
// zero — which is a gate that passes while saying the thing it gates on is
// wrong.
func TestJudgeStatusExitsNonZeroOnAWarning(t *testing.T) {
	engine := requireDocDag(t)
	vault := judgeVault(t)

	// A calibration in force, whose verdict is that the judge does not agree
	// with people. Nothing else is wrong: it is fresh, it is binding, and its
	// validity was measured.
	id := "calibration/test-judge@2026-09-05"
	relative, err := vocab.Path(vocab.KindCalibration, id)
	if err != nil {
		t.Fatal(err)
	}
	judgeWrite(t, filepath.Join(vault, "calibrations", "x", "report.json"), "{}\n")
	judgeWrite(t, filepath.Join(vault, filepath.FromSlash(relative)), strings.Join([]string{
		"---", "id: " + id, "kind: calibration", "title: a calibration",
		"date: \"2026-09-05\"", "judge: test-judge", "pool: external",
		"window_from: \"2026-09-05\"", "window_to: \"2026-09-05\"",
		"in_force_until: \"2026-10-06\"", "n_items: \"200\"",
		"tie_handling: abstain-as-category", "swap_kappa: \"0.589\"",
		"rerun_kappa: \"0.820\"", "human_kappa: \"0.281\"", "n_human: \"200\"",
		"verdict: uncalibrated", "report: calibrations/x/report.json", "---", "",
		"# a calibration", "", "body", "",
	}, "\n"))

	got := run(t, "judge", "status", "--vault", vault, "--docdag", engine, "--as-of", "2026-09-20")
	if !strings.Contains(got.stdout, "is binding and says `uncalibrated`") {
		t.Fatalf("no warning about a judge measured and rejected:\n%s", got.stdout)
	}
	if got.code == exitOK {
		t.Fatalf("status printed a warning and exited zero:\n%s", got.stdout)
	}

	// The same vault a day after the calibration expires: still non-zero, for
	// the other reason.
	expired := run(t, "judge", "status", "--vault", vault, "--docdag", engine, "--as-of", "2026-10-07")
	if expired.code == exitOK {
		t.Fatalf("an expired calibration exited zero:\n%s", expired.stdout)
	}
}

// TestJudgeCalibrateReplay drives the rebuild through the command: the report
// is recomputed from the traces it already read, no judge is asked anything,
// and the vault document is not touched.
func TestJudgeCalibrateReplay(t *testing.T) {
	vault := t.TempDir()
	suite := judgeSuite(t, filepath.Join(vault, "suite"))
	bin := fakeJudgeCMoA(t, vault)
	config := judgeConfig(t, vault, "gpt-oss-20b")

	first := run(t, "judge", "calibrate", "--suite", suite, "--cmoa", bin, "--config", config,
		"--vault", vault, "--rerun", "1", "--as-of", "2026-09-06")
	if first.code != exitOK {
		t.Fatalf("calibrate exit = %d\nstderr:\n%s", first.code, first.stderr)
	}
	dir := filepath.Join(vault, "calibrations", "gpt-oss-20b@2026-09-06")
	documents := filepath.Join(vault, filepath.FromSlash(vocab.DirCalibrations))
	before, err := os.ReadDir(documents)
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}

	got := run(t, "judge", "calibrate", "--replay", dir, "--vault", vault)
	if got.code != exitOK {
		t.Fatalf("replay exit = %d\nstderr:\n%s", got.code, got.stderr)
	}
	if want := filepath.Join(dir, judge.ReportFile); strings.TrimSpace(got.stdout) != want {
		t.Fatalf("replay printed %q, want the report it rebuilt", strings.TrimSpace(got.stdout))
	}

	// The traces are all ok calls that named one of the two answers, three of
	// each per run, so the judge named the first answer exactly half the time.
	var report struct {
		Swap struct {
			Position struct {
				DecidedCalls int     `json:"decided_calls"`
				FirstChosen  int     `json:"first_chosen"`
				PFirst       float64 `json:"p_first"`
				CIMethod     string  `json:"ci_method"`
			} `json:"position"`
		} `json:"swap"`
	}
	body, err := os.ReadFile(filepath.Join(dir, judge.ReportFile))
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode the report: %v", err)
	}
	position := report.Swap.Position
	if position.DecidedCalls != 24 || position.FirstChosen != 12 || position.PFirst != 0.5 {
		t.Fatalf("position = %+v, want twelve of twenty-four", position)
	}
	if position.CIMethod != "cluster-jackknife" {
		t.Fatalf("the position interval is a %q; the calls share items", position.CIMethod)
	}

	// A replay is not a measurement, so it writes no record of one.
	after, err := os.ReadDir(documents)
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("a replay wrote %d document(s) into the vault", len(after)-len(before))
	}
}

// TestJudgeCalibrateReplayRefusesWhatItCannotDo: the flags that say what to
// judge have nothing to do in a rebuild, and a measurement still needs them.
func TestJudgeCalibrateReplayRefusesWhatItCannotDo(t *testing.T) {
	vault := t.TempDir()
	suite := judgeSuite(t, filepath.Join(vault, "suite"))

	refused := run(t, "judge", "calibrate", "--replay", vault, "--suite", suite)
	if refused.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", refused.code, exitUsage, refused.stderr)
	}
	if !strings.Contains(refused.stderr, "drop one of the two") {
		t.Fatalf("the refusal does not say what to do:\n%s", refused.stderr)
	}

	// Without a replay the two flags a measurement cannot do without are still
	// required, and leaving them off is a usage failure rather than a run.
	missing := run(t, "judge", "calibrate", "--vault", vault)
	if missing.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", missing.code, exitUsage, missing.stderr)
	}
	if !strings.Contains(missing.stderr, `required flag(s) "suite", "config" not set`) {
		t.Fatalf("the refusal does not name the flags:\n%s", missing.stderr)
	}
}
