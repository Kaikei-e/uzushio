package mutate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/mutate"
)

// each returns the mutated sources one operator produces for a snippet, in
// the order the package emits them.
func each(t *testing.T, src string, ops ...mutate.Operator) []string {
	t.Helper()
	mutations, err := mutate.Mutations("x.go", []byte(src), ops)
	if err != nil {
		t.Fatalf("Mutations: %v", err)
	}
	out := make([]string, 0, len(mutations))
	for _, m := range mutations {
		out = append(out, string(m.Source))
	}
	return out
}

const header = "package p\n\n"

func TestArith(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "plus becomes minus",
			src:  "func f(a, b int) int { return a + b }\n",
			want: []string{"func f(a, b int) int { return a - b }\n"},
		},
		{
			name: "minus becomes plus",
			src:  "func f(a, b int) int { return a - b }\n",
			want: []string{"func f(a, b int) int { return a + b }\n"},
		},
		{
			name: "times becomes divide",
			src:  "func f(a, b int) int { return a * b }\n",
			want: []string{"func f(a, b int) int { return a / b }\n"},
		},
		{
			name: "divide becomes times",
			src:  "func f(a, b int) int { return a / b }\n",
			want: []string{"func f(a, b int) int { return a * b }\n"},
		},
		{
			// `"a" - "b"` does not compile, and a mutant the compiler kills
			// says nothing about the verifier.
			name: "a string literal is left alone",
			src:  "func f(s string) string { return s + \"!\" }\n",
			want: nil,
		},
		{
			name: "a string conversion is left alone",
			src:  "func f(b []byte) string { return string(b) + \"!\" }\n",
			want: nil,
		},
		{
			// The judgement is syntactic, so a string in a variable is not
			// recognised. The mutant does not compile and is killed; the limit
			// is real and is written down rather than claimed away.
			name: "a string in a variable is not recognised",
			src:  "func f(a, b string) string { return a + b }\n",
			want: []string{"func f(a, b string) string { return a - b }\n"},
		},
		{
			name: "every site, left to right",
			src:  "func f(a, b, c int) int { return a + b - c }\n",
			want: []string{
				"func f(a, b, c int) int { return a - b - c }\n",
				"func f(a, b, c int) int { return a + b + c }\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := each(t, header+tt.src, mutate.OpArith)
			want := prefixed(tt.want)
			if !slices.Equal(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestBound(t *testing.T) {
	tests := []struct{ src, want string }{
		{"if a < b {\n\t\treturn\n\t}", "if a <= b {\n\t\treturn\n\t}"},
		{"if a <= b {\n\t\treturn\n\t}", "if a < b {\n\t\treturn\n\t}"},
		{"if a > b {\n\t\treturn\n\t}", "if a >= b {\n\t\treturn\n\t}"},
		{"if a >= b {\n\t\treturn\n\t}", "if a > b {\n\t\treturn\n\t}"},
		{"if a == b {\n\t\treturn\n\t}", "if a != b {\n\t\treturn\n\t}"},
		{"if a != b {\n\t\treturn\n\t}", "if a == b {\n\t\treturn\n\t}"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			src := header + "func f(a, b int) {\n\t" + tt.src + "\n}\n"
			want := header + "func f(a, b int) {\n\t" + tt.want + "\n}\n"
			got := each(t, src, mutate.OpBound)
			if len(got) != 1 || got[0] != want {
				t.Fatalf("got %q, want [%q]", got, want)
			}
		})
	}
}

func TestCond(t *testing.T) {
	src := header + "func f(a int) int {\n\tif a > 0 {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"
	want := header + "func f(a int) int {\n\tif !(a > 0) {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"
	got := each(t, src, mutate.OpCond)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %q, want [%q]", got, want)
	}
}

func TestConst(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "decimal",
			src:  "func f() int { return 41 }\n",
			want: []string{"func f() int { return 42 }\n"},
		},
		{
			// The literal is re-rendered in decimal. It is a mutant, not a
			// reformatting, so the spelling does not have to be preserved.
			name: "hexadecimal comes back decimal",
			src:  "func f() int { return 0x0f }\n",
			want: []string{"func f() int { return 16 }\n"},
		},
		{
			name: "underscores are read",
			src:  "func f() int { return 1_000 }\n",
			want: []string{"func f() int { return 1001 }\n"},
		},
		{
			name: "a float is not an integer literal",
			src:  "func f() float64 { return 1.5 }\n",
			want: nil,
		},
		{
			name: "one that overflows an int64 is left alone",
			src:  "func f() uint64 { return 99999999999999999999 }\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := each(t, header+tt.src, mutate.OpConst)
			want := prefixed(tt.want)
			if !slices.Equal(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestStmt(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The plain assignment and the call go; the short variable
			// declaration stays, because removing it leaves every later use of
			// x undefined and the mutant would be killed by the compiler.
			name: "an assignment goes, line and all; a := stays",
			src:  "func f() {\n\tx := 1\n\tx = 2\n\tprintln(x)\n}\n",
			want: []string{
				"func f() {\n\tx := 1\n\tprintln(x)\n}\n",
				"func f() {\n\tx := 1\n\tx = 2\n}\n",
			},
		},
		{
			name: "a short variable declaration stays",
			src:  "func f() {\n\tx := 1\n\t_ = x\n}\n",
			want: []string{"func f() {\n\tx := 1\n}\n"},
		},
		{
			name: "a trailing comment goes with the line",
			src:  "func f() {\n\tprintln(1) // why\n}\n",
			want: []string{"func f() {\n}\n"},
		},
		{
			// A declaration or a return changes what the function is rather
			// than what it does, and removing either rarely compiles.
			name: "a declaration and a return stay",
			src:  "func f() int {\n\tvar x int\n\treturn x\n}\n",
			want: nil,
		},
		{
			// The statement has to own its lines, or the deletion would cut a
			// line in half.
			name: "two statements on one line stay",
			src:  "func f() {\n\ta := 1; b := 2\n\tprintln(a, b)\n}\n",
			want: []string{"func f() {\n\ta := 1; b := 2\n}\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := each(t, header+tt.src, mutate.OpStmt)
			want := prefixed(tt.want)
			if !slices.Equal(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestRet(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "the declared int result decides",
			src:  "func f(a, b int) int { return a + b }\n",
			want: []string{"func f(a, b int) int { return 0 }\n"},
		},
		{
			name: "a string",
			src:  "func f(s string) string { return s }\n",
			want: []string{"func f(s string) string { return \"\" }\n"},
		},
		{
			name: "a bool",
			src:  "func f(a int) bool { return a > 0 }\n",
			want: []string{"func f(a int) bool { return false }\n"},
		},
		{
			name: "a slice is nil",
			src:  "func f(s []int) []int { return s }\n",
			want: []string{"func f(s []int) []int { return nil }\n"},
		},
		{
			name: "a pointer is nil",
			src:  "type T struct{}\n\nfunc f() *T { return &T{} }\n",
			want: []string{"type T struct{}\n\nfunc f() *T { return nil }\n"},
		},
		{
			// `return 0` zeroed is `return 0`, which is an equivalent mutant
			// by construction and is dropped rather than written out.
			name: "a return that is already zero produces nothing",
			src:  "func f() int { return 0 }\n",
			want: nil,
		},
		{
			// Two results is two zero values, and which one to change is a
			// choice this operator does not make.
			name: "two results are left alone",
			src:  "func f() (int, error) { return 1, nil }\n",
			want: nil,
		},
		{
			// A named type this package cannot resolve. Guessing from the
			// expression buys a handful of mutants and a steady supply of ones
			// that do not compile, so the site is skipped either way.
			name: "an unresolvable result type is left alone",
			src:  "type T struct{}\n\nfunc f(t T) T { return t }\n",
			want: nil,
		},
		{
			name: "an unresolvable result type returning a literal is left alone too",
			src:  "type Code int\n\nfunc f() Code { return 5 }\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := each(t, header+tt.src, mutate.OpRet)
			want := prefixed(tt.want)
			if !slices.Equal(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// TestCommentsAndFormattingSurvive is the reason this package splices bytes
// rather than reprinting the tree. A go/printer round-trip normalises the
// whole file and moves free-floating comments; a splice cannot, because it
// only ever replaces one range.
func TestCommentsAndFormattingSurvive(t *testing.T) {
	src := header +
		"// Add adds.\nfunc Add(a, b int) int {\n" +
		"\t// a deliberately    badly aligned comment\n" +
		"\treturn a + b // and a trailing one\n}\n\n" +
		"//   a free-floating comment nobody attached to anything\n"
	got := each(t, src, mutate.OpArith)
	if len(got) != 1 {
		t.Fatalf("got %d mutations, want 1", len(got))
	}
	if !strings.Contains(got[0], "//   a free-floating comment nobody attached to anything\n") ||
		!strings.Contains(got[0], "// a deliberately    badly aligned comment") ||
		!strings.Contains(got[0], "return a - b // and a trailing one") {
		t.Fatalf("the mutation reformatted the file:\n%s", got[0])
	}
	// One changed line, which is what makes a committed mutant reviewable.
	if changed := lineDiffCount(src, got[0]); changed != 1 {
		t.Fatalf("%d lines changed, want 1:\n%s", changed, got[0])
	}
}

// TestOrderIsDeterministic pins that the same source always yields the same
// mutants in the same order — by offset, then by the order the operators are
// declared in. A committed set of mutants that reshuffled between runs would
// make every regeneration a diff.
func TestOrderIsDeterministic(t *testing.T) {
	src := header + "func f(a, b int) int {\n\tif a > 1 {\n\t\treturn a + b\n\t}\n\treturn a * b\n}\n"
	first, err := mutate.Mutations("x.go", []byte(src), mutate.AllOperators())
	if err != nil {
		t.Fatalf("Mutations: %v", err)
	}
	var names []string
	for _, m := range first {
		names = append(names, m.Note)
	}
	want := []string{
		"x.go:4:5: negate the if condition",
		"x.go:4:9: 1 -> 2",
		"x.go:4:7: \">\" -> \">=\"",
		"x.go:5:10: return a + b -> return 0",
		"x.go:5:12: \"+\" -> \"-\"",
		"x.go:7:9: return a * b -> return 0",
		"x.go:7:11: \"*\" -> \"/\"",
	}
	slices.Sort(want)
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Fatalf("mutations = %q, want %q", sorted, want)
	}
	for range 5 {
		again, err := mutate.Mutations("x.go", []byte(src), mutate.AllOperators())
		if err != nil {
			t.Fatalf("Mutations: %v", err)
		}
		for i := range again {
			if again[i].Note != first[i].Note || string(again[i].Source) != string(first[i].Source) {
				t.Fatalf("run %d disagrees at %d: %q vs %q", i, i, again[i].Note, first[i].Note)
			}
		}
	}
	// And the order really is by offset.
	for i := 1; i < len(first); i++ {
		before, after := first[i-1], first[i]
		if after.Line < before.Line || (after.Line == before.Line && after.Column < before.Column) {
			t.Fatalf("%s comes after %s", after.Note, before.Note)
		}
	}
}

func TestSkip(t *testing.T) {
	tests := []struct {
		name string
		file string
		src  string
		skip bool
	}{
		{"a source file", "add.go", "package p\n", false},
		{"a test file", "add_test.go", "package p\n", true},
		{"not Go", "README.md", "# hello\n", true},
		{
			"generated", "zz.go",
			"// Code generated by stringer. DO NOT EDIT.\n\npackage p\n", true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := mutate.Skip(tt.file, []byte(tt.src)); got != tt.skip {
				t.Fatalf("Skip(%q) = %v, want %v", tt.file, got, tt.skip)
			}
		})
	}
}

func TestOperators(t *testing.T) {
	all, err := mutate.Operators("")
	if err != nil {
		t.Fatalf("Operators(\"\"): %v", err)
	}
	if !slices.Equal(all, mutate.AllOperators()) {
		t.Fatalf("Operators(\"\") = %v, want all of them", all)
	}
	got, err := mutate.Operators("arith, ret ,arith")
	if err != nil {
		t.Fatalf("Operators: %v", err)
	}
	if !slices.Equal(got, []mutate.Operator{mutate.OpArith, mutate.OpRet}) {
		t.Fatalf("Operators = %v, want arith and ret once each", got)
	}
	if _, err := mutate.Operators("arith,nonesuch"); err == nil {
		t.Fatal("Operators accepted an operator that does not exist")
	}
}

func TestMutationsRejectsUnparseableSource(t *testing.T) {
	if _, err := mutate.Mutations("x.go", []byte("package p\nfunc ("), mutate.AllOperators()); err == nil {
		t.Fatal("Mutations parsed a file that is not Go")
	}
}

func prefixed(bodies []string) []string {
	if bodies == nil {
		return nil
	}
	out := make([]string, 0, len(bodies))
	for _, body := range bodies {
		out = append(out, header+body)
	}
	return out
}

// lineDiffCount counts the lines that differ between two versions of a file.
func lineDiffCount(before, after string) int {
	b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(b) != len(a) {
		return -1
	}
	changed := 0
	for i := range b {
		if b[i] != a[i] {
			changed++
		}
	}
	return changed
}

// TestProbeSkipsTheSitesThatCannotCompile runs every operator over the syntax
// an adversarial review found producing mutants the toolchain refuses, and
// pins the sites that are now refused syntactically.
//
// It matters because a mutant that does not compile makes `go test ./...` exit
// non-zero whatever the tests do, so the verifier says fail and the health
// check scores it killed. A verifier that did nothing but build the code would
// earn those kills for free, and the kill rate would be an upper bound rather
// than a measurement. What syntax cannot see — a subtraction on two string
// variables — Generate's build check catches instead.
func TestProbeSkipsTheSitesThatCannotCompile(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "probe.go.txt"))
	if err != nil {
		t.Fatalf("read the probe: %v", err)
	}
	mutations, err := mutate.Mutations("probe.go", src, mutate.AllOperators())
	if err != nil {
		t.Fatalf("Mutations: %v", err)
	}
	notes := make([]string, 0, len(mutations))
	for _, m := range mutations {
		notes = append(notes, m.Note)
	}
	all := strings.Join(notes, "\n")
	on := func(line int) []string {
		var out []string
		for _, note := range notes {
			if strings.HasPrefix(note, fmt.Sprintf("probe.go:%d:", line)) {
				out = append(out, note)
			}
		}
		return out
	}

	// probe.go:28 is `var table = [3]int{1, 2, 3}`. The three elements are
	// mutable; the length is part of the type, and bumping it stops every
	// value of the old type assigning to the new one.
	if got := on(28); len(got) != 3 {
		t.Errorf("line 28 offers %v, want only the three array elements", got)
	}
	// probe.go:32 is `func Table() [3]int { return table }` — the same trap in
	// a result type, plus a return whose zero value cannot be written.
	if got := on(32); len(got) != 0 {
		t.Errorf("line 32 offers %v, want nothing: the length is a type and the zero value is not a token", got)
	}
	// probe.go:15 is inside an iota block, whose specs repeat each other's
	// expression. The operators may swap the arithmetic; they may not bump a
	// literal, which would change the type or the value of every later spec.
	for _, note := range on(15) {
		if literalBump.MatchString(note) {
			t.Errorf("a literal inside an iota block was bumped: %s", note)
		}
	}
	// probe.go:40 is `prefix := "x-"`, used on the next line.
	if strings.Contains(all, "probe.go:40:2: remove the statement") {
		t.Error("a short variable declaration was offered for removal; every later use would be undefined")
	}
	// probe.go:104 is `func Status() Code { return 5 }` — a named type this
	// package cannot resolve, so the return is left alone. The literal itself
	// is still fair game.
	if strings.Contains(all, "probe.go:104:29: return 5") {
		t.Error("a return was zeroed through a type this package cannot resolve")
	}

	// And the sites that must still be found, so the refusals above are not
	// the operators giving up.
	for _, want := range []string{
		"probe.go:22:12: 3 -> 4",                 // a plain const
		"probe.go:35:27: 0x_FF -> 256",           // every base, answered in decimal
		"probe.go:35:35: 0o755 -> 494",           //
		"probe.go:35:43: 1_000 -> 1001",          //
		"probe.go:47:16: \"<\" -> \"<=\"",        // a loop boundary
		"probe.go:48:3: remove the statement",    // a plain assignment
		"probe.go:50:2: remove the statement",    // a call
		"probe.go:100:33: return 42 -> return 0", // a named single result
		"probe.go:83:41: \"==\" -> \"!=\"",       // an interface against nil
		"probe.go:104:29: 5 -> 6",                // the literal behind the skipped return
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the operators no longer find %q:\n%s", want, all)
		}
	}

	// Two results is a choice this operator does not make, and a type
	// parameter list is not a pair of relational operators.
	if strings.Contains(all, "probe.go:98:24: return") {
		t.Error("a multi-result return was zeroed")
	}
	for _, note := range on(88) {
		t.Errorf("the type parameter list at line 88 was mutated: %s", note)
	}
	// A switch head is not an if.
	for _, note := range on(73) {
		if strings.Contains(note, "negate") {
			t.Errorf("a switch was negated: %s", note)
		}
	}
}

// literalBump matches a const operator's note, which is the only one whose
// change is a bare number on both sides.
var literalBump = regexp.MustCompile(`^probe\.go:\d+:\d+: [0-9_a-fA-Fxo]+ -> \d+$`)
