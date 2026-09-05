package feed

import (
	"testing"
	"time"
)

// drain reads everything Stream sends, giving the whole exchange two
// seconds; a channel nobody closes never ends a range.
func drain(t *testing.T, items []string) []string {
	t.Helper()
	got := make(chan []string, 1)
	go func() {
		var out []string
		for s := range Stream(items) {
			out = append(out, s)
		}
		got <- out
	}()
	select {
	case out := <-got:
		return out
	case <-time.After(2 * time.Second):
		t.Fatal("ranging over the stream never ended")
		return nil
	}
}

func TestStream(t *testing.T) {
	items := []string{"a", "b", "c"}
	out := drain(t, items)
	if len(out) != len(items) {
		t.Fatalf("read %q, want %q", out, items)
	}
	for i := range items {
		if out[i] != items[i] {
			t.Errorf("item %d is %q, want %q", i, out[i], items[i])
		}
	}
}

func TestStreamOfNothing(t *testing.T) {
	if out := drain(t, nil); len(out) != 0 {
		t.Errorf("read %q from an empty stream, want nothing", out)
	}
}
