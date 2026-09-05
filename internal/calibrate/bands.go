package calibrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the version of the bands file this package writes.
const SchemaVersion = 1

// Statistic names the rule a band was derived under. It is written into the
// file so that a reader who disagrees with the numbers knows what to disagree
// with.
const Statistic = "normal-tolerance-interval"

// Coverage and Confidence are the p and γ of the tolerance interval: the share
// of the population a band covers, and how sure of that the sample makes us.
const (
	Coverage   = 0.95
	Confidence = 0.90
)

// Bands is the file `uzushio task calibrate` writes.
//
// It is JSON and it is generic. What a particular verifier wants its bands in —
// a TOML table per invariant, a CSV, a command line — is that task's business,
// and the task is where the adapter lives. This says only what was measured,
// what it came from, and under what rule; turning it into whatever the gate
// reads is a step the task owns, because the shape of that file is a fact about
// somebody else's project and not about calibration.
type Bands struct {
	SchemaVersion int    `json:"schema_version"`
	Task          string `json:"task"`
	Rev           string `json:"rev"`
	// SourceRuns are the doctor runs the measurements came from, in the order
	// they were given.
	SourceRuns []string `json:"source_runs"`
	// Day is when they were measured, or the span when they were not all
	// measured on one day.
	Day string `json:"day"`
	// N is the size of the calibration set.
	N int `json:"n"`
	// Rule is how a band was derived from it.
	Rule RuleJSON `json:"rule"`
	// Invariants is one entry per invariant the reference runs judged. It is a
	// map, so the file's key order is the sorted one whatever order the
	// verifier reported its rows in.
	Invariants map[string]InvariantJSON `json:"invariants"`
}

// RuleJSON is the rule, as the file records it.
type RuleJSON struct {
	Statistic string  `json:"statistic"`
	P         float64 `json:"p"`
	Gamma     float64 `json:"gamma"`
	// K2 is the tolerance factor actually applied at this N. It is written out
	// rather than left to be looked up, so the arithmetic in the file can be
	// checked without this program.
	K2 float64 `json:"k2"`
	// RelFloor is the share of the centre no half-width falls below. It is what
	// keeps a band from collapsing when an invariant reads identically on every
	// run and its name carries no unit.
	RelFloor float64    `json:"rel_floor"`
	Floors   FloorsJSON `json:"floors"`
	Lower    string     `json:"lower"`
}

// FloorsJSON is the absolute floor under a half-width, by the unit an
// invariant's name ends in.
type FloorsJSON struct {
	US float64 `json:"us"`
	MS float64 `json:"ms"`
}

// InvariantJSON is one band.
type InvariantJSON struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
	// Centre is the median of what the reference runs measured. It is present
	// for a kept band too: it is what the invariant reads on this host, which
	// is worth knowing even where the band was not moved.
	Centre float64 `json:"centre"`
	// HalfWidth is null for a kept band, because none was derived.
	HalfWidth *float64 `json:"half_width"`
	// ThreeCIHalf is three times the largest half-range the verifier itself
	// reported — the width a gate's own guidance usually asks for. It is
	// recorded for comparison and is not used. Null where the verifier
	// reported no half-range.
	ThreeCIHalf *float64 `json:"three_ci_half"`
	Kept        bool     `json:"kept"`
	Reason      string   `json:"reason"`
}

// JSON renders the calibration as the bands file.
func (r *Result) JSON() (*Bands, error) {
	k := r.Rule.K
	if k == 0 {
		factor, err := ToleranceK(r.N)
		if err != nil {
			return nil, err
		}
		k = factor
	}
	out := &Bands{
		SchemaVersion: SchemaVersion,
		Task:          r.Task,
		Rev:           r.Rev,
		Day:           r.Day(),
		N:             r.N,
		Rule: RuleJSON{
			Statistic: Statistic,
			P:         Coverage,
			Gamma:     Confidence,
			K2:        k,
			RelFloor:  r.Rule.RelFloor,
			Floors:    FloorsJSON{US: r.Rule.FloorUS, MS: r.Rule.FloorMS},
			Lower:     string(r.Rule.Lower),
		},
		Invariants: make(map[string]InvariantJSON, len(r.Derived)),
	}
	for _, provenance := range r.Reports {
		out.SourceRuns = append(out.SourceRuns, provenance.RunID)
	}
	for _, entry := range r.Derived {
		if entry.N != r.N {
			// `n` and `k2` are written once for the file, so they have to be
			// true of every band in it. absorb refuses the way this could
			// happen; this is the guard that says so if another one appears.
			return nil, fmt.Errorf(
				"%w: %s was measured %d times and the calibration set is %d runs; "+
					"one file cannot record one N for both",
				ErrCalibrate, entry.Invariant, entry.N, r.N)
		}
		band := InvariantJSON{
			Lo:     entry.Band.Lo,
			Hi:     entry.Band.Hi,
			Centre: round(entry.Centre),
			Kept:   entry.Copied(),
			Reason: entry.Reason(),
		}
		if !entry.Copied() {
			width := round(entry.HalfWidth)
			band.HalfWidth = &width
		}
		if len(entry.CIHalf) > 0 {
			guide := round(entry.CIGuide)
			band.ThreeCIHalf = &guide
		}
		out.Invariants[entry.Invariant] = band
	}
	return out, nil
}

// Reason says in one phrase what happened to a band, for the file and for a
// person reading a column of them.
func (d Derived) Reason() string {
	if d.Copied() {
		return "kept: " + string(d.Why)
	}
	return "derived: " + d.Binding
}

// Day is when the calibration set was measured: one day, or the span between
// the first and the last when it was gathered across more than one.
func (r *Result) Day() string {
	var days []string
	seen := map[string]bool{}
	for _, provenance := range r.Reports {
		if !seen[provenance.Day] {
			seen[provenance.Day] = true
			days = append(days, provenance.Day)
		}
	}
	sort.Strings(days)
	switch len(days) {
	case 0:
		return ""
	case 1:
		return days[0]
	}
	return days[0] + ".." + days[len(days)-1]
}

// Bytes renders the bands file: two-space indent, one trailing newline, and no
// HTML escaping. The key order is deterministic — the struct's for the outside,
// sorted for the invariants — so a re-calibration that changed nothing is an
// empty diff.
func (b *Bands) Bytes() ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return nil, fmt.Errorf("%w: encode the bands: %w", ErrCalibrate, err)
	}
	return out.Bytes(), nil
}

// ReadBands decodes a bands file.
func ReadBands(body []byte) (*Bands, error) {
	var bands Bands
	if err := json.Unmarshal(body, &bands); err != nil {
		return nil, fmt.Errorf("%w: decode the bands: %w", ErrCalibrate, err)
	}
	if bands.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: the bands are schema_version %d; this build reads %d",
			ErrCalibrate, bands.SchemaVersion, SchemaVersion)
	}
	return &bands, nil
}

// Names is the invariants a bands file holds, sorted.
func (b *Bands) Names() []string {
	names := make([]string, 0, len(b.Invariants))
	for name := range b.Invariants {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// round holds a written statistic to the precision a band edge is written at.
// A centre carried to seventeen digits is a number that looks measured to
// seventeen digits.
func round(value float64) float64 {
	parsed, err := json.Number(strings.TrimSpace(fmt.Sprintf("%.*f", Places, value))).Float64()
	if err != nil {
		return value
	}
	return parsed
}
