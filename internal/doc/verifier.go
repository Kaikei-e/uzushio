package doc

import (
	"fmt"
	"strconv"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Verifier is one health check of one task's verifier: what the verifier did
// to a reference solution it should have accepted, and to the mutants it should
// have rejected. It is written by `uzushio task doctor` and never by a person,
// which is why it has no status — there is nothing about a measurement to
// accept or withdraw.
//
// Its identity is its parts — the task whose verifier was checked, the day, and
// a sequence number where one task was checked twice in a day — so the
// identifier is derived rather than carried. It holds a slash, so a verifier
// always writes `id:`.
//
// The numbers are held here as the numbers they are and written as strings: a
// scalar field is compared as text, and a rate that reads back as
// 0.8300000000000001 because it went through a float is a value nobody can
// match. Report names the report.json the check wrote, which is where a machine
// reads the numbers from.
type Verifier struct {
	// Task is the CMoA task identifier the verifier belongs to.
	Task string
	// Day is the day the check was made, as YYYY-MM-DD.
	Day string
	// Seq separates two checks of one task on one day. Zero is written as no
	// sequence number at all.
	Seq int
	// Title is the heading and the frontmatter title.
	Title string
	// Date is the day the document was written. It is the day of the check
	// unless the record was written later.
	Date string
	// Verdict is what the check concluded about the verifier.
	Verdict vocab.Health
	// KillRate is the share of the mutants expected to be killed that were
	// killed. It is written with two decimal places. Every real rate is in
	// 0..1, so the two negative sentinels are unambiguous: NoKillRate writes
	// no kill_rate key at all, and KillRateNotEvidence writes "n/a".
	KillRate float64
	// Mutants is how many mutants the check ran.
	Mutants int
	// ReferenceRuns is how many times the reference solution was verified.
	ReferenceRuns int
	// Report is the path to the report.json the check wrote, relative to the
	// task directory.
	Report string
	// Body is the Markdown under the heading.
	Body string
}

// NoKillRate is the KillRate of a check that measured none: a task whose
// mutants are all `expect: equivalent`, or one whose every mutant was
// inconclusive. It is negative because every real rate is in 0..1, so no
// measured value can be mistaken for it.
const NoKillRate float64 = -1

// KillRateNotEvidence is the KillRate of a check that measured a rate which is
// not evidence of anything, and is written `n/a`.
//
// The case it exists for is a verifier that rejected the reference solution:
// every mutant is then killed, the rate is 1.00, and the number says the
// verifier says no to everything rather than that it catches everything. A
// record is read long after the run, usually by whoever is deciding whether the
// task is worth using, and `kill_rate: "1.00"` under `verdict: unhealthy` is
// the pair most likely to be half-read. The word refuses to be half-read.
//
// It is a distinct value from NoKillRate because the two are different facts —
// nothing was measured, against something was measured and means nothing — and
// a reader who cannot tell them apart cannot tell whether the task has mutants.
const KillRateNotEvidence float64 = -2

// killRateNotEvidenceText is what KillRateNotEvidence writes. The field is
// declared with no vocabulary in the vault configuration, so DocDag compares it
// as text and any word would validate; this one is the word the summary uses.
const killRateNotEvidenceText = "n/a"

// VerifierFrontmatter is a verifier's frontmatter in the order it is written.
type VerifierFrontmatter struct {
	ID            string `yaml:"id"`
	Kind          string `yaml:"kind"`
	Title         string `yaml:"title"`
	Date          string `yaml:"date"`
	Verdict       string `yaml:"verdict"`
	KillRate      string `yaml:"kill_rate,omitempty"`
	Mutants       string `yaml:"mutants"`
	ReferenceRuns string `yaml:"reference_runs"`
	Report        string `yaml:"report,omitempty"`
}

// ID returns the verifier's identifier, or the empty string where the parts do
// not make one. Validate says which part is wrong.
func (v Verifier) ID() string {
	id, err := vocab.VerifierID(v.Task, v.Day, v.Seq)
	if err != nil {
		return ""
	}
	return id
}

// Kind returns the kind a verifier health check answers to.
func (v Verifier) Kind() vocab.Kind { return vocab.KindVerifier }

// Path returns where the check is written, relative to the vault root.
func (v Verifier) Path() (string, error) {
	id, err := vocab.VerifierID(v.Task, v.Day, v.Seq)
	if err != nil {
		return "", err
	}
	return vocab.Path(vocab.KindVerifier, id)
}

// Validate reports the first thing about the check DocDag would refuse.
func (v Verifier) Validate() error {
	id, err := vocab.VerifierID(v.Task, v.Day, v.Seq)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if err := requireText("verifier "+id+" title", v.Title); err != nil {
		return err
	}
	if err := requireDay("verifier "+id+" date", v.Date); err != nil {
		return err
	}
	if err := requireVocabulary("verifier "+id+" verdict", v.Verdict, vocab.AllHealths()); err != nil {
		return err
	}
	if !isKillRateSentinel(v.KillRate) && (v.KillRate < 0 || v.KillRate > 1) {
		return fmt.Errorf("%w: verifier %s kill_rate %v is outside 0..1", ErrDocument, id, v.KillRate)
	}
	if v.Mutants < 0 {
		return fmt.Errorf("%w: verifier %s mutants %d is negative", ErrDocument, id, v.Mutants)
	}
	// A check that never verified the reference solution has not measured the
	// thing false positives are read off, so it is not a check.
	if v.ReferenceRuns < 1 {
		return fmt.Errorf("%w: verifier %s reference_runs %d is not positive", ErrDocument, id, v.ReferenceRuns)
	}
	return nil
}

// Frontmatter returns the check's frontmatter.
func (v Verifier) Frontmatter() (VerifierFrontmatter, error) {
	if err := v.Validate(); err != nil {
		return VerifierFrontmatter{}, err
	}
	front := VerifierFrontmatter{
		ID:            v.ID(),
		Kind:          vocab.KindVerifier.String(),
		Title:         v.Title,
		Date:          v.Date,
		Verdict:       v.Verdict.String(),
		Mutants:       strconv.Itoa(v.Mutants),
		ReferenceRuns: strconv.Itoa(v.ReferenceRuns),
		Report:        v.Report,
	}
	switch v.KillRate {
	case NoKillRate:
		// No key: there was no rate to write.
	case KillRateNotEvidence:
		front.KillRate = killRateNotEvidenceText
	default:
		front.KillRate = strconv.FormatFloat(v.KillRate, 'f', 2, 64)
	}
	return front, nil
}

// isKillRateSentinel reports whether a rate is one of the two values that are
// not rates.
func isKillRateSentinel(rate float64) bool {
	return rate == NoKillRate || rate == KillRateNotEvidence
}

// Bytes returns the document as it is written to disk.
func (v Verifier) Bytes() ([]byte, error) {
	front, err := v.Frontmatter()
	if err != nil {
		return nil, err
	}
	return render(front, v.Title, v.Body)
}
