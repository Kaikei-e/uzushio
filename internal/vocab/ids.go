package vocab

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"time"
)

// The identifier shapes, exactly as the generated docdag.yaml declares them
// under each kind's `id:`. They are the contract between the file names on
// disk, the wikilinks in prose and the graph DocDag builds, so they live here
// as the literal strings the configuration writes rather than as something
// assembled at run time.
const (
	// EditIDPattern is a four-digit harness edit: he-0001.
	EditIDPattern = `^he-\d{4}$`
	// PatternIDPattern is a slugged failure pattern: fp/retry-storm.
	PatternIDPattern = `^fp/[a-z0-9-]+$`
	// RunIDPattern is an evaluation run: the edit it measured, the day it ran,
	// the model slug, the split, and a sequence number where one edit was
	// measured twice the same day on the same model and split.
	RunIDPattern = `^run/he-\d{4}@\d{4}-\d{2}-\d{2}-[a-z0-9.]+(-[a-z0-9.]+)*-(in|out)(-\d+)?$`
	// VerifierIDPattern is one health check of one task's verifier: the task
	// the verifier belongs to, the day it was checked, and a sequence number
	// where the same task was checked twice in a day. The task part is CMoA's
	// task id shape, because that is what the identifier names.
	VerifierIDPattern = `^verifier/[a-z0-9][a-z0-9-]{0,63}@\d{4}-\d{2}-\d{2}(-\d+)?$`
	// CalibrationIDPattern is one calibration of one judge: the judge's model
	// slug, the day the window closed, and a sequence number where one judge
	// was calibrated twice in a day. The slug is spelled the way a run
	// identifier spells a model, because it names the same sort of thing.
	CalibrationIDPattern = `^calibration/[a-z0-9.]+(-[a-z0-9.]+)*@\d{4}-\d{2}-\d{2}(-\d+)?$`
)

// The same three shapes without their anchors, for composing the single
// alternation `references.pattern` gates wikilinks with. A token the reference
// pattern rejects is dropped in silence rather than reported, so the pattern
// and the kinds' identifiers have to stay in step; the test that rebuilds each
// anchored pattern from its body is what keeps them there.
const (
	EditIDBody        = `he-\d{4}`
	PatternIDBody     = `fp/[a-z0-9-]+`
	RunIDBody         = `run/he-\d{4}@\d{4}-\d{2}-\d{2}-[a-z0-9.]+(-[a-z0-9.]+)*-(in|out)(-\d+)?`
	VerifierIDBody    = `verifier/[a-z0-9][a-z0-9-]{0,63}@\d{4}-\d{2}-\d{2}(-\d+)?`
	CalibrationIDBody = `calibration/[a-z0-9.]+(-[a-z0-9.]+)*@\d{4}-\d{2}-\d{2}(-\d+)?`
)

// TaskIDPattern is the shape CMoA holds a task identifier to, and which a
// verifier identifier carries. uzushio only reads it: it is declared in CMoA's
// internal/task and appears here because a verifier document is named after the
// task whose verifier it checked.
const TaskIDPattern = `^[a-z0-9][a-z0-9-]{0,63}$`

// CMoATraceIDPattern is the identifier CMoA gives one run of the harness it
// owns: a UTC timestamp and eight hexadecimal digits, as
// 20260905T012345Z-a1b2c3d4. It is CMoA's trace-schema run-id — the shape is
// documented in CMoA's docs/trace-schema.md and enforced by CMoA itself — and
// uzushio only reads it.
//
// It is the vocabulary of a failure pattern's evidence: what a pattern cites is
// the trace it was seen in, not an evaluation run of uzushio's own. Those are
// reached the other way round, from the edit that answers the pattern across
// the validates edge.
const CMoATraceIDPattern = `^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`

// DayLayout is the one day format uzushio writes, which is the one DocDag
// reads periods and dates in.
const DayLayout = "2006-01-02"

// ErrID is the sentinel every identifier constructor and parser wraps, so a
// caller can tell a malformed identifier from an I/O failure without matching
// on message text.
var ErrID = errors.New("vocab: invalid identifier")

// The compiled matchers. Compilation failure is a programmer error in the
// table above and there is nothing to recover from, so it panics at init.
var (
	editID        = regexp.MustCompile(EditIDPattern)
	patternID     = regexp.MustCompile(PatternIDPattern)
	runID         = regexp.MustCompile(RunIDPattern)
	verifierID    = regexp.MustCompile(VerifierIDPattern)
	calibrationID = regexp.MustCompile(CalibrationIDPattern)
	taskID        = regexp.MustCompile(TaskIDPattern)
	// calibrationShape is CalibrationIDPattern with the parts named, held to
	// the same accept/reject decisions by the same kind of test that holds
	// runShape and verifierShape to theirs.
	calibrationShape = regexp.MustCompile(
		`^calibration/([a-z0-9.]+(?:-[a-z0-9.]+)*)@(\d{4}-\d{2}-\d{2})(?:-(\d+))?$`)
	// verifierShape is VerifierIDPattern with the parts named, the way
	// runShape is RunIDPattern with the parts named, and held to the same
	// accept/reject decisions by the same kind of test.
	verifierShape = regexp.MustCompile(
		`^verifier/([a-z0-9][a-z0-9-]{0,63})@(\d{4}-\d{2}-\d{2})(?:-(\d+))?$`)
	// runShape is RunIDPattern with the parts named, which is what parsing
	// needs and what the configuration must not carry. The test that holds the
	// two to the same accept/reject decisions is what keeps them one shape.
	runShape = regexp.MustCompile(
		`^run/(he-\d{4})@(\d{4}-\d{2}-\d{2})-([a-z0-9.]+(?:-[a-z0-9.]+)*)-(in|out)(?:-(\d+))?$`)
	modelSlug = regexp.MustCompile(`^[a-z0-9.]+(-[a-z0-9.]+)*$`)
	slug      = regexp.MustCompile(`^[a-z0-9-]+$`)
	// cmoaTraceID is CMoA's, not uzushio's. It is compiled here because a
	// pattern's evidence is checked against it and nothing else in the corpus
	// checks it at all.
	cmoaTraceID = regexp.MustCompile(CMoATraceIDPattern)
)

// ValidCMoATraceID reports whether id names one CMoA trace run.
func ValidCMoATraceID(id string) bool { return cmoaTraceID.MatchString(id) }

// ValidEditID reports whether id is a well-formed edit identifier.
func ValidEditID(id string) bool { return editID.MatchString(id) }

// ValidPatternID reports whether id is a well-formed pattern identifier.
func ValidPatternID(id string) bool { return patternID.MatchString(id) }

// ValidRunID reports whether id is a well-formed run identifier.
func ValidRunID(id string) bool { return runID.MatchString(id) }

// ValidVerifierID reports whether id is a well-formed verifier identifier.
func ValidVerifierID(id string) bool { return verifierID.MatchString(id) }

// ValidCalibrationID reports whether id is a well-formed calibration
// identifier.
func ValidCalibrationID(id string) bool { return calibrationID.MatchString(id) }

// ValidTaskID reports whether s names a task the way CMoA spells one.
func ValidTaskID(s string) bool { return taskID.MatchString(s) }

// ValidModelSlug reports whether s names a model the way a run identifier
// spells one: lowercase letters, digits and dots, in segments joined by single
// hyphens.
func ValidModelSlug(s string) bool { return modelSlug.MatchString(s) }

// EditNumberMax is the largest edit the four-digit shape can name.
const EditNumberMax = 9999

// EditID returns the identifier of the n-th harness edit.
func EditID(n int) (string, error) {
	if n < 1 || n > EditNumberMax {
		return "", fmt.Errorf("%w: edit number %d is outside 1..%d", ErrID, n, EditNumberMax)
	}
	return fmt.Sprintf("he-%04d", n), nil
}

// ParseEditID returns the number an edit identifier names.
func ParseEditID(id string) (int, error) {
	if !ValidEditID(id) {
		return 0, fmt.Errorf("%w: %q is not an edit identifier (want %s)", ErrID, id, EditIDPattern)
	}
	n, err := strconv.Atoi(id[len("he-"):])
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", ErrID, id, err)
	}
	return n, nil
}

// PatternID returns the identifier of the failure pattern named by slug.
func PatternID(s string) (string, error) {
	if !slug.MatchString(s) {
		return "", fmt.Errorf("%w: pattern slug %q is not lowercase letters, digits and hyphens", ErrID, s)
	}
	return "fp/" + s, nil
}

// ParsePatternID returns the slug a pattern identifier names.
func ParsePatternID(id string) (string, error) {
	if !ValidPatternID(id) {
		return "", fmt.Errorf("%w: %q is not a pattern identifier (want %s)", ErrID, id, PatternIDPattern)
	}
	return id[len("fp/"):], nil
}

// Suffix returns the split as a run identifier spells it. The identifier is
// read by people in file listings, where held-in and held-out would double the
// hyphens the model slug already uses; the frontmatter keeps the long words.
func (s Split) Suffix() (string, error) {
	switch s {
	case SplitHeldIn:
		return "in", nil
	case SplitHeldOut:
		return "out", nil
	}
	return "", fmt.Errorf("%w: unknown split %q", ErrID, s)
}

// SplitFromSuffix returns the split a run identifier's suffix names.
func SplitFromSuffix(suffix string) (Split, error) {
	switch suffix {
	case "in":
		return SplitHeldIn, nil
	case "out":
		return SplitHeldOut, nil
	}
	return "", fmt.Errorf("%w: unknown split suffix %q", ErrID, suffix)
}

// RunRef is a run identifier taken apart: the edit the run measured, the day
// it ran, the model it ran on, the split it measured, and the sequence number
// that separates two runs that agree on all four. Seq is zero where the
// identifier carries none.
type RunRef struct {
	Edit      string
	Day       string
	ModelSlug string
	Split     Split
	Seq       int
}

// RunID returns the identifier of one evaluation run. seq is written only when
// it is positive: the first run of a day needs no sequence number, and a zero
// would make two spellings of one run.
func RunID(edit, day, model string, split Split, seq int) (string, error) {
	if !ValidEditID(edit) {
		return "", fmt.Errorf("%w: run edit %q is not an edit identifier", ErrID, edit)
	}
	if _, err := time.Parse(DayLayout, day); err != nil {
		return "", fmt.Errorf("%w: run day %q is not a %s day", ErrID, day, DayLayout)
	}
	if !ValidModelSlug(model) {
		return "", fmt.Errorf("%w: model slug %q is not lowercase letters, digits and dots in hyphenated segments", ErrID, model)
	}
	suffix, err := split.Suffix()
	if err != nil {
		return "", err
	}
	if seq < 0 {
		return "", fmt.Errorf("%w: run sequence %d is negative", ErrID, seq)
	}
	id := fmt.Sprintf("run/%s@%s-%s-%s", edit, day, model, suffix)
	if seq > 0 {
		id += "-" + strconv.Itoa(seq)
	}
	if !ValidRunID(id) {
		return "", fmt.Errorf("%w: %q is not a run identifier (want %s)", ErrID, id, RunIDPattern)
	}
	return id, nil
}

// ParseRunID takes a run identifier apart. It accepts exactly what
// RunIDPattern accepts, which is a shape rather than a calendar: RunID also
// refuses a day no calendar has, and this does not, so an identifier DocDag
// admits never fails to parse here.
func ParseRunID(id string) (RunRef, error) {
	match := runShape.FindStringSubmatch(id)
	if match == nil {
		return RunRef{}, fmt.Errorf("%w: %q is not a run identifier (want %s)", ErrID, id, RunIDPattern)
	}
	split, err := SplitFromSuffix(match[4])
	if err != nil {
		return RunRef{}, err
	}
	ref := RunRef{Edit: match[1], Day: match[2], ModelSlug: match[3], Split: split}
	if match[5] != "" {
		seq, err := strconv.Atoi(match[5])
		if err != nil {
			return RunRef{}, fmt.Errorf("%w: %q: %w", ErrID, id, err)
		}
		ref.Seq = seq
	}
	return ref, nil
}

// ID rebuilds the identifier a run reference came from.
func (r RunRef) ID() (string, error) {
	return RunID(r.Edit, r.Day, r.ModelSlug, r.Split, r.Seq)
}

// VerifierRef is a verifier identifier taken apart: the task whose verifier was
// checked, the day it was checked, and the sequence number that separates two
// checks of one task on one day. Seq is zero where the identifier carries none.
type VerifierRef struct {
	Task string
	Day  string
	Seq  int
}

// VerifierID returns the identifier of one verifier health check. seq is
// written only when it is positive, for the same reason a run's is: the first
// check of a day needs no sequence number, and a zero would make two spellings
// of one document.
func VerifierID(task, day string, seq int) (string, error) {
	if !ValidTaskID(task) {
		return "", fmt.Errorf("%w: verifier task %q is not a task identifier (want %s)", ErrID, task, TaskIDPattern)
	}
	if _, err := time.Parse(DayLayout, day); err != nil {
		return "", fmt.Errorf("%w: verifier day %q is not a %s day", ErrID, day, DayLayout)
	}
	if seq < 0 {
		return "", fmt.Errorf("%w: verifier sequence %d is negative", ErrID, seq)
	}
	id := fmt.Sprintf("verifier/%s@%s", task, day)
	if seq > 0 {
		id += "-" + strconv.Itoa(seq)
	}
	if !ValidVerifierID(id) {
		return "", fmt.Errorf("%w: %q is not a verifier identifier (want %s)", ErrID, id, VerifierIDPattern)
	}
	return id, nil
}

// ParseVerifierID takes a verifier identifier apart. Like ParseRunID it accepts
// exactly what the pattern accepts, which is a shape rather than a calendar.
func ParseVerifierID(id string) (VerifierRef, error) {
	match := verifierShape.FindStringSubmatch(id)
	if match == nil {
		return VerifierRef{}, fmt.Errorf("%w: %q is not a verifier identifier (want %s)", ErrID, id, VerifierIDPattern)
	}
	ref := VerifierRef{Task: match[1], Day: match[2]}
	if match[3] != "" {
		seq, err := strconv.Atoi(match[3])
		if err != nil {
			return VerifierRef{}, fmt.Errorf("%w: %q: %w", ErrID, id, err)
		}
		ref.Seq = seq
	}
	return ref, nil
}

// ID rebuilds the identifier a verifier reference came from.
func (v VerifierRef) ID() (string, error) { return VerifierID(v.Task, v.Day, v.Seq) }

// CalibrationRef is a calibration identifier taken apart: the judge that was
// calibrated, the day its measurement window closed, and the sequence number
// that separates two calibrations of one judge on one day.
type CalibrationRef struct {
	Judge string
	Day   string
	Seq   int
}

// CalibrationID returns the identifier of one judge calibration. seq is
// written only when it is positive, for the reason a run's and a verifier's
// are: the first of a day needs no sequence number, and a zero would make two
// spellings of one document.
func CalibrationID(judge, day string, seq int) (string, error) {
	if !ValidModelSlug(judge) {
		return "", fmt.Errorf("%w: judge slug %q is not lowercase letters, digits and dots in hyphenated segments", ErrID, judge)
	}
	if _, err := time.Parse(DayLayout, day); err != nil {
		return "", fmt.Errorf("%w: calibration day %q is not a %s day", ErrID, day, DayLayout)
	}
	if seq < 0 {
		return "", fmt.Errorf("%w: calibration sequence %d is negative", ErrID, seq)
	}
	id := fmt.Sprintf("calibration/%s@%s", judge, day)
	if seq > 0 {
		id += "-" + strconv.Itoa(seq)
	}
	if !ValidCalibrationID(id) {
		return "", fmt.Errorf("%w: %q is not a calibration identifier (want %s)", ErrID, id, CalibrationIDPattern)
	}
	return id, nil
}

// ParseCalibrationID takes a calibration identifier apart. Like the other two
// parsers it accepts exactly what the pattern accepts, which is a shape rather
// than a calendar.
func ParseCalibrationID(id string) (CalibrationRef, error) {
	match := calibrationShape.FindStringSubmatch(id)
	if match == nil {
		return CalibrationRef{}, fmt.Errorf("%w: %q is not a calibration identifier (want %s)", ErrID, id, CalibrationIDPattern)
	}
	ref := CalibrationRef{Judge: match[1], Day: match[2]}
	if match[3] != "" {
		seq, err := strconv.Atoi(match[3])
		if err != nil {
			return CalibrationRef{}, fmt.Errorf("%w: %q: %w", ErrID, id, err)
		}
		ref.Seq = seq
	}
	return ref, nil
}

// ID rebuilds the identifier a calibration reference came from.
func (c CalibrationRef) ID() (string, error) { return CalibrationID(c.Judge, c.Day, c.Seq) }

// Filename returns the file one document is written to, without its directory.
// DocDag derives a kind's file name from the last segment of the identifier
// and ignores the filename template wherever the kind declares an `id:`
// pattern, so this is that rule and not a choice of ours.
func Filename(id string) string { return path.Base(id) + ".md" }

// Path returns the path one document is written to, relative to the vault
// root. It reports an error for an identifier the kind's shape does not
// accept, so a caller cannot write a run under the pattern directory.
func Path(k Kind, id string) (string, error) {
	dir, ok := Dir(k)
	if !ok {
		return "", fmt.Errorf("%w: unknown kind %q", ErrID, k)
	}
	valid := map[Kind]func(string) bool{
		KindEdit:        ValidEditID,
		KindPattern:     ValidPatternID,
		KindRun:         ValidRunID,
		KindVerifier:    ValidVerifierID,
		KindCalibration: ValidCalibrationID,
	}[k]
	if !valid(id) {
		return "", fmt.Errorf("%w: %q is not a %s identifier", ErrID, id, k)
	}
	return path.Join(dir, Filename(id)), nil
}

// WritesID reports whether a kind's documents have to write `id:` in
// frontmatter. A slash in the identifier is what forces it: DocDag reads an
// identifier off the file name's stem where the frontmatter writes none, and a
// stem can never carry a slash.
func WritesID(k Kind) bool { return k != KindEdit }
