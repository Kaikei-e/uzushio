// Package mine reads CMoA's traces and writes down what keeps going wrong.
//
// A trace is a record of one run of the harness: what each proposer was asked,
// what came back, whether it applied, and whether the verifier accepted it.
// Mining turns a directory of those records into failure patterns — an STPA
// unsafe control action per recurring failure, with the runs it was seen in
// cited as evidence.
//
// Two properties make the reading deterministic, and both are deliberate on
// CMoA's side. Every candidate is verified even after the first one passes, so
// a run says how many proposers agreed rather than only who won; and the prompt
// bytes are digested, so "the same prompt, a different outcome" is decidable
// without running anything again. No model is asked anything here: every rule
// in rules.go is a predicate over JSON fields and a small closed set of regular
// expressions over the text `git apply` and the runner produced.
//
// The package reads CMoA's schema and never writes it. Unknown keys are
// ignored, because a field CMoA adds must not stop mining; a schema_version
// this build does not know is skipped, because reading a document that says it
// is a different shape is not leniency but a guess.
package mine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrTrace is the sentinel every failure to read a trace wraps, so a caller can
// tell a malformed trace from an I/O failure without matching message text.
var ErrTrace = errors.New("mine: unreadable trace")

// SchemaVersion is the trace schema this build reads. A run declaring another
// is skipped rather than guessed at.
const SchemaVersion = 1

// runIDPattern is CMoA's run identifier, which is also the name of the
// directory one run lives in. It is repeated from CMoA rather than imported:
// uzushio does not depend on CMoA's module, and internal/vocab already spells
// the same shape for a pattern's evidence.
var runIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

// The candidate statuses CMoA writes. A status outside this set is carried as
// it was read; no rule fires on one, which is what an unknown word deserves.
const (
	CandidateOK        = "ok"
	CandidateHTTPError = "http_error"
	CandidateTimeout   = "timeout"
	CandidateMalformed = "malformed"
	CandidateNoDiff    = "no_diff"
)

// The verify statuses CMoA writes.
const (
	VerifyPass        = "pass"
	VerifyFail        = "fail"
	VerifyApplyFailed = "apply_failed"
	VerifyTimeout     = "timeout"
	VerifyRunnerError = "runner_error"
	VerifySkipped     = "skipped"
)

// The selection kinds CMoA writes.
const (
	SelectionSelected       = "selected"
	SelectionNoCandidate    = "no_candidate"
	SelectionVerifierFailed = "verifier_failed"
	SelectionJudgeTimeout   = "judge_timeout"
)

// Run is one CMoA run directory, loaded far enough for the rules to read it.
// Everything a rule needs is here; nothing else is, so a trace directory of any
// size costs one pass and a bounded amount of memory.
type Run struct {
	// ID is the run identifier, which is also the directory's name.
	ID string
	// Dir is the run directory, absolute.
	Dir string
	// TaskID is the task the run was about.
	TaskID string
	// TaskDir is the task directory as the run recorded it. It may no longer
	// exist: a trace outlives the checkout it was made from.
	TaskDir string
	// TaskFiles are the repository files the task named.
	TaskFiles []string
	// PromptVersion digests the templates the prompt was built from.
	PromptVersion string
	// Proposers are the proposers in configured order.
	Proposers []Proposer
	// Candidates are what came back, one per proposer, sorted by proposer id.
	Candidates []Candidate
	// Verifies are what the verifier said, keyed by proposer id.
	Verifies map[string]VerifyResult
	// Selection is what select concluded, empty where select never ran.
	Selection Selection
	// VerifyTimeoutSeconds is the run's limit on one verification, zero where
	// the effective config declared none.
	VerifyTimeoutSeconds int
}

// Proposer is one proposer as the run recorded it.
type Proposer struct {
	ID    string
	Model string
	// TimeoutSeconds is the proposer's own request budget, read from the
	// effective config; zero where the config declared none.
	TimeoutSeconds int
}

// Candidate is candidates/<proposer-id>.json.
type Candidate struct {
	ProposerID       string
	Model            string
	Status           string
	Error            string
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	// DiffFiles are the paths the extracted diff touches, empty unless the
	// status is ok.
	DiffFiles []string
}

// VerifyResult is verify/<proposer-id>/result.json.
type VerifyResult struct {
	CandidateID string
	Status      string
	ExitCode    int
	ApplyError  string
	Error       string
	// BandFailed names the invariants a banded verifier judged and failed.
	BandFailed []string
	// BandRows keeps every row the banded verifier printed.
	BandRows []BandRow
}

// BandRow is one invariant as the gate CSV reported it. The numbers are
// pointers because an unmeasured invariant carries none, and a zero would read
// as a measurement of zero.
type BandRow struct {
	Invariant string   `json:"invariant"`
	Value     *float64 `json:"value"`
	CIHalf    *float64 `json:"ci_half"`
	BandLo    *float64 `json:"band_lo"`
	BandHi    *float64 `json:"band_hi"`
	Verdict   string   `json:"verdict"`
}

// Selection is select.json, reduced to what the rules read.
type Selection struct {
	Kind        string
	CandidateID string
	AlsoPassed  []string
}

// Model returns the model behind one proposer id, or the id itself where the
// run named no model for it: a context sentence that says which proposer is a
// better sentence than one that says nothing.
func (r *Run) Model(proposerID string) string {
	for _, p := range r.Proposers {
		if p.ID == proposerID {
			if p.Model != "" {
				return p.Model
			}
			return p.ID
		}
	}
	return proposerID
}

// ProposerTimeoutSeconds returns one proposer's request budget, zero where the
// run recorded none.
func (r *Run) ProposerTimeoutSeconds(proposerID string) int {
	for _, p := range r.Proposers {
		if p.ID == proposerID {
			return p.TimeoutSeconds
		}
	}
	return 0
}

// Load reads every run under the directories named. A directory is walked
// whole: a task directory, a `runs/` directory, a tree of task directories and
// a single run directory all work, because what is recognised is a directory
// whose name is a run identifier and which holds a run.json.
//
// Runs come back sorted by identifier, which is chronological — CMoA's run id
// leads with a UTC timestamp — so a mining pass over the same traces produces
// the same evidence order every time.
func Load(dirs ...string) ([]Run, error) {
	var runs []Run
	seen := map[string]bool{}
	for _, dir := range dirs {
		found, err := loadTree(dir)
		if err != nil {
			return nil, err
		}
		for _, run := range found {
			if seen[run.ID] {
				// The same run reached through two arguments is one run. Its
				// identifier is CMoA's and unique, so the second reading is a
				// duplicate rather than a second observation.
				continue
			}
			seen[run.ID] = true
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	return runs, nil
}

// loadTree walks one directory for run directories.
func loadTree(dir string) ([]Run, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("mine: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("mine: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrTrace, abs)
	}
	var runs []Run
	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || !runIDPattern.MatchString(d.Name()) {
			return nil
		}
		if _, statErr := os.Stat(filepath.Join(path, "run.json")); statErr != nil {
			return nil
		}
		run, loadErr := loadRun(path)
		if loadErr != nil {
			return loadErr
		}
		if run != nil {
			runs = append(runs, *run)
		}
		// A run directory holds no run directories.
		return fs.SkipDir
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return runs, nil
}

// runJSON is run.json, reduced to the keys the rules read. The effective
// config is decoded separately because only two of its numbers are wanted and
// the rest is CMoA's business.
type runJSON struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	PromptVersion string `json:"prompt_version"`
	Task          struct {
		ID    string   `json:"id"`
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
	} `json:"task"`
	Config struct {
		Proposers []struct {
			ID             string `json:"id"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		} `json:"proposers"`
		Verify struct {
			TimeoutSeconds int `json:"timeout_seconds"`
		} `json:"verify"`
	} `json:"config"`
	Proposers []struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	} `json:"proposers"`
}

// candidateJSON is candidates/<id>.json, reduced the same way.
type candidateJSON struct {
	ProposerID   string `json:"proposer_id"`
	Model        string `json:"model"`
	Status       string `json:"status"`
	Error        string `json:"error"`
	FinishReason string `json:"finish_reason"`
	Usage        struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Diff *struct {
		Files []string `json:"files"`
	} `json:"diff"`
}

// verifyJSON is verify/<id>/result.json, reduced the same way.
type verifyJSON struct {
	CandidateID string `json:"candidate_id"`
	Status      string `json:"status"`
	ExitCode    int    `json:"exit_code"`
	ApplyError  string `json:"apply_error"`
	Error       string `json:"error"`
	Band        *struct {
		Failed []string  `json:"failed"`
		Rows   []BandRow `json:"rows"`
	} `json:"band"`
}

// selectJSON is select.json, reduced the same way.
type selectJSON struct {
	Selection struct {
		Kind        string `json:"kind"`
		CandidateID string `json:"candidate_id"`
	} `json:"selection"`
	AlsoPassed []string `json:"also_passed"`
}

// loadRun reads one run directory. A run whose schema_version this build does
// not know comes back nil rather than as an error: a corpus of traces spanning
// a schema change is the ordinary case, and refusing the whole pass because of
// one old run would make mining unusable exactly when there is most to mine.
func loadRun(dir string) (*Run, error) {
	var header runJSON
	if err := readJSON(filepath.Join(dir, "run.json"), &header); err != nil {
		return nil, err
	}
	if header.SchemaVersion != SchemaVersion {
		return nil, nil
	}
	id := header.RunID
	if id == "" {
		id = filepath.Base(dir)
	}
	if !runIDPattern.MatchString(id) {
		return nil, fmt.Errorf("%w: %s: run_id %q is not a CMoA run identifier", ErrTrace, dir, id)
	}
	run := &Run{
		ID:                   id,
		Dir:                  dir,
		TaskID:               header.Task.ID,
		TaskDir:              header.Task.Dir,
		TaskFiles:            append([]string(nil), header.Task.Files...),
		PromptVersion:        header.PromptVersion,
		Verifies:             map[string]VerifyResult{},
		VerifyTimeoutSeconds: header.Config.Verify.TimeoutSeconds,
	}
	budgets := map[string]int{}
	for _, p := range header.Config.Proposers {
		budgets[p.ID] = p.TimeoutSeconds
	}
	for _, p := range header.Proposers {
		run.Proposers = append(run.Proposers, Proposer{ID: p.ID, Model: p.Model, TimeoutSeconds: budgets[p.ID]})
	}
	if err := loadCandidates(run); err != nil {
		return nil, err
	}
	if err := loadVerifies(run); err != nil {
		return nil, err
	}
	if err := loadSelection(run); err != nil {
		return nil, err
	}
	return run, nil
}

// loadCandidates reads candidates/*.json, ignoring the raw text and the diff
// beside them: what a rule needs is the status and the summary CMoA already
// computed, and re-reading the bytes would be a second opinion about them.
func loadCandidates(run *Run) error {
	names, err := jsonNames(filepath.Join(run.Dir, "candidates"))
	if err != nil {
		return err
	}
	for _, name := range names {
		var raw candidateJSON
		if err := readJSON(filepath.Join(run.Dir, "candidates", name), &raw); err != nil {
			return err
		}
		candidate := Candidate{
			ProposerID:       raw.ProposerID,
			Model:            raw.Model,
			Status:           raw.Status,
			Error:            raw.Error,
			FinishReason:     raw.FinishReason,
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
		}
		if candidate.ProposerID == "" {
			candidate.ProposerID = strings.TrimSuffix(name, ".json")
		}
		if raw.Diff != nil {
			candidate.DiffFiles = append([]string(nil), raw.Diff.Files...)
		}
		run.Candidates = append(run.Candidates, candidate)
	}
	sort.Slice(run.Candidates, func(i, j int) bool {
		return run.Candidates[i].ProposerID < run.Candidates[j].ProposerID
	})
	return nil
}

// loadVerifies reads verify/<id>/result.json for every candidate directory.
func loadVerifies(run *Run) error {
	root := filepath.Join(run.Dir, "verify")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mine: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "result.json")
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var raw verifyJSON
		if err := readJSON(path, &raw); err != nil {
			return err
		}
		result := VerifyResult{
			CandidateID: raw.CandidateID,
			Status:      raw.Status,
			ExitCode:    raw.ExitCode,
			ApplyError:  raw.ApplyError,
			Error:       raw.Error,
		}
		if result.CandidateID == "" {
			result.CandidateID = entry.Name()
		}
		if raw.Band != nil {
			result.BandFailed = append([]string(nil), raw.Band.Failed...)
			result.BandRows = append([]BandRow(nil), raw.Band.Rows...)
		}
		run.Verifies[entry.Name()] = result
	}
	return nil
}

// loadSelection reads select.json, which is absent on a run propose wrote and
// select never reached.
func loadSelection(run *Run) error {
	path := filepath.Join(run.Dir, "select.json")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var raw selectJSON
	if err := readJSON(path, &raw); err != nil {
		return err
	}
	run.Selection = Selection{
		Kind:        raw.Selection.Kind,
		CandidateID: raw.Selection.CandidateID,
		AlsoPassed:  append([]string(nil), raw.AlsoPassed...),
	}
	return nil
}

// jsonNames lists the .json files in a directory, sorted. A missing directory
// is not a failure: a run that never reached the proposers has no candidates.
func jsonNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mine: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readJSON decodes one file, naming it in every failure. Unknown keys are
// ignored on purpose: CMoA owns the schema, and a reader that refused a key
// CMoA added would break on CMoA's next release rather than on its own mistake.
func readJSON(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("mine: %w", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrTrace, path, err)
	}
	return nil
}
