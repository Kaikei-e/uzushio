package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/Kaikei-e/uzushio/internal/task"
)

// Environment is the fingerprint of everything outside the task's repository
// that decides what a verification measures.
//
// It exists for one purpose: to say whether a reference block measured earlier
// is still a measurement of the same thing. A reference run of a slow verifier
// costs minutes and a check runs several, so reusing them is the difference
// between a ninety-minute loop and a seven-minute one — and the only thing that
// makes reuse honest rather than convenient is a check that the verifier has
// not changed underneath it.
//
// What goes in is what the container is built from and what is mounted into it,
// discovered from the compose file rather than from a list of names: a task's
// verifier is whatever its compose service says it is. The repository revision
// is not in here — it is `rev` on the report, and it is compared separately,
// because "a different commit" and "a different harness" are different reasons
// to refuse a reuse and the message should say which.
//
// Components is the list of what was hashed, in order. It is written out so
// that a fingerprint which fails to match can be argued with: two fingerprints
// over different component lists differ for a reason a reader can see, and a
// component that could not be read says so rather than silently not counting.
type Environment struct {
	SHA256     string   `json:"sha256"`
	Components []string `json:"components"`
}

// Same reports whether two fingerprints cover the same components with the same
// contents. A nil fingerprint matches nothing, including another nil: a report
// written before fingerprints existed cannot be shown to be comparable.
func (e *Environment) Same(other *Environment) bool {
	if e == nil || other == nil {
		return false
	}
	return e.SHA256 == other.SHA256
}

// Differences names what two fingerprints do not agree about, for the message
// that refuses a reuse.
//
// It compares the component lists, which is as far as a hash lets anyone go: if
// both fingerprints cover the same components then the difference is in one of
// their contents and there is no way to say which, so it says that instead of
// guessing.
func (e *Environment) Differences(other *Environment) []string {
	switch {
	case e == nil && other == nil:
		return []string{"neither report carries an environment fingerprint"}
	case e == nil:
		return []string{"this check has no environment fingerprint"}
	case other == nil:
		return []string{"the earlier report carries no environment fingerprint"}
	}
	var out []string
	have := map[string]bool{}
	for _, name := range e.Components {
		have[name] = true
	}
	for _, name := range other.Components {
		if !have[name] {
			out = append(out, "the earlier check hashed "+name+" and this one does not")
		}
	}
	had := map[string]bool{}
	for _, name := range other.Components {
		had[name] = true
	}
	for _, name := range e.Components {
		if !had[name] {
			out = append(out, "this check hashes "+name+" and the earlier one did not")
		}
	}
	if len(out) == 0 {
		out = append(out, "the same components ("+strings.Join(e.Components, ", ")+
			") hash differently, so one of them has changed on disk")
	}
	return out
}

// dockerImages is how the fingerprint learns which images a compose file names.
// It is a variable so a test never shells out to docker.
var dockerImages = composeImages

// dockerImageID is how the fingerprint resolves an image name to its content
// identifier. It is a variable for the same reason.
var dockerImageID = inspectImageID

// dockerConfig is how the fingerprint gets the compose file as compose itself
// reads it, with every variable resolved. It is a variable for the same reason.
var dockerConfig = composeConfig

// EnvironmentTimeout bounds the docker calls. The fingerprint is a preliminary
// to a check that takes an hour, but it is also computed before anything has
// been done, and a docker daemon that is not answering should not be the thing
// that makes a health check look hung.
const EnvironmentTimeout = 10 * time.Second

// part is one hashed component: the name that goes into Components, and the
// bytes that go into the digest.
type part struct {
	name string
	body []byte
}

// Fingerprint computes the environment fingerprint for a task.
//
// The files it covers are the ones the task's own compose service names — the
// host paths it bind-mounts, and the Dockerfile it builds from — rather than a
// list of conventional names. A fingerprint over "whatever is in the task
// directory" changes when somebody leaves a scratch file there, and one over a
// fixed list of names covers one task and quietly covers nothing of the next.
// What the container is given is what decides what it measures, and the compose
// file is where that is written down.
func Fingerprint(ctx context.Context, t *task.Task) (*Environment, error) {
	verify, err := json.Marshal(map[string]any{
		"compose_file":    t.Verify.ComposeFile,
		"service":         t.Verify.Service,
		"kind":            string(t.Verify.Kind),
		"timeout_seconds": t.Verify.TimeoutSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: fingerprint the verify block: %w", ErrDoctor, err)
	}
	parts := []part{{name: "verify", body: verify}}

	if t.Verify.ComposeFile != "" {
		body, err := os.ReadFile(t.AbsPath(t.Verify.ComposeFile))
		switch {
		case errors.Is(err, os.ErrNotExist):
			// A declared compose file that is not there is recorded as absent
			// rather than refused. Whether the verifier can run is `cmoa
			// verify`'s to answer — a check driven by another runner is a real
			// thing — and a fingerprint's job is only to notice when the answer
			// changes, which "absent" does as well as any digest.
			parts = append(parts, part{name: t.Verify.ComposeFile + " (absent)"})
		case err != nil:
			return nil, fmt.Errorf("%w: fingerprint %s: %w", ErrDoctor, t.Verify.ComposeFile, err)
		default:
			parts = append(parts, part{name: t.Verify.ComposeFile, body: body})
			mounted, err := mountedParts(t, body)
			if err != nil {
				return nil, err
			}
			parts = append(parts, mounted...)
			parts = append(parts, resolvedParts(ctx, t)...)
			parts = append(parts, imageParts(ctx, t)...)
		}
	}

	digest := sha256.New()
	components := make([]string, 0, len(parts))
	for _, p := range parts {
		// The name and the length are hashed alongside the bytes, so that two
		// files whose contents happen to concatenate the same way do not
		// produce the same fingerprint.
		fmt.Fprintf(digest, "%s\n%d\n", p.name, len(p.body))
		digest.Write(p.body)
		components = append(components, p.name)
	}
	return &Environment{SHA256: hex.EncodeToString(digest.Sum(nil)), Components: components}, nil
}

// composeFile is as much of a compose file as a fingerprint needs.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// composeService is one service's mounts and build.
type composeService struct {
	Volumes []composeVolume `yaml:"volumes"`
	Build   *composeBuild   `yaml:"build"`
}

// composeBuild is `build:` in either form. Compose accepts a bare string, which
// is the context.
type composeBuild struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}

// UnmarshalYAML accepts `build: .` as well as `build: {context: .}`.
func (b *composeBuild) UnmarshalYAML(body []byte) error {
	var short string
	if err := yaml.Unmarshal(body, &short); err == nil {
		b.Context = short
		return nil
	}
	var long struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
	}
	if err := yaml.Unmarshal(body, &long); err != nil {
		return err
	}
	b.Context, b.Dockerfile = long.Context, long.Dockerfile
	return nil
}

// composeVolume is one mount, in either syntax.
type composeVolume struct {
	Source string
	Type   string
}

// UnmarshalYAML reads both the short syntax — `./gate.sh:/gate.sh:ro` — and the
// long one, `{type: bind, source: ./gate.sh, target: /gate.sh}`.
func (v *composeVolume) UnmarshalYAML(body []byte) error {
	var short string
	if err := yaml.Unmarshal(body, &short); err == nil {
		// SOURCE:TARGET[:MODE]. A source that turns out not to be a path under
		// the task — a named volume, an interpolated variable — is dropped
		// later, by underTask.
		if source, _, ok := strings.Cut(short, ":"); ok {
			v.Source, v.Type = source, "bind"
		}
		return nil
	}
	var long struct {
		Source string `yaml:"source"`
		Type   string `yaml:"type"`
	}
	if err := yaml.Unmarshal(body, &long); err != nil {
		return err
	}
	v.Source, v.Type = long.Source, long.Type
	if v.Type == "" {
		v.Type = "bind"
	}
	return nil
}

// mountedParts hashes what the verify service is given: the host paths it
// bind-mounts from inside the task directory, and the Dockerfile it builds
// from.
//
// Only paths under the task directory are hashed. A named volume is not a file;
// an absolute host path is somebody's machine rather than part of what the task
// declares; and the candidate worktree compose is handed at run time is the
// thing being measured rather than part of the instrument.
func mountedParts(t *task.Task, compose []byte) ([]part, error) {
	var parsed composeFile
	if err := yaml.Unmarshal(compose, &parsed); err != nil {
		// A compose file this cannot read is not a reason to refuse a health
		// check — docker is the authority on that file, not this — but it is a
		// reason to say the mounts are not covered, so that a fingerprint taken
		// now never matches one taken when it could be read.
		return []part{{name: "mounts (omitted: the compose file could not be parsed)"}}, nil
	}
	// Compose resolves every relative path in a file against the directory
	// holding THAT FILE, not against the directory the tool was run from. The
	// two coincide only when the compose file sits at the task root, which is
	// what makes getting this wrong invisible: a task that keeps its compose
	// file in a subdirectory would have a file of the same name at the task
	// root hashed instead of the one it actually mounts.
	base := path.Dir(filepath.ToSlash(t.Verify.ComposeFile))
	service := parsed.Services[t.Verify.Service]
	var parts []part
	for _, volume := range service.Volumes {
		if volume.Type != "" && volume.Type != "bind" {
			continue
		}
		relative, ok := mountUnderTask(base, volume.Source)
		if !ok {
			continue
		}
		hashed, err := pathParts(t.AbsPath(relative), relative)
		if err != nil {
			return nil, err
		}
		parts = append(parts, hashed...)
	}
	if service.Build != nil {
		relative, ok := underTask(base, dockerfileOf(*service.Build))
		if !ok {
			return parts, nil
		}
		hashed, err := pathParts(t.AbsPath(relative), relative)
		if err != nil {
			return nil, err
		}
		parts = append(parts, hashed...)
	}
	return parts, nil
}

// dockerfileOf is the Dockerfile a build stanza names, relative to the compose
// file. Compose's own defaults are a context of `.` and a Dockerfile named
// `Dockerfile` inside it.
func dockerfileOf(build composeBuild) string {
	context := build.Context
	if context == "" {
		context = "."
	}
	dockerfile := build.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if filepath.IsAbs(dockerfile) || strings.HasPrefix(dockerfile, "/") {
		return dockerfile
	}
	return filepath.ToSlash(filepath.Join(context, dockerfile))
}

// mountUnderTask is a volume's source, resolved against base and reported when
// it is a path inside the task directory.
//
// A named volume is not a path at all, and compose tells the two apart by the
// leading `.` or `/` on the raw source — so that test is applied here, before
// anything is joined, and not in underTask, which also serves a `build:` path
// already resolved to a plain relative name.
func mountUnderTask(base, source string) (string, bool) {
	if !strings.HasPrefix(source, ".") && !strings.HasPrefix(source, "/") {
		// A bare name is a named volume, which has no bytes to hash.
		return "", false
	}
	return underTask(base, source)
}

// underTask resolves a compose path against base — the directory holding the
// compose file — and reports whether it stays inside the task directory. A path
// that escapes, one that is absolute, and one compose has to interpolate are all
// things this does not hash.
func underTask(base, source string) (string, bool) {
	if source == "" || strings.Contains(source, "$") {
		// An interpolated source is the candidate worktree, or something else
		// only compose can resolve. It is what is being measured rather than
		// the instrument.
		return "", false
	}
	slashed := filepath.ToSlash(source)
	if filepath.IsAbs(source) || strings.HasPrefix(slashed, "/") {
		// Absolute is tested before joining: an absolute source names somebody's
		// machine rather than anything the task declares, and joining it onto
		// base first would turn it into a path under the task that happens to
		// exist.
		return "", false
	}
	joined := path.Join(base, slashed)
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}
	return joined, true
}

// pathParts hashes one mounted path: a file's bytes, or for a directory the
// sorted list of relative paths and their bytes.
//
// A directory is hashed as its tree because that is what is mounted: a file
// added to it changes what the container sees, and a fingerprint that covered
// only the directory's own entry would not notice.
func pathParts(absolute, name string) ([]part, error) {
	info, err := os.Stat(absolute)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return []part{{name: name + " (absent)"}}, nil
	case err != nil:
		return nil, fmt.Errorf("%w: fingerprint %s: %w", ErrDoctor, name, err)
	}
	if !info.IsDir() {
		body, err := os.ReadFile(absolute)
		if err != nil {
			return nil, fmt.Errorf("%w: fingerprint %s: %w", ErrDoctor, name, err)
		}
		return []part{{name: name, body: body}}, nil
	}
	var files []string
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: fingerprint %s: %w", ErrDoctor, name, err)
	}
	sort.Strings(files)
	parts := make([]part, 0, len(files)+1)
	// The sorted list of names is hashed as well as the bytes, so a file that
	// is removed changes the digest even when nothing else does.
	parts = append(parts, part{name: name + "/", body: []byte(strings.Join(files, "\n"))})
	for _, relative := range files {
		body, err := os.ReadFile(filepath.Join(absolute, filepath.FromSlash(relative)))
		if err != nil {
			return nil, fmt.Errorf("%w: fingerprint %s/%s: %w", ErrDoctor, name, relative, err)
		}
		parts = append(parts, part{name: name + "/" + relative, body: body})
	}
	return parts, nil
}

// resolvedParts is the compose file as compose itself reads it: every variable
// interpolated, every default applied.
//
// The raw bytes are hashed too, and they are not enough. A compose file that
// writes its tuning as `cpuset: "${TASK_CPUSET:-0-3}"` has the same bytes at
// every setting of that variable, so a reference block measured on four
// processors would be reused for a check running on eight — and for a banded
// verifier, which measures latency, that is not the same machine. Resolving the
// file folds every such value into the digest, and keeps working for the next
// task that puts its tuning somewhere this package has never heard of.
//
// It is a separate component from the raw file rather than a replacement,
// because the raw file is the reviewed artefact and the resolved one is what
// ran.
func resolvedParts(ctx context.Context, t *task.Task) []part {
	ctx, cancel := context.WithTimeout(ctx, EnvironmentTimeout)
	defer cancel()
	body, err := dockerConfig(ctx, t)
	if err != nil {
		return []part{{name: "compose config (omitted: " + reason(err) + ")"}}
	}
	return []part{{name: "compose config", body: body}}
}

// composeConfig asks compose for the fully resolved file.
func composeConfig(ctx context.Context, t *task.Task) ([]byte, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, docker, "compose",
		"-f", t.AbsPath(t.Verify.ComposeFile), "config")
	// The candidate mount is pinned to a fixed value: it is a different
	// directory on every verification, and it is what is being measured rather
	// than part of the instrument. Everything else compose interpolates — the
	// environment this check is actually running under — is what makes this
	// worth hashing.
	cmd.Env = append(os.Environ(), "CMOA_CANDIDATE_DIR="+t.Dir)
	cmd.Dir = t.Dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// imageParts is the built image the verifier runs, when docker will say.
//
// The image is the largest part of the environment and the one least visible in
// a diff: a rebuilt image with a newer base layer is a different machine to
// measure on, and nothing in the task directory changes when it happens. So it
// is hashed when it can be, and its absence is recorded rather than passed over
// — a fingerprint that quietly stopped covering the image would let a reference
// block be reused across a rebuild.
func imageParts(ctx context.Context, t *task.Task) []part {
	ctx, cancel := context.WithTimeout(ctx, EnvironmentTimeout)
	defer cancel()
	images, err := dockerImages(ctx, t)
	if err != nil {
		return []part{{name: "image (omitted: " + reason(err) + ")"}}
	}
	var parts []part
	for _, image := range images {
		id, err := dockerImageID(ctx, image)
		if err != nil {
			parts = append(parts, part{name: "image " + image + " (omitted: " + reason(err) + ")"})
			continue
		}
		parts = append(parts, part{name: "image " + image, body: []byte(id)})
	}
	return parts
}

// reason is a short phrase for why the image could not be hashed. It is the
// text that lands in a committed report, so it names the tool and the shape of
// the failure and never the command line.
func reason(err error) string {
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "docker is not available"
	case errors.Is(err, context.DeadlineExceeded):
		return "docker did not answer"
	}
	return "docker declined"
}

// composeImages asks compose which images the file names.
func composeImages(ctx context.Context, t *task.Task) ([]string, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, docker, "compose",
		"-f", t.AbsPath(t.Verify.ComposeFile), "config", "--images")
	// A compose file may interpolate the mount `cmoa verify` sets, and config
	// fails on an unset required variable. It is not part of the image.
	cmd.Env = append(os.Environ(), "CMOA_CANDIDATE_DIR="+t.Dir)
	cmd.Dir = t.Dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var images []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			images = append(images, line)
		}
	}
	return images, nil
}

// inspectImageID resolves one image name to the identifier of the image on this
// machine.
func inspectImageID(ctx context.Context, image string) (string, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, docker,
		"image", "inspect", "--format", "{{.Id}}", image).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
