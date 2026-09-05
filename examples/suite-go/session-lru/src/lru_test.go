package lru

import "testing"

func has(t *testing.T, c *Cache, k, want string) {
	t.Helper()
	got, ok := c.Get(k)
	if !ok || got != want {
		t.Errorf("Get(%q) = (%q, %v), want (%q, true)", k, got, ok, want)
	}
}

func gone(t *testing.T, c *Cache, k string) {
	t.Helper()
	if got, ok := c.Get(k); ok {
		t.Errorf("Get(%q) = (%q, true), want the record to have been dropped", k, got)
	}
}

func TestReadingKeepsARecord(t *testing.T) {
	c := New(2)
	c.Put("a", "1")
	c.Put("b", "2")
	if _, ok := c.Get("a"); !ok {
		t.Fatal(`Get("a") lost the record before anything was evicted`)
	}
	c.Put("c", "3")
	has(t, c, "a", "1")
	has(t, c, "c", "3")
	gone(t, c, "b")
}

func TestWritingKeepsARecord(t *testing.T) {
	c := New(2)
	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("a", "9")
	c.Put("c", "3")
	has(t, c, "a", "9")
	has(t, c, "c", "3")
	gone(t, c, "b")
}

func TestOldestGoesFirst(t *testing.T) {
	c := New(2)
	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("c", "3")
	gone(t, c, "a")
	has(t, c, "b", "2")
	has(t, c, "c", "3")
}
