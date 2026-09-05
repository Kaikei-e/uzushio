package calibrate

import (
	"fmt"
	"sort"
	"strings"
)

// Generator names the command that wrote a bands file.
const Generator = "uzushio task calibrate"

// Table is the derived bands beside the original ones, for a person reading
// stderr. It is the whole of what a --dry-run has to say.
//
// The `3x ci_half` column is the width a gate's own guidance usually asks for —
// three times the largest half-range the verifier itself reported. It is
// printed and not used, which is what makes the deviation from that guidance
// checkable rather than asserted.
func (r *Result) Table() []string {
	rows := [][]string{{
		"invariant", "N", "original band", "derived band", "centre", "half-width", "3x ci_half", "why",
	}}
	for _, entry := range r.Derived {
		centre, width, derived := Number(entry.Centre), "—", "unchanged"
		if !entry.Copied() {
			width, derived = Number(entry.HalfWidth), entry.Band.String()
		}
		guide := "—"
		if len(entry.CIHalf) > 0 {
			guide = Number(entry.CIGuide)
		}
		rows = append(rows, []string{
			entry.Invariant, fmt.Sprint(entry.N), entry.Original.String(), derived,
			centre, width, guide, entry.Reason(),
		})
	}
	return align(rows)
}

// align pads a table of cells into columns.
func align(rows [][]string) []string {
	if len(rows) == 0 {
		return nil
	}
	width := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if n := len([]rune(cell)); n > width[i] {
				width[i] = n
			}
		}
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = cell + strings.Repeat(" ", width[i]-len([]rune(cell)))
		}
		out = append(out, strings.TrimRight(strings.Join(cells, "  "), " "))
	}
	return out
}

// Summary is the calibration in a few lines: what it read and what it changed.
func (r *Result) Summary() []string {
	var recentred, kept []string
	for _, entry := range r.Derived {
		if entry.Copied() {
			kept = append(kept, entry.Invariant)
			continue
		}
		recentred = append(recentred, entry.Invariant)
	}
	sort.Strings(kept)
	runs := make([]string, 0, len(r.Reports))
	for _, provenance := range r.Reports {
		runs = append(runs, provenance.RunID)
	}
	lines := []string{
		fmt.Sprintf("task %s at %s, N=%d reference run(s) from %s (%s)",
			r.Task, short(r.Rev), r.N, plural(len(r.Reports), "report"), strings.Join(runs, ", ")),
		"rule: " + r.Rule.Describe(r.N),
	}
	if len(recentred) > 0 {
		lines = append(lines, fmt.Sprintf("re-centred %d: %s",
			len(recentred), strings.Join(recentred, ", ")))
	}
	if len(kept) > 0 {
		lines = append(lines, fmt.Sprintf("kept %d: %s", len(kept), strings.Join(kept, ", ")))
	}
	return lines
}

// plural is "1 report" or "2 reports".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
