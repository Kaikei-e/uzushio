package doc

import (
	"fmt"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Run is one evaluation of one edit on one split. It is written by the harness
// and never by a person, which is why it has no status: there is nothing about
// a measurement to accept or withdraw.
//
// Its identity is its parts — the edit it measured, the day, the model, the
// split, and a sequence number where one edit was measured twice the same day
// on the same model and split — so the identifier is derived rather than
// carried. The identifier holds a slash, so a run always writes `id:`.
type Run struct {
	// Edit is the identifier of the edit the run measured.
	Edit string
	// Day is the day the run was made, as YYYY-MM-DD.
	Day string
	// ModelSlug names the model as the identifier spells it.
	ModelSlug string
	// Split is the half of the suite the run measured.
	Split vocab.Split
	// Seq separates two runs that agree on everything else. Zero is written as
	// no sequence number at all: the first run of a day needs none, and a zero
	// would make two spellings of one run.
	Seq int
	// Title is the heading and the frontmatter title.
	Title string
	// Date is the day the document was written. It is the day the run was made
	// unless the record was written later.
	Date string
	// Verdict is what the run concluded about the edit.
	Verdict vocab.Verdict
	// Suite names the task set the split is defined over.
	Suite string
	// Trials is how many attempts the rate was measured over.
	Trials int
	// Trace is the path to the recorded transcript.
	Trace string
	// Validates is what the run measured, per edit. A run ordinarily validates
	// exactly the edit its identifier names; the field is a list because the
	// edge is one, and Validate holds every entry to the edit identifier shape.
	Validates []Validation
	// Body is the Markdown under the heading.
	Body string
}

// RunFrontmatter is a run's frontmatter in the order it is written.
type RunFrontmatter struct {
	ID        string           `yaml:"id"`
	Kind      string           `yaml:"kind"`
	Title     string           `yaml:"title"`
	Date      string           `yaml:"date"`
	Split     string           `yaml:"split"`
	Verdict   string           `yaml:"verdict"`
	Suite     string           `yaml:"suite"`
	Trials    int              `yaml:"trials,omitempty"`
	Trace     string           `yaml:"trace,omitempty"`
	Validates []validatesEntry `yaml:"validates,omitempty"`
}

// ID returns the run's identifier, or the empty string where the parts do not
// make one. Validate says which part is wrong.
func (r Run) ID() string {
	id, err := vocab.RunID(r.Edit, r.Day, r.ModelSlug, r.Split, r.Seq)
	if err != nil {
		return ""
	}
	return id
}

// Kind returns the kind a run answers to.
func (r Run) Kind() vocab.Kind { return vocab.KindRun }

// Path returns where the run is written, relative to the vault root.
func (r Run) Path() (string, error) {
	id, err := vocab.RunID(r.Edit, r.Day, r.ModelSlug, r.Split, r.Seq)
	if err != nil {
		return "", err
	}
	return vocab.Path(vocab.KindRun, id)
}

// Measuring returns the run with the validates edge that records what it
// measured about the edit it names. It is the ordinary case written once:
// a run's identifier already names an edit, and the edge says what the run
// found out about it.
func (r Run) Measuring(model string, passRate, baselinePassRate float64) Run {
	r.Validates = append(r.Validates, Validation{
		Edit:             r.Edit,
		Model:            model,
		PassRate:         passRate,
		BaselinePassRate: baselinePassRate,
	})
	return r
}

// Validate reports the first thing about the run DocDag would refuse.
func (r Run) Validate() error {
	id, err := vocab.RunID(r.Edit, r.Day, r.ModelSlug, r.Split, r.Seq)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if err := requireText("run "+id+" title", r.Title); err != nil {
		return err
	}
	if err := requireDay("run "+id+" date", r.Date); err != nil {
		return err
	}
	if err := requireVocabulary("run "+id+" verdict", r.Verdict, vocab.AllVerdicts()); err != nil {
		return err
	}
	if err := requireText("run "+id+" suite", r.Suite); err != nil {
		return err
	}
	if r.Trials < 0 {
		return fmt.Errorf("%w: run %s trials %d is negative", ErrDocument, id, r.Trials)
	}
	for _, validation := range r.Validates {
		if err := validation.validate(id); err != nil {
			return err
		}
	}
	return nil
}

// Frontmatter returns the run's frontmatter.
func (r Run) Frontmatter() (RunFrontmatter, error) {
	if err := r.Validate(); err != nil {
		return RunFrontmatter{}, err
	}
	front := RunFrontmatter{
		ID:      r.ID(),
		Kind:    vocab.KindRun.String(),
		Title:   r.Title,
		Date:    r.Date,
		Split:   r.Split.String(),
		Verdict: r.Verdict.String(),
		Suite:   r.Suite,
		Trials:  r.Trials,
		Trace:   r.Trace,
	}
	for _, validation := range r.Validates {
		front.Validates = append(front.Validates, validatesEntry{
			Ref:              validation.Edit,
			Model:            validation.Model,
			PassRate:         validation.PassRate,
			BaselinePassRate: validation.BaselinePassRate,
		})
	}
	return front, nil
}

// Bytes returns the document as it is written to disk.
func (r Run) Bytes() ([]byte, error) {
	front, err := r.Frontmatter()
	if err != nil {
		return nil, err
	}
	return render(front, r.Title, r.Body)
}

// validate holds one measurement to the edit it names and to the two rates the
// validates edge requires of every entry.
func (v Validation) validate(run string) error {
	if !vocab.ValidEditID(v.Edit) {
		return fmt.Errorf("%w: run %s validates %q, which is not an edit identifier (want %s)",
			ErrDocument, run, v.Edit, vocab.EditIDPattern)
	}
	if err := requireText("run "+run+" validates model", v.Model); err != nil {
		return err
	}
	for _, rate := range []struct {
		what  string
		value float64
	}{
		{"pass_rate", v.PassRate},
		{"baseline_pass_rate", v.BaselinePassRate},
	} {
		if rate.value < 0 || rate.value > 1 {
			return fmt.Errorf("%w: run %s validates %s: %s %v is outside 0..1",
				ErrDocument, run, v.Edit, rate.what, rate.value)
		}
	}
	return nil
}
