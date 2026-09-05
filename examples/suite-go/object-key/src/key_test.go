package objectkey

import "testing"

func TestKey(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"two parts", []string{"bucket", "file.txt"}, "bucket/file.txt"},
		{"empty part", []string{"bucket", "", "file.txt"}, "bucket/file.txt"},
		{"stray separators", []string{"bucket/", "/file.txt"}, "bucket/file.txt"},
		{"leading separator", []string{"/bucket", "file.txt"}, "bucket/file.txt"},
		{"trailing separator", []string{"bucket", "dir/"}, "bucket/dir"},
		{"step back", []string{"bucket", "dir", "..", "file.txt"}, "bucket/file.txt"},
		{"dot part", []string{"bucket", ".", "file.txt"}, "bucket/file.txt"},
		{"nothing at all", nil, ""},
		{"one part", []string{"bucket"}, "bucket"},
		{"only separators", []string{"/", "/"}, ""},
	}
	for _, c := range cases {
		if got := Key(c.parts...); got != c.want {
			t.Errorf("%s: Key(%q) = %q, want %q", c.name, c.parts, got, c.want)
		}
	}
}
