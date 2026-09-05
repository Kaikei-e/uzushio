// Package judge calibrates a judge: it asks one to choose between three
// answers on items where people have already chosen, and reports how far the
// two agree.
//
// Three quantities are kept apart because they are three different claims, and
// the ordinary mistake is to report one and call it the others.
//
//   - Swap reliability. The same judge, the same prompt, the two candidates in
//     the other order. A judge that changes its mind is telling you about its
//     position bias, not about the answers. This is a consistency statistic and
//     not a two-rater reliability: the two readings are not independent, which
//     inflates them.
//   - Re-run reliability. The same judge, the same order, another seed. Also
//     consistency, and also not independent.
//   - Validity. The judge against the people. This is the only one of the three
//     that says the judge is measuring the right thing, and it is the one that
//     needs human labels — which is why a calibration with none says
//     `unmeasured` rather than borrowing a number from the other two. A judge
//     can be perfectly self-consistent and consistently wrong; the published
//     work that measures both finds exactly that combination.
//
// Every number is reported under a named tie handling, because how abstentions
// and ties are treated is a choice of estimand rather than a preprocessing
// detail: the same verdicts, scored two defensible ways, move a reported
// accuracy from 0.55 to 0.90 and take kappa across zero.
package judge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrJudge is the sentinel every failure in this package wraps.
var ErrJudge = errors.New("judge")

// SuiteSchemaVersion is the manifest version this package reads.
const SuiteSchemaVersion = 1

// FaceChat is the only face a judge calibration runs on.
const FaceChat = "chat"

// Positions are the three candidate slots of an item, and the categories a
// judge's answer falls into besides Abstain.
var Positions = []string{"c1", "c2", "c3"}

// Abstain is the category the protocol's own refusals fold into: a
// `no_candidate` outcome for any of its sub-reasons — a cycle, no majority,
// all draws, an unreadable answer — and a human label of `tie` or `all_bad`.
//
// It is one category rather than several because kappa needs a partition, and
// splitting "the judge saw a cycle" from "the judge saw no majority" would make
// a judge that abstains in two different ways disagree with itself. The
// breakdown is reported beside the coefficient instead.
//
// It does **not** cover a judge that timed out, failed, or could not be run.
// Those are the machine rather than the judgement, they are excluded from
// every coefficient, and they are counted separately. See Judged.Measured.
const Abstain = "abstain"

// Suite is a chat calibration suite: the items, and where they came from.
type Suite struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Face          string `json:"face"`
	Split         string `json:"split"`
	Source        string `json:"source"`
	License       string `json:"license"`
	Tasks         []Task `json:"tasks"`
	// Dir is the directory the manifest was read from; a task directory is
	// relative to it.
	Dir string `json:"-"`
}

// Task is one item of the suite.
type Task struct {
	ID      string `json:"id"`
	Dir     string `json:"dir"`
	Stratum string `json:"stratum"`
	Gold    string `json:"gold"`
}

// Gold is the human label of one item, as the item's gold file carries it.
// Only the keys a calibration reads are declared; the rest is the deriving
// adapter's business.
type Gold struct {
	Gold          string            `json:"gold"`
	Method        string            `json:"method"`
	MarginStratum string            `json:"margin_stratum"`
	BTTop         string            `json:"bt_top"`
	Hard          bool              `json:"hard"`
	Models        map[string]string `json:"models"`
	Judgments     int               `json:"judgments_used"`
	Annotators    int               `json:"human_annotators"`
}

// Category is the gold label as a kappa category: the position the people
// chose, or Abstain where they left it undecided.
func (g Gold) Category() string {
	for _, position := range Positions {
		if g.Gold == position {
			return position
		}
	}
	return Abstain
}

// LoadSuite reads a chat suite manifest.
func LoadSuite(name string) (Suite, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the suite
	if err != nil {
		return Suite{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var suite Suite
	if err := json.Unmarshal(body, &suite); err != nil {
		return Suite{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	if suite.SchemaVersion != SuiteSchemaVersion {
		return Suite{}, fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrJudge, name, suite.SchemaVersion, SuiteSchemaVersion)
	}
	if suite.ID == "" {
		return Suite{}, fmt.Errorf("%w: %s names no suite", ErrJudge, name)
	}
	if len(suite.Tasks) == 0 {
		return Suite{}, fmt.Errorf("%w: %s holds no items", ErrJudge, name)
	}
	seen := map[string]bool{}
	for i, task := range suite.Tasks {
		if task.ID == "" {
			return Suite{}, fmt.Errorf("%w: %s: item %d has no id", ErrJudge, name, i)
		}
		if seen[task.ID] {
			return Suite{}, fmt.Errorf("%w: %s names item %s twice", ErrJudge, name, task.ID)
		}
		seen[task.ID] = true
		if suite.Tasks[i].Dir == "" {
			suite.Tasks[i].Dir = task.ID
		}
		if suite.Tasks[i].Gold == "" {
			suite.Tasks[i].Gold = "gold.json"
		}
	}
	suite.Dir = filepath.Dir(name)
	return suite, nil
}

// TaskDir is where one item lives.
func (s Suite) TaskDir(t Task) string {
	return filepath.Join(s.Dir, filepath.FromSlash(t.Dir))
}

// GoldOf reads one item's human label. An item with no gold file is not an
// error: a suite may hold items whose only labels come from a labels file.
func (s Suite) GoldOf(t Task) (Gold, bool, error) {
	name := filepath.Join(s.TaskDir(t), filepath.FromSlash(t.Gold))
	body, err := os.ReadFile(name) //nolint:gosec // a path the manifest named
	if errors.Is(err, os.ErrNotExist) {
		return Gold{}, false, nil
	}
	if err != nil {
		return Gold{}, false, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var gold Gold
	if err := json.Unmarshal(body, &gold); err != nil {
		return Gold{}, false, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	return gold, true, nil
}

// Candidates are the three candidate files of an item, in position order.
func (s Suite) Candidates(t Task) []string {
	dir := s.TaskDir(t)
	out := make([]string, 0, len(Positions))
	for _, position := range Positions {
		out = append(out, filepath.Join(dir, "candidates", position+".txt"))
	}
	return out
}

// CheckCandidates reports the first item whose candidate answers are not on
// disk.
//
// They can legitimately be absent. A derived suite commits the prompts and the
// human labels — those are the licensed part — and leaves the model answers to
// be fetched locally, so a fresh clone has a suite manifest, a gold file per
// item and no answers at all. That is a state a calibration has to name rather
// than crash in the middle of: the fix is one command, and finding out about
// it two hours into a run is not.
func (s Suite) CheckCandidates() error {
	for _, task := range s.Tasks {
		for _, name := range s.Candidates(task) {
			if _, err := os.Stat(name); err != nil {
				return fmt.Errorf("%w: item %s has no candidate answers (%s missing). "+
					"Model responses are not committed to this repository; fetch them with "+
					"`uzushio judge import-mtbench --candidates-only --out %s` "+
					"at the seed and target DERIVATION.md records",
					ErrJudge, task.ID, filepath.Base(name), s.Dir)
			}
		}
	}
	return nil
}
