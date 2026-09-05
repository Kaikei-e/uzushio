package metrics

import (
	"sync"
	"testing"
)

func TestCounterUnderConcurrentUse(t *testing.T) {
	const (
		writers = 8
		reads   = 500
		perName = 500
	)
	c := New()

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perName; j++ {
				c.Inc("requests")
				c.Inc("bytes")
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < reads; j++ {
				_ = c.Get("requests")
			}
		}()
	}
	wg.Wait()

	if got := c.Get("requests"); got != writers*perName {
		t.Errorf(`Get("requests") = %d, want %d`, got, writers*perName)
	}
	if got := c.Get("bytes"); got != writers*perName {
		t.Errorf(`Get("bytes") = %d, want %d`, got, writers*perName)
	}
	if got := c.Get("never recorded"); got != 0 {
		t.Errorf(`Get("never recorded") = %d, want 0`, got)
	}
}
