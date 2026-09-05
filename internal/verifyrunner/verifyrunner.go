// Package verifyrunner runs one verification and reads the answer back.
//
// The verification itself is CMoA's: `cmoa verify --task <dir> --diff <file>`
// creates a worktree at the task's revision, applies the diff, runs the compose
// verifier and prints one JSON object on standard output. uzushio's part is to
// call it, decode that object, and hand back a value the health check can
// classify. Everything else about the verifier — docker, the compose project
// name, the timeout — belongs to CMoA and is not repeated here.
//
// Runner is an interface because a health check is a procedure worth testing
// without docker: the tests drive it with a fake that answers from a table, and
// the exec runner is tested on its own against a canned `cmoa`.
package verifyrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrRunner is the sentinel every failure to get an answer wraps. It means
// "there is no verification result", which is a different thing from a
// verification that came back saying no.
var ErrRunner = errors.New("verifyrunner")

// Status is what one verification concluded. It is CMoA's vocabulary, which is
// trace.VerifyStatus without `skipped`: nothing `cmoa verify` runs is skipped,
// because the caller named the one diff to verify.
type Status string

// The statuses.
const (
	// StatusPass is a verifier that exited zero.
	StatusPass Status = "pass"
	// StatusFail is a verifier that exited non-zero: the answer is no.
	StatusFail Status = "fail"
	// StatusApplyFailed is a diff that did not apply, so nothing was verified.
	StatusApplyFailed Status = "apply_failed"
	// StatusTimeout is a verifier that ran out of time.
	StatusTimeout Status = "timeout"
	// StatusRunnerError is docker or the runner itself failing.
	StatusRunnerError Status = "runner_error"
)

// String returns the status as the JSON writes it.
func (s Status) String() string { return string(s) }

// Valid reports whether s is one of the five statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusPass, StatusFail, StatusApplyFailed, StatusTimeout, StatusRunnerError:
		return true
	}
	return false
}

// SchemaVersion is the version of the verify result this build reads. It is
// the one field leniency does not extend to: ignoring a key CMoA added is
// right, and reading a document that says it is a different shape is not.
const SchemaVersion = 1

// BandRow is one invariant a banded verifier measured: what it read, the band
// it was held to, and what that means.
//
// Every number is a pointer because every number may be absent. A `skipped` row
// is an invariant whose input never arrived — no k6 in the image, a generator
// that did not run — and an `info` row is one that is reported and never
// judged; neither carries a value, and a zero would read as a measurement.
type BandRow struct {
	Invariant string   `json:"invariant"`
	Value     *float64 `json:"value"`
	CIHalf    *float64 `json:"ci_half"`
	BandLo    *float64 `json:"band_lo"`
	BandHi    *float64 `json:"band_hi"`
	Verdict   string   `json:"verdict"`
}

// Band is what a banded verifier concluded, as `cmoa verify` reports it. It is
// absent for an exit-code verifier, and absent for a banded one that never got
// as far as printing its rows.
//
// uzushio does not judge it: CMoA read the rows and turned them into the
// status, and reading them a second time here would be a second opinion nobody
// asked for. It is carried so that a report says which invariant moved, which
// is the difference between "the verifier rejected this mutant" and "the
// verifier rejected this mutant because rr_spread_req left 0-0".
type Band struct {
	Judged  int       `json:"judged"`
	Failed  []string  `json:"failed"`
	Skipped []string  `json:"skipped"`
	Rows    []BandRow `json:"rows"`
}

// Result is the object `cmoa verify` prints. Unknown keys are ignored: CMoA
// owns the contract and may add to it.
type Result struct {
	SchemaVersion int      `json:"schema_version"`
	Task          string   `json:"task"`
	Rev           string   `json:"rev"`
	DiffSHA256    string   `json:"diff_sha256"`
	Label         string   `json:"label"`
	Status        Status   `json:"status"`
	Band          *Band    `json:"band,omitempty"`
	ExitCode      int      `json:"exit_code"`
	DurationMS    int64    `json:"duration_ms"`
	Command       []string `json:"command"`
	ProjectName   string   `json:"project_name"`
	ApplyError    string   `json:"apply_error"`
	Error         string   `json:"error"`
	StartedAt     string   `json:"started_at"`
	FinishedAt    string   `json:"finished_at"`
	CMoAVersion   string   `json:"cmoa_version"`
}

// Request is one verification: which task, which diff, what to call the run
// and where to keep what it produced.
//
// It is a struct rather than five positional arguments because four of the
// five are strings and a caller that swapped two of them would get a run that
// worked and measured the wrong thing.
type Request struct {
	// TaskDir is the task directory, absolute.
	TaskDir string
	// DiffPath is the diff to verify, absolute.
	DiffPath string
	// Label names the run. It goes into the compose project name and into the
	// result, so it is how a report line is matched to a container.
	Label string
	// OutDir is where the verifier's result.json, stdout.txt and stderr.txt
	// are kept. Empty discards them.
	OutDir string
	// Timeout limits one verification. Zero leaves the limit to the task and
	// to CMoA's configuration.
	Timeout time.Duration
}

// Runner produces one verification result.
type Runner interface {
	Verify(ctx context.Context, req Request) (Result, error)
}

// Exec runs the real `cmoa verify`.
type Exec struct {
	// Bin is the cmoa binary. Empty means "cmoa", found on PATH.
	Bin string
}

// Verify runs `cmoa verify` and decodes what it printed.
//
// CMoA's exit codes are 0 for pass, 1 for an answer of no, 3 for a runner
// error and 2 for a task or usage mistake. The first three all print the JSON
// object, so the object is what decides: a run whose output decodes is a
// result whatever the exit code was, and one whose output does not decode is a
// failure to get an answer at all, with cmoa's own diagnostics carried into
// the error.
func (e Exec) Verify(ctx context.Context, req Request) (Result, error) {
	bin := e.Bin
	if bin == "" {
		bin = "cmoa"
	}
	args := []string{"verify", "--task", req.TaskDir, "--diff", req.DiffPath}
	if req.Label != "" {
		args = append(args, "--label", req.Label)
	}
	if req.OutDir != "" {
		args = append(args, "--out", req.OutDir)
	}
	if req.Timeout > 0 {
		args = append(args, "--timeout", req.Timeout.String())
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	runErr := cmd.Run()

	// The error carries the tool's name and its own diagnostics and not the
	// argument list. The argv holds the task directory and the diff path, both
	// absolute, and this string is copied into a report that is committed.
	result, decodeErr := Decode(out.Bytes())
	if decodeErr != nil {
		return Result{}, fmt.Errorf("%w: %s verify: %w: %s",
			ErrRunner, filepath.Base(bin), decodeErr, tail(errOut.String()))
	}
	if runErr != nil && result.Status == StatusPass {
		// A pass that exited non-zero is a contract violation rather than a
		// verdict, and believing the JSON over the exit code would turn a
		// broken runner into a green report.
		return Result{}, fmt.Errorf("%w: %s reported %s and exited with %w: %s",
			ErrRunner, filepath.Base(bin), result.Status, runErr, tail(errOut.String()))
	}
	if req.Label != "" && result.Label != req.Label {
		// An answer about another run is not an answer about this one. Nothing
		// downstream would notice: the report line is built from the request.
		return Result{}, fmt.Errorf("%w: asked %s about %q and it answered about %q",
			ErrRunner, filepath.Base(bin), req.Label, result.Label)
	}
	return result, nil
}

// Decode reads the one JSON object `cmoa verify` prints, and reports a status
// outside the vocabulary rather than passing it on as something to classify.
func Decode(body []byte) (Result, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		// CMoA prints nothing on stdout for a usage or task error, which is
		// the ordinary way to get here: the run never happened.
		return Result{}, errors.New("nothing was printed on stdout")
	}
	var result Result
	if err := json.Unmarshal(trimmed, &result); err != nil {
		return Result{}, fmt.Errorf("decode the verify result: %w", err)
	}
	if result.SchemaVersion != SchemaVersion {
		// The version is what says when leniency stops. A schema_version 2 with
		// a changed status vocabulary would otherwise be read as a verdict.
		return Result{}, fmt.Errorf("the verify result is schema_version %d; this build reads %d",
			result.SchemaVersion, SchemaVersion)
	}
	if !result.Status.Valid() {
		return Result{}, fmt.Errorf("the verify result carries status %q, which is not one of "+
			"pass, fail, apply_failed, timeout or runner_error", result.Status)
	}
	return result, nil
}

// tail returns the last few lines of a stream, which is the part of a
// diagnostic that says what went wrong.
func tail(s string) string {
	const lines = 5
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "(no output)"
	}
	split := strings.Split(trimmed, "\n")
	if len(split) > lines {
		split = split[len(split)-lines:]
	}
	return strings.Join(split, "\n")
}
