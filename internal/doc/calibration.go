package doc

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Calibration is one measurement of one judge: how often it gives the same
// answer when the two candidates change places, how often it gives the same
// answer on a second seed, and how far it agrees with the human labels. It is
// written by `uzushio judge calibrate` and never by a person, which is why it
// has no status — there is nothing about a measurement to accept or withdraw.
//
// Its identity is its parts — the judge, the day the measurement window
// closed, and a sequence number where one judge was calibrated twice in a day.
// It holds a slash, so a calibration always writes `id:`.
//
// The three coefficients are held as the numbers they are and written as
// strings, for the reason a verifier's kill rate is: a scalar field is
// compared as text, and a coefficient that reads back as 0.4300000000000001
// because it went through a float is a value nobody can match. Report names
// the report.json the calibration wrote, which is where a machine reads the
// numbers, the marginals and the intervals from.
type Calibration struct {
	// Judge is the model slug of the judge that was measured.
	Judge string
	// Day is the day the measurement window closed, as YYYY-MM-DD. It is part
	// of the identifier.
	Day string
	// Seq separates two calibrations of one judge on one day. Zero is written
	// as no sequence number at all.
	Seq int
	// Title is the heading and the frontmatter title.
	Title string
	// Date is the day the document was written.
	Date string
	// Pool names the proposers whose answers were judged, or PoolExternal
	// where the candidates came from files rather than from a fleet.
	Pool string
	// WindowFrom and WindowTo are the first and last day of the measurement
	// window.
	WindowFrom string
	WindowTo   string
	// NItems is how many items the reliability numbers were measured over.
	NItems int
	// TieHandling names what was done with the answers that are not a choice.
	// Every number in the document was computed under it, which is why it is
	// one field on the document rather than a column in the report.
	TieHandling vocab.TieHandling
	// SwapKappa, RerunKappa and HumanKappa are the three coefficients, kept
	// apart because they are three different claims. KappaUnmeasured writes
	// the word instead of a number.
	SwapKappa  float64
	RerunKappa float64
	HumanKappa float64
	// NHuman is how many items carried a human label.
	NHuman int
	// Verdict is what the calibration concluded about the judge.
	Verdict vocab.Calibrated
	// RescoredFrom names the calibration directory whose recorded judge
	// calls this one re-aggregated, empty where the judge was asked. A
	// rescoring is a measurement of the rule over answers already given, and
	// nothing else in the frontmatter says so.
	RescoredFrom string
	// Report is the path to the report.json the calibration wrote, relative
	// to the vault root. It is required: the three coefficients cannot be read
	// without the marginals and the intervals beside them, and those are in
	// the report (UZ-C-009).
	Report string
	// Supersedes is the calibration this one replaces: the last measurement of
	// the same judge that was still binding when this one was made. The old
	// document is not touched — it is append-only history — and what retires
	// it is the edge plus the projection that reads it, so a judge that has
	// just been measured as uncalibrated stops being a judge the vault says is
	// calibrated.
	Supersedes []Supersession
	// Body is the Markdown under the heading.
	Body string
}

// PoolExternal is the Pool of a calibration whose candidates were read from
// files rather than produced by a proposer fleet — which is what a derived
// corpus of somebody else's model outputs is.
const PoolExternal = "external"

// KappaUnmeasured is the value of a coefficient that was not measured. Every
// real kappa is in −1..1, so a value outside that range cannot be mistaken for
// one, and the distinction it preserves is the one that matters: a judge whose
// agreement with people came out at zero has been checked and disagrees, and a
// judge whose agreement is `unmeasured` has not been checked at all.
const KappaUnmeasured float64 = -2

// ValidityDays is how many days a calibration carries force for after its
// window closes. It binds through window_to + ValidityDays, inclusive.
//
// Thirty days is not a property of any judge. It is the interval at which the
// measurement has to be repeated for the word `calibrated` to keep meaning
// anything, and it is spelled as a period so that the graph drops the document
// out of `binding` on its own rather than waiting for somebody to notice. The
// industry habit the research reports — re-score a gold set monthly, replace
// part of it quarterly — is the same interval arrived at by practice; the
// mechanism here is that nobody has to remember it.
const ValidityDays = 30

// CalibrationFrontmatter is a calibration's frontmatter in the order it is
// written.
type CalibrationFrontmatter struct {
	ID           string            `yaml:"id"`
	Kind         string            `yaml:"kind"`
	Title        string            `yaml:"title"`
	Date         string            `yaml:"date"`
	Judge        string            `yaml:"judge"`
	Pool         string            `yaml:"pool"`
	WindowFrom   string            `yaml:"window_from"`
	WindowTo     string            `yaml:"window_to"`
	InForceUntil string            `yaml:"in_force_until"`
	NItems       string            `yaml:"n_items"`
	TieHandling  string            `yaml:"tie_handling"`
	SwapKappa    string            `yaml:"swap_kappa"`
	RerunKappa   string            `yaml:"rerun_kappa"`
	HumanKappa   string            `yaml:"human_kappa"`
	NHuman       string            `yaml:"n_human"`
	Verdict      string            `yaml:"verdict"`
	Report       string            `yaml:"report"`
	RescoredFrom string            `yaml:"rescored_from,omitempty"`
	Supersedes   []supersedesEntry `yaml:"supersedes,omitempty"`
}

// ID returns the calibration's identifier, or the empty string where the parts
// do not make one. Validate says which part is wrong.
func (c Calibration) ID() string {
	id, err := vocab.CalibrationID(c.Judge, c.Day, c.Seq)
	if err != nil {
		return ""
	}
	return id
}

// Kind returns the kind a judge calibration answers to.
func (c Calibration) Kind() vocab.Kind { return vocab.KindCalibration }

// Path returns where the calibration is written, relative to the vault root.
func (c Calibration) Path() (string, error) {
	id, err := vocab.CalibrationID(c.Judge, c.Day, c.Seq)
	if err != nil {
		return "", err
	}
	return vocab.Path(vocab.KindCalibration, id)
}

// LastDay is the last day the calibration is in force: ValidityDays after the
// window closed, inclusive. It is derived rather than carried so that no
// document can claim a longer life than the measurement behind it earns.
func (c Calibration) LastDay() (string, error) {
	closed, err := time.Parse(vocab.DayLayout, c.WindowTo)
	if err != nil {
		return "", fmt.Errorf("%w: calibration window_to %q is not a %s day",
			ErrDocument, c.WindowTo, vocab.DayLayout)
	}
	return closed.AddDate(0, 0, ValidityDays).Format(vocab.DayLayout), nil
}

// InForceUntil is the day the frontmatter writes, which is one day past
// LastDay.
//
// DocDag reads `period.until` as **exclusive**: a document with
// `in_force_until: 2026-10-06` binds on the fifth and not on the sixth. So
// writing window_to + 30 would give twenty-nine days of force under a clause,
// a README and a warning that all say thirty, and the boundary day would be
// the one where `judge status` printed "in force until" a day the document was
// no longer in force on. The off-by-one is the engine's convention, not a
// choice, and it is handled here — once — rather than in each reader.
func (c Calibration) InForceUntil() (string, error) {
	last, err := c.LastDay()
	if err != nil {
		return "", err
	}
	day, err := time.Parse(vocab.DayLayout, last)
	if err != nil {
		return "", err
	}
	return day.AddDate(0, 0, 1).Format(vocab.DayLayout), nil
}

// Validate reports the first thing about the calibration DocDag would refuse.
func (c Calibration) Validate() error {
	id, err := vocab.CalibrationID(c.Judge, c.Day, c.Seq)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDocument, err)
	}
	where := "calibration " + id + " "
	if err := requireText(where+"title", c.Title); err != nil {
		return err
	}
	if err := requireDay(where+"date", c.Date); err != nil {
		return err
	}
	if err := requireText(where+"pool", c.Pool); err != nil {
		return err
	}
	if err := requireDay(where+"window_from", c.WindowFrom); err != nil {
		return err
	}
	if err := requireDay(where+"window_to", c.WindowTo); err != nil {
		return err
	}
	if c.WindowTo < c.WindowFrom {
		return fmt.Errorf("%w: %swindow closes on %s, before it opens on %s",
			ErrDocument, where, c.WindowTo, c.WindowFrom)
	}
	if err := requireVocabulary(where+"tie_handling", c.TieHandling, vocab.AllTieHandlings()); err != nil {
		return err
	}
	if err := requireVocabulary(where+"verdict", c.Verdict, vocab.AllCalibrateds()); err != nil {
		return err
	}
	for _, kappa := range []struct {
		what  string
		value float64
	}{
		{"swap_kappa", c.SwapKappa},
		{"rerun_kappa", c.RerunKappa},
		{"human_kappa", c.HumanKappa},
	} {
		if kappa.value != KappaUnmeasured && (kappa.value < -1 || kappa.value > 1) {
			return fmt.Errorf("%w: %s%s %v is outside -1..1", ErrDocument, where, kappa.what, kappa.value)
		}
	}
	// A judge called calibrated on nothing is the failure this whole kind
	// exists to stop: `calibrated` is a claim about agreement with people, and
	// there is no such agreement to report where nobody labelled anything.
	if c.Verdict != vocab.CalibratedUnmeasured && c.HumanKappa == KappaUnmeasured {
		return fmt.Errorf("%w: %sis %q with human_kappa unmeasured; a judge nobody has compared with people is %q",
			ErrDocument, where, c.Verdict, vocab.CalibratedUnmeasured)
	}
	if c.NItems < 1 {
		return fmt.Errorf("%w: %sn_items %d is not positive", ErrDocument, where, c.NItems)
	}
	if c.NHuman < 0 {
		return fmt.Errorf("%w: %sn_human %d is negative", ErrDocument, where, c.NHuman)
	}
	// UZ-C-009: the coefficients are unreadable without the marginals and the
	// intervals, and those are in the report. A document without one is a
	// verdict with its evidence deleted.
	if err := requireText(where+"report", c.Report); err != nil {
		return err
	}
	for _, superseded := range c.Supersedes {
		if !vocab.ValidCalibrationID(superseded.Edit) {
			return fmt.Errorf("%w: %ssupersedes %q, which is not a calibration identifier",
				ErrDocument, where, superseded.Edit)
		}
		if err := requireText(where+"supersedes reason", superseded.Reason); err != nil {
			return err
		}
	}
	return nil
}

// Frontmatter returns the calibration's frontmatter.
func (c Calibration) Frontmatter() (CalibrationFrontmatter, error) {
	if err := c.Validate(); err != nil {
		return CalibrationFrontmatter{}, err
	}
	until, err := c.InForceUntil()
	if err != nil {
		return CalibrationFrontmatter{}, err
	}
	return CalibrationFrontmatter{
		ID:           c.ID(),
		Kind:         vocab.KindCalibration.String(),
		Title:        c.Title,
		Date:         c.Date,
		Judge:        c.Judge,
		Pool:         c.Pool,
		WindowFrom:   c.WindowFrom,
		WindowTo:     c.WindowTo,
		InForceUntil: until,
		NItems:       strconv.Itoa(c.NItems),
		TieHandling:  c.TieHandling.String(),
		SwapKappa:    Kappa(c.SwapKappa),
		RerunKappa:   Kappa(c.RerunKappa),
		HumanKappa:   Kappa(c.HumanKappa),
		NHuman:       strconv.Itoa(c.NHuman),
		Verdict:      c.Verdict.String(),
		Report:       c.Report,
		RescoredFrom: c.RescoredFrom,
		Supersedes:   supersedesEntries(c.Supersedes),
	}, nil
}

// Kappa formats a coefficient the way a document and a summary both write it:
// three decimal places, or the word for the one that was never computed.
func Kappa(value float64) string {
	if value == KappaUnmeasured {
		return vocab.KappaUnmeasured
	}
	return strconv.FormatFloat(value, 'f', 3, 64)
}

// Bytes returns the document as it is written to disk.
func (c Calibration) Bytes() ([]byte, error) {
	front, err := c.Frontmatter()
	if err != nil {
		return nil, err
	}
	return render(front, c.Title, c.Body)
}
