package report

import (
	"strings"
	"testing"
)

func TestSection(t *testing.T) {
	var r Renderer

	first := r.Section("Summary", []string{"all green", "no regressions"})
	want := "## Summary\nall green\nno regressions\n"
	if first != want {
		t.Errorf("first section = %q, want %q", first, want)
	}

	second := r.Section("Details", []string{"one line"})
	want = "## Details\none line\n"
	if second != want {
		t.Errorf("second section = %q, want %q", second, want)
	}
	if strings.Contains(second, "Summary") {
		t.Error("the second section carries the first one's heading")
	}

	third := r.Section("Empty", nil)
	if third != "## Empty\n" {
		t.Errorf("third section = %q, want %q", third, "## Empty\n")
	}
}

func TestSectionsAreIndependentOfOrder(t *testing.T) {
	var a, b Renderer
	a.Section("One", []string{"x"})
	got := a.Section("Two", []string{"y"})
	want := b.Section("Two", []string{"y"})
	if got != want {
		t.Errorf("a used renderer gives %q, a fresh one gives %q", got, want)
	}
}
