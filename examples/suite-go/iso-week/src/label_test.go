package isoweek

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestLabel(t *testing.T) {
	cases := []struct {
		date       string
		year, week int
	}{
		{"2026-06-15", 2026, 25},
		{"2021-01-01", 2020, 53}, // a Friday: the last week of 2020
		{"2023-01-01", 2022, 52}, // a Sunday: the last week of 2022
		{"2019-12-30", 2020, 1},  // a Monday: already week 1 of 2020
		{"2025-12-31", 2026, 1},  // week 1 of 2026
		{"2020-12-31", 2020, 53},
		{"2024-12-29", 2024, 52},
		{"2027-01-03", 2026, 53},
	}
	for _, c := range cases {
		y, w := Label(day(c.date))
		if y != c.year || w != c.week {
			t.Errorf("Label(%s) = %d-W%02d, want %d-W%02d", c.date, y, w, c.year, c.week)
		}
	}
}
