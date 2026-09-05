package csvimport

import "testing"

func TestClean(t *testing.T) {
	const bom = "\ufeff"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already clean", "order_id", "order_id"},
		{"surrounding spaces", "  order_id  ", "order_id"},
		{"tabs and newline", "\torder_id\r\n", "order_id"},
		{"byte order mark", bom + "order_id", "order_id"},
		{"byte order mark and spaces", bom + " order_id ", "order_id"},
		{"inner spaces kept", "Ada  Lovelace", "Ada  Lovelace"},
		{"inner spaces kept after trimming", "  Ada Lovelace\t", "Ada Lovelace"},
		{"whitespace only", " \t\n", ""},
		{"empty", "", ""},
		{"mark only", bom, ""},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("%s: Clean(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
