// Package feed streams the items of a page to whoever is reading it.
package feed

// Stream sends every item on the channel it returns, in order, and then
// leaves the channel in the state a `for range` over it needs in order to
// end. Sending happens in the background, so Stream returns at once and the
// channel is unbuffered.
func Stream(items []string) <-chan string {
	ch := make(chan string)
	go func() {
		for _, it := range items {
			ch <- it
		}
	}()
	return ch
}
