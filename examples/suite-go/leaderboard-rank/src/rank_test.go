package leaderboard

import (
	"fmt"
	"testing"
)

// entries builds a board where many players are level, so that any reorder
// of equal scores shows up in the result.
func entries() []Entry {
	var in []Entry
	for i := 0; i < 40; i++ {
		in = append(in, Entry{Name: fmt.Sprintf("p%02d", i), Score: 10 * (i % 4)})
	}
	return in
}

func TestRankKeepsTiesInOrder(t *testing.T) {
	in := entries()
	got := Rank(in)

	if len(got) != len(in) {
		t.Fatalf("Rank returned %d entries, want %d", len(got), len(in))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Fatalf("Rank is not ordered by score: %v then %v", got[i-1], got[i])
		}
	}
	// Within one score, the names must still climb: that is the input order.
	for i := 1; i < len(got); i++ {
		if got[i-1].Score == got[i].Score && got[i-1].Name > got[i].Name {
			t.Errorf("tie at score %d reordered: %q before %q", got[i].Score, got[i-1].Name, got[i].Name)
		}
	}
}

func TestRankDoesNotModifyItsArgument(t *testing.T) {
	in := entries()
	before := make([]Entry, len(in))
	copy(before, in)
	Rank(in)
	for i := range in {
		if in[i] != before[i] {
			t.Fatalf("Rank modified its argument at %d: %v, was %v", i, in[i], before[i])
		}
	}
}
