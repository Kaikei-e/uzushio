package export

import "testing"

func TestRow(t *testing.T) {
	cases := []struct {
		name   string
		fields []string
		want   string
	}{
		{"plain", []string{"a", "b"}, "a,b\r\n"},
		{"nothing to quote", []string{"Ada Lovelace", "1250"}, "Ada Lovelace,1250\r\n"},
		{"comma", []string{"a,b", "c"}, "\"a,b\",c\r\n"},
		{"quote", []string{`he said "hi"`}, "\"he said \"\"hi\"\"\"\r\n"},
		{"quote and comma", []string{`a,"b"`, "c"}, "\"a,\"\"b\"\"\",c\r\n"},
		{"newline", []string{"line1\nline2"}, "\"line1\nline2\"\r\n"},
		{"carriage return", []string{"a\rb"}, "\"a\rb\"\r\n"},
		{"empty fields", []string{"", ""}, ",\r\n"},
		{"no fields", nil, "\r\n"},
	}
	for _, c := range cases {
		if got := Row(c.fields); got != c.want {
			t.Errorf("%s: Row(%q) = %q, want %q", c.name, c.fields, got, c.want)
		}
	}
}
