// Package metrics counts events by name for a server that handles many
// requests at once.
package metrics

// Counter counts events by name. Handlers running in different goroutines
// record into the same counter and read from it while others are recording,
// so every exported method must be safe to call concurrently.
type Counter struct {
	n map[string]int
}

// New returns an empty counter.
func New() *Counter { return &Counter{n: map[string]int{}} }

// Inc records one event called name.
func (c *Counter) Inc(name string) {
	c.n[name]++
}

// Get returns how many events called name have been recorded.
func (c *Counter) Get(name string) int {
	return c.n[name]
}
