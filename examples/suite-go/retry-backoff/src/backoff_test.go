package backoff

import (
	"testing"
	"time"
)

func TestDelay(t *testing.T) {
	const (
		base = 100 * time.Millisecond
		max  = 2 * time.Second
	)
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1600 * time.Millisecond},
		{5, max},
		{6, max},
		{31, max},
		{62, max},
		{63, max},
		{64, max},
		{1000, max},
	}
	for _, c := range cases {
		if got := Delay(base, max, c.attempt); got != c.want {
			t.Errorf("Delay(%v, %v, %d) = %v, want %v", base, max, c.attempt, got, c.want)
		}
	}
}
