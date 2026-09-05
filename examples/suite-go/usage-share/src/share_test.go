package usage

import (
	"math"
	"testing"
)

func TestShare(t *testing.T) {
	cases := []struct {
		used, total int
		want        float64
	}{
		{0, 5, 0},
		{5, 5, 100},
		{1, 2, 50},
		{3, 4, 75},
		{1, 3, 100.0 / 3.0},
		{2, 7, 200.0 / 7.0},
		{7, 2, 350},
		{1, 0, 0},
		{0, 0, 0},
	}
	for _, c := range cases {
		got := Share(c.used, c.total)
		if math.IsNaN(got) || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Share(%d, %d) = %v, want %v", c.used, c.total, got, c.want)
		}
	}
}
