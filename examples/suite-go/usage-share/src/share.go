// Package usage turns raw counters into the percentages a dashboard shows.
package usage

// Share returns used as a percentage of total. A total of zero has no
// share, and answers 0 rather than a division by zero.
func Share(used, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(used / total * 100)
}
