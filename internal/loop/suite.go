package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// DefaultMinTasksPerSplit is the floor a split has to clear before a run will
// spend anything on it.
//
// Eight is a floor rather than a target. The research puts the hard floor at
// twelve and the target at twenty to twenty-five per split, because below
// twelve a single task flipping moves the pass rate by eight points and the
// confidence sequence is wider than any effect worth arguing about. Eight is
// what a suite under construction can clear, and a run below the research's
// floor says so in its record rather than pretending the number means what it
// would mean at twenty.
const DefaultMinTasksPerSplit = 8

// Suite is the task set a run measures over, and the split each task belongs
// to. The split is fixed in the file and never resampled: a split chosen after
// the outcomes are known is not a held-out split.
type Suite struct {
	SchemaVersion int `json:"schema_version"`
	ID            string
	Tasks         []SuiteTask
	// MinTasksPerSplit is the floor this suite declares. Zero means
	// DefaultMinTasksPerSplit.
	MinTasksPerSplit int
	// Dir is where the suite file was read from; task directories are
	// relative to it.
	Dir string
}

// SuiteTask is one task and the split it is in.
type SuiteTask struct {
	ID    string
	Dir   string
	Split vocab.Split
}

// suiteFile is the on-disk shape. Unknown keys are ignored on purpose: a suite
// file carries constraints and notes that are somebody else's to read, and a
// run that refused to start because a key it does not use had appeared would
// be a coupling nobody asked for.
type suiteFile struct {
	SchemaVersion    int    `json:"schema_version"`
	ID               string `json:"id"`
	MinTasksPerSplit int    `json:"min_tasks_per_split"`
	Tasks            []struct {
		ID    string `json:"id"`
		Dir   string `json:"dir"`
		Split string `json:"split"`
	} `json:"tasks"`
}

// SuiteSchemaVersion is the version of the suite file this build reads.
const SuiteSchemaVersion = 1

// LoadSuite reads a suite file.
func LoadSuite(name string) (Suite, error) {
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the suite
	if err != nil {
		return Suite{}, fmt.Errorf("%w: %w", ErrRun, err)
	}
	var file suiteFile
	if err := json.Unmarshal(body, &file); err != nil {
		return Suite{}, fmt.Errorf("%w: %s: %w", ErrRun, name, err)
	}
	if file.SchemaVersion != SuiteSchemaVersion {
		return Suite{}, fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrRun, name, file.SchemaVersion, SuiteSchemaVersion)
	}
	if file.ID == "" {
		return Suite{}, fmt.Errorf("%w: %s names no suite; a pass rate without the suite it was measured over is a number about nothing",
			ErrRun, name)
	}
	suite := Suite{
		SchemaVersion:    file.SchemaVersion,
		ID:               file.ID,
		MinTasksPerSplit: file.MinTasksPerSplit,
		Dir:              filepath.Dir(name),
	}
	seen := map[string]bool{}
	for _, task := range file.Tasks {
		if !vocab.ValidTaskID(task.ID) {
			return Suite{}, fmt.Errorf("%w: %s: %q is not a task identifier (want %s)",
				ErrRun, name, task.ID, vocab.TaskIDPattern)
		}
		if seen[task.ID] {
			return Suite{}, fmt.Errorf("%w: %s names task %s twice", ErrRun, name, task.ID)
		}
		seen[task.ID] = true
		split := vocab.Split(task.Split)
		if !vocab.Valid(split, vocab.AllSplits()) {
			return Suite{}, fmt.Errorf("%w: %s: task %s is in split %q, which is outside %v",
				ErrRun, name, task.ID, task.Split, vocab.Strings(vocab.AllSplits()))
		}
		dir := task.Dir
		if dir == "" {
			dir = task.ID
		}
		suite.Tasks = append(suite.Tasks, SuiteTask{ID: task.ID, Dir: dir, Split: split})
	}
	return suite, nil
}

// Floor is the minimum number of tasks a split has to hold.
func (s Suite) Floor() int {
	if s.MinTasksPerSplit > 0 {
		return s.MinTasksPerSplit
	}
	return DefaultMinTasksPerSplit
}

// Of returns the tasks in one split, in the order the suite file lists them —
// which is the order the scheduler round-robins over, so the file is where a
// reader looks to know what ran first.
func (s Suite) Of(split vocab.Split) []SuiteTask {
	var out []SuiteTask
	for _, task := range s.Tasks {
		if task.Split == split {
			out = append(out, task)
		}
	}
	return out
}

// TaskDir is where one task lives, as an absolute path.
func (s Suite) TaskDir(task SuiteTask) (string, error) {
	return filepath.Abs(filepath.Join(s.Dir, filepath.FromSlash(task.Dir)))
}

// Digest is a stable identifier for the split assignment, so a record can say
// which split it was measured under and a later run can notice that the suite
// has been resampled. It is the sorted "id split" listing, hashed.
func (s Suite) Digest() string {
	lines := make([]string, 0, len(s.Tasks))
	for _, task := range s.Tasks {
		lines = append(lines, task.ID+" "+task.Split.String())
	}
	slices.Sort(lines)
	return digestLines(lines)
}
