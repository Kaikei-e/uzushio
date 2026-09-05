package inventory

import "testing"

func TestReserve(t *testing.T) {
	cases := []struct {
		name  string
		stock int
		n     int
		want  int
		ok    bool
	}{
		{"part of the stock", 10, 3, 7, true},
		{"the whole stock", 10, 10, 0, true},
		{"more than there is", 10, 11, 10, false},
		{"nothing left", 0, 1, 0, false},
		{"zero units", 10, 0, 10, false},
		{"negative units", 10, -2, 10, false},
		{"one unit of one", 1, 1, 0, true},
	}
	for _, c := range cases {
		got, ok := Reserve(c.stock, c.n)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: Reserve(%d, %d) = (%d, %v), want (%d, %v)",
				c.name, c.stock, c.n, got, ok, c.want, c.ok)
		}
	}
}
