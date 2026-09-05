// Package lru caches a fixed number of session records.
package lru

// Cache keeps at most a fixed number of records and drops the one that has
// gone longest without being used.
type Cache struct {
	max   int
	order []string // least recently used first
	vals  map[string]string
}

// New returns a cache holding at most max records.
func New(max int) *Cache {
	return &Cache{max: max, vals: map[string]string{}}
}

// touch moves k to the most recently used end.
func (c *Cache) touch(k string) {
	for i, o := range c.order {
		if o == k {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, k)
}

// Get returns the record stored under k. Reading a record counts as using
// it.
func (c *Cache) Get(k string) (string, bool) {
	v, ok := c.vals[k]
	return v, ok
}

// Put stores v under k. Writing a record counts as using it. When the cache
// is over its size, the record that has gone longest without being used is
// dropped.
func (c *Cache) Put(k, v string) {
	c.vals[k] = v
	c.touch(k)
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.vals, oldest)
	}
}
