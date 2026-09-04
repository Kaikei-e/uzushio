package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/vault"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	return runContext(t, t.Context(), args...)
}

func runContext(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := executeWith(ctx, args, &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func TestVersion(t *testing.T) {
	got := run(t, "version")
	if got.code != exitOK {
		t.Fatalf("version exit = %d, stderr = %q", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) == "" {
		t.Fatal("version printed nothing on stdout")
	}
	// Under `go test` the build carries no module version, so the fallback is
	// what runs here; either way the subcommand and the flag agree.
	flag := run(t, "--version")
	if flag.code != exitOK {
		t.Fatalf("--version exit = %d, stderr = %q", flag.code, flag.stderr)
	}
	if !strings.Contains(flag.stdout, strings.TrimSpace(got.stdout)) {
		t.Fatalf("--version printed %q, which does not carry %q", flag.stdout, got.stdout)
	}
}

func TestVersionTakesNoArguments(t *testing.T) {
	got := run(t, "version", "extra")
	if got.code == exitOK {
		t.Fatal("version accepted an argument")
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	got := run(t, "no-such-command")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %q", got.code, exitUsage, got.stderr)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	got := run(t, "docdag-config", "--no-such-flag")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %q", got.code, exitUsage, got.stderr)
	}
}

func TestDocDagConfigWritesToOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.yaml")
	got := run(t, "docdag-config", "--out", path)
	if got.code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", got.code, got.stderr)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written file: %v", err)
	}
	want, err := vault.Generate()
	if err != nil {
		t.Fatalf("vault.Generate: %v", err)
	}
	if !bytes.Equal(written, want) {
		t.Fatal("the written file is not what the generator renders")
	}
	if !bytes.HasPrefix(written, []byte(vault.Header())) {
		t.Fatal("the written file does not open with the generated-file header")
	}
	// The path written to is a diagnostic, not the answer: stdout stays clean
	// so `docdag-config --out /dev/stdout` remains usable.
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want nothing", got.stdout)
	}
	if !strings.Contains(got.stderr, path) {
		t.Fatalf("stderr = %q, want it to name %s", got.stderr, path)
	}
}

func TestDocDagConfigCheckOnAFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docdag.yaml")
	if code := run(t, "docdag-config", "--out", path).code; code != exitOK {
		t.Fatalf("writing exit = %d", code)
	}
	got := run(t, "docdag-config", "--out", path, "--check")
	if got.code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "is up to date") {
		t.Fatalf("stdout = %q", got.stdout)
	}
}

func TestDocDagConfigCheckOnAStaleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docdag.yaml")
	generated, err := vault.Generate()
	if err != nil {
		t.Fatalf("vault.Generate: %v", err)
	}
	stale := bytes.Replace(generated, []byte("preset_version: 2"), []byte("preset_version: 99"), 1)
	if bytes.Equal(stale, generated) {
		t.Fatal("the staleness probe changed nothing; the generated file no longer writes preset_version")
	}
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := run(t, "docdag-config", "--out", path, "--check")
	if got.code != exitFailure {
		t.Fatalf("exit = %d, want %d; stderr = %q", got.code, exitFailure, got.stderr)
	}
	for _, want := range []string{
		"is stale; run uzushio docdag-config",
		"--- " + path + " (on disk)",
		"+++ " + path + " (generated)",
		"-preset_version: 99",
		"+preset_version: 2",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("stdout does not carry %q:\n%s", want, got.stdout)
		}
	}
	// A stale file is reported, not rewritten.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(after, stale) {
		t.Fatal("--check rewrote the file")
	}
}

func TestDocDagConfigCheckOnAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	got := run(t, "docdag-config", "--out", path, "--check")
	if got.code != exitFailure {
		t.Fatalf("exit = %d, want %d", got.code, exitFailure)
	}
	if !strings.Contains(got.stdout, "does not exist") {
		t.Fatalf("stdout = %q", got.stdout)
	}
}

func TestDocDagConfigTakesNoArguments(t *testing.T) {
	if code := run(t, "docdag-config", "somewhere.yaml").code; code == exitOK {
		t.Fatal("docdag-config accepted a positional argument")
	}
}

// TestRepositoryConfigIsCurrent is the committed answer to `make check`: the
// docdag.yaml in the repository has to be the one this code writes.
func TestRepositoryConfigIsCurrent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}
	got := run(t, "docdag-config", "--out", filepath.Join(root, "docdag.yaml"), "--check")
	if got.code != exitOK {
		t.Fatalf("the committed docdag.yaml is stale; run make generate\n%s", got.stdout)
	}
}

// TestDocDagConfigCheckNamesTheFileAsked holds --check's report to the file it
// was pointed at. The diff header is where a reader goes next, and one naming
// docdag.yaml while the stale file is elsewhere sends them to a file that is
// perfectly fine.
func TestDocDagConfigCheckNamesTheFileAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.yaml")
	generated, err := vault.Generate()
	if err != nil {
		t.Fatalf("vault.Generate: %v", err)
	}
	stale := bytes.Replace(generated, []byte("preset_version: 2"), []byte("preset_version: 99"), 1)
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := run(t, "docdag-config", "--out", path, "--check")
	if got.code != exitFailure {
		t.Fatalf("exit = %d, want %d", got.code, exitFailure)
	}
	for _, want := range []string{
		path + " is stale",
		"--- " + path + " (on disk)",
		"+++ " + path + " (generated)",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("stdout does not carry %q:\n%s", want, got.stdout)
		}
	}
	if strings.Contains(got.stdout, "docdag.yaml") {
		t.Fatalf("stdout names docdag.yaml, which is not the file asked about:\n%s", got.stdout)
	}
}
