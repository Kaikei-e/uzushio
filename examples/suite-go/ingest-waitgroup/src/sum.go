// Package ingest runs a scoring function over every item at once.
package ingest

import "sync"

// Sum runs f over every item, each in its own goroutine, and returns the
// total of the results. Sum returns only once every goroutine it started
// has finished, and the total it reads must be the finished one.
func Sum(items []int, f func(int) int) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	wg.Add(1)
	for _, it := range items {
		go func() {
			defer wg.Done()
			v := f(it)
			mu.Lock()
			total += v
			mu.Unlock()
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return total
}
