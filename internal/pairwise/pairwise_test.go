package pairwise_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/pairwise"
)

// prompt is the shared conversation every fixture group carries.
var prompt = []pairwise.Message{{Role: "user", Content: "the question"}}

// vote adds one comparison to a corpus, with an answer per system named after
// the system, so a written item can be checked for carrying the right text.
func vote(t *testing.T, c *pairwise.Corpus, group, first, second, winner, labeler string) {
	t.Helper()
	err := c.Add(group, prompt,
		pairwise.Vote{First: first, Second: second, Winner: winner, Labeler: labeler},
		"answer from "+first, "answer from "+second)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
}

// transitive builds one group where a beats b, b beats c and a beats c: the
// shape a three-way item is derived from.
func transitive(t *testing.T, group string) *pairwise.Corpus {
	t.Helper()
	c := pairwise.NewCorpus()
	vote(t, c, group, "a", "b", pairwise.WinnerFirst, "p1")
	vote(t, c, group, "b", "c", pairwise.WinnerFirst, "p2")
	vote(t, c, group, "a", "c", pairwise.WinnerFirst, "p3")
	return c
}

func derive(t *testing.T, c *pairwise.Corpus, opts pairwise.Options) ([]pairwise.Item, pairwise.Stats) {
	t.Helper()
	items, stats, err := pairwise.Derive(c, opts)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return items, stats
}

func TestAcyclicTripleBecomesAnItem(t *testing.T) {
	items, stats := derive(t, transitive(t, "g1"), pairwise.Options{Seed: 7})
	if len(items) != 1 {
		t.Fatalf("derived %d items, want 1", len(items))
	}
	if stats.Eligible != 1 || stats.Cyclic != 0 || stats.Incomplete != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	item := items[0]
	// The gold is a position rather than a system name: the mapping is in the
	// item, and the point of the positions is that they say nothing.
	var goldSystem string
	for n, position := range pairwise.Positions {
		if position == item.Gold {
			goldSystem = item.Systems[n]
		}
	}
	if goldSystem != "a" {
		t.Fatalf("gold is %q, which is system %q; a beat both others", item.Gold, goldSystem)
	}
	if item.Judgments != 3 || item.Annotators != 1 {
		t.Fatalf("item rests on %d judgments by %d people", item.Judgments, item.Annotators)
	}
	for n := range pairwise.Positions {
		if want := "answer from " + item.Systems[n]; item.Answers[n] != want {
			t.Fatalf("position %d carries %q, want %q", n, item.Answers[n], want)
		}
	}
}

// TestCyclicTripleIsDiscarded is the rule the whole derivation turns on. An
// item whose majorities cycle has no true answer, and a label invented for it
// is noise put directly into the coefficient the corpus exists to estimate.
func TestCyclicTripleIsDiscarded(t *testing.T) {
	c := pairwise.NewCorpus()
	vote(t, c, "g1", "a", "b", pairwise.WinnerFirst, "p1")
	vote(t, c, "g1", "b", "c", pairwise.WinnerFirst, "p2")
	vote(t, c, "g1", "c", "a", pairwise.WinnerFirst, "p3")
	items, stats := derive(t, c, pairwise.Options{})
	if len(items) != 0 {
		t.Fatalf("a cyclic triple was kept: %+v", items)
	}
	if stats.Cyclic != 1 {
		t.Fatalf("cyclic = %d, want 1", stats.Cyclic)
	}
	if stats.CycleRate() != 1 {
		t.Fatalf("cycle rate = %v, want 1", stats.CycleRate())
	}
}

// TestMissingPairIsDiscarded records the other refusal: a triple whose third
// pair nobody compared is not completed by transitivity, because transitivity
// is the assumption the corpus is meant to test.
func TestMissingPairIsDiscarded(t *testing.T) {
	c := pairwise.NewCorpus()
	vote(t, c, "g1", "a", "b", pairwise.WinnerFirst, "p1")
	vote(t, c, "g1", "b", "c", pairwise.WinnerFirst, "p2")
	// a against c is never compared, and there is no answer for c from that
	// pair either — so the third system is present but the pair is not.
	items, stats := derive(t, c, pairwise.Options{})
	if len(items) != 0 || stats.Incomplete != 1 {
		t.Fatalf("items = %d, incomplete = %d, want 0 and 1", len(items), stats.Incomplete)
	}
}

// TestDrawnPairGivesATieGold records that a triple can be complete, acyclic
// and still leave no winner. The label is `tie`, not a guess.
func TestDrawnPairGivesATieGold(t *testing.T) {
	c := pairwise.NewCorpus()
	vote(t, c, "g1", "a", "b", pairwise.WinnerTie, "p1")
	vote(t, c, "g1", "b", "c", pairwise.WinnerTie, "p2")
	vote(t, c, "g1", "a", "c", pairwise.WinnerTie, "p3")
	items, stats := derive(t, c, pairwise.Options{})
	if len(items) != 1 || items[0].Gold != pairwise.GoldTie {
		t.Fatalf("items = %+v", items)
	}
	if stats.TieGold != 1 {
		t.Fatalf("tie_gold = %d, want 1", stats.TieGold)
	}
}

// TestGoldIsTheUnbeatenSystem is the defect that put a third of the corpus's
// human marginal into a category the annotators never used.
//
// A beat B, B beat C, and A against C is a one-all split. Nobody beat A. The
// earlier rule asked for two wins and called the item undecided, which is a
// label the people did not give — and it is the human side of every validity
// table, so it depressed exactly the coefficient the verdict reads.
func TestGoldIsTheUnbeatenSystem(t *testing.T) {
	c := pairwise.NewCorpus()
	for range 3 {
		vote(t, c, "g1", "a", "b", pairwise.WinnerFirst, "p1")
		vote(t, c, "g1", "b", "c", pairwise.WinnerFirst, "p2")
	}
	vote(t, c, "g1", "a", "c", pairwise.WinnerFirst, "p3")
	vote(t, c, "g1", "a", "c", pairwise.WinnerSecond, "p4")

	items, stats := derive(t, c, pairwise.Options{Seed: 1})
	if len(items) != 1 {
		t.Fatalf("derived %d items", len(items))
	}
	item := items[0]
	if item.Gold == pairwise.GoldTie {
		t.Fatal("a triple with an unbeaten system was called undecided")
	}
	if system := systemAt(item, item.Gold); system != "a" {
		t.Fatalf("gold is system %q; a was beaten by nobody", system)
	}
	// It is unbeaten and not a Condorcet winner, and the corpus records which.
	if item.Wins != 1 {
		t.Fatalf("gold_wins = %d, want the one pair it won outright", item.Wins)
	}
	if stats.Condorcet != 0 || stats.TieGold != 0 {
		t.Fatalf("stats = %+v", stats)
	}

	// Two unbeaten systems is what `tie` is for: A beat B and nobody else
	// settled anything, so A and C are both unbeaten.
	open := pairwise.NewCorpus()
	vote(t, open, "g1", "a", "b", pairwise.WinnerFirst, "p1")
	vote(t, open, "g1", "b", "c", pairwise.WinnerTie, "p2")
	vote(t, open, "g1", "a", "c", pairwise.WinnerTie, "p3")
	two, _ := derive(t, open, pairwise.Options{Seed: 1})
	if len(two) != 1 || two[0].Gold != pairwise.GoldTie {
		t.Fatalf("two unbeaten systems gave gold %q", two[0].Gold)
	}
}

// TestATieIsAVerdictAndNotASpoiltBallot records the other half of the same
// argument. `tie` is one of the three things the source lets a person say, so
// five people calling two answers equal outweigh the sixth who preferred one:
// the pair is drawn, not a defeat.
func TestATieIsAVerdictAndNotASpoiltBallot(t *testing.T) {
	c := pairwise.NewCorpus()
	for range 5 {
		vote(t, c, "g1", "a", "b", pairwise.WinnerTie, "p1")
	}
	vote(t, c, "g1", "a", "b", pairwise.WinnerFirst, "p2")
	vote(t, c, "g1", "b", "c", pairwise.WinnerFirst, "p3")
	vote(t, c, "g1", "a", "c", pairwise.WinnerFirst, "p4")

	items, _ := derive(t, c, pairwise.Options{Seed: 1})
	if len(items) != 1 {
		t.Fatalf("derived %d items", len(items))
	}
	// a drew with b and beat c; b beat c. Nobody beat a and nobody beat b, so
	// there is no unique source.
	if items[0].Gold != pairwise.GoldTie {
		t.Fatalf("gold = %q; five people said a and b were of a kind",
			systemAt(items[0], items[0].Gold))
	}

	// One preference against one shrug is thin, but it is not outweighed, and
	// this corpus is thin enough that requiring a plurality over the ties as
	// well would leave four items in ten with no unbeaten system.
	thin := pairwise.NewCorpus()
	vote(t, thin, "g1", "a", "b", pairwise.WinnerFirst, "p1")
	vote(t, thin, "g1", "a", "b", pairwise.WinnerTie, "p2")
	vote(t, thin, "g1", "b", "c", pairwise.WinnerFirst, "p3")
	vote(t, thin, "g1", "a", "c", pairwise.WinnerFirst, "p4")
	decided, _ := derive(t, thin, pairwise.Options{Seed: 1})
	if len(decided) != 1 || systemAt(decided[0], decided[0].Gold) != "a" {
		t.Fatalf("gold = %q, want a", systemAt(decided[0], decided[0].Gold))
	}
}

// systemAt is the system behind a position.
func systemAt(item pairwise.Item, position string) string {
	for n, name := range pairwise.Positions {
		if name == position {
			return item.Systems[n]
		}
	}
	return ""
}

// TestThinTripleIsDiscarded checks the floor on how much evidence a label
// rests on.
func TestThinTripleIsDiscarded(t *testing.T) {
	items, stats := derive(t, transitive(t, "g1"), pairwise.Options{MinJudgments: 4})
	if len(items) != 0 || stats.Thin != 1 {
		t.Fatalf("items = %d, thin = %d, want 0 and 1", len(items), stats.Thin)
	}
}

// TestDerivationIsDeterministic is what `--seed` promises: the same corpus and
// the same seed write the same suite, positions included.
func TestDerivationIsDeterministic(t *testing.T) {
	build := func() *pairwise.Corpus {
		c := pairwise.NewCorpus()
		for g := range 6 {
			group := "g" + strconv.Itoa(g)
			for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}, {"a", "c"}, {"a", "d"}, {"b", "d"}, {"c", "d"}} {
				winner := pairwise.WinnerFirst
				if (g+len(pair[0]))%3 == 0 {
					winner = pairwise.WinnerSecond
				}
				vote(t, c, group, pair[0], pair[1], winner, "p1")
			}
		}
		return c
	}
	first, _ := derive(t, build(), pairwise.Options{Seed: 42, Target: 5})
	second, _ := derive(t, build(), pairwise.Options{Seed: 42, Target: 5})
	other, _ := derive(t, build(), pairwise.Options{Seed: 43, Target: 5})
	if len(first) != 5 {
		t.Fatalf("sampled %d, want 5", len(first))
	}
	for i := range first {
		if first[i].Group != second[i].Group || first[i].Systems != second[i].Systems ||
			first[i].Gold != second[i].Gold {
			t.Fatalf("item %d differs between two runs at one seed", i)
		}
	}
	same := true
	for i := range first {
		if i < len(other) && (first[i].Group != other[i].Group || first[i].Systems != other[i].Systems) {
			same = false
		}
	}
	if same {
		t.Fatal("two seeds produced the same sample; the seed does nothing")
	}
}

// TestStrataAreTheDeclaredMix checks the sample's mix rather than its members:
// stratifying buys an exact mix, which is the whole reason it is done.
func TestStrataAreTheDeclaredMix(t *testing.T) {
	c := pairwise.NewCorpus()
	// Twenty groups of four systems, with the margins spread out by giving the
	// top system a different number of wins in each group.
	for g := range 20 {
		group := "g" + strconv.Itoa(g)
		for range g%4 + 1 {
			vote(t, c, group, "a", "b", pairwise.WinnerFirst, "p1")
		}
		vote(t, c, group, "b", "c", pairwise.WinnerFirst, "p1")
		vote(t, c, group, "a", "c", pairwise.WinnerFirst, "p1")
		vote(t, c, group, "a", "d", pairwise.WinnerFirst, "p1")
		vote(t, c, group, "b", "d", pairwise.WinnerFirst, "p1")
		vote(t, c, group, "c", "d", pairwise.WinnerFirst, "p1")
	}
	_, stats := derive(t, c, pairwise.Options{Seed: 3, Target: 40})
	want := map[string]int{pairwise.StratumWide: 16, pairwise.StratumMid: 16, pairwise.StratumNarrow: 8}
	for name, count := range want {
		if stats.PerStratum[name] != count {
			t.Fatalf("stratum %s got %d items, want %d (%v)", name, stats.PerStratum[name], count, stats.PerStratum)
		}
	}
}

func TestWriteMaterialisesASuite(t *testing.T) {
	items, stats := derive(t, transitive(t, "g1"), pairwise.Options{Seed: 1})
	dir := t.TempDir()
	provenance := pairwise.Provenance{
		SuiteID:     "suite-test",
		Source:      "a corpus",
		License:     "CC-BY-4.0",
		Attribution: "# Attribution\n\nfrom a corpus.\n",
		Command:     "uzushio judge import-something",
		ID:          func(pairwise.Item) (string, error) { return "item-1", nil },
		GoldExtra:   func(pairwise.Item) []pairwise.Field { return []pairwise.Field{{Key: "group", Value: "g1"}} },
	}
	if err := pairwise.Write(dir, items, stats, provenance); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var suite struct {
		SchemaVersion int    `json:"schema_version"`
		Face          string `json:"face"`
		Split         string `json:"split"`
		Tasks         []struct {
			ID      string `json:"id"`
			Stratum string `json:"stratum"`
		} `json:"tasks"`
	}
	readJSON(t, filepath.Join(dir, "suite.json"), &suite)
	if suite.Face != pairwise.FaceChat || suite.Split != pairwise.SplitCalibration {
		t.Fatalf("suite = %+v", suite)
	}
	if len(suite.Tasks) != 1 || suite.Tasks[0].ID != "item-1" {
		t.Fatalf("suite tasks = %+v", suite.Tasks)
	}

	var task struct {
		Version      int    `json:"version"`
		Face         string `json:"face"`
		Conversation string `json:"conversation"`
	}
	readJSON(t, filepath.Join(dir, "item-1", "task.json"), &task)
	if task.Version != pairwise.TaskVersion || task.Face != pairwise.FaceChat {
		t.Fatalf("task = %+v", task)
	}

	// The gold file carries the adapter's own keys and, first of all, the
	// licence: an item copied out of the suite carries its terms with it.
	body, err := os.ReadFile(filepath.Join(dir, "item-1", "gold.json"))
	if err != nil {
		t.Fatalf("read gold: %v", err)
	}
	for _, want := range []string{`"license": "CC-BY-4.0"`, `"group": "g1"`, `"method": "acyclic-majority"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("gold.json does not carry %s:\n%s", want, body)
		}
	}
	for _, position := range pairwise.Positions {
		if _, err := os.Stat(filepath.Join(dir, "item-1", "candidates", position+".txt")); err != nil {
			t.Fatalf("candidate %s: %v", position, err)
		}
	}
	derivation, err := os.ReadFile(filepath.Join(dir, "DERIVATION.md"))
	if err != nil {
		t.Fatalf("read derivation: %v", err)
	}
	if !strings.Contains(string(derivation), "uzushio judge import-something") {
		t.Fatal("DERIVATION.md does not record the command that made the corpus")
	}

	// A second write into the same directory would leave a manifest and a set
	// of directories that came from two derivations, which nothing downstream
	// could detect.
	if err := pairwise.Write(dir, items, stats, provenance); err == nil {
		t.Fatal("Write overwrote an existing suite")
	}
}

func readJSON(t *testing.T, name string, into any) {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// TestRowsPagesAndRetries drives the reader against a server that throttles
// once, which is the failure a public rows endpoint actually produces.
func TestRowsPagesAndRetries(t *testing.T) {
	var throttled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset == 2 && throttled.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		rows := []map[string]any{}
		for i := offset; i < min(offset+2, 5); i++ {
			rows = append(rows, map[string]any{"row": map[string]any{"n": i}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"num_rows_total": 5, "rows": rows})
	}))
	defer server.Close()

	rows, err := pairwise.Rows{
		Endpoint: server.URL, Dataset: "d", Config: "default", Split: "s",
		Page: 2, Pause: 1, Retries: 3,
	}.All(context.Background())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("read %d rows, want 5", len(rows))
	}
	if !throttled.Load() {
		t.Fatal("the throttle never fired; the retry path is untested")
	}
	var first struct {
		N int `json:"n"`
	}
	if err := json.Unmarshal(rows[0], &first); err != nil || first.N != 0 {
		t.Fatalf("first row = %s (%v)", rows[0], err)
	}
}

// TestRowsGivesUpOnAWrongQuestion records the other half: a 404 is not retried,
// because asking the same wrong question again is not a strategy.
func TestRowsGivesUpOnAWrongQuestion(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	_, err := pairwise.Rows{Endpoint: server.URL, Dataset: "d", Split: "s", Pause: 1, Retries: 5}.
		All(context.Background())
	if err == nil {
		t.Fatal("a 404 was read as a split")
	}
	if calls.Load() != 1 {
		t.Fatalf("the reader asked %d times for something that is not there", calls.Load())
	}
}

// TestWriteCandidatesFillsInASuite is the redistribution rule made runnable:
// the corpus commits the prompts and the labels and leaves the model answers
// out, so a clone has a suite with no answers in it and one command that fills
// them in.
func TestWriteCandidatesFillsInASuite(t *testing.T) {
	items, stats := derive(t, transitive(t, "g1"), pairwise.Options{Seed: 1})
	dir := t.TempDir()
	provenance := pairwise.Provenance{
		SuiteID: "suite-test", Source: "a corpus", License: "CC-BY-4.0",
		Attribution: "# Attribution\n", Command: "uzushio judge import-something",
		ID: func(pairwise.Item) (string, error) { return "item-1", nil },
	}
	if err := pairwise.Write(dir, items, stats, provenance); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Everything the answers are not stays; the answers go, as a clone would
	// have them.
	if err := os.RemoveAll(filepath.Join(dir, "item-1", "candidates")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := pairwise.WriteCandidates(dir, items, provenance); err != nil {
		t.Fatalf("WriteCandidates: %v", err)
	}
	for _, position := range pairwise.Positions {
		if _, err := os.Stat(filepath.Join(dir, "item-1", "candidates", position+".txt")); err != nil {
			t.Fatalf("candidate %s was not filled in: %v", position, err)
		}
	}
	// It is idempotent, which is what makes it safe to run on a clone that
	// already has some of them.
	if err := pairwise.WriteCandidates(dir, items, provenance); err != nil {
		t.Fatalf("a second fill: %v", err)
	}

	// A derivation that produced other items must not write into this suite:
	// answers beside gold labels that belong to other answers is a corpus that
	// looks right and measures nothing.
	other := pairwise.Provenance{
		SuiteID: provenance.SuiteID, Source: provenance.Source, License: provenance.License,
		ID: func(pairwise.Item) (string, error) { return "item-9", nil },
	}
	if err := pairwise.WriteCandidates(dir, items, other); err == nil {
		t.Fatal("answers were written into a suite that lists other items")
	}
	if err := pairwise.WriteCandidates(dir, nil, provenance); err == nil {
		t.Fatal("a derivation of a different size was accepted")
	}
}

// TestRubricIsWrittenWhereTheAdapterAsks records the hook a candidate that is
// not one plain answer needs: the judge has to be told what it is reading.
func TestRubricIsWrittenWhereTheAdapterAsks(t *testing.T) {
	items, stats := derive(t, transitive(t, "g1"), pairwise.Options{Seed: 1})
	dir := t.TempDir()
	if err := pairwise.Write(dir, items, stats, pairwise.Provenance{
		SuiteID: "suite-test", Source: "a corpus", License: "CC-BY-4.0",
		Attribution: "# Attribution\n", Command: "x",
		ID:     func(pairwise.Item) (string, error) { return "item-1", nil },
		Rubric: func(pairwise.Item) string { return "The candidate is a transcript.\n" },
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "item-1", "rubric.md"))
	if err != nil {
		t.Fatalf("read rubric: %v", err)
	}
	if !strings.Contains(string(body), "transcript") {
		t.Fatalf("rubric.md = %q", body)
	}
	task, err := os.ReadFile(filepath.Join(dir, "item-1", "task.json"))
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !strings.Contains(string(task), `"rubric": "rubric.md"`) {
		t.Fatalf("the task does not name its rubric:\n%s", task)
	}
}
