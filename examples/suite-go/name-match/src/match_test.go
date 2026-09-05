package namematch

import "testing"

func TestSame(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Ada Lovelace", "ada lovelace", true},
		{"ADA LOVELACE", "Ada Lovelace", true},
		{"  Ada  ", "Ada", true},
		{"Ada", "  Ada ", true},
		{"\tAda\n", "Ada", true},
		{"Ada", "Adam", false},
		{"Ada Lovelace", "Ada  Lovelace", false},
		{"ΣΙΣΥΦΟΣ", "σισυφος", true},
		{"Οδυσσεύς", "Οδυσσεύσ", true},
		{"ς", "Σ", true},
		{"ẞ", "ß", true},
		{"ı", "I", false},
		{"", "", true},
		{"   ", "", true},
	}
	for _, c := range cases {
		if got := Same(c.a, c.b); got != c.want {
			t.Errorf("Same(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
