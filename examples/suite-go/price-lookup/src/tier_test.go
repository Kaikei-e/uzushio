package pricing

import "testing"

func TestTier(t *testing.T) {
	mins := []int{1, 10, 100, 1000}
	cases := []struct {
		qty  int
		want int
	}{
		{-5, -1}, {0, -1},
		{1, 0}, {2, 0}, {9, 0},
		{10, 1}, {11, 1}, {99, 1},
		{100, 2}, {101, 2}, {999, 2},
		{1000, 3}, {1001, 3}, {50000, 3},
	}
	for _, c := range cases {
		if got := Tier(mins, c.qty); got != c.want {
			t.Errorf("Tier(%v, %d) = %d, want %d", mins, c.qty, got, c.want)
		}
	}
}

func TestTierEdges(t *testing.T) {
	if got := Tier(nil, 5); got != -1 {
		t.Errorf("Tier(nil, 5) = %d, want -1", got)
	}
	if got := Tier([]int{7}, 6); got != -1 {
		t.Errorf("Tier([7], 6) = %d, want -1", got)
	}
	if got := Tier([]int{7}, 7); got != 0 {
		t.Errorf("Tier([7], 7) = %d, want 0", got)
	}
	long := make([]int, 64)
	for i := range long {
		long[i] = i * 10
	}
	for i := range long {
		if got := Tier(long, i*10+9); got != i {
			t.Fatalf("Tier(long, %d) = %d, want %d", i*10+9, got, i)
		}
	}
}
