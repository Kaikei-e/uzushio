package vocab

import (
	"errors"
	"regexp"
	"testing"
)

// TestPatternsCompile is the guard the package's own init already is: it says
// out loud that every declared shape is a Go regular expression, so a bad one
// fails a test rather than a docdag run.
func TestPatternsCompile(t *testing.T) {
	for name, pattern := range map[string]string{
		"EditIDPattern":    EditIDPattern,
		"PatternIDPattern": PatternIDPattern,
		"RunIDPattern":     RunIDPattern,
		"EditIDBody":       EditIDBody,
		"PatternIDBody":    PatternIDBody,
		"RunIDBody":        RunIDBody,
	} {
		if _, err := regexp.Compile(pattern); err != nil {
			t.Fatalf("%s does not compile: %v", name, err)
		}
	}
}

// TestBodiesAnchorToPatterns keeps the reference alternation and the kinds'
// identifiers one shape. They drift apart in silence otherwise: a wikilink the
// reference pattern rejects is dropped without a finding.
func TestBodiesAnchorToPatterns(t *testing.T) {
	for _, tt := range []struct{ body, pattern string }{
		{EditIDBody, EditIDPattern},
		{PatternIDBody, PatternIDPattern},
		{RunIDBody, RunIDPattern},
	} {
		if got := "^" + tt.body + "$"; got != tt.pattern {
			t.Fatalf("anchored body = %q, want %q", got, tt.pattern)
		}
	}
}

func TestEditID(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{{1, "he-0001"}, {42, "he-0042"}, {9999, "he-9999"}} {
		got, err := EditID(tt.n)
		if err != nil {
			t.Fatalf("EditID(%d): %v", tt.n, err)
		}
		if got != tt.want {
			t.Fatalf("EditID(%d) = %q, want %q", tt.n, got, tt.want)
		}
		if !ValidEditID(got) {
			t.Fatalf("EditID(%d) = %q, which the pattern rejects", tt.n, got)
		}
		back, err := ParseEditID(got)
		if err != nil {
			t.Fatalf("ParseEditID(%q): %v", got, err)
		}
		if back != tt.n {
			t.Fatalf("ParseEditID(%q) = %d, want %d", got, back, tt.n)
		}
	}
	for _, n := range []int{0, -1, 10000} {
		if _, err := EditID(n); !errors.Is(err, ErrID) {
			t.Fatalf("EditID(%d) error = %v, want ErrID", n, err)
		}
	}
	for _, id := range []string{"", "he-1", "he-00001", "HE-0001", "he-0001 ", "fp/x"} {
		if _, err := ParseEditID(id); !errors.Is(err, ErrID) {
			t.Fatalf("ParseEditID(%q) error = %v, want ErrID", id, err)
		}
	}
}

func TestPatternID(t *testing.T) {
	for _, slug := range []string{"retry-storm", "a", "tool-loop-2"} {
		id, err := PatternID(slug)
		if err != nil {
			t.Fatalf("PatternID(%q): %v", slug, err)
		}
		if !ValidPatternID(id) {
			t.Fatalf("PatternID(%q) = %q, which the pattern rejects", slug, id)
		}
		back, err := ParsePatternID(id)
		if err != nil {
			t.Fatalf("ParsePatternID(%q): %v", id, err)
		}
		if back != slug {
			t.Fatalf("ParsePatternID(%q) = %q, want %q", id, back, slug)
		}
	}
	for _, slug := range []string{"", "Retry", "retry_storm", "fp/retry", "retry storm"} {
		if _, err := PatternID(slug); !errors.Is(err, ErrID) {
			t.Fatalf("PatternID(%q) error = %v, want ErrID", slug, err)
		}
	}
	for _, id := range []string{"retry-storm", "fp/", "fp/Retry", "FP/retry"} {
		if _, err := ParsePatternID(id); !errors.Is(err, ErrID) {
			t.Fatalf("ParsePatternID(%q) error = %v, want ErrID", id, err)
		}
	}
}

func TestRunID(t *testing.T) {
	tests := []struct {
		name string
		ref  RunRef
		want string
	}{
		{
			name: "held-out, no sequence",
			ref:  RunRef{Edit: "he-0001", Day: "2026-09-05", ModelSlug: "gemma-3-12b", Split: SplitHeldOut},
			want: "run/he-0001@2026-09-05-gemma-3-12b-out",
		},
		{
			name: "held-in with a sequence",
			ref:  RunRef{Edit: "he-0042", Day: "2026-01-31", ModelSlug: "granite-3.3-8b", Split: SplitHeldIn, Seq: 2},
			want: "run/he-0042@2026-01-31-granite-3.3-8b-in-2",
		},
		{
			name: "a model slug that ends in a split word",
			ref:  RunRef{Edit: "he-0007", Day: "2026-02-02", ModelSlug: "qwen-in", Split: SplitHeldOut},
			want: "run/he-0007@2026-02-02-qwen-in-out",
		},
		{
			name: "a one-segment model slug",
			ref:  RunRef{Edit: "he-0100", Day: "2026-12-31", ModelSlug: "m", Split: SplitHeldIn, Seq: 11},
			want: "run/he-0100@2026-12-31-m-in-11",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.ref.ID()
			if err != nil {
				t.Fatalf("ID: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ID = %q, want %q", got, tt.want)
			}
			if !ValidRunID(got) {
				t.Fatalf("%q is rejected by RunIDPattern", got)
			}
			back, err := ParseRunID(got)
			if err != nil {
				t.Fatalf("ParseRunID(%q): %v", got, err)
			}
			if back != tt.ref {
				t.Fatalf("ParseRunID(%q) = %+v, want %+v", got, back, tt.ref)
			}
		})
	}
}

func TestRunIDRejects(t *testing.T) {
	for _, tt := range []struct {
		name string
		ref  RunRef
	}{
		{"no edit", RunRef{Edit: "0001", Day: "2026-09-05", ModelSlug: "m", Split: SplitHeldIn}},
		{"no day", RunRef{Edit: "he-0001", Day: "2026-9-5", ModelSlug: "m", Split: SplitHeldIn}},
		{"impossible day", RunRef{Edit: "he-0001", Day: "2026-13-01", ModelSlug: "m", Split: SplitHeldIn}},
		{"upper-case model", RunRef{Edit: "he-0001", Day: "2026-09-05", ModelSlug: "Gemma", Split: SplitHeldIn}},
		{"double hyphen", RunRef{Edit: "he-0001", Day: "2026-09-05", ModelSlug: "a--b", Split: SplitHeldIn}},
		{"trailing hyphen", RunRef{Edit: "he-0001", Day: "2026-09-05", ModelSlug: "a-", Split: SplitHeldIn}},
		{"unknown split", RunRef{Edit: "he-0001", Day: "2026-09-05", ModelSlug: "m", Split: Split("held")}},
		{"negative sequence", RunRef{Edit: "he-0001", Day: "2026-09-05", ModelSlug: "m", Split: SplitHeldIn, Seq: -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.ref.ID(); !errors.Is(err, ErrID) {
				t.Fatalf("ID error = %v, want ErrID", err)
			}
		})
	}
	for _, id := range []string{
		"", "he-0001", "run/he-0001@2026-09-05-gemma", "run/he-0001@2026-09-05-out",
		"run/he-0001@2026-09-05-gemma-out-",
		"RUN/he-0001@2026-09-05-gemma-out",
	} {
		if _, err := ParseRunID(id); !errors.Is(err, ErrID) {
			t.Fatalf("ParseRunID(%q) error = %v, want ErrID", id, err)
		}
	}
}

// TestRunShapeAgreesWithRunIDPattern holds the parsing expression and the
// expression the configuration declares to one decision. Two spellings of one
// shape is exactly the drift this package exists to prevent.
func TestRunShapeAgreesWithRunIDPattern(t *testing.T) {
	candidates := []string{
		"run/he-0001@2026-09-05-gemma-3-12b-out",
		"run/he-0001@2026-09-05-gemma-3-12b-in-3",
		"run/he-0001@2026-09-05-m-in",
		"run/he-0001@2026-09-05-qwen-in-out",
		"run/he-0001@2026-09-05-a.b-out-0",
		"run/he-0001@2026-09-05-gemma-held-out",
		"run/he-0001@2026-09-05-out",
		"run/he-0001@2026-09-05--out",
		"run/he-001@2026-09-05-m-out",
		"he-0001@2026-09-05-m-out",
		"run/he-0001@2026-09-05-m-out-x",
	}
	for _, id := range candidates {
		declared := runID.MatchString(id)
		parsed := runShape.MatchString(id)
		if declared != parsed {
			t.Fatalf("%q: RunIDPattern says %v, the parsing shape says %v", id, declared, parsed)
		}
	}
}

func TestSplitSuffix(t *testing.T) {
	for _, s := range AllSplits() {
		suffix, err := s.Suffix()
		if err != nil {
			t.Fatalf("Suffix(%s): %v", s, err)
		}
		back, err := SplitFromSuffix(suffix)
		if err != nil {
			t.Fatalf("SplitFromSuffix(%q): %v", suffix, err)
		}
		if back != s {
			t.Fatalf("SplitFromSuffix(%q) = %q, want %q", suffix, back, s)
		}
	}
	if _, err := SplitFromSuffix("held-in"); !errors.Is(err, ErrID) {
		t.Fatal("SplitFromSuffix accepted the frontmatter spelling")
	}
}

func TestPathAndFilename(t *testing.T) {
	tests := []struct {
		kind Kind
		id   string
		want string
	}{
		{KindEdit, "he-0001", "spec/edits/he-0001.md"},
		{KindPattern, "fp/retry-storm", "spec/patterns/retry-storm.md"},
		{KindRun, "run/he-0001@2026-09-05-m-out", "spec/runs/he-0001@2026-09-05-m-out.md"},
	}
	for _, tt := range tests {
		got, err := Path(tt.kind, tt.id)
		if err != nil {
			t.Fatalf("Path(%s, %q): %v", tt.kind, tt.id, err)
		}
		if got != tt.want {
			t.Fatalf("Path(%s, %q) = %q, want %q", tt.kind, tt.id, got, tt.want)
		}
	}
	if _, err := Path(KindRun, "he-0001"); !errors.Is(err, ErrID) {
		t.Fatal("Path wrote an edit under the run directory")
	}
	if _, err := Path(Kind("clause"), "UZ-V-001"); !errors.Is(err, ErrID) {
		t.Fatal("Path answered for a kind uzushio does not declare")
	}
}

// TestWritesID records DocDag's rule rather than a preference of ours: an
// identifier carrying a slash can never be read off a file name's stem.
func TestWritesID(t *testing.T) {
	if WritesID(KindEdit) {
		t.Fatal("an edit's stem carries its identifier; it need not write id:")
	}
	for _, k := range []Kind{KindPattern, KindRun} {
		if !WritesID(k) {
			t.Fatalf("%s identifiers carry a slash and must be written in frontmatter", k)
		}
	}
}

// TestModelSlugMayEndInASplitWord records that the run shape is unambiguous
// where it looks ambiguous. A model slug whose last segment is `held` leaves
// `-out` to the split, and the greedy slug group resolves it the same way in
// both expressions.
func TestModelSlugMayEndInASplitWord(t *testing.T) {
	ref, err := ParseRunID("run/he-0001@2026-09-05-gemma-held-out")
	if err != nil {
		t.Fatalf("ParseRunID: %v", err)
	}
	if ref.ModelSlug != "gemma-held" || ref.Split != SplitHeldOut {
		t.Fatalf("ParseRunID = %+v, want model gemma-held on the held-out split", ref)
	}
}
