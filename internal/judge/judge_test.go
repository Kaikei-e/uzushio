package judge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/judge"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// script is what a fake judge answers for one item at one seed: the position
// it chose (or the empty string for an abstention), and the three pairs' two
// orders, as the positions each order picked.
type script struct {
	outcome string
	reason  string
	choice  string
	orders  [][2]string
}

// fakeRunner answers from a table, keyed by item and seed. It is the whole
// point of the Runner interface: the arithmetic above it is a unit test.
type fakeRunner struct {
	answers map[string][]script
}

func (f fakeRunner) Judge(_ context.Context, _ judge.Suite, task judge.Task, seed int) (judge.Judged, error) {
	all, ok := f.answers[task.ID]
	if !ok {
		return judge.Judged{}, fmt.Errorf("no script for %s", task.ID)
	}
	s := all[min(seed-judge.DefaultSeed, len(all)-1)]
	out := judge.Judged{
		Seed: seed, Outcome: s.outcome, Candidate: s.choice, Reason: s.reason,
		LatencyMS: 1000,
	}
	pairs := [3][2]string{{"c1", "c2"}, {"c1", "c3"}, {"c2", "c3"}}
	for i, members := range pairs {
		pair := judge.Pair{Members: members}
		for order := range 2 {
			chose := ""
			if i < len(s.orders) {
				chose = s.orders[i][order]
			}
			status := "ok"
			if chose == "" {
				status = "invalid_output"
			}
			pair.Orders = append(pair.Orders, judge.Order{
				First: members[order], Second: members[1-order],
				ChoiceCandidate: chose, Status: status, LatencyMS: 300,
			})
		}
		if pair.Orders[0].ChoiceCandidate == pair.Orders[1].ChoiceCandidate &&
			pair.Orders[0].ChoiceCandidate != "" {
			pair.Verdict = pair.Orders[0].ChoiceCandidate
			out.SwapConsistent++
		} else {
			pair.Verdict = "draw"
		}
		out.Pairs = append(out.Pairs, pair)
	}
	return out, nil
}

// agrees is a script where the judge picks one candidate in both orders of
// every pair it is in, and the loser of the other pair consistently.
func agrees(choice string) script {
	pick := func(a, b string) [2]string {
		if a == choice || b == choice {
			return [2]string{choice, choice}
		}
		return [2]string{a, a}
	}
	return script{
		outcome: judge.OutcomeSelected, choice: choice,
		orders: [][2]string{pick("c1", "c2"), pick("c1", "c3"), pick("c2", "c3")},
	}
}

// flips is a script where every pair comes out the other way when the order is
// reversed, which is what a position-biased judge does.
func flips() script {
	return script{
		outcome: judge.OutcomeNoCandidate, reason: "all_draws",
		orders: [][2]string{{"c1", "c2"}, {"c1", "c3"}, {"c2", "c3"}},
	}
}

// suite writes a suite of items with the given gold labels and returns it.
func suite(t *testing.T, gold map[string]string) judge.Suite {
	t.Helper()
	dir := t.TempDir()
	type entry struct {
		ID      string `json:"id"`
		Dir     string `json:"dir"`
		Stratum string `json:"stratum"`
	}
	var tasks []entry
	for _, id := range sortedKeys(gold) {
		tasks = append(tasks, entry{ID: id, Dir: id, Stratum: "mid"})
		if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		write(t, filepath.Join(dir, id, "gold.json"), map[string]any{
			"gold": gold[id], "margin_stratum": "mid", "method": "acyclic-majority",
		})
	}
	write(t, filepath.Join(dir, "suite.json"), map[string]any{
		"schema_version": 1, "id": "suite-test", "face": "chat",
		"split": "calibration", "tasks": tasks,
	})
	loaded, err := judge.LoadSuite(filepath.Join(dir, "suite.json"))
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	return loaded
}

func sortedKeys(m map[string]string) []string {
	var out []string
	for key := range m {
		out = append(out, key)
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

func write(t *testing.T, name string, value any) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(name, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func calibrate(t *testing.T, s judge.Suite, runner judge.Runner, opts judge.Options) judge.Result {
	t.Helper()
	opts.Suite = s
	opts.Runner = runner
	if opts.Day == "" {
		opts.Day = "2026-09-06"
	}
	if opts.Judge == "" {
		opts.Judge = "test-judge"
	}
	if opts.Pool == "" {
		opts.Pool = doc.PoolExternal
	}
	result, err := judge.Calibrate(context.Background(), opts)
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	return result
}

// TestPerfectAgreement is the arithmetic at its easiest end: the judge picks
// what the people picked on every item, and nothing flips.
func TestPerfectAgreement(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2", "i3": "c3", "i4": "c1", "i5": "c2", "i6": "c3"}
	answers := map[string][]script{}
	for id, choice := range gold {
		answers[id] = []script{agrees(choice)}
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{Reruns: 0})
	report := result.Report

	human := report.Validity[judge.ReferenceHuman]
	if human.Primary.PO != 1 {
		t.Fatalf("p_o = %v, want 1", human.Primary.PO)
	}
	if human.Primary.Kappa.Float() != 1 {
		t.Fatalf("kappa = %v, want 1", human.Primary.Kappa)
	}
	if human.Primary.N != 6 {
		t.Fatalf("n = %d, want 6", human.Primary.N)
	}
	if report.Verdict != vocab.CalibratedYes {
		t.Fatalf("verdict = %s, want %s", report.Verdict, vocab.CalibratedYes)
	}
	if report.Swap.Flips != 0 || report.Swap.Decided != 18 {
		t.Fatalf("swap = %+v, want no flips over 18 decided pairs", report.Swap)
	}
	// The gold and the human reference are the same labels here, and the
	// labels reference has nothing in it — an empty one must not be reported
	// as agreement of zero.
	if report.Validity[judge.ReferenceLabels].Primary.N != 0 {
		t.Fatal("the labels reference counted items nobody labelled")
	}
}

// TestPositionBiasIsVisible is the failure the swap arm exists to catch: a
// judge that answers differently when the candidates change places. It reaches
// no verdict at all, so validity is unmeasurable, and the flip rate says why.
func TestPositionBiasIsVisible(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2", "i3": "c3", "i4": "c1"}
	answers := map[string][]script{}
	for id := range gold {
		answers[id] = []script{flips()}
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{Reruns: 0})
	report := result.Report

	if report.Swap.FlipRate != 1 {
		t.Fatalf("flip rate = %v, want 1", report.Swap.FlipRate)
	}
	if report.Swap.FlipCI.Lo <= 0.5 {
		t.Fatalf("flip CI = %v, want a lower bound well above a coin", report.Swap.FlipCI)
	}
	if report.Outcomes.ByKind[judge.OutcomeNoCandidate] != 4 {
		t.Fatalf("outcomes = %+v", report.Outcomes)
	}
	if report.Outcomes.NoCandidateByReason["all_draws"] != 4 {
		t.Fatalf("no-candidate reasons = %+v", report.Outcomes.NoCandidateByReason)
	}
	// Every answer is an abstention and every gold is a choice, so the two
	// raters have disjoint marginals and observed agreement is zero.
	human := report.Validity[judge.ReferenceHuman]
	if human.Primary.PO != 0 {
		t.Fatalf("p_o = %v, want 0", human.Primary.PO)
	}
	// Under the secondary handling every item is dropped, and a coefficient
	// over nothing must be undefined rather than zero.
	if human.Secondary.N != 0 || human.Secondary.Kappa.Defined() {
		t.Fatalf("decided-only over no decided items = %+v", human.Secondary)
	}
	if report.Verdict != vocab.CalibratedNo {
		t.Fatalf("verdict = %s, want %s", report.Verdict, vocab.CalibratedNo)
	}
}

// TestTieHandlingsDisagree is the reason the handling's name is on every
// number: the same verdicts score differently under the two, and the
// difference is not small.
func TestTieHandlingsDisagree(t *testing.T) {
	gold := map[string]string{}
	answers := map[string][]script{}
	// Six items the judge and the people both decide and agree on, and six the
	// people left undecided and the judge decided. The primary handling counts
	// the second six as disagreements; the secondary drops them.
	for i := range 6 {
		id := fmt.Sprintf("agree%d", i)
		choice := judge.Positions[i%3]
		gold[id] = choice
		answers[id] = []script{agrees(choice)}
	}
	for i := range 6 {
		id := fmt.Sprintf("tie%d", i)
		gold[id] = "tie"
		answers[id] = []script{agrees(judge.Positions[i%3])}
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{Reruns: 0})
	human := result.Report.Validity[judge.ReferenceHuman]

	if human.Primary.PO != 0.5 {
		t.Fatalf("primary p_o = %v, want 0.5 over twelve items", human.Primary.PO)
	}
	if human.Secondary.PO != 1 || human.Secondary.N != 6 {
		t.Fatalf("secondary = p_o %v over %d items, want 1 over 6", human.Secondary.PO, human.Secondary.N)
	}
	if human.Primary.TieHandling != vocab.TieAbstainAsCategory.String() {
		t.Fatalf("primary handling = %q", human.Primary.TieHandling)
	}
	if human.Secondary.TieHandling != vocab.TieDecidedOnly.String() {
		t.Fatalf("secondary handling = %q", human.Secondary.TieHandling)
	}
	// The document body must carry the handling beside the numbers, since a
	// number quoted out of it is quoted without its context.
	summary := result.Report.Summary()
	for _, want := range []string{vocab.TieAbstainAsCategory.String(), vocab.TieDecidedOnly.String()} {
		if !strings.Contains(summary, want) {
			t.Fatalf("the summary does not name the %q handling:\n%s", want, summary)
		}
	}
}

// TestRerunDisagreementIsMeasured drives the second consistency arm: the same
// item answered differently on the second seed.
func TestRerunDisagreementIsMeasured(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2", "i3": "c3", "i4": "c1"}
	answers := map[string][]script{
		"i1": {agrees("c1"), agrees("c2")},
		"i2": {agrees("c2"), agrees("c2")},
		"i3": {agrees("c3"), agrees("c3")},
		"i4": {agrees("c1"), agrees("c1")},
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{Reruns: 1})
	rerun := result.Report.Rerun
	if rerun.Comparisons != 4 {
		t.Fatalf("rerun comparisons = %d, want one per item", rerun.Comparisons)
	}
	if rerun.Agreement.PO != 0.75 {
		t.Fatalf("rerun p_o = %v, want 0.75", rerun.Agreement.PO)
	}
	if result.Report.Seeds != 2 {
		t.Fatalf("seeds = %d, want 2", result.Report.Seeds)
	}
	if len(result.Items[0].Runs) != 2 {
		t.Fatalf("item journal holds %d runs", len(result.Items[0].Runs))
	}
}

// TestNoHumanLabelIsUnmeasured is the rule that keeps a judge from being
// called calibrated on its own consistency.
func TestNoHumanLabelIsUnmeasured(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "suite.json"), map[string]any{
		"schema_version": 1, "id": "suite-test", "face": "chat", "split": "calibration",
		"tasks": []map[string]string{{"id": "i1", "dir": "i1"}},
	})
	if err := os.MkdirAll(filepath.Join(dir, "i1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	loaded, err := judge.LoadSuite(filepath.Join(dir, "suite.json"))
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	result := calibrate(t, loaded, fakeRunner{answers: map[string][]script{"i1": {agrees("c1")}}},
		judge.Options{Reruns: 0})
	if result.Report.Verdict != vocab.CalibratedUnmeasured {
		t.Fatalf("verdict = %s, want %s", result.Report.Verdict, vocab.CalibratedUnmeasured)
	}
	document, err := result.Report.Document("2026-09-06", "calibrations/x/report.json", 0)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if document.HumanKappa != doc.KappaUnmeasured {
		t.Fatalf("human_kappa = %v, want the unmeasured sentinel", document.HumanKappa)
	}
	front, err := document.Frontmatter()
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if front.HumanKappa != vocab.KappaUnmeasured {
		t.Fatalf("human_kappa reads %q", front.HumanKappa)
	}
	// The period is the whole staleness mechanism, and it is derived rather
	// than carried so no document can claim a longer life than it earned.
	if front.InForceUntil != "2026-10-06" {
		t.Fatalf("in_force_until = %q, want thirty days after the window", front.InForceUntil)
	}
}

// TestLabelsBeatGold checks that an explicit human label is preferred to the
// derived one on the items that carry both, and that the ceiling is measured
// where two people labelled the same items.
func TestLabelsBeatGold(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2"}
	answers := map[string][]script{"i1": {agrees("c3")}, "i2": {agrees("c2")}}
	labels := []judge.Label{
		{SchemaVersion: 1, Item: "i1", Labeler: "one", Choice: "c3"},
		{SchemaVersion: 1, Item: "i2", Labeler: "one", Choice: "c2"},
		{SchemaVersion: 1, Item: "i1", Labeler: "two", Choice: "c3"},
		{SchemaVersion: 1, Item: "i2", Labeler: "two", Choice: "c1"},
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers},
		judge.Options{Reruns: 0, Labels: labels})
	report := result.Report

	if got := report.Validity[judge.ReferenceGold].Primary.PO; got != 0.5 {
		t.Fatalf("against gold p_o = %v, want 0.5", got)
	}
	if got := report.Validity[judge.ReferenceHuman].Primary.PO; got != 1 {
		t.Fatalf("against the labels p_o = %v, want 1", got)
	}
	if report.HumanHuman == nil {
		t.Fatal("two people labelled the same items and no ceiling was measured")
	}
	if report.HumanHuman.N != 2 || report.HumanHuman.PO != 0.5 {
		t.Fatalf("ceiling = n %d p_o %v, want 2 and 0.5", report.HumanHuman.N, report.HumanHuman.PO)
	}
	if !strings.Contains(report.Summary(), "ceiling") {
		t.Fatal("the summary does not report the ceiling")
	}
}

func TestReportAndJournalAreWritten(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2"}
	answers := map[string][]script{"i1": {agrees("c1")}, "i2": {agrees("c2")}}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{Reruns: 0})
	dir := filepath.Join(t.TempDir(), "out")
	if err := result.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, judge.ReportFile))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var back judge.Report
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("the report does not read back: %v", err)
	}
	if back.Items != 2 || back.Verdict != vocab.CalibratedYes {
		t.Fatalf("report read back as %+v", back)
	}
	journal, err := os.ReadFile(filepath.Join(dir, judge.ItemsFile))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(journal), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the journal holds %d lines, want one per item", len(lines))
	}
	var first judge.ItemResult
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("journal line: %v", err)
	}
	if first.Item != "i1" || first.Gold != "c1" || first.Runs[0].Category != "c1" {
		t.Fatalf("journal line = %+v", first)
	}
}

func TestCalibrateRefusesTheWrongFace(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "suite.json"), map[string]any{
		"schema_version": 1, "id": "suite-go", "face": "coding",
		"tasks": []map[string]string{{"id": "i1"}},
	})
	loaded, err := judge.LoadSuite(filepath.Join(dir, "suite.json"))
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if _, err := judge.Calibrate(context.Background(), judge.Options{Suite: loaded}); err == nil {
		t.Fatal("a coding suite was accepted for a judge calibration")
	}
}

func TestReadJudged(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, judge.JudgeFile), map[string]any{
		"schema_version": 1,
		"candidates":     []string{"c1", "c2", "c3"},
		"pairs": []map[string]any{{
			"pair": []string{"c1", "c2"},
			"orders": []map[string]any{
				{"first": "c1", "second": "c2", "choice": "A", "choice_candidate": "c1", "status": "ok"},
				{"first": "c2", "second": "c1", "choice": "B", "choice_candidate": "c1", "status": "ok"},
			},
			"verdict": "c1",
		}},
		"outcome":               map[string]any{"kind": "selected", "candidate_id": "c1", "reason": "condorcet"},
		"swap_consistent_pairs": 1,
		"latency_ms":            4200,
		// A key this build does not read must not stop a calibration.
		"injection_flags": map[string]any{"c1": []string{}},
	})
	judged, err := judge.ReadJudged(dir)
	if err != nil {
		t.Fatalf("ReadJudged: %v", err)
	}
	if judged.Category() != "c1" || judged.SwapConsistent != 1 || judged.LatencyMS != 4200 {
		t.Fatalf("judged = %+v", judged)
	}
	if len(judged.Pairs) != 1 || judged.Pairs[0].Members != [2]string{"c1", "c2"} {
		t.Fatalf("pairs = %+v", judged.Pairs)
	}
	write(t, filepath.Join(dir, judge.JudgeFile), map[string]any{"schema_version": 1})
	if _, err := judge.ReadJudged(dir); err == nil {
		t.Fatal("a record with no outcome was accepted")
	}
}

func TestLoadLabels(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "labels.jsonl")
	body := `{"schema_version":1,"item":"ja-017","labeler":"owner","presented":["c3","c1","c2"],` +
		`"choice":"c1","at":"2026-09-05T12:00:00Z","duration_ms":41000,"note":""}` + "\n\n" +
		`{"schema_version":1,"item":"ja-018","labeler":"owner","presented":["c1","c2","c3"],` +
		`"choice":"all_bad","at":"2026-09-05T12:01:00Z","duration_ms":9000,"note":"none of them"}` + "\n"
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	labels, err := judge.LoadLabels([]string{name})
	if err != nil {
		t.Fatalf("LoadLabels: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("read %d labels, want 2", len(labels))
	}
	if labels[0].Category() != "c1" {
		t.Fatalf("a choice of c1 reads as %q", labels[0].Category())
	}
	// all_bad folds into abstention for the coefficient, because a judge has
	// no way to say it and kappa needs one partition.
	if labels[1].Category() != judge.Abstain {
		t.Fatalf("all_bad reads as %q, want %q", labels[1].Category(), judge.Abstain)
	}

	for _, bad := range []string{
		`{"schema_version":2,"item":"a","labeler":"b","choice":"c1"}`,
		`{"schema_version":1,"item":"a","labeler":"b","choice":"best"}`,
		`{"schema_version":1,"labeler":"b","choice":"c1"}`,
		`{"schema_version":1,"item":"a","labeler":"b","choice":"c1","unknown":1}`,
	} {
		if err := os.WriteFile(name, []byte(bad+"\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := judge.LoadLabels([]string{name}); err == nil {
			t.Fatalf("a labels file was accepted: %s", bad)
		}
	}
}

// TestStatusWarnings is the Bainbridge half of the kind: a judge nobody has
// re-checked has to say so out loud, and the day is what decides.
func TestStatusWarnings(t *testing.T) {
	measured := calibration(t, "2026-08-01", 0.7)
	unmeasured := calibration(t, "2026-09-01", doc.KappaUnmeasured)

	fresh := judge.StatusOf("2026-08-10", []string{measured.ID()}, []doc.Calibration{measured})
	if len(fresh.Warnings) != 0 {
		t.Fatalf("a nine-day-old measurement warned: %v", fresh.Warnings)
	}
	if len(fresh.Binding) != 1 || fresh.DaysSinceHuman != 9 {
		t.Fatalf("status = %+v", fresh)
	}

	// The same document, past its period: DocDag stops listing it as binding,
	// and the reading says both that nothing binds and how long it has been.
	stale := judge.StatusOf("2026-09-20", nil, []doc.Calibration{measured})
	if len(stale.Warnings) != 2 {
		t.Fatalf("warnings = %v, want the staleness and the empty binding set", stale.Warnings)
	}
	if !strings.Contains(stale.Warnings[0], "validity not measured for 50 days") {
		t.Fatalf("first warning = %q", stale.Warnings[0])
	}

	// A binding calibration that measured only consistency is the case the
	// whole vocabulary exists to name.
	consistent := judge.StatusOf("2026-09-05", []string{unmeasured.ID()},
		[]doc.Calibration{unmeasured})
	if len(consistent.Warnings) != 2 {
		t.Fatalf("warnings = %v", consistent.Warnings)
	}
	if !strings.Contains(strings.Join(consistent.Warnings, "\n"), "never been measured") {
		t.Fatalf("warnings = %v", consistent.Warnings)
	}
	if !strings.Contains(strings.Join(consistent.Lines(), "\n"), vocab.KappaUnmeasured) {
		t.Fatalf("lines = %v", consistent.Lines())
	}
}

// calibration builds a document to read a status off.
func calibration(t *testing.T, day string, human float64) doc.Calibration {
	t.Helper()
	verdict := vocab.CalibratedYes
	if human == doc.KappaUnmeasured {
		verdict = vocab.CalibratedUnmeasured
	}
	c := doc.Calibration{
		Judge: "test-judge", Day: day, Title: "a calibration", Date: day,
		Pool: doc.PoolExternal, WindowFrom: day, WindowTo: day, NItems: 10,
		TieHandling: vocab.TieAbstainAsCategory,
		SwapKappa:   0.9, RerunKappa: 0.95, HumanKappa: human, NHuman: 10,
		Verdict: verdict, Report: "calibrations/x/report.json", Body: "body",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("the fixture is not a document: %v", err)
	}
	return c
}

// TestKappaRoundTrip is the pair the vault rests on: a coefficient written as
// text and read back as the number it was.
func TestKappaRoundTrip(t *testing.T) {
	for _, value := range []float64{-1, -0.125, 0, 0.667, 1, doc.KappaUnmeasured} {
		text := doc.Kappa(value)
		back, err := doc.ParseKappa(text)
		if err != nil {
			t.Fatalf("ParseKappa(%q): %v", text, err)
		}
		if math.Abs(back-value) > 0.0005 {
			t.Fatalf("%v wrote as %q and read back as %v", value, text, back)
		}
	}
	if _, err := doc.ParseKappa("about a half"); err == nil {
		t.Fatal("a coefficient nobody can parse was accepted")
	}
}
