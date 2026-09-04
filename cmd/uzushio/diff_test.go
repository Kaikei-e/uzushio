package main

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	tests := []struct {
		name     string
		onDisk   string
		want     string
		contains []string
		absent   []string
	}{
		{
			name:   "a changed line",
			onDisk: "a\nb\nc\n",
			want:   "a\nB\nc\n",
			contains: []string{
				"--- docdag.yaml (on disk)",
				"+++ docdag.yaml (generated)",
				"@@ -1,3 +1,3 @@",
				" a", "-b", "+B", " c",
			},
		},
		{
			name:     "an added line",
			onDisk:   "a\nc\n",
			want:     "a\nb\nc\n",
			contains: []string{"@@ -1,2 +1,3 @@", "+b"},
			absent:   []string{"-a", "-b", "-c"},
		},
		{
			name:     "a removed line",
			onDisk:   "a\nb\nc\n",
			want:     "a\nc\n",
			contains: []string{"-b"},
		},
		{
			name:     "a far-apart pair of changes becomes two hunks",
			onDisk:   "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16\n17\n18\n19\n20\n",
			want:     "1\nX\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16\n17\n18\nY\n20\n",
			contains: []string{"-2", "+X", "-19", "+Y"},
			absent:   []string{" 10"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := strings.Join(diff("docdag.yaml", tt.onDisk, tt.want), "\n")
			for _, want := range tt.contains {
				if !containsLine(out, want) {
					t.Fatalf("diff does not carry the line %q:\n%s", want, out)
				}
			}
			for _, absent := range tt.absent {
				if containsLine(out, absent) {
					t.Fatalf("diff carries the line %q, which it should not:\n%s", absent, out)
				}
			}
		})
	}
}

// TestDiffOfIdenticalInputIsOnlyTheHeader records that diff is never asked for
// one: checkFile compares bytes first, and an empty edit script produces no
// hunks at all.
func TestDiffOfIdenticalInputIsOnlyTheHeader(t *testing.T) {
	out := diff("docdag.yaml", "a\nb\n", "a\nb\n")
	if len(out) != 2 {
		t.Fatalf("diff of identical input = %v, want the two header lines", out)
	}
}

func TestDiffHandlesAnEmptySide(t *testing.T) {
	added := strings.Join(diff("docdag.yaml", "", "a\nb\n"), "\n")
	if !containsLine(added, "+a") || !containsLine(added, "+b") {
		t.Fatalf("diff from empty = %s", added)
	}
	removed := strings.Join(diff("docdag.yaml", "a\nb\n", ""), "\n")
	if !containsLine(removed, "-a") || !containsLine(removed, "-b") {
		t.Fatalf("diff to empty = %s", removed)
	}
}

// TestSplitLines keeps the line count honest: a trailing newline terminates
// the last line, it does not begin an empty one.
func TestSplitLines(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{{"", 0}, {"a", 1}, {"a\n", 1}, {"a\nb\n", 2}, {"a\n\n", 2}} {
		if got := len(splitLines(tt.in)); got != tt.want {
			t.Fatalf("splitLines(%q) has %d lines, want %d", tt.in, got, tt.want)
		}
	}
}

func containsLine(text, line string) bool {
	for _, candidate := range strings.Split(text, "\n") {
		if candidate == line {
			return true
		}
	}
	return false
}

// TestDiffHeaderNamesTheFileAsked keeps the header honest for --out: a reader
// sent to docdag.yaml when the stale file is somewhere else looks at the wrong
// file and finds nothing wrong with it.
func TestDiffHeaderNamesTheFileAsked(t *testing.T) {
	out := strings.Join(diff("other.yaml", "a\n", "b\n"), "\n")
	if !containsLine(out, "--- other.yaml (on disk)") || !containsLine(out, "+++ other.yaml (generated)") {
		t.Fatalf("diff header does not name other.yaml:\n%s", out)
	}
	if strings.Contains(out, "docdag.yaml") {
		t.Fatalf("diff header still names docdag.yaml:\n%s", out)
	}
}
