package release

import (
	"fmt"
	"sort"
	"testing"
)

func board() []Release {
	var rs []Release
	for i := 0; i < 60; i++ {
		rs = append(rs, Release{Day: i % 5, Name: fmt.Sprintf("r%02d", i)})
	}
	return rs
}

func TestOrder(t *testing.T) {
	rs := board()
	before := append([]Release(nil), rs...)
	Order(rs)

	for i := 1; i < len(rs); i++ {
		a, b := rs[i-1], rs[i]
		if a.Day < b.Day {
			t.Fatalf("day %d comes before day %d at %d", a.Day, b.Day, i)
		}
		if a.Day == b.Day && a.Name >= b.Name {
			t.Fatalf("on day %d, %q comes before %q at %d", a.Day, a.Name, b.Name, i)
		}
	}

	sort.Slice(before, func(i, j int) bool { return before[i].Name < before[j].Name })
	after := append([]Release(nil), rs...)
	sort.Slice(after, func(i, j int) bool { return after[i].Name < after[j].Name })
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("Order lost or duplicated a release: %v became %v", before[i], after[i])
		}
	}
}
