package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The outcome kinds a judge run can report. They are CMoA's vocabulary; this
// package reads them and does not invent any.
const (
	OutcomeSelected     = "selected"
	OutcomeNoCandidate  = "no_candidate"
	OutcomeJudgeTimeout = "judge_timeout"
	OutcomeJudgeFailed  = "judge_failed"
)

// JudgeFile is the record one judge run leaves in its run directory.
const JudgeFile = "judge.json"

// Judged is one run of the judge over one item, as read back from its trace.
type Judged struct {
	// Seed is the presentation seed the run was made at.
	Seed int `json:"seed"`
	// RunDir is the trace directory, relative to the suite where it can be.
	RunDir string `json:"run_dir"`
	// Outcome is what the judge concluded, Candidate the position it chose
	// where it chose one, and Reason the sub-reason of a no-candidate.
	Outcome   string `json:"outcome"`
	Candidate string `json:"candidate,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Pairs is the six calls, as three pairs of two orders.
	Pairs []Pair `json:"pairs"`
	// SwapConsistent, InvalidRetries and LatencyMS are the judge's own
	// summary of the run.
	SwapConsistent int   `json:"swap_consistent_pairs"`
	InvalidRetries int   `json:"invalid_output_retries"`
	LatencyMS      int64 `json:"latency_ms"`
}

// Measured says the run produced an answer about the candidates.
//
// `selected` and `no_candidate` do. `judge_timeout` and `judge_failed` do not:
// they are the machine failing, not the judge deciding, and folding them into
// abstention would make a fleet outage look like a judge that abstains
// consistently — which pushes swap and re-run agreement *up*, exactly the
// wrong way. An unmeasured run is counted and reported and enters no
// coefficient.
func (j Judged) Measured() bool {
	return j.Outcome == OutcomeSelected || j.Outcome == OutcomeNoCandidate
}

// Category is the run's answer as a kappa category. It is meaningful only for
// a measured run; a caller checks Measured first.
func (j Judged) Category() string {
	if j.Outcome == OutcomeSelected && j.Candidate != "" {
		return j.Candidate
	}
	return Abstain
}

// Pair is one pair of candidates and the two orders it was judged in.
type Pair struct {
	Members [2]string `json:"members"`
	Orders  []Order   `json:"orders"`
	Verdict string    `json:"verdict"`
}

// Order is one judge call.
type Order struct {
	First           string `json:"first"`
	Second          string `json:"second"`
	Choice          string `json:"choice"`
	ChoiceCandidate string `json:"choice_candidate"`
	Status          string `json:"status"`
	Retries         int    `json:"retries"`
	LatencyMS       int64  `json:"latency_ms"`
}

// side returns the order's answer as one of the pair's two canonical slots, or
// Abstain.
//
// The slot rather than the candidate is what the swap table is over: the two
// raters are "this pair shown one way round" and "shown the other", and a
// candidate name would make each pair its own vocabulary.
func (p Pair) side(o Order) string {
	switch o.ChoiceCandidate {
	case p.Members[0]:
		return "pair0"
	case p.Members[1]:
		return "pair1"
	}
	return Abstain
}

// judgeFile is the shape of judge.json this package reads. Every other key is
// CMoA's business, so the decoder is not strict: a judge that starts recording
// something new must not stop a calibration.
type judgeFile struct {
	SchemaVersion int      `json:"schema_version"`
	Candidates    []string `json:"candidates"`
	Pairs         []struct {
		Pair    []string `json:"pair"`
		Orders  []Order  `json:"orders"`
		Verdict string   `json:"verdict"`
	} `json:"pairs"`
	Outcome struct {
		Kind        string `json:"kind"`
		CandidateID string `json:"candidate_id"`
		Reason      string `json:"reason"`
	} `json:"outcome"`
	SwapConsistent int   `json:"swap_consistent_pairs"`
	InvalidRetries int   `json:"invalid_output_retries"`
	LatencyMS      int64 `json:"latency_ms"`
}

// Runner performs one judge run of one item. It is an interface because the
// arithmetic above it is worth testing without a fleet.
type Runner interface {
	Judge(ctx context.Context, suite Suite, task Task, seed int) (Judged, error)
}

// CMoARunner runs the judge by asking the harness binary to do it.
type CMoARunner struct {
	// Binary is the harness command line tool.
	Binary string
	// Config is the harness configuration passed to every call.
	Config string
	// Env is added to the environment of the call.
	Env []string
	// Log receives one line per call.
	Log func(string)
}

// Judge runs `cmoa judge` over one item and reads the trace it left.
func (r CMoARunner) Judge(ctx context.Context, suite Suite, task Task, seed int) (Judged, error) {
	args := []string{"judge", "--task", suite.TaskDir(task)}
	for _, candidate := range suite.Candidates(task) {
		args = append(args, "--candidate", candidate)
	}
	args = append(args, "--seed", strconv.Itoa(seed))
	if r.Config != "" {
		args = append(args, "--config", r.Config)
	}
	cmd := exec.CommandContext(ctx, r.Binary, args...) //nolint:gosec // the caller names the harness
	cmd.Env = append(os.Environ(), r.Env...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return Judged{}, fmt.Errorf("%w: %s seed %d: %w: %s",
			ErrJudge, task.ID, seed, err, firstLine(errOut.String()))
	}
	dir, err := runDir(out.String())
	if err != nil {
		return Judged{}, fmt.Errorf("%w: %s seed %d: %w", ErrJudge, task.ID, seed, err)
	}
	judged, err := ReadJudged(dir)
	if err != nil {
		return Judged{}, err
	}
	judged.Seed = seed
	if r.Log != nil {
		r.Log(fmt.Sprintf("judge %s seed=%d -> %s", task.ID, seed, judged.Outcome))
	}
	return judged, nil
}

// runDir finds the trace directory in what the harness printed.
//
// The command prints the outcome as JSON and the run directory, and this does
// not depend on which comes first: it walks the lines backwards and takes the
// first one that names a directory holding a judge record. A rule that read a
// fixed line would break the day the harness printed a warning.
func runDir(stdout string) (string, error) {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		if _, err := os.Stat(filepath.Join(line, JudgeFile)); err == nil {
			return line, nil
		}
	}
	return "", fmt.Errorf("the harness printed no run directory holding a %s", JudgeFile)
}

// ReadJudged reads one judge record out of a trace directory.
func ReadJudged(dir string) (Judged, error) {
	name := filepath.Join(dir, JudgeFile)
	body, err := os.ReadFile(name) //nolint:gosec // a file the harness just wrote
	if err != nil {
		return Judged{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var file judgeFile
	if err := json.Unmarshal(body, &file); err != nil {
		return Judged{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	if file.Outcome.Kind == "" {
		return Judged{}, fmt.Errorf("%w: %s records no outcome", ErrJudge, name)
	}
	judged := Judged{
		RunDir:         dir,
		Outcome:        file.Outcome.Kind,
		Candidate:      file.Outcome.CandidateID,
		Reason:         file.Outcome.Reason,
		SwapConsistent: file.SwapConsistent,
		InvalidRetries: file.InvalidRetries,
		LatencyMS:      file.LatencyMS,
	}
	for _, pair := range file.Pairs {
		if len(pair.Pair) != 2 {
			return Judged{}, fmt.Errorf("%w: %s: a pair names %d candidates", ErrJudge, name, len(pair.Pair))
		}
		judged.Pairs = append(judged.Pairs, Pair{
			Members: [2]string{pair.Pair[0], pair.Pair[1]},
			Orders:  pair.Orders,
			Verdict: pair.Verdict,
		})
	}
	return judged, nil
}

// firstLine is the one line of a failure worth putting in a message.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// scrub takes the machine out of text this package did not write.
//
// A harness's error message is copied into the journal verbatim, and it names
// configuration files and task directories by absolute path, so a report
// committed from a developer's machine would otherwise carry that machine's
// layout into the corpus. Paths under one of the bases become relative; the
// home directory becomes a tilde.
func scrub(text string, bases ...string) string {
	for _, base := range bases {
		root, err := filepath.Abs(base)
		if err != nil || root == "" || root == "/" {
			continue
		}
		text = strings.ReplaceAll(text, root+string(filepath.Separator), "")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return text
	}
	return strings.ReplaceAll(text, home, "~")
}

// RecordPath is how a trace directory is written into a report.
//
// It answers with a path relative to the first base the directory lies under,
// and with the directory's own name where it lies under none. It never answers
// with an absolute path, and that is the whole of its job: a calibration report
// is committed, the traces are written wherever the harness's configuration
// points, and the two facts together used to put the home directory of
// whichever machine ran the judge into a public file. The earlier version
// asked filepath.Rel to relate a relative base to an absolute target, which
// fails, and fell back to the absolute path — so the leak happened exactly
// when the caller passed `--suite examples/…` rather than a full path, which
// is how anyone would type it.
//
// Losing the prefix loses nothing a reader needs: the last element is the run
// identifier, which is what names the trace.
func RecordPath(dir string, bases ...string) string {
	if dir == "" {
		return ""
	}
	target, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Base(dir)
	}
	for _, base := range bases {
		if base == "" {
			continue
		}
		root, err := filepath.Abs(base)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.ToSlash(relative)
	}
	return filepath.Base(dir)
}
