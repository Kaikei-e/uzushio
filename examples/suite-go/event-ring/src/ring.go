// Package eventring keeps the most recent events and drops the rest.
package eventring

// Ring keeps at most a fixed number of events, dropping the oldest to make
// room.
type Ring struct {
	buf  []string
	next int
	full bool
}

// New returns a ring that keeps n events.
func New(n int) *Ring { return &Ring{buf: make([]string, n)} }

// Push records one event.
func (r *Ring) Push(e string) {
	r.buf[r.next] = e
	r.next++
	if r.next == len(r.buf) {
		r.next = 0
		r.full = true
	}
}

// Events returns the events the ring is holding, oldest first.
func (r *Ring) Events() []string {
	if !r.full {
		return append([]string(nil), r.buf[:r.next]...)
	}
	return append([]string(nil), r.buf...)
}
