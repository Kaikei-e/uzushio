package callback

import (
	"net/url"
	"testing"
)

const base = "https://id.example/cb"

func check(t *testing.T, state, next string) {
	t.Helper()
	got := URL(base, state, next)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("URL(base, %q, %q) = %q, which does not parse: %v", state, next, got, err)
	}
	if u.Scheme+"://"+u.Host+u.Path != base {
		t.Errorf("%q points at another address", got)
	}
	q := u.Query()
	if len(q) != 2 || q.Get("state") != state || q.Get("next") != next {
		t.Errorf("%q carries %v, want state=%q and next=%q only", got, q, state, next)
	}
}

func TestURL(t *testing.T) {
	check(t, "plain", "/home")
	check(t, "a b&c", "/p?q=1")
	check(t, "x=1&y=2", "/a/b#frag")
	check(t, "", "")
	check(t, "100%", "/π/ページ")
}
