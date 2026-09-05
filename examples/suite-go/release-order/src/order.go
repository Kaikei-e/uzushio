// Package release orders the releases a changelog page lists.
package release

import "sort"

// Release is one shipped release.
type Release struct {
	Day  int // days since the project started
	Name string
}

// Order sorts the releases in place: newest day first, and releases shipped
// on the same day by name, ascending.
func Order(rs []Release) {
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].Day > rs[j].Day || rs[i].Name < rs[j].Name
	})
}
