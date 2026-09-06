package judge_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

// consensus is a script for a run the candidates settled between themselves:
// the harness selected, said so, and made no judge call at all.
func consensus(choice string) script {
	return script{
		outcome: judge.OutcomeSelected, choice: choice,
		reason:    "consensus: 2 of 3 agree on the normalised answer (numeric)",
		consensus: "numeric", tieBreak: "hash",
	}
}

// Which stage settled a run is read off the two structured fields where they
// are there, and off the reason sentence only for the one distinction the
// trace records in words. A sentence this build has not learned is named as
// such rather than folded into a neighbour.
func TestRuleReadsTheStage(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  judge.Judged
		want string
	}{
		{"consensus", judge.Judged{
			Outcome: judge.OutcomeSelected, Candidate: "c1",
			Reason: "consensus: 2 of 3 agree on the normalised answer (exact)",
			// A consensus run breaks a tie inside the group too, and the
			// stage is still consensus.
			Consensus: "exact", TieBreak: "hash",
		}, judge.RuleConsensus},
		{"condorcet", judge.Judged{
			Outcome: judge.OutcomeSelected, Candidate: "c1",
			Reason: "condorcet winner, 2 of 3 pairs agreed under both orders",
		}, judge.RuleCondorcet},
		{"copeland", judge.Judged{
			Outcome: judge.OutcomeSelected, Candidate: "c1",
			// The sentence names Condorcet to say there was none of one.
			Reason: "copeland winner, score 1.5 of 2 (no condorcet winner)",
		}, judge.RuleCopeland},
		{"tie break", judge.Judged{
			Outcome: judge.OutcomeSelected, Candidate: "c1",
			Reason:   "copeland tie, score 1 of 2, tie broken by length among [c1 c2]",
			TieBreak: "length",
		}, judge.RuleTieBreak},
		{"a word this build does not know", judge.Judged{
			Outcome: judge.OutcomeSelected, Candidate: "c1", Reason: "borda count, 4 points",
		}, judge.RuleUnstated},
		{"no candidate", judge.Judged{
			Outcome: judge.OutcomeNoCandidate, Reason: "invalid_output",
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.run.Rule(); got != tc.want {
				t.Errorf("Rule() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A rescoring reads a previous calibration's journal for the run directories
// and for what it concluded, and refuses before spending anything when a run
// it will be asked for is not there.
func TestRescoreReadsTheSourceAndRefusesAGap(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, []map[string]any{
		{"item": "i1", "runs": []map[string]any{
			{"seed": 1, "outcome": "selected", "category": "c1", "measured": true,
				"reason":  "condorcet winner, 2 of 3 pairs agreed under both orders",
				"run_dir": "traces/i1-1"},
			{"seed": 2, "outcome": "no_candidate", "reason": "all_draws", "measured": true,
				"run_dir": "traces/i1-2"},
		}},
		{"item": "i2", "runs": []map[string]any{
			{"seed": 1, "outcome": "selected", "category": "c2", "measured": true,
				"reason":  "condorcet winner, 2 of 3 pairs agreed under both orders",
				"run_dir": "traces/i2-1"},
		}},
	})

	rescore, err := judge.OpenRescore(dir, "/vault")
	if err != nil {
		t.Fatal(err)
	}
	if got := rescore.Seeds(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("Seeds() = %v", got)
	}
	if got := rescore.Reruns(); got != 1 {
		t.Errorf("Reruns() = %d, want 1", got)
	}
	if got, want := rescore.RunDir("i1", 2), filepath.Join("/vault", "traces/i1-2"); got != want {
		t.Errorf("RunDir = %q, want %q", got, want)
	}
	kind, candidate := rescore.Recorded("i1", 1)
	if kind != judge.OutcomeSelected || candidate != "c1" {
		t.Errorf("Recorded = %q/%q", kind, candidate)
	}
	// A no-candidate run names no candidate, however it is spelled.
	if _, candidate := rescore.Recorded("i1", 2); candidate != "" {
		t.Errorf("a no-candidate run recorded candidate %q", candidate)
	}

	// i2 has no second seed. Asking for one is refused before a single
	// invocation, because a missing run would otherwise be reported as a
	// harness that would not start.
	s := suite(t, map[string]string{"i1": "c1", "i2": "c2"})
	err = rescore.Check(s, []int{1, 2})
	if err == nil || !strings.Contains(err.Error(), "i2 seed 2") {
		t.Fatalf("Check with a gap: %v", err)
	}
	if err := rescore.Check(s, []int{1}); err != nil {
		t.Errorf("Check with no gap: %v", err)
	}
}

// The two coefficients a consensus stage makes into two different claims.
//
// A run the candidates settled cost no judge call, so the judge answered for
// nothing on that item. `human` counts it anyway — it is what the harness
// returned, and that is the selector's validity — while the restricted
// reading leaves it out, which is the judge's own. The report carries both
// and the summary says which is which.
func TestJudgeDecidedValidityDropsTheJudgelessRuns(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2", "i3": "c3", "i4": "c1"}
	answers := map[string][]script{
		"i1": {agrees("c1")},
		"i2": {agrees("c2")},
		// The candidates agreed with each other on these two, and on the
		// second of them they agreed on the answer the people did not pick.
		"i3": {consensus("c3")},
		"i4": {consensus("c2")},
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{})
	report := result.Report

	if got := report.Outcomes.Judgeless; got != 2 {
		t.Errorf("judgeless runs %d, want 2", got)
	}
	if got := report.Outcomes.ByRule[judge.RuleConsensus]; got != 2 {
		t.Errorf("consensus runs %d, want 2", got)
	}
	if got := report.Outcomes.ByConsensus["numeric"]; got != 2 {
		t.Errorf("numeric agreements %d, want 2", got)
	}
	if got := report.Outcomes.ByTieBreak["hash"]; got != 2 {
		t.Errorf("hash tie-breaks %d, want 2", got)
	}
	all := report.Validity[judge.ReferenceHuman]
	judged := report.Validity[judge.ReferenceHumanJudged]
	if all.Primary.N != 4 {
		t.Errorf("human over %d item(s), want 4", all.Primary.N)
	}
	if judged.Primary.N != 2 {
		t.Errorf("judge-decided over %d item(s), want 2", judged.Primary.N)
	}
	// The judge was right about both items it was asked about; the selector
	// was wrong about one of the two it was not.
	if k := judged.Primary.Kappa; !k.Defined() || k.Float() <= all.Primary.Kappa.Float() {
		t.Errorf("judge-decided kappa %v is not above the selector's %v", k, all.Primary.Kappa)
	}
	body := report.Summary()
	if !strings.Contains(body, "the judge was actually asked about") {
		t.Errorf("the summary does not say what the restricted reading is over:\n%s", body)
	}
}

// A rescoring says in the document that the judge was not asked, puts the two
// calibrations side by side, and reports the one check it can run on itself.
func TestRescoredSummaryComparesAndChecksItself(t *testing.T) {
	gold := map[string]string{"i1": "c1", "i2": "c2"}
	answers := map[string][]script{"i1": {agrees("c1")}, "i2": {agrees("c2")}}
	before := &judge.Rescored{
		Dir: "calibrations/j@2026-09-05", Day: "2026-09-05", Items: 2, Seeds: 1,
		ByKind:   map[string]int{judge.OutcomeSelected: 1, judge.OutcomeNoCandidate: 1},
		ByReason: map[string]int{"no_majority": 1},
		ByRule:   map[string]int{judge.RuleCondorcet: 1},
		Calls:    12, TotalMS: 15443671,
		SwapKappa: 0.589, RerunKappa: 0.820, HumanKappa: 0.281,
		HumanDecided: 0.763, HumanDecidedItems: 1,
	}
	result := calibrate(t, suite(t, gold), fakeRunner{answers: answers}, judge.Options{
		RescoredFrom: before.Dir,
		Rescored:     before,
		Recorded: func(item string, _ int) (string, string) {
			// The source selected the same candidate on i1 and nothing on i2.
			if item == "i1" {
				return judge.OutcomeSelected, "c1"
			}
			return judge.OutcomeNoCandidate, ""
		},
	})
	body := result.Report.Summary()
	for _, want := range []string{
		"The judge was not asked anything",
		"calibrations/j@2026-09-05",
		"| no candidate, `no_majority` | 1 | 0 |",
		"| run time, summed | 4 h 17 min |",
		"whole selector",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the summary does not carry %q:\n%s", want, body)
		}
	}
	// Both items were settled by a Condorcet sweep here; the source agreed
	// about one of them and had reached no candidate on the other.
	if before.SweptAgree != 1 || before.SweptDiffer != 1 {
		t.Errorf("self-check %d agreed and %d differed, want 1 and 1",
			before.SweptAgree, before.SweptDiffer)
	}
	document, err := result.Report.Document("2026-09-06", "calibrations/j@2026-09-06/report.json", 0)
	if err != nil {
		t.Fatal(err)
	}
	if document.RescoredFrom != before.Dir {
		t.Errorf("the document records rescored_from %q", document.RescoredFrom)
	}
}

// A rescoring runs the harness against a recorded run rather than the
// candidates, because the harness reads both the candidates and the seed out
// of the run it replays and refuses either on the command line.
func TestRunnerReplaysWhenItIsGivenASource(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "fake-cmoa")
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\n" +
		"mkdir -p " + dir + "/run\n" +
		"printf '%s' '{\"outcome\":{\"kind\":\"no_candidate\",\"reason\":\"invalid_output\"}," +
		"\"pairs\":[]}' > " + dir + "/run/judge.json\n" +
		"echo " + dir + "/run\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil { //nolint:gosec // a test's own fake
		t.Fatal(err)
	}
	s := suite(t, map[string]string{"i1": "c1"})
	runner := judge.CMoARunner{
		Binary: binary,
		Replay: func(task judge.Task, seed int) string {
			if task.ID != "i1" || seed != 1 {
				return ""
			}
			return "traces/i1-1"
		},
	}
	if _, err := runner.Judge(context.Background(), s, s.Tasks[0], 1); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(body))
	if !contains(got, "--replay-from") || !contains(got, "traces/i1-1") {
		t.Errorf("the replay did not name its source: %v", got)
	}
	for _, unwanted := range []string{"--candidate", "--seed"} {
		if contains(got, unwanted) {
			t.Errorf("the replay passed %s, which the harness refuses: %v", unwanted, got)
		}
	}
}

func contains(all []string, want string) bool {
	for _, got := range all {
		if got == want {
			return true
		}
	}
	return false
}

// writeJournal writes an items.jsonl of the given rows.
func writeJournal(t *testing.T, dir string, rows []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, row := range rows {
		body, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(body)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "items.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
