package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EDoctorOnTheExample runs the real health check: the real `cmoa verify`,
// the real compose verifier, the real docker.
//
// It is gated on UZUSHIO_E2E=1 because it needs a container runtime, pulls an
// image the first time, and takes tens of seconds — none of which belongs in
// `go test ./...`. `make e2e` is the way to ask for it. What it proves is the
// one thing the fake runner cannot: that the JSON contract uzushio decodes is
// the JSON contract CMoA writes.
func TestE2EDoctorOnTheExample(t *testing.T) {
	if os.Getenv("UZUSHIO_E2E") != "1" {
		t.Skip("set UZUSHIO_E2E=1 to run the health check against real docker")
	}
	cmoa := os.Getenv("UZUSHIO_CMOA_BIN")
	if cmoa == "" {
		found, err := exec.LookPath("cmoa")
		if err != nil {
			t.Skip("no cmoa on PATH; set UZUSHIO_CMOA_BIN")
		}
		cmoa = found
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("no docker")
	}

	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "task-hello"))
	if err != nil {
		t.Fatalf("example task: %v", err)
	}
	// setup.sh builds repo/ from src/, and is idempotent.
	setup := exec.Command("sh", filepath.Join(dir, "setup.sh"))
	setup.Dir = dir
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("setup.sh: %v\n%s", err, out)
	}

	out := filepath.Join(t.TempDir(), "doctor")
	got := run(t, "task", "doctor", "--task", dir, "--cmoa", cmoa, "--out", out, "--parallel", "3", "--json")
	t.Logf("stderr:\n%s", got.stderr)
	if got.code != exitOK {
		t.Fatalf("exit = %d, want %d (healthy)\n%s", got.code, exitOK, got.stderr)
	}
	if !strings.Contains(got.stderr, "verdict: healthy") {
		t.Fatalf("the example task's verifier is not healthy:\n%s", got.stderr)
	}
	if !strings.Contains(got.stdout, `"schema_version": 1`) {
		t.Fatalf("stdout is not the report:\n%s", got.stdout)
	}
	if _, err := os.Stat(filepath.Join(out, "report.json")); err != nil {
		t.Fatalf("no report was written: %v", err)
	}
}
