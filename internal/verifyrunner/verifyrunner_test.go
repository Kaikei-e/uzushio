package verifyrunner_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
)

// fakeCMoA writes a `cmoa` that prints what the test tells it to and exits
// with the code the test names. It is a shell script rather than a committed
// fixture so that the executable bit is set by the code that needs it.
func fakeCMoA(t *testing.T, stdout, stderr string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake runner is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cmoa")
	script := "#!/bin/sh\n" +
		"printf '%s' " + shellQuote(stdout) + "\n" +
		"printf '%s' " + shellQuote(stderr) + " >&2\n" +
		"exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // a fake binary has to be executable
		t.Fatalf("write the fake cmoa: %v", err)
	}
	return path
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const passJSON = `{"schema_version":1,"task":"hello","rev":"abc","diff_sha256":"d",` +
	`"label":"reference-1","status":"pass","exit_code":0,"duration_ms":3495,` +
	`"project_name":"cmoa-hello-verify-reference-1","cmoa_version":"v0.0.0-test"}`

func TestVerifyPass(t *testing.T) {
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, passJSON, "", 0)}
	got, err := runner.Verify(t.Context(), verifyrunner.Request{
		TaskDir: "/tmp/task", DiffPath: "/tmp/reference.diff", Label: "reference-1",
		Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Status != verifyrunner.StatusPass || got.ExitCode != 0 || got.DurationMS != 3495 {
		t.Fatalf("result = %+v", got)
	}
	if got.CMoAVersion != "v0.0.0-test" {
		t.Errorf("cmoa_version = %q", got.CMoAVersion)
	}
}

// TestVerifyFail is the ordinary "no". CMoA exits 1 for it, which is a verdict
// rather than a failure, so the runner returns the result and no error.
func TestVerifyFail(t *testing.T) {
	body := strings.ReplaceAll(passJSON, `"status":"pass","exit_code":0`, `"status":"fail","exit_code":1`)
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, body, "go test failed\n", 1)}
	got, err := runner.Verify(t.Context(), verifyrunner.Request{TaskDir: "/tmp/task", DiffPath: "/tmp/m.diff"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Status != verifyrunner.StatusFail || got.ExitCode != 1 {
		t.Fatalf("result = %+v", got)
	}
}

// TestVerifyRunnerError is CMoA's exit 3: docker itself failed. The JSON is
// still printed, so it is still a result — one the health check reads as
// inconclusive rather than as a mutant surviving.
func TestVerifyRunnerError(t *testing.T) {
	body := strings.ReplaceAll(passJSON, `"status":"pass","exit_code":0`,
		`"status":"runner_error","exit_code":0,"error":"docker: no such host"`)
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, body, "docker: no such host\n", 3)}
	got, err := runner.Verify(t.Context(), verifyrunner.Request{TaskDir: "/tmp/task", DiffPath: "/tmp/m.diff"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Status != verifyrunner.StatusRunnerError || got.Error == "" {
		t.Fatalf("result = %+v", got)
	}
}

// TestVerifyUsageError is CMoA's exit 2, which prints nothing on stdout. There
// is no result to read, so the runner fails and carries cmoa's own complaint.
func TestVerifyUsageError(t *testing.T) {
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, "", "cmoa: task.json: version: must be 1 or 2\n", 2)}
	_, err := runner.Verify(t.Context(), verifyrunner.Request{TaskDir: "/tmp/task", DiffPath: "/tmp/m.diff"})
	if !errors.Is(err, verifyrunner.ErrRunner) {
		t.Fatalf("Verify error = %v, want ErrRunner", err)
	}
	if !strings.Contains(err.Error(), "version: must be 1 or 2") {
		t.Errorf("Verify error = %q, which drops cmoa's own message", err)
	}
}

// TestVerifyRefusesAPassThatFailed is the contract violation worth catching. A
// runner that printed pass and exited non-zero is broken, and believing the
// JSON over the exit code would turn a broken runner into a green report.
func TestVerifyRefusesAPassThatFailed(t *testing.T) {
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, passJSON, "", 1)}
	_, err := runner.Verify(t.Context(), verifyrunner.Request{TaskDir: "/tmp/task", DiffPath: "/tmp/m.diff"})
	if !errors.Is(err, verifyrunner.ErrRunner) {
		t.Fatalf("Verify error = %v, want ErrRunner", err)
	}
}

func TestVerifyMissingBinary(t *testing.T) {
	runner := verifyrunner.Exec{Bin: filepath.Join(t.TempDir(), "no-such-cmoa")}
	_, err := runner.Verify(t.Context(), verifyrunner.Request{TaskDir: "/tmp/task", DiffPath: "/tmp/m.diff"})
	if !errors.Is(err, verifyrunner.ErrRunner) {
		t.Fatalf("Verify error = %v, want ErrRunner", err)
	}
}

func TestDecodeRejectsAnUnknownStatus(t *testing.T) {
	body := strings.ReplaceAll(passJSON, `"status":"pass"`, `"status":"probably"`)
	if _, err := verifyrunner.Decode([]byte(body)); err == nil {
		t.Fatal("Decode accepted a status outside the vocabulary")
	}
	if _, err := verifyrunner.Decode([]byte("not json")); err == nil {
		t.Fatal("Decode accepted something that is not JSON")
	}
}

// TestDecodeIgnoresUnknownKeys is the other half of the contract: CMoA owns
// the object and may add to it, and a key uzushio does not know is not an
// error.
func TestDecodeIgnoresUnknownKeys(t *testing.T) {
	body := strings.ReplaceAll(passJSON, `"schema_version":1`, `"schema_version":1,"added_later":{"a":1}`)
	got, err := verifyrunner.Decode([]byte(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Status != verifyrunner.StatusPass {
		t.Fatalf("result = %+v", got)
	}
}

// TestOptionalKeysMayBeAbsent records what CMoA omits when it is empty:
// apply_error, error, command and project_name are omitempty, so an object
// without them is well formed.
func TestOptionalKeysMayBeAbsent(t *testing.T) {
	got, err := verifyrunner.Decode([]byte(`{"schema_version":1,"task":"hello","status":"pass"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Command != nil || got.ProjectName != "" || got.Error != "" || got.ApplyError != "" {
		t.Fatalf("result = %+v", got)
	}
}

func TestStatusVocabulary(t *testing.T) {
	for _, status := range []verifyrunner.Status{
		verifyrunner.StatusPass, verifyrunner.StatusFail, verifyrunner.StatusApplyFailed,
		verifyrunner.StatusTimeout, verifyrunner.StatusRunnerError,
	} {
		if !status.Valid() {
			t.Errorf("%s is not in the vocabulary", status)
		}
	}
	if verifyrunner.Status("skipped").Valid() {
		t.Error("skipped is a select status; nothing cmoa verify runs is skipped")
	}
}

// TestDecodeRefusesAnotherSchemaVersion is the one field leniency does not
// extend to. Ignoring a key CMoA added is right; reading an object that says
// it is a different shape, and calling its status a verdict, is not.
func TestDecodeRefusesAnotherSchemaVersion(t *testing.T) {
	body := strings.ReplaceAll(passJSON, `"schema_version":1`, `"schema_version":2`)
	_, err := verifyrunner.Decode([]byte(body))
	if err == nil {
		t.Fatal("Decode accepted a schema version this build does not read")
	}
	if !strings.Contains(err.Error(), "schema_version 2") {
		t.Errorf("Decode error = %q, which does not name the version", err)
	}
	// Missing entirely is version 0, which is not 1 either.
	if _, err := verifyrunner.Decode([]byte(`{"task":"hello","status":"pass"}`)); err == nil {
		t.Fatal("Decode accepted an object with no schema_version")
	}
}

// TestVerifyRefusesAnAnswerAboutAnotherRun closes the hole nothing downstream
// would notice: the report line is built from the request, so a mislabelled
// answer would be filed against the wrong mutant.
func TestVerifyRefusesAnAnswerAboutAnotherRun(t *testing.T) {
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, passJSON, "", 0)}
	_, err := runner.Verify(t.Context(), verifyrunner.Request{
		TaskDir: "/tmp/task", DiffPath: "/tmp/m.diff", Label: "mutant-3-something",
	})
	if !errors.Is(err, verifyrunner.ErrRunner) {
		t.Fatalf("Verify error = %v, want ErrRunner", err)
	}
	if !strings.Contains(err.Error(), "mutant-3-something") {
		t.Errorf("Verify error = %q, which does not name what was asked", err)
	}
}

// TestErrorsCarryNoArgv is half of keeping absolute paths out of a committed
// report: the task directory and the diff path are both absolute, and this
// string is copied into report.json.
func TestErrorsCarryNoArgv(t *testing.T) {
	runner := verifyrunner.Exec{Bin: fakeCMoA(t, "", "cmoa: something went wrong\n", 2)}
	_, err := runner.Verify(t.Context(), verifyrunner.Request{
		TaskDir: "/tmp/some/task/directory", DiffPath: "/tmp/some/diff/path.diff", Label: "reference-1",
	})
	if err == nil {
		t.Fatal("Verify succeeded")
	}
	for _, absent := range []string{"/tmp/some/task/directory", "/tmp/some/diff/path.diff", "--task"} {
		if strings.Contains(err.Error(), absent) {
			t.Errorf("the error carries %q: %v", absent, err)
		}
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("the error drops cmoa's own message: %v", err)
	}
}
