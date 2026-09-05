package pagination

import (
	"reflect"
	"testing"
)

func TestPage(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		name     string
		cursor   int
		size     int
		want     []string
		wantNext int
	}{
		{"first page", 0, 2, []string{"a", "b"}, 2},
		{"middle page", 2, 2, []string{"c", "d"}, 4},
		{"short last page", 4, 2, []string{"e"}, -1},
		{"exact last page", 3, 2, []string{"d", "e"}, -1},
		{"whole slice at once", 0, 5, ids, -1},
		{"page larger than the slice", 0, 9, ids, -1},
		{"empty page at the end", 5, 2, []string{}, -1},
	}
	for _, c := range cases {
		got, next := Page(ids, c.cursor, c.size)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Page(ids, %d, %d) page = %v, want %v", c.name, c.cursor, c.size, got, c.want)
		}
		if next != c.wantNext {
			t.Errorf("%s: Page(ids, %d, %d) next = %d, want %d", c.name, c.cursor, c.size, next, c.wantNext)
		}
	}
}
