package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/task"
)

// stubDocker replaces the two docker calls for the length of a test. Nothing in
// `go test` may shell out to a daemon: the fingerprint is computed on every
// check, and a test that needed docker would make the whole package need it.
func stubDocker(t *testing.T, images func() ([]string, error), id func(string) (string, error)) {
	t.Helper()
	previousImages, previousID := dockerImages, dockerImageID
	dockerImages = func(context.Context, *task.Task) ([]string, error) { return images() }
	dockerImageID = func(_ context.Context, image string) (string, error) { return id(image) }
	t.Cleanup(func() { dockerImages, dockerImageID = previousImages, previousID })
	stubConfig(t, func() ([]byte, error) { return nil, exec.ErrNotFound })
}

// stubConfig replaces the resolved-compose call for the length of a test.
func stubConfig(t *testing.T, config func() ([]byte, error)) {
	t.Helper()
	previous := dockerConfig
	dockerConfig = func(context.Context, *task.Task) ([]byte, error) { return config() }
	t.Cleanup(func() { dockerConfig = previous })
}

// composeTask is a task whose compose file exists, which is what makes the
// image worth asking about.
func composeTask(t *testing.T) *task.Task {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("compose.yaml", `services:
  verify:
    image: example:1
    volumes:
      - ./one.txt:/one.txt:ro
      - ./two.txt:/two.txt:ro
`)
	write("reference.diff", "")
	write(task.ManifestFile, `{
  "version": 2,
  "id": "hello",
  "repo": ".",
  "rev": "HEAD",
  "files": ["add.go"],
  "reference": {"diff": "reference.diff"},
  "mutants": [],
  "doctor": {"kill_rate_min": 0.8, "reference_runs": 1}
}
`)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

// TestFingerprintHashesTheImage: a rebuilt image is a different machine to
// measure on, and nothing in the task directory changes when it happens. That
// is the case the image id is in the digest for.
func TestFingerprintHashesTheImage(t *testing.T) {
	loaded := composeTask(t)
	id := "sha256:aaaa"
	stubDocker(t,
		func() ([]string, error) { return []string{"example:1"}, nil },
		func(string) (string, error) { return id, nil })

	before, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if !strings.Contains(strings.Join(before.Components, ","), "image example:1") {
		t.Errorf("components = %v, want the image", before.Components)
	}
	id = "sha256:bbbb"
	after, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if after.SHA256 == before.SHA256 {
		t.Error("rebuilding the image did not change the fingerprint")
	}
	// The components are unchanged, so the difference is reported as a content
	// change rather than as a different set of things hashed.
	if got := after.Differences(before); !strings.Contains(got[0], "changed on disk") {
		t.Errorf("Differences = %v", got)
	}
}

// TestFingerprintWithoutDocker: the image is omitted and the components say so,
// rather than the fingerprint quietly stopping covering it.
func TestFingerprintWithoutDocker(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"docker is not installed", exec.ErrNotFound, "docker is not available"},
		{"the daemon did not answer", context.DeadlineExceeded, "docker did not answer"},
		{"compose refused", errors.New("no configuration file"), "docker declined"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			loaded := composeTask(t)
			stubDocker(t,
				func() ([]string, error) { return nil, testCase.err },
				func(string) (string, error) { return "", testCase.err })
			got, err := Fingerprint(t.Context(), loaded)
			if err != nil {
				t.Fatalf("Fingerprint: %v", err)
			}
			components := strings.Join(got.Components, ",")
			if !strings.Contains(components, "image (omitted: "+testCase.want+")") {
				t.Errorf("components = %v, want the omission and its reason", got.Components)
			}
			// A fingerprint that covers the image and one that does not are
			// never mistaken for each other: the component list differs, and
			// Differences names it.
			stubDocker(t,
				func() ([]string, error) { return []string{"example:1"}, nil },
				func(string) (string, error) { return "sha256:aaaa", nil })
			withImage, err := Fingerprint(t.Context(), loaded)
			if err != nil {
				t.Fatalf("Fingerprint: %v", err)
			}
			if withImage.Same(got) {
				t.Error("a fingerprint with the image matched one without it")
			}
			if diff := strings.Join(withImage.Differences(got), "; "); !strings.Contains(diff, "image") {
				t.Errorf("Differences = %q, want it to name the image", diff)
			}
		})
	}
}

// TestFingerprintWhenTheImageIsNotBuilt: compose names an image and this
// machine does not have it, which is recorded per image rather than dropped.
func TestFingerprintWhenTheImageIsNotBuilt(t *testing.T) {
	loaded := composeTask(t)
	stubDocker(t,
		func() ([]string, error) { return []string{"example:1"}, nil },
		func(string) (string, error) { return "", errors.New("no such image") })
	got, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if !strings.Contains(strings.Join(got.Components, ","), "image example:1 (omitted:") {
		t.Errorf("components = %v, want the image named and its omission recorded", got.Components)
	}
}

// TestFingerprintSkipsDockerWithoutAComposeFile: no compose file, no images to
// ask about, and no docker round trip to be told so.
func TestFingerprintSkipsDockerWithoutAComposeFile(t *testing.T) {
	loaded := composeTask(t)
	if err := os.Remove(filepath.Join(loaded.Dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	asked := false
	stubDocker(t,
		func() ([]string, error) { asked = true; return nil, nil },
		func(string) (string, error) { asked = true; return "", nil })
	got, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if asked {
		t.Error("docker was asked about the images of a compose file that is not there")
	}
	if !strings.Contains(strings.Join(got.Components, ","), "compose.yaml (absent)") {
		t.Errorf("components = %v", got.Components)
	}
}

// TestFingerprintNameAndLengthAreHashed: two files whose contents concatenate
// the same way must not produce the same fingerprint.
func TestFingerprintNameAndLengthAreHashed(t *testing.T) {
	first, second := composeTask(t), composeTask(t)
	stubDocker(t,
		func() ([]string, error) { return nil, exec.ErrNotFound },
		func(string) (string, error) { return "", exec.ErrNotFound })
	write := func(loaded *task.Task, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(loaded.Dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two mounted files split two ways, the same bytes end to end.
	write(first, "one.txt", "ab")
	write(first, "two.txt", "c")
	write(second, "one.txt", "a")
	write(second, "two.txt", "bc")
	a, err := Fingerprint(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Fingerprint(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == b.SHA256 {
		t.Error("two different sets of files produced one fingerprint")
	}
}

// taskWithCompose is a task whose compose file is whatever the caller writes.
func taskWithCompose(t *testing.T, compose string) *task.Task {
	t.Helper()
	loaded := composeTask(t)
	if err := os.WriteFile(filepath.Join(loaded.Dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := task.Load(loaded.Dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return again
}

// components is a task's fingerprint components, joined for a substring check.
func components(t *testing.T, loaded *task.Task) string {
	t.Helper()
	got, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	return strings.Join(got.Components, ",")
}

// TestMountSyntaxes: compose accepts two ways of writing a bind mount, and a
// fingerprint that read only one would silently cover nothing on a task that
// used the other.
func TestMountSyntaxes(t *testing.T) {
	noDocker(t)
	cases := map[string]string{
		"short": `services:
  verify:
    image: example:1
    volumes:
      - ./one.txt:/one.txt:ro
`,
		"short without a mode": `services:
  verify:
    image: example:1
    volumes:
      - ./one.txt:/one.txt
`,
		"long": `services:
  verify:
    image: example:1
    volumes:
      - type: bind
        source: ./one.txt
        target: /one.txt
        read_only: true
`,
	}
	for name, compose := range cases {
		t.Run(name, func(t *testing.T) {
			loaded := taskWithCompose(t, compose)
			if err := os.WriteFile(filepath.Join(loaded.Dir, "one.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := components(t, loaded); !strings.Contains(got, "one.txt") {
				t.Errorf("components = %s, want the mounted file", got)
			}
		})
	}
}

// TestMountsThatAreNotFiles: a named volume has no bytes, an absolute host path
// is somebody's machine rather than what the task declares, an interpolated
// source is the candidate worktree being measured, and a path that escapes the
// task directory is not the task's to hash.
func TestMountsThatAreNotFiles(t *testing.T) {
	noDocker(t)
	loaded := taskWithCompose(t, `services:
  verify:
    image: example:1
    volumes:
      - cache:/cache
      - /etc/hosts:/etc/hosts:ro
      - ${CMOA_CANDIDATE_DIR:?set by cmoa verify}:/work
      - ../elsewhere:/elsewhere
      - type: volume
        source: named
        target: /named
volumes:
  cache:
`)
	got := components(t, loaded)
	for _, unwanted := range []string{"cache", "/etc/hosts", "CMOA_CANDIDATE_DIR", "elsewhere", "named"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("components = %s, want %q left out", got, unwanted)
		}
	}
	if !strings.Contains(got, "verify,compose.yaml") {
		t.Errorf("components = %s, want the verify block and the compose file", got)
	}
}

// TestMountedDirectoryIsHashedAsItsTree: a directory mount is what the
// container sees, so a file added inside it changes what is measured.
func TestMountedDirectoryIsHashedAsItsTree(t *testing.T) {
	noDocker(t)
	loaded := taskWithCompose(t, `services:
  verify:
    image: example:1
    volumes:
      - ./scripts:/scripts:ro
`)
	scripts := filepath.Join(loaded.Dir, "scripts")
	if err := os.MkdirAll(filepath.Join(scripts, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(scripts, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.sh", "one\n")
	write("nested/b.sh", "two\n")
	before, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(before.Components, ",")
	for _, want := range []string{"scripts/", "scripts/a.sh", "scripts/nested/b.sh"} {
		if !strings.Contains(got, want) {
			t.Errorf("components = %s, want %q", got, want)
		}
	}
	write("nested/b.sh", "three\n")
	edited, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if edited.SHA256 == before.SHA256 {
		t.Error("editing a file inside a mounted directory did not change the fingerprint")
	}
	write("c.sh", "four\n")
	added, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if added.SHA256 == edited.SHA256 {
		t.Error("adding a file to a mounted directory did not change the fingerprint")
	}
	// The sorted list of names is hashed too, so removing a file is noticed
	// even though nothing else about the remaining ones changed.
	if err := os.Remove(filepath.Join(scripts, "c.sh")); err != nil {
		t.Fatal(err)
	}
	back, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if back.SHA256 != edited.SHA256 {
		t.Error("removing the added file did not return the fingerprint to what it was")
	}
}

// TestBuildDockerfileIsHashed: a service that builds its own image is a service
// whose Dockerfile decides what it measures.
func TestBuildDockerfileIsHashed(t *testing.T) {
	noDocker(t)
	cases := map[string]string{
		"the short form": `services:
  verify:
    build: .
`,
		"a context only": `services:
  verify:
    build:
      context: .
`,
		"a named dockerfile": `services:
  verify:
    build:
      context: .
      dockerfile: Dockerfile
`,
	}
	for name, compose := range cases {
		t.Run(name, func(t *testing.T) {
			loaded := taskWithCompose(t, compose)
			dockerfile := filepath.Join(loaded.Dir, "Dockerfile")
			if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := Fingerprint(t.Context(), loaded)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(before.Components, ","), "Dockerfile") {
				t.Fatalf("components = %v, want the Dockerfile", before.Components)
			}
			if err := os.WriteFile(dockerfile, []byte("FROM alpine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			after, err := Fingerprint(t.Context(), loaded)
			if err != nil {
				t.Fatal(err)
			}
			if after.SHA256 == before.SHA256 {
				t.Error("editing the Dockerfile did not change the fingerprint")
			}
		})
	}
}

// TestMountedFileThatIsNotThere is recorded rather than refused: whether the
// verifier can run is docker's answer, and a fingerprint only has to notice
// when the answer would change.
func TestMountedFileThatIsNotThere(t *testing.T) {
	noDocker(t)
	loaded := taskWithCompose(t, `services:
  verify:
    image: example:1
    volumes:
      - ./missing.sh:/missing.sh:ro
`)
	if got := components(t, loaded); !strings.Contains(got, "missing.sh (absent)") {
		t.Errorf("components = %s, want the absent mount recorded", got)
	}
}

// TestUnparseableComposeFile: docker is the authority on a compose file, not
// this, so an unreadable one is recorded as "the mounts are not covered" rather
// than failing a health check.
func TestUnparseableComposeFile(t *testing.T) {
	noDocker(t)
	loaded := taskWithCompose(t, "services: [this is not a mapping\n")
	if got := components(t, loaded); !strings.Contains(got, "mounts (omitted:") {
		t.Errorf("components = %s, want the omission recorded", got)
	}
}

// TestOnlyTheVerifyServiceIsHashed: another service's mounts are not what this
// verifier is given.
func TestOnlyTheVerifyServiceIsHashed(t *testing.T) {
	noDocker(t)
	loaded := taskWithCompose(t, `services:
  verify:
    image: example:1
    volumes:
      - ./mine.txt:/mine.txt:ro
  other:
    image: example:2
    volumes:
      - ./theirs.txt:/theirs.txt:ro
`)
	for _, name := range []string{"mine.txt", "theirs.txt"} {
		if err := os.WriteFile(filepath.Join(loaded.Dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := components(t, loaded)
	if !strings.Contains(got, "mine.txt") {
		t.Errorf("components = %s, want the verify service's mount", got)
	}
	if strings.Contains(got, "theirs.txt") {
		t.Errorf("components = %s, want another service's mount left out", got)
	}
}

// noDocker stubs the two docker calls out for a test that is about the compose
// file rather than about images.
func noDocker(t *testing.T) {
	t.Helper()
	stubDocker(t,
		func() ([]string, error) { return nil, exec.ErrNotFound },
		func(string) (string, error) { return "", exec.ErrNotFound })
}

// TestMountsResolveAgainstTheComposeFile is F1: compose resolves a relative
// path against the directory holding the compose file, not against the
// directory the tool ran from. The two agree only when the compose file sits at
// the task root — so a task that keeps it in a subdirectory would otherwise
// have a same-named file at the root hashed instead of the one it mounts, and
// editing the real adapter would not move the fingerprint.
func TestMountsResolveAgainstTheComposeFile(t *testing.T) {
	noDocker(t)
	loaded := composeTask(t)
	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(loaded.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(loaded.Dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	write("docker/compose.yaml", `services:
  verify:
    image: example:1
    volumes:
      - ./gate.sh:/gate.sh:ro
`)
	write("docker/gate.sh", "the real adapter, beside the compose file\n")
	write("gate.sh", "a decoy at the task root\n")
	write(task.ManifestFile, `{
  "version": 2,
  "id": "hello",
  "repo": ".",
  "rev": "HEAD",
  "files": ["add.go"],
  "verify": {"compose_file": "docker/compose.yaml", "service": "verify"},
  "reference": {"diff": "reference.diff"},
  "mutants": [],
  "doctor": {"kill_rate_min": 0.8, "reference_runs": 1}
}
`)
	reloaded, err := task.Load(loaded.Dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	before, err := Fingerprint(t.Context(), reloaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	got := strings.Join(before.Components, ",")
	if !strings.Contains(got, "docker/gate.sh") {
		t.Errorf("components = %s, want docker/gate.sh — the file the service mounts", got)
	}
	// Editing the real adapter moves the fingerprint...
	write("docker/gate.sh", "the real adapter, changed\n")
	changed, err := Fingerprint(t.Context(), reloaded)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SHA256 == before.SHA256 {
		t.Error("editing the mounted adapter did not change the fingerprint")
	}
	// ...and editing the decoy does not.
	write("gate.sh", "the decoy, changed\n")
	decoyed, err := Fingerprint(t.Context(), reloaded)
	if err != nil {
		t.Fatal(err)
	}
	if decoyed.SHA256 != changed.SHA256 {
		t.Error("a file at the task root that nothing mounts changed the fingerprint")
	}
}

// TestBuildResolvesAgainstTheComposeFile is the same rule for `build:`.
func TestBuildResolvesAgainstTheComposeFile(t *testing.T) {
	noDocker(t)
	loaded := composeTask(t)
	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(loaded.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(loaded.Dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	write("docker/compose.yaml", "services:\n  verify:\n    build: .\n")
	write("docker/Dockerfile", "FROM scratch\n")
	write("Dockerfile", "FROM decoy\n")
	write(task.ManifestFile, `{
  "version": 2,
  "id": "hello",
  "repo": ".",
  "rev": "HEAD",
  "files": ["add.go"],
  "verify": {"compose_file": "docker/compose.yaml", "service": "verify"},
  "reference": {"diff": "reference.diff"},
  "mutants": [],
  "doctor": {"kill_rate_min": 0.8, "reference_runs": 1}
}
`)
	reloaded, err := task.Load(loaded.Dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := components(t, reloaded); !strings.Contains(got, "docker/Dockerfile") {
		t.Errorf("components = %s, want docker/Dockerfile", got)
	}
}

// TestResolvedComposeConfigIsHashed is S3/F8: a compose file that writes its
// tuning as an interpolated variable has the same bytes at every setting of it,
// so the resolved file is hashed too.
func TestResolvedComposeConfigIsHashed(t *testing.T) {
	loaded := composeTask(t)
	stubDocker(t,
		func() ([]string, error) { return nil, exec.ErrNotFound },
		func(string) (string, error) { return "", exec.ErrNotFound })
	resolved := "services:\n  verify:\n    cpuset: 0-3\n"
	stubConfig(t, func() ([]byte, error) { return []byte(resolved), nil })

	before, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if !strings.Contains(strings.Join(before.Components, ","), "compose config") {
		t.Errorf("components = %v, want the resolved compose file", before.Components)
	}
	// The same raw bytes, a different resolved value: the fingerprint moves.
	resolved = "services:\n  verify:\n    cpuset: 0-7\n"
	after, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if after.SHA256 == before.SHA256 {
		t.Error("a different interpolated value did not change the fingerprint")
	}
	if got := after.Differences(before); !strings.Contains(got[0], "changed on disk") {
		t.Errorf("Differences = %v", got)
	}
}

// TestResolvedComposeConfigWithoutDocker: omitted with a reason, so a
// fingerprint taken without docker never matches one taken with it.
func TestResolvedComposeConfigWithoutDocker(t *testing.T) {
	loaded := composeTask(t)
	noDocker(t)
	got, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if !strings.Contains(strings.Join(got.Components, ","), "compose config (omitted: docker is not available)") {
		t.Errorf("components = %v, want the omission and its reason", got.Components)
	}
	stubConfig(t, func() ([]byte, error) { return []byte("services: {}\n"), nil })
	withConfig, err := Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if withConfig.Same(got) {
		t.Error("a fingerprint with the resolved file matched one without it")
	}
}
