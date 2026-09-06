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
	// CIMethod names how the interval was made and Level is its coverage.
	// They travel with the number for the same reason the handling does: a
	// width is only readable next to what produced it, and an interval
	// computed over rows that share an item is a different claim from one
	// computed over independent rows.
	CIMethod string  `json:"ci_method"`
	Level    float64 `json:"level"`
	// Labelers names the two people a ceiling was measured between, where the
	// agreement is a human-human one.
	Labelers []string `json:"labelers,omitempty"`
	stats.Coefficients
}

// Swap is the judge against itself with the candidates in the other order.
//
// Every run of every item contributes its three pairs, so a calibration at
// three seeds over two hundred items reads eighteen hundred pairs rather than
// six hundred. That is deliberate: position consistency is a property of the
// judge under one prompt, and a second seed is another reading of it, not a
// different question.
type Swap struct {
	// Pairs is how many pairs were judged in both orders, over every seed;
	// Decided how many of those the judge answered both times, and Flips how
	// many of the decided ones it answered differently.
	Pairs   int `json:"pairs"`
	Decided int `json:"decided"`
	Flips   int `json:"flips"`
	// Rate is Flips over Decided with a leave-one-item-out interval — not a
	// score interval, because the rows share items. It is published beside the
	// coefficient because it is the raw quantity a reader can check, and
	// because a flip rate and a kappa can move in opposite directions when the
	// marginals are lopsided. It is a `decided-only` quantity: a pair either
	// side abstained on is in neither the numerator nor the denominator.
	Rate stats.Proportion `json:"flip_rate"`
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
	// FromGold and FromLabels say which labeler set each item's human side
	// came from. For the `human` reference the two are a mixture, and a
	// coefficient over a mixture has to say what of.
	FromGold   int `json:"from_gold"`
	FromLabels int `json:"from_labels"`
}

// Outcomes is what the judge did, as distinct from how well it did it.
type Outcomes struct {
	Total  int            `json:"total"`
	ByKind map[string]int `json:"by_kind"`
	// Unmeasured is how many runs produced no judgement — a timeout, a failed
	// judge, a harness that would not run. They are in no coefficient.
	Unmeasured          int            `json:"unmeasured_runs"`
	NoCandidateByReason map[string]int `json:"no_candidate_by_reason"`
	// NoCandidateRate is over measured items at the first seed, which is one
	// row per item and so the one rate in this report whose rows really are
	// independent. It is the only one with a score interval.
	NoCandidateRate  stats.Proportion `json:"no_candidate_rate"`
	Calls            int              `json:"calls"`
	InvalidRetries   int              `json:"invalid_output_retries"`
	InvalidRetryRate float64          `json:"invalid_output_retry_rate"`
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
	// Unmeasured is how many items produced no usable judgement at all, and
	// UnmeasuredRate the share of the suite they are. Over MaxUnmeasured the
	// calibration reaches no verdict: the items a failing fleet drops are not
	// a random sample of the suite.
	Unmeasured           int     `json:"unmeasured_items"`
	UnmeasuredRate       float64 `json:"unmeasured_rate"`
	MaxUnmeasured        float64 `json:"max_unmeasured"`
	OverUnmeasuredBudget bool    `json:"over_unmeasured_budget"`
	// AllBadLabels is how many labels said none of the three was worth
	// choosing. It folds into abstention for the coefficient and is counted
	// here, because a corpus with many of them says something about the
	// candidates rather than about the judge.
	AllBadLabels int            `json:"all_bad_labels"`
	Alpha        float64        `json:"alpha"`
	Level        float64        `json:"level"`
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
		"flip rate %.3f under `%s`, %s. kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %s, %d row(s) over %d item(s)), %s.\n",
		r.Swap.Pairs, r.Swap.Decided, r.Swap.Flips, r.Swap.Rate.Value,
		vocab.TieDecidedOnly, interval(r.Swap.Rate.CI, r.Swap.Rate.Level, r.Swap.Rate.Method),
		coefficient(r.Swap.Agreement.Kappa), r.Swap.Agreement.TieHandling,
		r.Swap.Agreement.PO, r.Swap.Agreement.PE, coefficient(r.Swap.Agreement.PABAK),
		r.Swap.Agreement.N, r.Swap.Agreement.Clusters,
		interval(r.Swap.Agreement.CI, r.Swap.Agreement.Level, r.Swap.Agreement.CIMethod))
	fmt.Fprintf(&b, "- re-run: %d comparison(s) across %d seed(s). kappa %s under `%s` "+
		"(p_o %.3f, p_e %.3f, PABAK %s), %s.\n",
		r.Rerun.Comparisons, r.Rerun.Seeds, coefficient(r.Rerun.Agreement.Kappa),
		r.Rerun.Agreement.TieHandling, r.Rerun.Agreement.PO, r.Rerun.Agreement.PE,
		coefficient(r.Rerun.Agreement.PABAK),
		interval(r.Rerun.Agreement.CI, r.Rerun.Agreement.Level, r.Rerun.Agreement.CIMethod))
	b.WriteString("\nBoth are the judge against itself. The two readings are not independent,\n")
	b.WriteString("which pushes observed agreement up, so neither is a two-rater reliability\n")
	b.WriteString("and neither is evidence that the judge is measuring the right thing.\n")
	b.WriteString("\nThe re-run arm moves no candidate. Both orders of every pair are asked at\n")
	b.WriteString("every seed, so a second seed reorders nothing; what it moves is the nonce\n")
	b.WriteString("inside the candidate fences. The re-run coefficient is therefore the same\n")
	b.WriteString("decision under an irrelevant-token perturbation, together with whatever the\n")
	b.WriteString("server does differently at temperature 0, and it does not separate the two.\n")

	b.WriteString("\n## Validity, which is the judge against people\n\n")
	for _, reference := range []string{ReferenceHuman, ReferenceGold, ReferenceLabels} {
		v, ok := r.Validity[reference]
		if !ok || v.Primary.N == 0 {
			continue
		}
		fmt.Fprintf(&b, "- against `%s` (%s): kappa %s under `%s` over %d item(s) "+
			"(p_o %.3f, p_e %.3f, PABAK %s, %s); "+
			"kappa %s under `%s` over %d item(s).\n",
			reference, labelerSet(v), coefficient(v.Primary.Kappa), v.Primary.TieHandling,
			v.Primary.N, v.Primary.PO, v.Primary.PE, coefficient(v.Primary.PABAK),
			interval(v.Primary.CI, v.Primary.Level, v.Primary.CIMethod),
			coefficient(v.Secondary.Kappa), v.Secondary.TieHandling, v.Secondary.N)
	}
	if r.HumanHuman != nil {
		fmt.Fprintf(&b, "- ceiling: %s agree at kappa %s under `%s` over %d shared item(s). "+
			"A judge is read against this, not against 1.0.\n",
			strings.Join(r.HumanHuman.Labelers, " and "), coefficient(r.HumanHuman.Kappa),
			r.HumanHuman.TieHandling, r.HumanHuman.N)
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
	fmt.Fprintf(&b, "  - no candidate, over measured items: %.3f, %s\n",
		r.Outcomes.NoCandidateRate.Value,
		interval(r.Outcomes.NoCandidateRate.CI, r.Outcomes.NoCandidateRate.Level,
			r.Outcomes.NoCandidateRate.Method))
	fmt.Fprintf(&b, "- %d judge call(s), %d retried for an unreadable answer (%.3f).\n",
		r.Outcomes.Calls, r.Outcomes.InvalidRetries, r.Outcomes.InvalidRetryRate)
	fmt.Fprintf(&b, "- %d run(s) and %d item(s) measured nothing — a timeout, a failed judge or a "+
		"harness that would not run. They are in no coefficient above: %.1f%% of the suite, "+
		"against a %.1f%% budget.\n",
		r.Outcomes.Unmeasured, r.Unmeasured, 100*r.UnmeasuredRate, 100*r.MaxUnmeasured)
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
	switch {
	case r.OverUnmeasuredBudget:
		fmt.Fprintf(&b, "%.1f%% of the suite produced no judgement at all, over the %.1f%% this\n"+
			"calibration will stand behind. The numbers above are what was left, and what a\n"+
			"failing harness drops is not a random sample of a suite, so they are reported\n"+
			"rather than concluded from.\n", 100*r.UnmeasuredRate, 100*r.MaxUnmeasured)
	case r.Verdict == vocab.CalibratedUnmeasured:
		b.WriteString("Nobody has compared this judge with people on this suite, so its\n" +
			"reliability is all that was measured, and reliability is not validity.\n")
	case r.Verdict == vocab.CalibratedYes:
		fmt.Fprintf(&b, "Agreement with people reached the %.3f threshold under `%s`.\n",
			r.MinKappa, vocab.TieAbstainAsCategory)
	case r.Verdict == vocab.CalibratedNo:
		fmt.Fprintf(&b, "Agreement with people did not reach the %.3f threshold under `%s`.\n",
			r.MinKappa, vocab.TieAbstainAsCategory)
	}
	fmt.Fprintf(&b, "\nThis record carries force for %d days after the window closes, and then\n"+
		"stops on its own. A judge running on an expired calibration is a judge nobody\n"+
		"has checked lately; `uzushio judge status` is where that shows up.\n", doc.ValidityDays)
	return b.String()
}

// labelerSet says where a validity coefficient's human side came from, which
// for the mixed reference is two answers rather than one.
func labelerSet(v Validity) string {
	switch {
	case v.FromGold > 0 && v.FromLabels > 0:
		return fmt.Sprintf("%d from the corpus's annotators, %d from a labels file",
			v.FromGold, v.FromLabels)
	case v.FromLabels > 0:
		return "a labels file"
	case v.FromGold > 0:
		return "the corpus's own annotators"
	}
	return "nothing"
}

// interval renders an interval for prose, naming its level and how it was
// made, and saying plainly where there is none. A width printed without those
// is a number a reader will take on trust.
func interval(ci *stats.Interval, level float64, method string) string {
	if ci == nil {
		return "no interval (too few independent units to resample)"
	}
	return fmt.Sprintf("%.0f%% %s CI [%.3f, %.3f]", 100*level, method, ci.Lo, ci.Hi)
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
