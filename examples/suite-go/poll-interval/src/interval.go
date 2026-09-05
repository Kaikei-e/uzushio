// Package poll turns a configured polling rate into a wait between polls.
package poll

import "time"

// Interval returns how long to wait between two polls when the caller has
// asked for pollsPerMinute polls every minute. A rate that is not positive
// means "do not poll", and answers zero.
func Interval(pollsPerMinute int) time.Duration {
	if pollsPerMinute <= 0 {
		return 0
	}
	return time.Duration(60 / pollsPerMinute)
}
