package ledger

import "testing"

func TestToCents(t *testing.T) {
	cases := []struct {
		amount float64
		want   int64
	}{
		{0, 0},
		{3, 300},
		{0.5, 50},
		{-0.5, -50},
		{0.125, 13},
		{-0.125, -13},
		{0.375, 38},
		{-0.375, -38},
		{2.625, 263},
		{-2.625, -263},
	}
	for _, c := range cases {
		if got := ToCents(c.amount); got != c.want {
			t.Errorf("ToCents(%v) = %d, want %d", c.amount, got, c.want)
		}
	}
}
