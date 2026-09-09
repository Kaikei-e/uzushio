package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Arm is one side of a matched pair.
type Arm string

// The two arms. They are spelled the way the per-trial record spells them.
const (
	// ArmBase is the harness as the vault has it today.
	ArmBase Arm = "base"
	// ArmEdit is that harness plus the candidate.
	ArmEdit Arm = "edit"
)

// String returns the arm as the record writes it.
func (a Arm) String() string { return string(a) }

// The error classes a trial can carry. They are the vocabulary of the
// per-trial record, and the distinction they draw is the one that matters: an
// answer the harness gave, versus no answer at all.
const (
	// ErrorNone is a trial that produced an answer, whether or not the answer
	// was a pass.
	ErrorNone = "none"
	// ErrorTimeout is a judge or a verifier that ran out of time.
	ErrorTimeout = "timeout"
	// ErrorInfra is the harness or the machine under it failing: a connection
	// refused, a binary that would not start, a run directory that could not
	// be written. It is not evidence about the edit and the pair it belongs
	// to is not counted.
	ErrorInfra = "infra"
	// ErrorHarnessCrash is the harness exiting on its own configuration.
	ErrorHarnessCrash = "harness_crash"
	// ErrorVerifier is the verifier itself failing rather than answering.
	ErrorVerifier = "verifier_error"
)

// Seed derives the seed one trial runs at, from the task and the repeat index
// and nothing else.
//
// Both arms of a pair get the same number, which is the whole point: pairing
// is the cheapest variance reduction available and the difference between a
// usable gate and a useless one at this budget. The hash is FNV-1a because it
// is in the standard library, it is stable across builds and platforms, and
// nothing here needs it to be hard to invert.
func Seed(task string, repeat int) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(task))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(repeat)))
	return int64(h.Sum64() & math.MaxInt64)
}

// Trial is one run of one task under one harness.
type Trial struct {
	// Task is the task identifier, TaskDir the directory it lives in.
	Task    string
	TaskDir string
	// Repeat is the repeat index within the task, from zero.
	Repeat int
	// Seed is Seed(Task, Repeat) — the same number on both arms.
	Seed int64
	// Temperature is the sampling temperature. Zero on every trial of a
	// measurement; it does not make a local fleet deterministic, and the A/A
	// calibration is what says how far from deterministic it is.
	Temperature float64
	// Arm says which harness this is.
	Arm Arm
	// Harness is the rendered harness directory the arm reads. It is the
	// directory for *this arm* — the calibration runs the baseline on both,
	// and deriving it from the arm's name instead would make the A/A a
	// mislabelled A/B.
	Harness string
	// SuiteDir is the suite file's directory. Nothing but path arithmetic
	// uses it: the trace directory the harness leaves behind is recorded
	// relative to it, so no record carries an absolute path.
	SuiteDir string
	// HarnessSHA256 is that directory's tree digest, which is what the cache
	// is keyed on and what the record names.
	HarnessSHA256 string
}

// Outcome is what one trial produced.
type Outcome struct {
	// Pass says the harness produced a candidate the verifier accepted.
	Pass bool `json:"pass"`
	// SelectionKind is what select.json concluded, verbatim.
	SelectionKind string `json:"selection_kind"`
	// ErrorClass is one of the classes above.
	ErrorClass string `json:"error_class"`
	// Error is the text where there was one.
	Error string `json:"error,omitempty"`
	// RunID and RunDir name the trace the harness left behind, so a reader of
	// this record can go and look at the prompts and the candidates. RunDir is
	// relative to the suite directory wherever it can be: a record that is
	// committed to a public repository must not carry the home directory of
	// whichever machine ran it.
	RunID  string `json:"run_id,omitempty"`
	RunDir string `json:"run_dir,omitempty"`
	// WallMS is how long the trial took.
	WallMS int64 `json:"wall_ms"`
	// TokensIn and TokensOut are what the proposers reported, summed.
	TokensIn  int `json:"tokens_in"`
	TokensOut int `json:"tokens_out"`
	// Candidates is how many proposers answered at all.
	Candidates int `json:"candidates"`
}

// Answered says the trial produced evidence about the edit. A trial that did
// not is not counted in any arithmetic: "not measured" must not enter the
// numbers that a verdict is read off.
//
// Only ErrorNone answers. A verifier that could not run says nothing about any
// candidate — it is the harness's own word for it — and a timeout says nothing
// either; scoring both as failures makes an absent container look like a
// regression, and it lands asymmetrically, because the baseline arm is often
// served from the cache while the edited arm is always run. The same goes for
// infrastructure and for the harness exiting on its own configuration. All
// four are journalled as `pending` and cost wall-clock rather than evidence.
func (o Outcome) Answered() bool { return o.ErrorClass == ErrorNone }

// Runner performs one trial. It is an interface because a run is a procedure
// worth testing without a fleet: the tests drive it with a fake that answers
// from a table, and the exec runner is tested on its own against a canned
// harness binary.
type Runner interface {
	Run(ctx context.Context, trial Trial) (Outcome, error)
}

// CMoARunner runs a trial by asking the harness binary to propose and then to
// select. It is two calls rather than one because that is the harness's own
// shape: propose writes a run directory and prints its name, select reads that
// directory and writes what it concluded into it.
type CMoARunner struct {
	// Binary is the harness command line tool.
	Binary string
	// Config is the harness configuration file passed to both calls.
	Config string
	// Env is added to the environment of both calls. Nil is the ordinary case.
	Env []string
	// Log receives one line per call, for a person watching a long run.
	Log func(string)
}

// selectFile is the part of select.json a run reads. Every other key is the
// harness's business.
type selectFile struct {
	Selection struct {
		Kind string `json:"kind"`
	} `json:"selection"`
}

// candidateFile is the part of a candidate record a run reads: the usage, so a
// record can say what the trial cost, and the status, so it can say how many
// proposers answered.
type candidateFile struct {
	Status string `json:"status"`
	Usage  struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// runFile is the part of run.json a run reads back: the identity of the trace,
// and the harness digest the tool computed for itself.
type runFile struct {
	RunID   string `json:"run_id"`
	Harness struct {
		Render struct {
			TreeSHA256 string `json:"tree_sha256"`
		} `json:"render"`
	} `json:"harness"`
}

// Run performs one trial.
func (r CMoARunner) Run(ctx context.Context, trial Trial) (Outcome, error) {
	started := time.Now()
	outcome := Outcome{ErrorClass: ErrorNone}
	finish := func(o Outcome) (Outcome, error) {
		o.WallMS = time.Since(started).Milliseconds()
		return o, nil
	}
	if !isSHA256(trial.HarnessSHA256) {
		return Outcome{}, fmt.Errorf(
			"%w: task %s %s: render supplied invalid harness digest %q",
			ErrRun, trial.Task, trial.Arm, trial.HarnessSHA256)
	}

	args := []string{
		"propose",
		"--task", trial.TaskDir,
		"--harness", trial.Harness,
		"--seed", strconv.FormatInt(trial.Seed, 10),
		"--temperature", strconv.FormatFloat(trial.Temperature, 'f', -1, 64),
	}
	if r.Config != "" {
		args = append(args, "--config", r.Config)
	}
	stdout, stderr, err := r.exec(ctx, args)
	if err != nil {
		outcome.ErrorClass = classify(ctx, err)
		outcome.Error = Scrub(firstLine(stderr, err))
		return finish(outcome)
	}
	outcome.RunDir = strings.TrimSpace(stdout)
	if outcome.RunDir == "" {
		outcome.ErrorClass = ErrorInfra
		outcome.Error = "propose printed no run directory"
		return finish(outcome)
	}
	r.logf("propose %s %s seed=%d -> %s", trial.Task, trial.Arm, trial.Seed, filepath.Base(outcome.RunDir))

	// The harness computes the digest of the directory it read, and this is
	// where the two are compared. A mismatch means the two arms of a pair were
	// not what the record says they were, which invalidates the pair rather
	// than one trial in it, so it is an error rather than an outcome.
	var header runFile
	if err := readJSON(filepath.Join(outcome.RunDir, "run.json"), &header); err != nil {
		return Outcome{}, fmt.Errorf("%w: task %s %s: read CMoA run header: %w", ErrRun, trial.Task, trial.Arm, err)
	}
	if strings.TrimSpace(header.RunID) == "" {
		return Outcome{}, fmt.Errorf("%w: task %s %s: CMoA run header has no run_id", ErrRun, trial.Task, trial.Arm)
	}
	outcome.RunID = header.RunID
	got := header.Harness.Render.TreeSHA256
	if !isSHA256(got) {
		return Outcome{}, fmt.Errorf(
			"%w: task %s %s: CMoA run header has invalid harness digest %q",
			ErrRun, trial.Task, trial.Arm, got)
	}
	if got != trial.HarnessSHA256 {
		return Outcome{}, fmt.Errorf(
			"%w: task %s %s: the harness read a directory with digest %s, the render says %s",
			ErrRun, trial.Task, trial.Arm, got, trial.HarnessSHA256)
	}

	args = []string{"select", "--task", trial.TaskDir, "--run", outcome.RunDir}
	if r.Config != "" {
		args = append(args, "--config", r.Config)
	}
	_, stderr, err = r.exec(ctx, args)
	if err != nil {
		outcome.ErrorClass = classify(ctx, err)
		outcome.Error = Scrub(firstLine(stderr, err))
		outcome.RunDir = relativeTo(trial.SuiteDir, outcome.RunDir)
		return finish(outcome)
	}

	var selection selectFile
	if err := readJSON(filepath.Join(outcome.RunDir, "select.json"), &selection); err != nil {
		outcome.ErrorClass = ErrorInfra
		outcome.Error = Scrub(err.Error())
		outcome.RunDir = relativeTo(trial.SuiteDir, outcome.RunDir)
		return finish(outcome)
	}
	outcome.SelectionKind = selection.Selection.Kind
	switch selection.Selection.Kind {
	case "selected":
		outcome.Pass = true
	case "no_candidate":
		// A real answer about the harness: it proposed, nothing passed.
	case "verifier_failed":
		outcome.ErrorClass = ErrorVerifier
	case "judge_timeout":
		outcome.ErrorClass = ErrorTimeout
	default:
		outcome.ErrorClass = ErrorInfra
		outcome.Error = "select.json says " + strconv.Quote(selection.Selection.Kind)
	}
	outcome.TokensIn, outcome.TokensOut, outcome.Candidates = usage(outcome.RunDir)
	r.logf("select %s %s -> %s", trial.Task, trial.Arm, outcome.SelectionKind)
	outcome.RunDir = relativeTo(trial.SuiteDir, outcome.RunDir)
	return finish(outcome)
}

// isSHA256 reports whether digest is a complete SHA-256 digest as the two
// renderers record it. An absent or malformed digest cannot establish that the
// prompt CMoA read was the harness uzushio rendered.
func isSHA256(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func (r CMoARunner) logf(format string, a ...any) {
	if r.Log != nil {
		r.Log(fmt.Sprintf(format, a...))
	}
}

// exec runs the harness binary and returns its two streams.
func (r CMoARunner) exec(ctx context.Context, args []string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, r.Binary, args...) //nolint:gosec // the caller names the harness
	cmd.Env = append(os.Environ(), r.Env...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// classify turns a failed harness call into one of the error classes.
//
// The exit codes are the harness's own contract: 2 is a usage mistake and 3 is
// a configuration or task it refuses, both of which are the run being set up
// wrong rather than the machine under it failing. A timeout is read from the
// context this call was made under, and from the signal a killed child died
// of — never from the text of the message, because "timeout" appears in
// `request_timeout` and `judge_timeout_ms` and in half the configuration keys
// the harness prints when it complains about one.
//
// Every class here is uncounted either way; what the distinction buys is a
// journal a person can read after an outage.
func classify(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return ErrorTimeout
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch {
		case exitErr.ExitCode() == 2, exitErr.ExitCode() == 3:
			return ErrorHarnessCrash
		case exitErr.ExitCode() == -1:
			// Killed by a signal, which for a child this process started is
			// the context's deadline or a cancellation.
			return ErrorTimeout
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorTimeout
	}
	return ErrorInfra
}

// firstLine is the one line of a failure worth putting in a record.
func firstLine(stderr string, err error) string {
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}

// usage sums what the proposers reported for one trial.
func usage(runDir string) (in, out, answered int) {
	entries, err := os.ReadDir(filepath.Join(runDir, "candidates"))
	if err != nil {
		return 0, 0, 0
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var candidate candidateFile
		if err := readJSON(filepath.Join(runDir, "candidates", entry.Name()), &candidate); err != nil {
			continue
		}
		in += candidate.Usage.PromptTokens
		out += candidate.Usage.CompletionTokens
		if candidate.Status == "ok" {
			answered++
		}
	}
	return in, out, answered
}

// relativeTo expresses a path under base as a relative one, and leaves it
// alone where it is not under base — an absolute path that has escaped is
// better reported than silently rewritten into a wrong relative one. The
// caller pairs it with Scrub for that case.
func relativeTo(base, name string) string {
	if base == "" || name == "" {
		return Scrub(name)
	}
	relative, err := filepath.Rel(base, name)
	if err != nil || strings.HasPrefix(relative, "..") {
		return Scrub(name)
	}
	return filepath.ToSlash(relative)
}

// Scrub replaces the running user's home directory with a tilde.
//
// It is the last line of defence for the one thing that must not reach a
// public repository: text this package did not write. The harness's stderr is
// copied verbatim into a trial record, and it names compose files by absolute
// path, so a record committed from a developer's machine would otherwise carry
// that machine's home directory into the corpus.
func Scrub(text string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return text
	}
	return strings.ReplaceAll(text, home, "~")
}

func readJSON(name string, into any) error {
	body, err := os.ReadFile(name) //nolint:gosec // a file the harness just wrote
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRun, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrRun, name, err)
	}
	return nil
}

// Cache remembers baseline outcomes.
//
// It is the single biggest saving available to this design and it is not an
// optimisation of the statistics: the baseline harness does not change while a
// night's candidates are measured, so one baseline draw per (task, seed,
// baseline digest, fleet) serves every candidate, and a candidate costs n
// trials rather than 2n.
//
// All four parts of the key are load-bearing. Without the fleet, two runs
// whose only difference is a swapped model share every baseline outcome, and
// the comparison the second one publishes is against a baseline the second
// fleet never ran — which is the failure that corrupts a result without ever
// producing an error. The fleet digest is written into the entry as well as
// into the path, so a hand-moved file is a miss rather than a lie.
type Cache struct {
	// Dir is the cache root. An empty Dir is a cache that remembers nothing,
	// which is what a fresh baseline draw needs.
	Dir string
	// Fleet is the digest of the proposer set the outcomes were drawn from.
	Fleet string
}

// Get returns a remembered outcome.
func (c Cache) Get(trial Trial) (Outcome, bool) {
	if c.Dir == "" {
		return Outcome{}, false
	}
	var entry cacheEntry
	if err := readJSON(c.path(trial), &entry); err != nil {
		return Outcome{}, false
	}
	// The path already carries the digest, so a mismatch here means the file
	// was moved rather than written by a run. Treat it as a miss: paying for
	// the trial again is cheaper than publishing a comparison against a
	// baseline some other fleet drew.
	if entry.Fleet != c.Fleet || entry.SchemaVersion != SchemaVersion {
		return Outcome{}, false
	}
	return entry.Outcome, true
}

// cacheEntry is one remembered outcome and what it was drawn under.
type cacheEntry struct {
	SchemaVersion int    `json:"schema_version"`
	Fleet         string `json:"fleet_sha256"`
	Harness       string `json:"harness_sha256"`
	Task          string `json:"task_id"`
	Seed          int64  `json:"seed"`
	Outcome       `json:"outcome"`
}

// Put remembers one. A cache that cannot be written is not an error: the run
// still has its answer, and the next one pays for it again.
//
// Only a genuine answer is remembered — the harness proposed and the verifier
// judged — and the selection kind is checked as well as the error class,
// because the cache outlives the run that wrote it. A transient timeout
// persisted here would be replayed as a baseline *failure* for every candidate
// measured afterwards, permanently handicapping the baseline on that
// (task, seed) cell, and no later run could tell.
func (c Cache) Put(trial Trial, outcome Outcome) {
	if c.Dir == "" || !outcome.Answered() {
		return
	}
	switch outcome.SelectionKind {
	case "selected", "no_candidate":
	default:
		return
	}
	name := c.path(trial)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return
	}
	body, err := json.MarshalIndent(cacheEntry{
		SchemaVersion: SchemaVersion,
		Fleet:         c.Fleet,
		Harness:       trial.HarnessSHA256,
		Task:          trial.Task,
		Seed:          trial.Seed,
		Outcome:       outcome,
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(name, append(body, '\n'), 0o644) //nolint:gosec // a cache entry is world-readable on purpose
}

// path is <dir>/<task>/<seed>/<harness digest>-<fleet digest>.json.
func (c Cache) path(trial Trial) string {
	fleet := c.Fleet
	if len(fleet) > 16 {
		fleet = fleet[:16]
	}
	return filepath.Join(c.Dir,
		trial.Task,
		strconv.FormatInt(trial.Seed, 10),
		trial.HarnessSHA256+"-"+fleet+".json")
}

// digestLines hashes a listing the way every other digest here does: one line
// each, newline terminated, SHA-256 of the concatenation.
func digestLines(lines []string) string {
	sum := sha256.New()
	for _, line := range lines {
		fmt.Fprintf(sum, "%s\n", line)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
