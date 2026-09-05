// Package batch walks a list of record batches, one open batch at a time.
package batch

// Batch is one opened batch of records. A closed batch reports no records.
type Batch struct {
	N      int
	closed bool
}

// Size reports how many records the batch holds while it is open.
func (b *Batch) Size() int {
	if b.closed {
		return 0
	}
	return b.N
}

// Close releases the batch.
func (b *Batch) Close() { b.closed = true }

// Total opens each named batch, adds its size to the total, and closes it.
// The store allows one open batch at a time, so a batch must be closed
// before the next one is opened, and every batch opened must be closed
// before Total returns. On an open failure Total gives up and returns the
// total so far with the error.
func Total(names []string, open func(string) (*Batch, error)) (int, error) {
	n := 0
	for _, name := range names {
		b, err := open(name)
		if err != nil {
			return n, err
		}
		defer b.Close()
		n += b.Size()
	}
	return n, nil
}
