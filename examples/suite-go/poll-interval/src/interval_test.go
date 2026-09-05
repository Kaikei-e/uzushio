package poll

import (
	"testing"
	"time"
)

func TestInterval(t *testing.T) {
	cases := []struct {
		rate int
		want time.Duration
	}{
		{1, time.Minute},
		{2, 30 * time.Second},
		{4, 15 * time.Second},
		{60, time.Second},
		{120, 500 * time.Millisecond},
		{7, time.Minute / 7},
		{0, 0},
		{-3, 0},
	}
	for _, c := range cases {
		if got := Interval(c.rate); got != c.want {
			t.Errorf("Interval(%d) = %v, want %v", c.rate, got, c.want)
		}
	}
}
