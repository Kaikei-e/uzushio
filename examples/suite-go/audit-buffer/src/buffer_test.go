package audit

import "testing"

// base is a one-entry trail with spare capacity, which is what makes the
// sharing visible.
func base() []Entry {
	b := make([]Entry, 0, 8)
	return append(b, Entry{Actor: "ada", Action: "login"})
}

func TestAppendDoesNotShareStorage(t *testing.T) {
	b := base()
	first := Append(b, Entry{Actor: "bob", Action: "read"})
	second := Append(b, Entry{Actor: "cy", Action: "write"})

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("lengths are %d and %d, want 2 and 2", len(first), len(second))
	}
	if first[1].Actor != "bob" {
		t.Errorf("the second append overwrote the first list: it ends with %+v", first[1])
	}
	if second[1].Actor != "cy" {
		t.Errorf("second list ends with %+v, want the entry appended to it", second[1])
	}
	if out := Append(nil, Entry{Actor: "ada"}); len(out) != 1 || out[0].Actor != "ada" {
		t.Errorf("Append(nil, e) = %+v, want a one-entry list", out)
	}
}

func TestAppendLeavesTheInputAlone(t *testing.T) {
	b := base()
	out := Append(b, Entry{Actor: "bob", Action: "read"})
	if len(b) != 1 {
		t.Fatalf("input list grew to %d entries", len(b))
	}
	if grown := b[:2]; grown[1] != (Entry{}) {
		t.Errorf("Append wrote %+v into the input list's spare capacity", grown[1])
	}
	out[0].Actor = "changed"
	if b[0].Actor != "ada" {
		t.Errorf("editing the result changed the input: %q", b[0].Actor)
	}
	if out[0].Actor != "changed" || out[1].Actor != "bob" {
		t.Errorf("result is %+v, want the input entries followed by the new one", out)
	}
}
