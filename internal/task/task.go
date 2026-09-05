// Package task reads the part of CMoA's task.json that uzushio needs.
//
// CMoA owns the schema. This package is a reader, not a second definition of
// it: unknown keys are ignored rather than refused, so a field CMoA adds does
// not stop `uzushio task doctor` from running, and every field this package
// does read is held to the same rules CMoA holds it to. The one place the
// leniency stops is Manifest.Write, which refuses to rewrite a file holding a
// key it does not know — rewriting through a typed struct would silently drop
// it, and losing a field of someone else's schema is worse than refusing.
//
// Version 1 loads (it is a task CMoA can run), but it carries neither a
// reference solution nor mutants, so the commands that need those say so.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ErrTask is the sentinel every malformed manifest wraps, so a caller can tell
// a task it cannot read from an I/O failure without matching message text.
var ErrTask = errors.New("task: invalid task.json")

// ManifestFile is the name CMoA gives the manifest inside a task directory.
const ManifestFile = "task.json"

// The defaults CMoA applies, repeated here because a reader that guessed
// differently would report a task as broken that CMoA runs happily.
const (
	// DefaultRev is the revision a task without one is built from.
	DefaultRev = "HEAD"
	// DefaultComposeFile and DefaultService name the verifier.
	DefaultComposeFile = "compose.yaml"
	DefaultService     = "verify"
	// DefaultKillRateMin is the share of killable mutants a healthy verifier
	// has to kill.
	DefaultKillRateMin = 0.8
	// DefaultReferenceRuns is how many times the reference solution is
	// verified before its passing is believed.
	DefaultReferenceRuns = 3
)

// Kind is how a verifier answers. It is a closed vocabulary so that a task
// asking for something uzushio cannot do is refused rather than misread.
type Kind string

// The verifier kinds.
const (
	// KindExitCode is a verifier whose exit code is the whole answer.
	KindExitCode Kind = "exit-code"
	// KindBand is a verifier that prints one measurement per invariant with
	// the band it is held to, and whose answer is read off those rows rather
	// than off the exit code. CMoA implements the reading; uzushio's part is
	// to accept the kind and to carry the rows into the report.
	KindBand Kind = "band"
)

// String returns the kind as task.json writes it.
func (k Kind) String() string { return string(k) }

// Expect is what a mutant is supposed to do to the verifier.
type Expect string

// The expectations.
const (
	// ExpectKilled is a mutant the verifier has to reject.
	ExpectKilled Expect = "killed"
	// ExpectEquivalent is a mutant that changes nothing observable, so the
	// verifier passing it is not a fault. It is run and reported, and left out
	// of the rate.
	ExpectEquivalent Expect = "equivalent"
)

// String returns the expectation as task.json writes it.
func (e Expect) String() string { return string(e) }

// Origin says who wrote a mutant.
type Origin string

// The origins.
const (
	// OriginHand is a mutant a person wrote, which is the set a verifier is
	// held to absolutely: a hand-written mutant that survives is a fault
	// whatever the rate says.
	OriginHand Origin = "hand"
	// OriginGenerated is a mutant `uzushio task mutate` produced.
	OriginGenerated Origin = "generated"
)

// String returns the origin as task.json writes it.
func (o Origin) String() string { return string(o) }

// Task is a loaded manifest with the paths resolved.
type Task struct {
	// Dir is the task directory, absolute.
	Dir string
	// Version is the manifest version, 1 or 2.
	Version int
	// ID is the task identifier, held to CMoA's shape.
	ID string
	// Repo is the git repository the task is about, absolute.
	Repo string
	// Rev is the revision as written; the doctor resolves it to a SHA.
	Rev string
	// Files are the repository files the task names, repo-relative with
	// forward slashes.
	Files []string
	// Verify names the verifier and how it answers.
	Verify Verify
	// Reference is the diff that turns the seed state into a solution the
	// verifier has to accept. Nil where the manifest declares none.
	Reference *Reference
	// Mutants are the diffs against the reference-applied tree that the
	// verifier is measured on.
	Mutants []Mutant
	// Doctor is the health check's own configuration, with defaults applied.
	Doctor Doctor
}

// Verify names the compose service that answers and how its answer is read.
type Verify struct {
	// ComposeFile is the compose file, task-relative as written.
	ComposeFile string
	// Service is the service inside it.
	Service string
	// Kind is how the answer is read.
	Kind Kind
	// TimeoutSeconds is the task's own limit on one verification, or zero
	// where it declares none.
	TimeoutSeconds int
}

// Reference is the diff a healthy verifier accepts every time.
type Reference struct {
	// Path is the diff, relative to the task directory as written.
	Path string
}

// Mutant is one deliberate defect, written against the reference-applied tree.
type Mutant struct {
	// Diff is the path to the diff, relative to the task directory.
	Diff string
	// Expect is what the verifier is supposed to do with it.
	Expect Expect
	// Origin says who wrote it.
	Origin Origin
	// Operator names the mutation operator, empty for a hand-written mutant.
	Operator string
	// Note describes the change in one line.
	Note string
}

// Doctor is the health check's configuration.
type Doctor struct {
	// KillRateMin is the share of killable mutants a healthy verifier kills.
	KillRateMin float64
	// ReferenceRuns is how many times the reference solution is verified.
	ReferenceRuns int
}

// AbsPath resolves a task-relative path against the task directory.
func (t *Task) AbsPath(relative string) string {
	if filepath.IsAbs(relative) {
		return relative
	}
	return filepath.Join(t.Dir, filepath.FromSlash(relative))
}

// Load reads dir/task.json. It accepts version 1 and version 2 and applies
// CMoA's defaults; a caller that needs what only version 2 carries says so
// itself, because the two commands need different parts of it.
func Load(dir string) (*Task, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	m, err := ReadManifest(abs)
	if err != nil {
		return nil, err
	}
	return m.Task(abs)
}

// Task turns a decoded manifest into a Task, applying defaults and reporting
// the first field uzushio cannot use.
func (m *Manifest) Task(dir string) (*Task, error) {
	if m.Version != 1 && m.Version != 2 {
		return nil, fieldError("version", fmt.Sprintf("must be 1 or 2, got %d", m.Version))
	}
	if !vocab.ValidTaskID(m.ID) {
		return nil, fieldError("id", fmt.Sprintf("%q is not a task identifier (want %s)", m.ID, vocab.TaskIDPattern))
	}
	if m.Repo == "" {
		return nil, fieldError("repo", "is required")
	}
	repo := m.Repo
	if !filepath.IsAbs(repo) {
		repo = filepath.Join(dir, filepath.FromSlash(repo))
	}
	t := &Task{
		Dir:     dir,
		Version: m.Version,
		ID:      m.ID,
		Repo:    filepath.Clean(repo),
		Rev:     m.Rev,
		Files:   append([]string(nil), m.Files...),
		Verify: Verify{
			ComposeFile: DefaultComposeFile,
			Service:     DefaultService,
			Kind:        KindExitCode,
		},
		Doctor: Doctor{KillRateMin: DefaultKillRateMin, ReferenceRuns: DefaultReferenceRuns},
	}
	if t.Rev == "" {
		t.Rev = DefaultRev
	}
	if m.Verify != nil {
		if m.Verify.ComposeFile != "" {
			t.Verify.ComposeFile = m.Verify.ComposeFile
		}
		if m.Verify.Service != "" {
			t.Verify.Service = m.Verify.Service
		}
		if m.Verify.Kind != "" {
			switch Kind(m.Verify.Kind) {
			case KindExitCode, KindBand:
				t.Verify.Kind = Kind(m.Verify.Kind)
			default:
				return nil, fieldError("verify.kind",
					fmt.Sprintf("%q is neither %s nor %s", m.Verify.Kind, KindExitCode, KindBand))
			}
		}
		if m.Verify.TimeoutSeconds < 0 {
			return nil, fieldError("verify.timeout_seconds", "must not be negative")
		}
		t.Verify.TimeoutSeconds = m.Verify.TimeoutSeconds
	}
	if m.Reference != nil {
		if err := checkRelative("reference.diff", m.Reference.Diff); err != nil {
			return nil, err
		}
		t.Reference = &Reference{Path: m.Reference.Diff}
	}
	seen := map[string]bool{}
	for i, entry := range m.Mutants {
		at := fmt.Sprintf("mutants[%d]", i)
		if err := checkRelative(at+".diff", entry.Diff); err != nil {
			return nil, err
		}
		if seen[entry.Diff] {
			return nil, fieldError(at+".diff", "duplicate path "+entry.Diff)
		}
		seen[entry.Diff] = true
		mutant := Mutant{
			Diff:     entry.Diff,
			Expect:   ExpectKilled,
			Origin:   OriginHand,
			Operator: entry.Operator,
			Note:     entry.Note,
		}
		switch Expect(entry.Expect) {
		case "":
		case ExpectKilled, ExpectEquivalent:
			mutant.Expect = Expect(entry.Expect)
		default:
			return nil, fieldError(at+".expect",
				fmt.Sprintf("%q is neither %s nor %s", entry.Expect, ExpectKilled, ExpectEquivalent))
		}
		switch Origin(entry.Origin) {
		case "":
		case OriginHand, OriginGenerated:
			mutant.Origin = Origin(entry.Origin)
		default:
			return nil, fieldError(at+".origin",
				fmt.Sprintf("%q is neither %s nor %s", entry.Origin, OriginHand, OriginGenerated))
		}
		t.Mutants = append(t.Mutants, mutant)
	}
	if m.Doctor != nil {
		if m.Doctor.KillRateMin != 0 {
			if m.Doctor.KillRateMin <= 0 || m.Doctor.KillRateMin > 1 {
				return nil, fieldError("doctor.kill_rate_min",
					fmt.Sprintf("%v is outside 0 < x <= 1", m.Doctor.KillRateMin))
			}
			t.Doctor.KillRateMin = m.Doctor.KillRateMin
		}
		if m.Doctor.ReferenceRuns != 0 {
			if m.Doctor.ReferenceRuns < 1 {
				return nil, fieldError("doctor.reference_runs",
					fmt.Sprintf("%d is not positive", m.Doctor.ReferenceRuns))
			}
			t.Doctor.ReferenceRuns = m.Doctor.ReferenceRuns
		}
	}
	return t, nil
}

// RequireDoctorable reports why the task cannot be health-checked, if it
// cannot.
//
// It asks for a reference and not for mutants. A task with no reference cannot
// be checked at all: there is nothing a healthy verifier has to accept, so
// there is no question to put. A task with no mutant is a different thing — it
// can be checked, and the answer is that the kill rate was not measured, which
// the verdict already spells `inconclusive`. Refusing it here would report a
// measurable state as a broken manifest.
func (t *Task) RequireDoctorable() error {
	if t.Version < 2 || t.Reference == nil {
		return fmt.Errorf("%w: task doctor needs task.json version 2 with a reference", ErrTask)
	}
	return nil
}

// RequireRunnableVerifier reports a verifier kind uzushio cannot run, before a
// command touches docker.
//
// Both kinds are runnable, and uzushio treats them the same way on purpose:
// `cmoa verify` decides what a verification concluded, and a health check reads
// that status. What the band kind adds is the rows behind the status, which the
// report carries and the verdict does not use. The check exists so that a kind
// CMoA adds later is refused here rather than read as an exit-code verifier.
func (t *Task) RequireRunnableVerifier() error {
	switch t.Verify.Kind {
	case KindExitCode, KindBand:
		return nil
	}
	return fmt.Errorf("%w: unknown verify.kind %q", ErrTask, t.Verify.Kind)
}

// fieldError names the JSON path of the field that is wrong, the way CMoA's
// own validation errors do.
func fieldError(path, msg string) error {
	return fmt.Errorf("%w: %s: %s", ErrTask, path, msg)
}

// checkRelative refuses a path that leaves the task directory. A manifest is
// data, and a diff path is used to open a file: an absolute path or one
// climbing out of the directory is the one input worth refusing outright.
func checkRelative(at, p string) error {
	if p == "" {
		return fieldError(at, "is required")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return fieldError(at, fmt.Sprintf("%q must be relative to the task directory", p))
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fieldError(at, fmt.Sprintf("%q escapes the task directory", p))
	}
	return nil
}

// Manifest is task.json as a Go value, with the keys in the order CMoA's
// documentation writes them. It exists so that `uzushio task mutate` can append
// to `mutants` and write the file back with its keys in the same order and its
// untouched values unchanged; nested objects are pointers so that one the file
// does not declare stays undeclared on the way out.
type Manifest struct {
	Version         int              `json:"version"`
	ID              string           `json:"id"`
	Repo            string           `json:"repo"`
	Rev             string           `json:"rev,omitempty"`
	Files           []string         `json:"files"`
	MaxContextBytes int              `json:"max_context_bytes,omitempty"`
	Verify          *VerifyManifest  `json:"verify,omitempty"`
	Reference       *RefManifest     `json:"reference,omitempty"`
	Mutants         []MutantManifest `json:"mutants,omitempty"`
	Doctor          *DoctorManifest  `json:"doctor,omitempty"`
}

// VerifyManifest is the verify object.
type VerifyManifest struct {
	ComposeFile    string `json:"compose_file,omitempty"`
	Service        string `json:"service,omitempty"`
	Kind           string `json:"kind,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// RefManifest is the reference object.
type RefManifest struct {
	Diff string `json:"diff"`
}

// DoctorManifest is the doctor object.
type DoctorManifest struct {
	KillRateMin   float64 `json:"kill_rate_min,omitempty"`
	ReferenceRuns int     `json:"reference_runs,omitempty"`
}

// MutantManifest is one entry of the mutants array.
type MutantManifest struct {
	Diff     string `json:"diff"`
	Expect   string `json:"expect,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Operator string `json:"operator,omitempty"`
	Note     string `json:"note,omitempty"`
}

// ReadManifest decodes dir/task.json, ignoring keys it does not know. CMoA
// owns the schema; a reader that refused a key CMoA added would break on
// CMoA's next release rather than on its own mistake.
func ReadManifest(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("task: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrTask, ManifestFile, err)
	}
	return &m, nil
}

// CheckRewritable reports whether dir/task.json can be written back through
// this package's types without losing anything.
//
// It exists so that a command can ask the question *before* it does any work.
// Reading is lenient — a key uzushio does not know is a key CMoA added — but
// writing through the typed struct would drop that key in silence, so the
// rewrite refuses. A command that discovered the refusal only at the end would
// have already written its diffs, and the retry after the key is removed would
// write them all again under new numbers.
func CheckRewritable(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return fmt.Errorf("task: %w", err)
	}
	return checkKnownFields(raw)
}

// Write renders the manifest back into dir/task.json with two-space indent and
// a trailing newline.
//
// It refuses a file holding a key this package does not know, for the reason
// CheckRewritable gives. The message names the key so that a person can either
// add the mutants by hand or take the key out.
func (m *Manifest) Write(dir string) error {
	path := filepath.Join(dir, ManifestFile)
	if err := CheckRewritable(dir); err != nil {
		return err
	}
	body, err := m.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil { //nolint:gosec // a task manifest is world-readable on purpose
		return fmt.Errorf("task: write %s: %w", path, err)
	}
	return nil
}

// Bytes renders the manifest as task.json is written: two-space indent, one
// trailing newline.
//
// HTML escaping is off. encoding/json turns <, > and & into <, > and
// & by default, on the assumption that the output may be pasted into a
// script tag; a mutant's note reads `'+' -> '-'`, and nobody should have to
// decode that to review a diff.
func (m *Manifest) Bytes() ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("%w: encode %s: %w", ErrTask, ManifestFile, err)
	}
	return out.Bytes(), nil
}

// checkKnownFields reports the first key in the file that Manifest does not
// declare. It is the strict decode this package deliberately does not do when
// reading, run only where the answer decides whether a rewrite loses data.
func checkKnownFields(raw []byte) error {
	var probe Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&probe); err != nil {
		return fmt.Errorf(
			"%w: %s holds something uzushio would drop if it rewrote the file (%w); "+
				"add the mutants by hand, or take the key out", ErrTask, ManifestFile, err)
	}
	return nil
}
