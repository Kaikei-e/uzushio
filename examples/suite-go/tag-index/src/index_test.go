package tagindex

import (
	"reflect"
	"testing"
)

func TestZeroIndexAccepts(t *testing.T) {
	var ix Index
	ix.Add("go", "d1")
	ix.Add("go", "d2")
	ix.Add("docs", "d3")

	if got, want := ix.IDs("go"), []string{"d1", "d2"}; !reflect.DeepEqual(got, want) {
		t.Errorf(`IDs("go") = %q, want %q`, got, want)
	}
	if got, want := ix.IDs("docs"), []string{"d3"}; !reflect.DeepEqual(got, want) {
		t.Errorf(`IDs("docs") = %q, want %q`, got, want)
	}
	if got := ix.IDs("never used"); len(got) != 0 {
		t.Errorf(`IDs("never used") = %q, want nothing`, got)
	}
}

func TestReadingFirstDoesNotBreakWriting(t *testing.T) {
	var ix Index
	if got := ix.IDs("go"); len(got) != 0 {
		t.Fatalf(`IDs on an empty index = %q, want nothing`, got)
	}
	ix.Add("go", "d1")
	if got, want := ix.IDs("go"), []string{"d1"}; !reflect.DeepEqual(got, want) {
		t.Errorf(`IDs("go") = %q, want %q`, got, want)
	}
}
