// Package backoff spaces out retries of a failing call.
package backoff

import "time"

// Delay returns how long to wait before retry number attempt. Attempt 0
// waits base, and every further attempt waits twice as long as the one
// before, up to max. The result is never above max and never below base,
// however large the caller's attempt number is.
func Delay(base, max time.Duration, attempt int) time.Duration {
	d := base << attempt
	if d > max {
		d = max
	}
	return d
}
