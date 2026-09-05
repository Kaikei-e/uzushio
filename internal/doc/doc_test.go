package doc_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/DocDag/lint"
	"github.com/Kaikei-e/DocDag/model"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/vault"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// The documents the round-trip writes into a copy of the repository vault. They
// are a consistent set on purpose: an edit that names a topic the vault already
// holds, the pattern it predicts, and the run that measured it. Anything less
// consistent would be reported by uzushio's own rules, and the point of the
// test is that a document this package writes is one DocDag accepts.
func roundTripDocuments() []doc.Document {
	pattern := doc.Pattern{
		PatternID: "fp/round-trip",
		Title:     "The harness forgets what it was told two turns ago",
		Date:      "2026-01-01",
		Status:    vocab.StatusOpen,
		Category:  vocab.CategoryNotProvided,
		Context:   "a task whose instruction is given once and used later",
		Component: "memory",
		// A CMoA trace run-id: the trace the failure was seen in. uzushio's own
		// evaluation runs are reached from the edit that answers the pattern,
		// across the validates edge, and never from here.
		Evidence: []string{"20260101T012345Z-a1b2c3d4"},
		Body:     "Seen often enough on the held-out split to be worth an edit.",
	}
	edit := doc.Edit{
		EditID:    "he-0001",
		Title:     "Carry the task instruction into the working note",
		Date:      "2026-01-01",
		Status:    vocab.StatusProposed,
		Component: "memory",
		Touches:   []string{"memory"},
		RootCause: "The note is rewritten each turn from the last turn alone.",
		Approval:  vocab.ApprovalHuman,
		About:     []string{"topic/spec-corpus"},
		Predicts:  []doc.Prediction{{Pattern: pattern.PatternID, Expect: vocab.ExpectFix}},
		Body:      "Append the instruction to the note rather than replacing it.",
	}
	run := doc.Run{
		Edit:      edit.EditID,
		Day:       "2026-01-01",
		ModelSlug: "sonnet",
		Split:     vocab.SplitHeldOut,
		Title:     "he-0001 on the held-out split",
		Date:      "2026-01-01",
		Verdict:   vocab.VerdictImprove,
		Suite:     "harness-tasks",
		Trials:    40,
		Body:      "Forty tasks, one seed each, the same harness revision throughout.",
	}.Measuring("sonnet", 0.72, 0.6)
	verifier := doc.Verifier{
		Task:          "hello",
		Day:           "2026-01-01",
		Title:         "The task-hello verifier kills every hand-written mutant",
		Date:          "2026-01-01",
		Verdict:       vocab.HealthHealthy,
		KillRate:      1,
		Mutants:       2,
		ReferenceRuns: 3,
		Report:        "doctor/20260101T012345Z-a1b2c3d4/report.json",
		Body:          "Three reference runs passed and both mutants were killed.",
	}
	return []doc.Document{edit, pattern, run, verifier}
}

func TestRoundTrip(t *testing.T) {
	documents := roundTripDocuments()
	root := tempVault(t)
	for _, document := range documents {
		writeInto(t, root, document)
	}

	cfg, err := vault.Config()
	if err != nil {
		t.Fatalf("vault.Config() error = %v", err)
	}
	// The corpus layer over the copied vault: the documents this package wrote
	// have to leave it without an error of any kind. Warnings are the corpus
	// layer's opinion of how young the vault is, not of what was written.
	findings, err := lint.Check(cfg, root, "")
	if err != nil {
		t.Fatalf("lint.Check() error = %v", err)
	}
	for _, f := range findings {
		if f.Severity == model.SeverityError {
			t.Errorf("lint.Check() reported %s %s %s: %s", f.Severity, f.Rule, f.ID, f.Detail)
		}
	}

	// And DocDag's own reader, where it is installed: the engine that decides
	// what a document is, rather than the library the test links against.
	binary, ok := docdagBinary()
	if !ok {
		t.Log("docdag is not installed; skipping the validate round-trip")
		return
	}
	cmd := exec.Command(binary, "validate")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docdag validate error = %v\n%s", err, out)
	}
	t.Logf("docdag validate: %s", strings.TrimSpace(string(out)))
}

func TestPaths(t *testing.T) {
	want := map[string]string{
		"he-0001":                           "spec/edits/he-0001.md",
		"fp/round-trip":                     "spec/patterns/round-trip.md",
		"run/he-0001@2026-01-01-sonnet-out": "spec/runs/he-0001@2026-01-01-sonnet-out.md",
		"verifier/hello@2026-01-01":         "spec/verifiers/hello@2026-01-01.md",
	}
	for _, document := range roundTripDocuments() {
		got, err := document.Path()
		if err != nil {
			t.Fatalf("%s.Path() error = %v", document.ID(), err)
		}
		if want[document.ID()] != got {
			t.Errorf("%s.Path() = %q, want %q", document.ID(), got, want[document.ID()])
		}
		body, err := document.Bytes()
		if err != nil {
			t.Fatalf("%s.Bytes() error = %v", document.ID(), err)
		}
		// A slashed identifier cannot be read off a file name's stem, so those
		// kinds write id: and the edit kind does not.
		writesID := strings.Contains(string(body), "\nid: ")
		if writesID != vocab.WritesID(document.Kind()) {
			t.Errorf("%s writes id: %v, want %v", document.ID(), writesID, vocab.WritesID(document.Kind()))
		}
	}
}

func TestTemplate(t *testing.T) {
	for _, kind := range vocab.AllKinds() {
		got, err := doc.Template(kind)
		if err != nil {
			t.Fatalf("Template(%s) error = %v", kind, err)
		}
		if !strings.HasPrefix(got, "---\n") || !strings.Contains(got, "\n---\n\n# ") {
			t.Errorf("Template(%s) is not frontmatter and a heading:\n%s", kind, got)
		}
		if !strings.Contains(got, "kind: "+kind.String()) {
			t.Errorf("Template(%s) does not name its kind:\n%s", kind, got)
		}
	}
	if _, err := doc.Template(vocab.Kind("clause")); !errors.Is(err, doc.ErrDocument) {
		t.Errorf("Template(clause) error = %v, want %v", err, doc.ErrDocument)
	}
}

// TestTemplatesAreDocuments writes every template into a copy of the vault and
// asks DocDag whether it is a document. A skeleton that does not validate is a
// skeleton nobody can start from.
func TestTemplatesAreDocuments(t *testing.T) {
	// The identifiers the skeletons carry. They are placeholders, and a
	// placeholder that does not parse is one nobody notices until a person has
	// already built a document on it.
	templates := []struct {
		kind vocab.Kind
		id   string
	}{
		{vocab.KindEdit, "he-0000"},
		{vocab.KindPattern, "fp/example"},
		{vocab.KindRun, "run/he-0000@" + doc.TemplateDay + "-model-out"},
		{vocab.KindVerifier, "verifier/example@" + doc.TemplateDay},
		{vocab.KindCalibration, "calibration/judge-model@" + doc.TemplateDay},
	}
	root := tempVault(t)
	for _, template := range templates {
		body, err := doc.Template(template.kind)
		if err != nil {
			t.Fatalf("Template(%s) error = %v", template.kind, err)
		}
		relative, err := vocab.Path(template.kind, template.id)
		if err != nil {
			t.Fatalf("vocab.Path(%s, %s) error = %v", template.kind, template.id, err)
		}
		writeText(t, filepath.Join(root, filepath.FromSlash(relative)), body)
	}
	cfg, err := vault.Config()
	if err != nil {
		t.Fatalf("vault.Config() error = %v", err)
	}
	findings, err := lint.Check(cfg, root, "")
	if err != nil {
		t.Fatalf("lint.Check() error = %v", err)
	}
	for _, f := range findings {
		// The template edit predicts fp/example and is about topic/spec-corpus;
		// the pattern it names is written beside it, so the only findings left
		// are the corpus layer's, and none of them may be an error.
		if f.Severity == model.SeverityError {
			t.Errorf("template corpus reported %s %s %s: %s", f.Severity, f.Rule, f.ID, f.Detail)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	base := roundTripDocuments()
	edit, pattern, run := base[0].(doc.Edit), base[1].(doc.Pattern), base[2].(doc.Run)
	verifier := base[3].(doc.Verifier)

	tests := []struct {
		name    string
		phrase  string
		subject doc.Document
	}{
		{"edit identifier", "is not an edit identifier", modifyEdit(edit, func(e *doc.Edit) { e.EditID = "he-1" })},
		{"edit status", "status", modifyEdit(edit, func(e *doc.Edit) { e.Status = vocab.StatusOpen })},
		{"edit component", "is not a harness surface", modifyEdit(edit, func(e *doc.Edit) { e.Component = "verifier" })},
		{"edit touches read-only", "seven harness surfaces", modifyEdit(edit, func(e *doc.Edit) {
			e.Touches = []string{"memory", "tracer"}
		})},
		// The vault reports an edit with no touches: as an error, so the writer
		// refuses to produce one. Both spellings of "nothing" are refused.
		{"edit touches nothing", "writes no touches:", modifyEdit(edit, func(e *doc.Edit) { e.Touches = nil })},
		{"edit touches empty", "writes no touches:", modifyEdit(edit, func(e *doc.Edit) { e.Touches = []string{} })},
		{"edit approval", "approval", modifyEdit(edit, func(e *doc.Edit) { e.Approval = "nobody" })},
		{"edit day", "is not a 2006-01-02 day", modifyEdit(edit, func(e *doc.Edit) { e.InForceFrom = "next week" })},
		{"edit topic", "is not an identifier", modifyEdit(edit, func(e *doc.Edit) { e.About = []string{"spec-corpus"} })},
		{"predicts target", "is not a pattern identifier", modifyEdit(edit, func(e *doc.Edit) {
			e.Predicts = []doc.Prediction{{Pattern: "retry-storm", Expect: vocab.ExpectFix}}
		})},
		{"predicts expect", "expect", modifyEdit(edit, func(e *doc.Edit) {
			e.Predicts = []doc.Prediction{{Pattern: "fp/round-trip", Expect: "maybe"}}
		})},
		{"predicts outcome", "outcome", modifyEdit(edit, func(e *doc.Edit) {
			e.Predicts = []doc.Prediction{{Pattern: "fp/round-trip", Expect: vocab.ExpectFix, Outcome: "unclear"}}
		})},
		{"supersedes reason", "which is outside", modifyEdit(edit, func(e *doc.Edit) {
			e.Supersedes = []doc.Supersession{{Edit: "he-0002", Reason: "rewrite"}}
		})},
		{"pattern identifier", "is not a pattern identifier", modifyPattern(pattern, func(p *doc.Pattern) {
			p.PatternID = "fp/Retry Storm"
		})},
		{"pattern category", "category", modifyPattern(pattern, func(p *doc.Pattern) { p.Category = "surprise" })},
		{"pattern context", "context is empty", modifyPattern(pattern, func(p *doc.Pattern) { p.Context = "  " })},
		{"pattern status", "status", modifyPattern(pattern, func(p *doc.Pattern) { p.Status = vocab.StatusAccepted })},
		// evidence is CMoA's vocabulary, not uzushio's: a uzushio run identifier
		// is exactly the plausible wrong answer, so it is the one tested.
		{"pattern evidence", vocab.CMoATraceIDPattern, modifyPattern(pattern, func(p *doc.Pattern) {
			p.Evidence = []string{"run/he-0001@2026-01-01-sonnet-out"}
		})},
		{"run model slug", "model slug", modifyRun(run, func(r *doc.Run) { r.ModelSlug = "Sonnet 4" })},
		{"run verdict", "verdict", modifyRun(run, func(r *doc.Run) { r.Verdict = "better" })},
		{"run suite", "suite is empty", modifyRun(run, func(r *doc.Run) { r.Suite = "" })},
		{"run split", "unknown split", modifyRun(run, func(r *doc.Run) { r.Split = "held-back" })},
		{"run rate", "is outside 0..1", modifyRun(run, func(r *doc.Run) {
			r.Validates = []doc.Validation{{Edit: "he-0001", Model: "sonnet", PassRate: 1.4}}
		})},
		{"run validates target", "is not an edit identifier", modifyRun(run, func(r *doc.Run) {
			r.Validates = []doc.Validation{{Edit: "fp/round-trip", Model: "sonnet"}}
		})},
		{"verifier task", "is not a task identifier", modifyVerifier(verifier, func(v *doc.Verifier) {
			v.Task = "Hello"
		})},
		{"verifier day", "is not a 2006-01-02 day", modifyVerifier(verifier, func(v *doc.Verifier) {
			v.Day = "2026-1-1"
		})},
		// The health words and the run verdicts are two vocabularies on one key,
		// so a run's word is the plausible wrong answer and the one tested.
		{"verifier verdict", "verdict", modifyVerifier(verifier, func(v *doc.Verifier) {
			v.Verdict = vocab.Health(vocab.VerdictImprove)
		})},
		{"verifier kill rate", "is outside 0..1", modifyVerifier(verifier, func(v *doc.Verifier) {
			v.KillRate = 1.5
		})},
		{"verifier mutants", "is negative", modifyVerifier(verifier, func(v *doc.Verifier) {
			v.Mutants = -1
		})},
		{"verifier reference runs", "is not positive", modifyVerifier(verifier, func(v *doc.Verifier) {
			v.ReferenceRuns = 0
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.subject.Validate()
			if !errors.Is(err, doc.ErrDocument) {
				t.Fatalf("Validate() error = %v, want %v", err, doc.ErrDocument)
			}
			if !strings.Contains(err.Error(), tt.phrase) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tt.phrase)
			}
			if _, err := tt.subject.Bytes(); !errors.Is(err, doc.ErrDocument) {
				t.Errorf("Bytes() error = %v, want %v", err, doc.ErrDocument)
			}
		})
	}
}

// TestEvidenceVocabulary pins what a failure pattern's evidence is: CMoA's
// trace run-id, and not uzushio's evaluation run. The two are easy to confuse
// and nothing in DocDag checks either — evidence is a plain list, and
// frontmatter reference scanning reads only wikilinks — so this check is the
// only one there is.
func TestEvidenceVocabulary(t *testing.T) {
	accepted := []string{"20260905T012345Z-a1b2c3d4", "19700101T000000Z-00000000"}
	refused := []string{
		"run/he-0001@2026-01-01-sonnet-out", // uzushio's own run identifier
		"20260905T012345Z-A1B2C3D4",         // hexadecimal is lower case
		"20260905T012345-a1b2c3d4",          // the zone is part of the shape
		"20260905T012345Z-a1b2c3d",          // seven digits, not eight
	}
	base := roundTripDocuments()[1].(doc.Pattern)
	for _, id := range accepted {
		if err := modifyPattern(base, func(p *doc.Pattern) { p.Evidence = []string{id} }).Validate(); err != nil {
			t.Errorf("evidence %q rejected: %v", id, err)
		}
	}
	for _, id := range refused {
		if err := modifyPattern(base, func(p *doc.Pattern) { p.Evidence = []string{id} }).Validate(); err == nil {
			t.Errorf("evidence %q accepted, want it held to %s", id, vocab.CMoATraceIDPattern)
		}
	}
}

// TestTouching pins the default the writer gives an edit's blast radius: an
// edit changes at least the surface it is about.
func TestTouching(t *testing.T) {
	base := roundTripDocuments()[0].(doc.Edit)
	if got := base.Touching().Touches; len(got) != 1 || got[0] != base.Component {
		t.Errorf("Touching() = %v, want [%s]", got, base.Component)
	}
	if got := base.Touching("memory", "skill").Touches; len(got) != 2 {
		t.Errorf("Touching(memory, skill) = %v, want both", got)
	}
}

// TestPresetIdentifiers holds the three preset identifier shapes this package
// writes against to the ones the assembled configuration declares. They are the
// spec preset's rather than uzushio's, so nothing else keeps them in step.
func TestPresetIdentifiers(t *testing.T) {
	cfg, err := vault.Config()
	if err != nil {
		t.Fatalf("vault.Config() error = %v", err)
	}
	tests := []struct {
		kind  string
		valid func(string) bool
		id    string
	}{
		{"topic", doc.ValidTopicID, "topic/spec-corpus"},
		{"premise", doc.ValidPremiseID, "premise/docdag-v0-3-0"},
		{"pm", doc.ValidPostMortemID, "pm-0001"},
	}
	for _, tt := range tests {
		spec, ok := cfg.Kind(tt.kind)
		if !ok {
			t.Fatalf("the configuration declares no %s kind", tt.kind)
		}
		if !tt.valid(tt.id) {
			t.Errorf("%s: %q is rejected here and declared as %s", tt.kind, tt.id, spec.ID)
		}
		if tt.valid(strings.ToUpper(tt.id)) {
			t.Errorf("%s: %q is accepted here and the kind declares %s", tt.kind, strings.ToUpper(tt.id), spec.ID)
		}
	}
}

// tempVault copies the repository's configuration and specification corpus into
// a directory of the test's own, so a document can be written beside real ones
// without touching the repository.
func tempVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := repoRoot(t)
	copyFile(t, filepath.Join(source, "docdag.yaml"), filepath.Join(root, "docdag.yaml"))
	specRoot := filepath.Join(source, "spec")
	err := filepath.WalkDir(specRoot, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(specRoot, p)
		if err != nil {
			return err
		}
		copyFile(t, p, filepath.Join(root, "spec", relative))
		return nil
	})
	if err != nil {
		t.Fatalf("copy spec: %v", err)
	}
	return root
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	return root
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(to), err)
	}
	if err := os.WriteFile(to, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", to, err)
	}
}

func writeInto(t *testing.T, root string, document doc.Document) {
	t.Helper()
	relative, err := document.Path()
	if err != nil {
		t.Fatalf("%s.Path() error = %v", document.ID(), err)
	}
	body, err := document.Bytes()
	if err != nil {
		t.Fatalf("%s.Bytes() error = %v", document.ID(), err)
	}
	writeText(t, filepath.Join(root, filepath.FromSlash(relative)), string(body))
}

func writeText(t *testing.T, target, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// docdagBinary names the engine to run the round-trip against: the one an
// environment names, the one on PATH, or the one `go install` would have put in
// GOBIN or GOPATH/bin. The last is asked of the toolchain rather than guessed
// from a home directory, because GOBIN and GOPATH are both configurable and a
// guess that misses reports the test as skipped rather than as wrong.
func docdagBinary() (string, bool) {
	if named := os.Getenv("UZUSHIO_DOCDAG_BIN"); named != "" {
		return named, true
	}
	if found, err := exec.LookPath("docdag"); err == nil {
		return found, true
	}
	out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	candidates := []string{}
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		candidates = append(candidates, filepath.Join(strings.TrimSpace(lines[0]), "docdag"))
	}
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
		candidates = append(candidates, filepath.Join(strings.TrimSpace(lines[1]), "bin", "docdag"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func modifyEdit(base doc.Edit, change func(*doc.Edit)) doc.Edit {
	change(&base)
	return base
}

func modifyPattern(base doc.Pattern, change func(*doc.Pattern)) doc.Pattern {
	change(&base)
	return base
}

func modifyRun(base doc.Run, change func(*doc.Run)) doc.Run {
	change(&base)
	return base
}

func modifyVerifier(base doc.Verifier, change func(*doc.Verifier)) doc.Verifier {
	change(&base)
	return base
}

// TestVerifierNumbersAreWrittenAsStrings pins the one thing about the verifier
// writer that is a decision rather than a shape. A scalar field is compared as
// text, so a rate is written with two decimal places and the counts are written
// as integers, and both are quoted: `kill_rate: 0.83` read back as a float is a
// value that renders as 0.8300000000000001 on the next machine.
func TestVerifierNumbersAreWrittenAsStrings(t *testing.T) {
	base := roundTripDocuments()[3].(doc.Verifier)
	body, err := modifyVerifier(base, func(v *doc.Verifier) {
		v.KillRate = 5.0 / 6.0
		v.Mutants = 6
		v.ReferenceRuns = 3
	}).Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	for _, want := range []string{"kill_rate: \"0.83\"", "mutants: \"6\"", "reference_runs: \"3\""} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the document does not carry %q:\n%s", want, body)
		}
	}
	// A check with nothing to measure a rate over writes no kill_rate key at
	// all, rather than a zero that reads as "everything survived".
	body, err = modifyVerifier(base, func(v *doc.Verifier) {
		v.KillRate = doc.NoKillRate
		v.Mutants = 0
		v.Verdict = vocab.HealthInconclusive
	}).Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if strings.Contains(string(body), "kill_rate") {
		t.Errorf("a check with no rate wrote a kill_rate key:\n%s", body)
	}
}

// TestVerifierWritesNotAvailableForARateThatIsNotEvidence is the third state of
// the kill rate, beside a number and no key at all: a rate that was measured
// and means nothing, because the verifier rejected the reference solution and
// so rejected every mutant with it.
//
// It is a word rather than a number on purpose. A record outlives its run, and
// `kill_rate: "1.00"` under `verdict: unhealthy` is the pair a hurried reader
// gets backwards.
func TestVerifierWritesNotAvailableForARateThatIsNotEvidence(t *testing.T) {
	base := roundTripDocuments()[3].(doc.Verifier)
	verifier := modifyVerifier(base, func(v *doc.Verifier) {
		v.KillRate = doc.KillRateNotEvidence
		v.Verdict = vocab.HealthUnhealthy
	})
	if err := verifier.Validate(); err != nil {
		t.Fatalf("Validate refused the not-evidence sentinel: %v", err)
	}
	front, err := verifier.Frontmatter()
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if front.KillRate != "n/a" {
		t.Fatalf("kill_rate = %q, want n/a", front.KillRate)
	}
	body, err := verifier.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !strings.Contains(string(body), "kill_rate: n/a\n") {
		t.Fatalf("the document does not carry the word:\n%s", body)
	}
	// The two sentinels are different facts — nothing was measured, against
	// something was measured and means nothing — so they must not collapse.
	if doc.NoKillRate == doc.KillRateNotEvidence {
		t.Fatal("the two sentinels are the same value")
	}
}

// TestVerifierStillRefusesARateOutsideTheRange checks that widening Validate
// for the sentinels did not widen it for everything: -0.5 is not a rate and not
// a sentinel, and a record carrying one is a bug worth refusing.
func TestVerifierStillRefusesARateOutsideTheRange(t *testing.T) {
	base := roundTripDocuments()[3].(doc.Verifier)
	for _, rate := range []float64{-0.5, -3, 1.5} {
		verifier := modifyVerifier(base, func(v *doc.Verifier) { v.KillRate = rate })
		if err := verifier.Validate(); err == nil {
			t.Errorf("Validate accepted kill_rate %v", rate)
		}
	}
}

// TestOneCalibrationLintsClean is a regression on the lint layer rather than
// on a document.
//
// A vault holding exactly one calibration is the state every vault is in the
// day its first judge is measured, and it used to fail `docdag lint --all`.
// The reason was structural: "a newer calibration has replaced this one" was a
// named projection, DocDag reports a projection that holds nowhere in the
// corpus as a warning, and a projection about *replacement* cannot hold until
// there are two documents to replace one another. The condition is written
// inline in `effective` now, so there is no named unit to be vacuous — the
// semantics are unchanged and the finding has nowhere to come from.
//
// DocDag v0.4.0 offers no way to say a projection may be vacuous: the
// projection spec carries only a name and a condition, and while `lint`
// downgrades a rule that never fired to INFO when a fixture shows it can fire,
// the equivalent check for projections has no such clause. `--corpus` turns
// the whole layer on or off and `--strict` only makes warnings fatal.
func TestOneCalibrationLintsClean(t *testing.T) {
	root := tempVault(t)
	writeText(t, filepath.Join(root, "calibrations", "j@2026-09-05", "report.json"), "{}\n")
	writeCalibration(t, root, aTestCalibration("2026-09-05", vocab.CalibratedNo, 0.281, nil))

	findings := lintVault(t, root)
	for _, f := range findings {
		if f.Severity == model.SeverityError || f.Severity == model.SeverityWarn {
			t.Errorf("a vault with one calibration reported %s %s %s: %s",
				f.Severity, f.Rule, f.ID, f.Detail)
		}
	}
}

// TestANewerCalibrationRetiresTheOlder is the semantics the inlining had to
// keep: the day a judge is measured again, the older measurement stops being
// what the vault answers with — without the older document being edited, since
// it is append-only history.
func TestANewerCalibrationRetiresTheOlder(t *testing.T) {
	root := tempVault(t)
	for _, day := range []string{"2026-09-05", "2026-09-20"} {
		writeText(t, filepath.Join(root, "calibrations", "j@"+day, "report.json"), "{}\n")
	}
	older := aTestCalibration("2026-09-05", vocab.CalibratedYes, 0.71, nil)
	newer := aTestCalibration("2026-09-20", vocab.CalibratedNo, 0.28,
		[]doc.Supersession{{Edit: older.ID(), Reason: vocab.ReasonRemeasured}})
	writeCalibration(t, root, older)
	writeCalibration(t, root, newer)

	for _, f := range lintVault(t, root) {
		if f.Severity == model.SeverityError || f.Severity == model.SeverityWarn {
			t.Errorf("a vault with two calibrations reported %s %s %s: %s",
				f.Severity, f.Rule, f.ID, f.Detail)
		}
	}

	// What binds is the engine's answer, so it is the engine that is asked.
	// Both are inside their thirty days; only the one nothing replaced binds.
	binary, ok := docdagBinary()
	if !ok {
		t.Skip("docdag is not installed; what binds is its answer and nothing substitutes for it")
	}
	cmd := exec.Command(binary, "query", "--binding", "--fields", "id",
		"--format", "json", "--as-of", "2026-09-25")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docdag query: %v\n%s", err, out)
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("docdag query returned %s: %v", out, err)
	}
	binding := map[string]bool{}
	for _, row := range rows {
		binding[row.ID] = true
	}
	if !binding[newer.ID()] {
		t.Fatalf("the newer measurement does not bind: %s", out)
	}
	if binding[older.ID()] {
		t.Fatalf("the replaced measurement still binds, so the vault answers "+
			"`calibrated` a fortnight after the measurement that says otherwise: %s", out)
	}
}

// lintVault runs every lint layer over a vault, the way CI does.
//
// The repository's own fixtures are read rather than nothing: a rule that
// fires nowhere in a young corpus is a warning until a fixture shows it can
// fire, and passing no fixtures would make this test complain about twelve
// rules that have nothing to do with what it is checking.
func lintVault(t *testing.T, root string) []model.Finding {
	t.Helper()
	cfg, err := vault.Config()
	if err != nil {
		t.Fatalf("vault.Config() error = %v", err)
	}
	findings, err := lint.Check(cfg, root, filepath.Join(repoRoot(t), "lint"))
	if err != nil {
		t.Fatalf("lint.Check() error = %v", err)
	}
	return findings
}

// aTestCalibration is one measurement of one judge, with everything that is
// not the argument held constant.
func aTestCalibration(day string, verdict vocab.Calibrated, human float64,
	supersedes []doc.Supersession,
) doc.Calibration {
	return doc.Calibration{
		Judge: "j", Day: day, Title: "judged on " + day, Date: day,
		Pool: doc.PoolExternal, WindowFrom: day, WindowTo: day, NItems: 200,
		TieHandling: vocab.TieAbstainAsCategory,
		SwapKappa:   0.589, RerunKappa: 0.820, HumanKappa: human, NHuman: 200,
		Verdict: verdict, Report: "calibrations/j@" + day + "/report.json",
		Supersedes: supersedes,
		Body:       "What the judge was measured on, and what it agreed with.",
	}
}

func writeCalibration(t *testing.T, root string, calibration doc.Calibration) {
	t.Helper()
	relative, err := calibration.Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	body, err := calibration.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	writeText(t, filepath.Join(root, filepath.FromSlash(relative)), string(body))
}
