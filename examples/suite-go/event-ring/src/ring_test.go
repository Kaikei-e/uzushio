package eventring

import (
	"reflect"
	"testing"
)

func TestEventsAreOldestFirst(t *testing.T) {
	r := New(3)
	steps := []struct {
		push string
		want []string
	}{
		{"a", []string{"a"}},
		{"b", []string{"a", "b"}},
		{"c", []string{"a", "b", "c"}},
		{"d", []string{"b", "c", "d"}},
		{"e", []string{"c", "d", "e"}},
		{"f", []string{"d", "e", "f"}},
		{"g", []string{"e", "f", "g"}},
	}
	for _, s := range steps {
		r.Push(s.push)
		if got := r.Events(); !reflect.DeepEqual(got, s.want) {
			t.Errorf("after pushing %q: Events = %q, want %q", s.push, got, s.want)
		}
	}
}

func TestEmptyRing(t *testing.T) {
	if got := New(3).Events(); len(got) != 0 {
		t.Errorf("Events on an empty ring = %q, want nothing", got)
	}
}
