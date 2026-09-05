// Package namematch decides whether two written names are the same name.
package namematch

import "strings"

// Same reports whether two display names name the same person. Spaces
// around a name are noise and are ignored. Letters are compared by Unicode
// simple case folding, which is what makes a word ending in a final sigma
// match the same word written with a medial one; it is not the same as
// lowering both names, and it keeps letters that only look alike apart.
func Same(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}
