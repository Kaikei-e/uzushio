package judge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// LabelSchemaVersion is the version of the labels file this package reads.
const LabelSchemaVersion = 1

// The two label values that are not a choice of candidate.
const (
	// ChoiceTie is a labeler saying the answers are of a kind.
	ChoiceTie = "tie"
	// ChoiceAllBad is a labeler saying none of them is worth choosing. It
	// folds into Abstain for the coefficient, because kappa needs a partition
	// and "none is good" and "they are equal" are not distinguishable in a
	// judge's answer — but it is counted separately in the report, because a
	// corpus where a third of the items are all bad is a corpus telling you
	// something about the proposers rather than about the judge.
	ChoiceAllBad = "all_bad"
)

// Label is one person's answer on one item.
type Label struct {
	SchemaVersion int      `json:"schema_version"`
	Item          string   `json:"item"`
	Labeler       string   `json:"labeler"`
	Presented     []string `json:"presented"`
	Choice        string   `json:"choice"`
	At            string   `json:"at"`
	DurationMS    int64    `json:"duration_ms"`
	Note          string   `json:"note"`
}

// Category is the label as a kappa category.
func (l Label) Category() string {
	if slices.Contains(Positions, l.Choice) {
		return l.Choice
	}
	return Abstain
}

// LoadLabels reads label files, one JSON object per line. Blank lines are
// skipped; a line that is not a label is an error, because a labelling page
// that started writing something else is a thing to find out about now rather
// than through a coefficient that moved.
func LoadLabels(paths []string) ([]Label, error) {
	var out []Label
	for _, path := range paths {
		file, err := os.Open(path) //nolint:gosec // the caller names the labels
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrJudge, err)
		}
		scanner := bufio.NewScanner(file)
		// A pasted answer can be long; the default 64 KiB line limit is not
		// enough for a note field somebody wrote a paragraph into.
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			var label Label
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&label); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("%w: %s:%d: %w", ErrJudge, path, line, err)
			}
			if label.SchemaVersion != LabelSchemaVersion {
				_ = file.Close()
				return nil, fmt.Errorf("%w: %s:%d is schema version %d, this build reads %d",
					ErrJudge, path, line, label.SchemaVersion, LabelSchemaVersion)
			}
			if label.Item == "" || label.Labeler == "" {
				_ = file.Close()
				return nil, fmt.Errorf("%w: %s:%d names no item or no labeler", ErrJudge, path, line)
			}
			if !slices.Contains(Positions, label.Choice) &&
				label.Choice != ChoiceTie && label.Choice != ChoiceAllBad {
				_ = file.Close()
				return nil, fmt.Errorf("%w: %s:%d: %q is not a choice", ErrJudge, path, line, label.Choice)
			}
			out = append(out, label)
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("%w: %s: %w", ErrJudge, path, err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrJudge, err)
		}
	}
	return out, nil
}

// byItem indexes labels by the item they are about, keeping the order they
// were read in so that "the first labeler" is a stable notion.
func byItem(labels []Label) map[string][]Label {
	out := map[string][]Label{}
	for _, label := range labels {
		out[label.Item] = append(out[label.Item], label)
	}
	return out
}

// labelers returns the distinct labelers, in the order they first appear.
func labelers(labels []Label) []string {
	var out []string
	for _, label := range labels {
		if !slices.Contains(out, label.Labeler) {
			out = append(out, label.Labeler)
		}
	}
	return out
}
