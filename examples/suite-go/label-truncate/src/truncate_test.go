package label

import "testing"

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than the limit", "abc", 5, "abc"},
		{"exactly the limit", "abcde", 5, "abcde"},
		{"one over the limit", "abcdef", 5, "abcde…"},
		{"multibyte, exactly the limit", "日本語", 3, "日本語"},
		{"multibyte, over the limit", "日本語のラベル", 3, "日本語…"},
		{"multibyte, one under the limit", "日本語", 4, "日本語"},
		{"mixed script", "ab日本語", 3, "ab日…"},
		{"limit of zero", "abc", 0, "…"},
		{"empty label", "", 3, ""},
		{"combining-free accents", "café au lait", 4, "café…"},
	}
	for _, c := range cases {
		if got := Truncate(c.in, c.n); got != c.want {
			t.Errorf("%s: Truncate(%q, %d) = %q, want %q", c.name, c.in, c.n, got, c.want)
		}
	}
}
