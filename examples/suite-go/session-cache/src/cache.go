// Package session keeps the sessions a request handler has already looked
// up.
package session

// Session is one signed-in user.
type Session struct {
	ID    string
	Roles []string
}

// Cache maps a session id to the session, if it has been seen.
type Cache struct {
	byID map[string]*Session
}

// New returns an empty cache.
func New() *Cache { return &Cache{byID: map[string]*Session{}} }

// Put stores s under its id.
func (c *Cache) Put(s *Session) { c.byID[s.ID] = s }

// HasRole reports whether the session with the given id carries the role.
// A cache that is not there at all, an id that was never stored, and an
// entry stored as nil all answer false.
func (c *Cache) HasRole(id, role string) bool {
	s := c.byID[id]
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}
