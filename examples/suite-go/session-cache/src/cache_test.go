package session

import "testing"

func TestHasRoleKnownSession(t *testing.T) {
	c := New()
	c.Put(&Session{ID: "s1", Roles: []string{"reader", "writer"}})
	if !c.HasRole("s1", "writer") {
		t.Error(`HasRole("s1", "writer") = false, want true`)
	}
	if c.HasRole("s1", "admin") {
		t.Error(`HasRole("s1", "admin") = true, want false`)
	}
}

func TestHasRoleMissing(t *testing.T) {
	c := New()
	c.Put(&Session{ID: "s1", Roles: []string{"reader"}})
	c.byID["s2"] = nil

	if c.HasRole("unknown", "reader") {
		t.Error(`HasRole("unknown", "reader") = true, want false`)
	}
	if c.HasRole("s2", "reader") {
		t.Error(`HasRole("s2", "reader") = true, want false`)
	}

	var nilCache *Cache
	if nilCache.HasRole("s1", "reader") {
		t.Error("(*Cache)(nil).HasRole = true, want false")
	}

	empty := &Cache{}
	if empty.HasRole("s1", "reader") {
		t.Error("(&Cache{}).HasRole = true, want false")
	}
}
