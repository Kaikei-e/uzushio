package judge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// ReportSchemaVersion is the version of the report.json this package writes.
const ReportSchemaVersion = 1

// The two files a calibration leaves behind beside the vault document.
const (
	ReportFile = "report.json"
	ItemsFile  = "items.jsonl"
)

// Agreement is one coefficient with the tie handling it was computed under.
// The handling travels with the number rather than being stated once at the
// top, because a number quoted out of a report is quoted without the top.
type Agreement struct {
	TieHandling string `json:"tie_handling"`
	stats.Coefficients
}

// Swap is the judge against itself with the candidates in the other order.
type Swap struct {
	// Pairs is how many pairs were judged in both orders, Decided how many of
	// those the judge answered both times, and Flips how many of the decided
	// ones it answered differently.
	Pairs   int `json:"pairs"`
	Decided int `json:"decided"`
	Flips   int `json:"flips"`
	// FlipRate is Flips over Decided, with a score interval. It is published
	// beside the coefficient because it is the raw quantity a reader can
	// check, and because a flip rate and a kappa can move in opposite
	// directions when the marginals are lopsided.
	FlipRate float64        `json:"flip_rate"`
	FlipCI   stats.Interval `json:"flip_ci"`
	// Agreement is the coefficient over the pair's two slots and abstention.
	//
	// It is a consistency statistic. The two readings come from one model
	// under one prompt, so they are not independent, and non-independence
	// pushes observed agreement — and so kappa — up. It must not be reported
	// as a two-rater reliability.
	Agreement Agreement `json:"agreement"`
}

// Rerun is the judge against itself on another seed.
type Rerun struct {
	Seeds       int       `json:"seeds"`
	Comparisons int       `json:"comparisons"`
	Agreement   Agreement `json:"agreement"`
}

// Validity is the judge against people.
type Validity struct {
	Reference string `json:"reference"`
	// Primary keeps every item and scores abstention as a category of its
	// own; Secondary drops the items either side abstained on. They are
	// different estimands rather than one number computed two ways, which is
	// why both are reported and both carry the handling's name.
	Primary   Agreement `json:"primary"`
	Secondary Agreement `json:"secondary"`
}

// Outcomes is what the judge did, as distinct from how well it did it.
type Outcomes struct {
	Total               int            `json:"total"`
	ByKind              map[string]int `json:"by_kind"`
	NoCandidateByReason map[string]int `json:"no_candidate_by_reason"`
	NoCandidateRate     float64        `json:"no_candidate_rate"`
	Calls               int            `json:"calls"`
	InvalidRetries      int            `json:"invalid_output_retries"`
	InvalidRetryRate    float64        `json:"invalid_output_retry_rate"`
}

// Latency is what the runs cost.
type Latency struct {
	MedianMS int64 `json:"median_ms"`
	MinMS    int64 `json:"min_ms"`
	MaxMS    int64 `json:"max_ms"`
	TotalMS  int64 `json:"total_ms"`
}

// Report is everything a calibration measured.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Suite         string `json:"suite"`
	Source        string `json:"source,omitempty"`
	License       string `json:"license,omitempty"`
	Judge         string `json:"judge"`
	Pool          string `json:"pool"`
	Day           string `json:"day"`
	Items         int    `json:"items"`
	Seeds         int    `json:"seeds"`
	// AllBadLabels is how many labels said none of the three was worth
	// choosing. It folds into abstention for the coefficient and is counted
	// here, because a corpus with many of them says something about the
	// candidates rather than about the judge.
	AllBadLabels int            `json:"all_bad_labels"`
	Alpha        float64        `json:"alpha"`
	MinKappa     float64        `json:"min_kappa"`
	Strata       map[string]int `json:"strata"`

	Swap       Swap                `json:"swap"`
	Rerun      Rerun               `json:"rerun"`
	Validity   map[string]Validity `json:"validity"`
	HumanHuman *Agreement          `json:"human_human,omitempty"`
	Outcomes   Outcomes            `json:"outcomes"`
	Latency    Latency             `json:"latency"`

	// HumanKappa and NHuman are what the document carries, lifted out of the
	// validity block so that the one number the verdict rests on is not
	// something a reader has to go looking for.
	HumanKappa stats.Coefficient `json:"human_kappa"`
	NHuman     int               `json:"n_human"`
	Verdict    vocab.Calibrated  `json:"verdict"`
}

// Write materialises the report and the journal under dir.
func (r Result) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	body, err := json.MarshalIndent(r.Report, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	if err := writeFile(filepath.Join(dir, ReportFile), append(body, '\n')); err != nil {
		return err
	}
	var journal bytes.Buffer
	for _, item := range r.Items {
		line, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrJudge, err)
		}
		journal.Write(line)
		journal.WriteByte('\n')
	}
	return writeFile(filepath.Join(dir, ItemsFile), journal.Bytes())
}

func writeFile(name string, body []byte) error {
	if err := os.WriteFile(name, body, 0o644); err != nil { //nolint:gosec // a report is world-readable on purpose
		return fmt.Errorf("%w: %w", ErrJudge, err)
	}
	return nil
}

// Document turns a report into the vault record of it. reportPath is where the
// report was written, relative to the vault root.
func (r Report) Document(windowFrom, reportPath string, seq int) (doc.Calibration, error) {
	kappa := func(c stats.Coefficient) float64 {
		if !c.Defined() {
			return doc.KappaUnmeasured
		}
		return c.Float()
	}
	human := r.Validity[ReferenceHuman]
	calibration := doc.Calibration{
		Judge:       r.Judge,
		Day:         r.Day,
		Seq:         seq,
		Title:       fmt.Sprintf("%s judged by %s: %s", r.Suite, r.Judge, r.Verdict),
		Date:        r.Day,
		Pool:        r.Pool,
		WindowFrom:  windowFrom,
		WindowTo:    r.Day,
		NItems:      r.Items,
		TieHandling: vocab.TieAbstainAsCategory,
		SwapKappa:   kappa(r.Swap.Agreement.Kappa),
		RerunKappa:  kappa(r.Rerun.Agreement.Kappa),
		HumanKappa:  kappa(human.Primary.Kappa),
		NHuman:      human.Primary.N,
		Verdict:     r.Verdict,
		Report:      reportPath,
		Body:        r.Summary(),
	}
	if err := calibration.Validate(); err != nil {
		return doc.Calibration{}, err
	}
	return calibration, nil
}

// Summary is the human-readable body of the document: every number with the
// tie handling it was computed under, and the three claims kept apart.
func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Judge `%s` over suite `%s`, %d item(s) at %d seed(s) each.\n",
		r.Judge, r.Suite, r.Items, r.Seeds)
	if r.Source != "" {
		fmt.Fprintf(&b, "Candidates: %s, from %s (%s).\n", r.Pool, r.Source, r.License)
	}
	b.WriteString("\n## Consistency, which is not validity\n\n")
	fmt.Fprintf(&b, "- swap: %d pair(s) judged both ways, %d decided both times, %d flipped — "+
		"flip rate %.3f, %.0f%% CI [%.3f, %.3f]. kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %.3f, n %d).\n",
		r.Swap.Pairs, r.Swap.Decided, r.Swap.Flips, r.Swap.FlipRate, 100*(1-r.Alpha),
		r.Swap.FlipCI.Lo, r.Swap.FlipCI.Hi,
		coefficient(r.Swap.Agreement.Kappa), r.Swap.Agreement.TieHandling,
		r.Swap.Agreement.PO, r.Swap.Agreement.PE, r.Swap.Agreement.PABAK, r.Swap.Agreement.N)
	fmt.Fprintf(&b, "- re-run: %d comparison(s) across %d seed(s). kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %.3f).\n",
		r.Rerun.Comparisons, r.Rerun.Seeds, coefficient(r.Rerun.Agreement.Kappa),
		r.Rerun.Agreement.TieHandling, r.Rerun.Agreement.PO, r.Rerun.Agreement.PE,
		r.Rerun.Agreement.PABAK)
	b.WriteString("\nBoth are the judge against itself. The two readings are not independent,\n")
	b.WriteString("which pushes observed agreement up, so neither is a two-rater reliability\n")
	b.WriteString("and neither is evidence that the judge is measuring the right thing.\n")

	b.WriteString("\n## Validity, which is the judge against people\n\n")
	for _, reference := range []string{ReferenceHuman, ReferenceGold, ReferenceLabels} {
		v, ok := r.Validity[reference]
		if !ok || v.Primary.N == 0 {
			continue
		}
		fmt.Fprintf(&b, "- against `%s`: kappa %s under `%s` over %d item(s) "+
			"(p_o %.3f, p_e %.3f, PABAK %.3f, jackknife CI [%.3f, %.3f]); "+
			"kappa %s under `%s` over %d item(s).\n",
			reference, coefficient(v.Primary.Kappa), v.Primary.TieHandling, v.Primary.N,
			v.Primary.PO, v.Primary.PE, v.Primary.PABAK, v.Primary.CI.Lo, v.Primary.CI.Hi,
			coefficient(v.Secondary.Kappa), v.Secondary.TieHandling, v.Secondary.N)
	}
	if r.HumanHuman != nil {
		fmt.Fprintf(&b, "- ceiling: two people agree at kappa %s under `%s` over %d shared item(s). "+
			"A judge is read against this, not against 1.0.\n",
			coefficient(r.HumanHuman.Kappa), r.HumanHuman.TieHandling, r.HumanHuman.N)
	} else {
		b.WriteString("- ceiling: not measured. No two people labelled the same item, so how far\n" +
			"  the labels themselves agree is unknown, and the coefficients above have no\n" +
			"  upper bound to be read against.\n")
	}
	if r.AllBadLabels > 0 {
		fmt.Fprintf(&b, "- %d label(s) said none of the three was worth choosing. "+
			"They count as abstentions in the coefficients above.\n", r.AllBadLabels)
	}

	b.WriteString("\n## What the judge did\n\n")
	for _, kind := range slices.Sorted(keysOf(r.Outcomes.ByKind)) {
		fmt.Fprintf(&b, "- `%s`: %d of %d run(s)\n", kind, r.Outcomes.ByKind[kind], r.Outcomes.Total)
	}
	for _, reason := range slices.Sorted(keysOf(r.Outcomes.NoCandidateByReason)) {
		fmt.Fprintf(&b, "  - no candidate, `%s`: %d\n", reason, r.Outcomes.NoCandidateByReason[reason])
	}
	fmt.Fprintf(&b, "- %d judge call(s), %d retried for an unreadable answer (%.3f).\n",
		r.Outcomes.Calls, r.Outcomes.InvalidRetries, r.Outcomes.InvalidRetryRate)
	fmt.Fprintf(&b, "- median run %d ms, longest %d ms.\n", r.Latency.MedianMS, r.Latency.MaxMS)
	if len(r.Strata) > 0 {
		var bands []string
		for _, name := range slices.Sorted(keysOf(r.Strata)) {
			bands = append(bands, fmt.Sprintf("%s %d", name, r.Strata[name]))
		}
		fmt.Fprintf(&b, "- margin strata: %s. Kappa is sensitive to the mix, so it is recorded.\n",
			strings.Join(bands, ", "))
	}

	fmt.Fprintf(&b, "\n## Verdict: %s\n\n", r.Verdict)
	switch r.Verdict {
	case vocab.CalibratedUnmeasured:
		b.WriteString("Nobody has compared this judge with people on this suite, so its\n" +
			"reliability is all that was measured, and reliability is not validity.\n")
	case vocab.CalibratedYes:
		fmt.Fprintf(&b, "Agreement with people reached the %.3f threshold under `%s`.\n",
			r.MinKappa, vocab.TieAbstainAsCategory)
	case vocab.CalibratedNo:
		fmt.Fprintf(&b, "Agreement with people did not reach the %.3f threshold under `%s`.\n",
			r.MinKappa, vocab.TieAbstainAsCategory)
	}
	fmt.Fprintf(&b, "\nThis record carries force for %d days after the window closes, and then\n"+
		"stops on its own. A judge running on an expired calibration is a judge nobody\n"+
		"has checked lately; `uzushio judge status` is where that shows up.\n", doc.ValidityDays)
	return b.String()
}

// coefficient renders a coefficient for prose, naming the undefined one.
func coefficient(c stats.Coefficient) string {
	if !c.Defined() {
		return "`" + vocab.KappaUnmeasured + "`"
	}
	return fmt.Sprintf("%.3f", c.Float())
}

// keysOf yields a map's keys for slices.Sorted, which is the one place this
// package reads one.
func keysOf(m map[string]int) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}
