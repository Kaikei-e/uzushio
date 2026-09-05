// Package report renders the sections of a written report.
package report

import "strings"

// Renderer renders one section at a time. The same renderer renders every
// section of a report, one after another.
type Renderer struct {
	b strings.Builder
}

// Section renders one section: a heading line, then one line per entry.
// What it returns is that section and nothing else — a renderer that has
// already rendered a section must not carry it into the next one.
func (r *Renderer) Section(title string, lines []string) string {
	r.b.WriteString("## " + title + "\n")
	for _, l := range lines {
		r.b.WriteString(l + "\n")
	}
	return r.b.String()
}
