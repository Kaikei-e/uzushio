// Package label shortens the text that goes on a fixed-width chip.
package label

// Truncate shortens s so that it carries at most n characters, where a
// character is one rune and not one byte. When s is shortened, a single
// horizontal ellipsis "…" is appended to what is kept, so the result of a
// shortened label is n characters plus the ellipsis. A label that already
// fits comes back unchanged, with no ellipsis.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
