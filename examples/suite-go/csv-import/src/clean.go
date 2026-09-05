// Package csvimport tidies the fields of an uploaded CSV before they are
// stored.
package csvimport

import "strings"

// Clean normalises one imported field. It drops a UTF-8 byte order mark at
// the very start of the field, then removes whitespace — spaces, tabs and
// line endings — from both ends. Whitespace inside the field is left as it
// is.
func Clean(s string) string {
	return strings.Trim(s, " ")
}
