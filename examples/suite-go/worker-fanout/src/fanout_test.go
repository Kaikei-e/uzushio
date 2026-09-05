package fanout

import "testing"

func TestHandlersReportTheirOwnShard(t *testing.T) {
	shards := []string{"eu-1", "eu-2", "us-1", "ap-1"}
	hs := Handlers(shards)
	if len(hs) != len(shards) {
		t.Fatalf("Handlers returned %d handlers, want %d", len(hs), len(shards))
	}
	// Called after Handlers returned, and out of order, because that is how
	// a dispatcher uses them.
	for _, i := range []int{2, 0, 3, 1, 0} {
		if got := hs[i](); got != shards[i] {
			t.Errorf("handler %d reported %q, want %q", i, got, shards[i])
		}
	}
}

func TestHandlersEmpty(t *testing.T) {
	if got := Handlers(nil); len(got) != 0 {
		t.Errorf("Handlers(nil) returned %d handlers, want none", len(got))
	}
}
