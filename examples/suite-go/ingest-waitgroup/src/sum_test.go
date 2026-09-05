package ingest

import (
	"testing"
	"time"
)

// slowDouble is what a real scorer looks like from here: it takes long
// enough that the goroutines are still running when Sum stops waiting.
func slowDouble(x int) int {
	time.Sleep(20 * time.Millisecond)
	return 2 * x
}

func TestSum(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	want := 0
	for _, it := range items {
		want += 2 * it
	}
	if got := Sum(items, slowDouble); got != want {
		t.Errorf("Sum = %d, want %d", got, want)
	}
}

func TestSumOfNothing(t *testing.T) {
	if got := Sum(nil, slowDouble); got != 0 {
		t.Errorf("Sum(nil) = %d, want 0", got)
	}
}
