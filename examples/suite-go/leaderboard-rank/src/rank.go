// Package leaderboard orders scored entries for display.
package leaderboard

import "sort"

// Entry is one player's standing.
type Entry struct {
	Name  string
	Score int
}

// Rank returns the entries ordered by score, highest first. Entries that
// share a score keep the order they were given in, so a board rebuilt from
// the same submissions does not shuffle the players who are level. The
// argument is not modified.
func Rank(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
