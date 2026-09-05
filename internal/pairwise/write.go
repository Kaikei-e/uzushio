package pairwise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// SuiteSchemaVersion is the version of the chat suite manifest this package
// writes and internal/judge reads.
const SuiteSchemaVersion = 1

// GoldSchemaVersion is the version of a gold file.
const GoldSchemaVersion = 1

// TaskVersion is the task manifest version a chat task is written at. It is
// CMoA's number, not uzushio's: version 3 is the one that carries `face`.
const TaskVersion = 3

// FaceChat is the face a derived item is a task for.
const FaceChat = "chat"

// SplitCalibration is the split a derived suite declares. It is neither
// held-in nor held-out: nothing is trained on it and no edit is promoted by
// it — it exists to measure the instrument.
const SplitCalibration = "calibration"

// MethodAcyclicMajority names how a gold label was derived, so a later reader
// knows what the label is a label of. It is not "the best answer": it is the
// answer the pairwise human majorities point at, on an item where those
// majorities do not contradict each other.
const MethodAcyclicMajority = "acyclic-majority"

// Field is one key of an ordered JSON object.
type Field struct {
	Key   string
	Value any
}

// object marshals its fields in declaration order. Ordering matters here for
// one reason: these files are committed, and a diff is the only review a
// generated corpus gets. A map would sort the keys and put `gold` between
// `count` and `license`, which reads as noise.
type object []Field

func (o object) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(field.Key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: key %s: %w", ErrCorpus, field.Key, err)
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// Provenance is what the adapter knows and this package does not: where the
// judgments came from, what may be done with them, and how to say so.
type Provenance struct {
	// SuiteID names the suite. It is the identifier a calibration report
	// quotes.
	SuiteID string
	// Source and License go into every gold file, so a single item carries its
	// own licence rather than relying on a file at the root nobody copies.
	Source  string
	License string
	// Attribution is the whole body of ATTRIBUTION.md, written by the adapter
	// because the wording of an attribution is a legal statement rather than a
	// template.
	Attribution string
	// Command is the invocation that produced the corpus, recorded so the
	// derivation can be repeated.
	Command string
	// Notes are paragraphs added to DERIVATION.md after the counts: whatever
	// the adapter has to say about how its corpus was read.
	Notes []string
	// ID names an item's task directory. The shape of a task identifier is the
	// adapter's, because it encodes what the corpus calls a prompt.
	ID func(Item) (string, error)
	// GoldExtra returns the source-specific keys of a gold file, written after
	// the licence and before the label.
	GoldExtra func(Item) []Field
}

// Write materialises a derived corpus under dir: one task directory per item,
// the suite manifest, the attribution and the derivation note.
//
// It refuses a directory that already holds a suite, rather than merging into
// it. A half-overwritten corpus whose manifest lists items from one derivation
// and whose directories hold another is not something a later reader can
// detect.
func Write(dir string, items []Item, stats Stats, p Provenance) error {
	if p.ID == nil {
		return fmt.Errorf("%w: the provenance names no way to identify an item", ErrCorpus)
	}
	if len(items) == 0 {
		return fmt.Errorf("%w: nothing to write; the derivation kept no items", ErrCorpus)
	}
	manifest := filepath.Join(dir, "suite.json")
	if _, err := os.Stat(manifest); err == nil {
		return fmt.Errorf("%w: %s already holds a suite; remove it to re-derive", ErrCorpus, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrCorpus, err)
	}

	type entry struct {
		ID      string `json:"id"`
		Dir     string `json:"dir"`
		Stratum string `json:"stratum"`
		Gold    string `json:"gold"`
	}
	entries := make([]entry, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		id, err := p.ID(item)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrCorpus, err)
		}
		if seen[id] {
			return fmt.Errorf("%w: two items are both called %s", ErrCorpus, id)
		}
		seen[id] = true
		if err := writeItem(filepath.Join(dir, id), id, item, p); err != nil {
			return err
		}
		entries = append(entries, entry{ID: id, Dir: id, Stratum: item.Stratum, Gold: "gold.json"})
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].ID < entries[b].ID })

	suite := object{
		{"schema_version", SuiteSchemaVersion},
		{"id", p.SuiteID},
		{"face", FaceChat},
		{"split", SplitCalibration},
		{"source", p.Source},
		{"license", p.License},
		{"tasks", entries},
	}
	if err := writeJSON(manifest, suite); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "ATTRIBUTION.md"), []byte(p.Attribution)); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "DERIVATION.md"), []byte(derivation(stats, p)))
}

// writeItem materialises one task directory.
func writeItem(dir, id string, item Item, p Provenance) error {
	if err := os.MkdirAll(filepath.Join(dir, "candidates"), 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrCorpus, err)
	}
	task := object{
		{"version", TaskVersion},
		{"id", id},
		{"face", FaceChat},
		{"conversation", "conversation.json"},
		{"judge", map[string]bool{"allow_tie": true}},
	}
	if err := writeJSON(filepath.Join(dir, "task.json"), task); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "conversation.json"), item.Conversation); err != nil {
		return err
	}
	for n, position := range Positions {
		// The answers are somebody else's model outputs. They are written
		// byte for byte, with one newline at the end so the file is a text
		// file, and nothing else is done to them.
		body := strings.TrimRight(item.Answers[n], "\n") + "\n"
		if err := writeFile(filepath.Join(dir, "candidates", position+".txt"), []byte(body)); err != nil {
			return err
		}
	}

	models := object{}
	for n, position := range Positions {
		models = append(models, Field{position, item.Systems[n]})
	}
	gold := object{
		{"schema_version", GoldSchemaVersion},
		{"source", p.Source},
		{"license", p.License},
	}
	if p.GoldExtra != nil {
		gold = append(gold, p.GoldExtra(item)...)
	}
	gold = append(gold,
		Field{"models", models},
		Field{"gold", item.Gold},
		Field{"method", MethodAcyclicMajority},
		Field{"margin_stratum", item.Stratum},
		Field{"bt_top", item.BTTop},
		Field{"hard", item.Hard()},
		Field{"judgments_used", item.Judgments},
		Field{"human_annotators", item.Annotators},
	)
	return writeJSON(filepath.Join(dir, "gold.json"), gold)
}

// derivation renders DERIVATION.md: what the numbers were, so that a reader
// asking "how much of the corpus was thrown away, and why" does not have to
// re-run anything to find out.
func derivation(stats Stats, p Provenance) string {
	var b strings.Builder
	b.WriteString("# How this suite was derived\n\n")
	b.WriteString("Generated by `" + p.Command + "`. Do not edit by hand: re-run the command.\n\n")
	b.WriteString("The source corpus holds **pairwise** human comparisons — two answers to one\n")
	b.WriteString("prompt and a person's verdict on which is better. A judge that picks one of\n")
	b.WriteString("three answers cannot be measured on those directly, so each item here is a\n")
	b.WriteString("triple of systems whose three pairwise majorities are all present and do not\n")
	b.WriteString("contradict each other, labelled with the system that beat both others.\n\n")

	b.WriteString("## Counts\n\n")
	b.WriteString("| | |\n|---|---:|\n")
	rows := [][2]string{
		{"prompts in the source", fmt.Sprint(stats.Groups)},
		{"human comparisons read", fmt.Sprint(stats.Votes)},
		{"candidate triples considered", fmt.Sprint(stats.Triples)},
		{"discarded: a pair nobody compared", fmt.Sprint(stats.Incomplete)},
		{"discarded: too few comparisons", fmt.Sprint(stats.Thin)},
		{"discarded: the three majorities cycle", fmt.Sprint(stats.Cyclic)},
		{"eligible", fmt.Sprint(stats.Eligible)},
		{"sampled into this suite", fmt.Sprint(stats.Sampled)},
		{"prompts contributing an item", fmt.Sprint(stats.GroupsUsed)},
		{"sampled items the people left undecided (`gold: tie`)", fmt.Sprint(stats.TieGold)},
		{"sampled items the majorities and the fit disagree about", fmt.Sprint(stats.Hard)},
	}
	for _, row := range rows {
		b.WriteString("| " + row[0] + " | " + row[1] + " |\n")
	}
	fmt.Fprintf(&b, "\nCycle rate among complete triples: **%.1f%%** (%d of %d).\n",
		100*stats.CycleRate(), stats.Cyclic, stats.Cyclic+stats.Eligible+stats.Thin)
	b.WriteString("\nThat rate is a measurement of the human labels rather than of any judge.\n")
	b.WriteString("The items behind it have no true answer — the people who produced them\n")
	b.WriteString("preferred x to y, y to z and z to x — so no judge could be right about them,\n")
	b.WriteString("and it is a floor under the disagreement any calibration will report.\n\n")

	b.WriteString("## Margin strata\n\n")
	b.WriteString("Items are banded by the Bradley-Terry log-strength gap between the best and\n")
	b.WriteString("the second-best of the three, and sampled in a fixed mix. Kappa is sensitive\n")
	b.WriteString("to how obvious the answer is: a suite of only easy items reports a\n")
	b.WriteString("coefficient that says nothing about hard ones, and a suite of only hard items\n")
	b.WriteString("reports one near zero that says nothing at all.\n\n")
	b.WriteString("| stratum | items |\n|---|---:|\n")
	for _, name := range slices.Sorted(sortedKeys(stats.PerStratum)) {
		fmt.Fprintf(&b, "| %s | %d |\n", name, stats.PerStratum[name])
	}

	if len(p.Notes) > 0 {
		b.WriteString("\n## Notes on the source\n")
		for _, note := range p.Notes {
			b.WriteString("\n" + note + "\n")
		}
	}
	b.WriteString("\n## What a gold label is not\n\n")
	b.WriteString("`gold` is the answer the human majorities point at. It is not a statement\n")
	b.WriteString("that the answer is good, and `gold: tie` is not a statement that the three\n")
	b.WriteString("are equal — it says the majorities left no single winner. A calibration\n")
	b.WriteString("reports every number under a named tie handling for exactly this reason.\n")
	return b.String()
}

// sortedKeys yields a map's keys, for the one place a generated document reads
// one.
func sortedKeys(m map[string]int) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// writeJSON writes a value as indented JSON with a trailing newline, which is
// what a committed file wants: a diff of a one-line JSON document is the whole
// document.
func writeJSON(name string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrCorpus, name, err)
	}
	return writeFile(name, append(body, '\n'))
}

func writeFile(name string, body []byte) error {
	if err := os.WriteFile(name, body, 0o644); err != nil { //nolint:gosec // a corpus is world-readable on purpose
		return fmt.Errorf("%w: %w", ErrCorpus, err)
	}
	return nil
}
