package shift

import (
	"testing"
	"time"
)

var east = time.FixedZone("east", 9*3600)  // nine hours ahead of UTC
var west = time.FixedZone("west", -5*3600) // five hours behind UTC

// at is 2026-03-<day> <hour>:<min> in loc.
func at(day, hour, min int, loc *time.Location) time.Time {
	return time.Date(2026, 3, day, hour, min, 0, 0, loc)
}

func TestDayStart(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		loc  *time.Location
		want time.Time
	}{
		{"late UTC evening is tomorrow in the east", at(1, 23, 30, time.UTC), east, at(2, 0, 0, east)},
		{"early UTC morning is yesterday in the west", at(2, 2, 15, time.UTC), west, at(1, 0, 0, west)},
		{"UTC midday is the same day in the east", at(2, 12, 0, time.UTC), east, at(2, 0, 0, east)},
		{"recorded in the east, read in the west", at(2, 8, 0, east), west, at(1, 0, 0, west)},
		{"already in loc", at(2, 17, 45, east), east, at(2, 0, 0, east)},
	}
	for _, c := range cases {
		got := DayStart(c.in, c.loc)
		if !got.Equal(c.want) {
			t.Errorf("%s: DayStart(%v, %v) = %v, want %v", c.name, c.in, c.loc, got, c.want)
		}
		if got.Location() != c.loc {
			t.Errorf("%s: DayStart returned a time in %v, want %v", c.name, got.Location(), c.loc)
		}
	}
}
