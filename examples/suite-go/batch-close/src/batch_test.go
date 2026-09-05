package batch

import (
	"errors"
	"testing"
)

// store hands out batches and refuses to open a second one while the first
// is still open, which is what the real store does.
type store struct {
	opened []*Batch
	live   *Batch
}

func (s *store) open(name string) (*Batch, error) {
	if s.live != nil && !s.live.closed {
		return nil, errors.New("a batch is already open: " + name)
	}
	b := &Batch{N: len(name)}
	s.opened = append(s.opened, b)
	s.live = b
	return b, nil
}

func TestTotalClosesEachBatchBeforeTheNext(t *testing.T) {
	s := &store{}
	names := []string{"a", "bb", "ccc", "dddd"}
	got, err := Total(names, s.open)
	if err != nil {
		t.Fatalf("Total returned %v", err)
	}
	if want := 1 + 2 + 3 + 4; got != want {
		t.Errorf("Total = %d, want %d", got, want)
	}
	if len(s.opened) != len(names) {
		t.Fatalf("%d batches were opened, want %d", len(s.opened), len(names))
	}
	for i, b := range s.opened {
		if !b.closed {
			t.Errorf("batch %d was left open after Total returned", i)
		}
	}
}

func TestTotalNoNames(t *testing.T) {
	s := &store{}
	if got, err := Total(nil, s.open); got != 0 || err != nil {
		t.Errorf("Total(nil) = (%d, %v), want (0, nil)", got, err)
	}
}
