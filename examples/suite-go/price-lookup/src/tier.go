// Package pricing picks the volume tier a quantity is charged at.
package pricing

// Tier returns the index of the tier a quantity is charged at. mins holds
// each tier's minimum quantity, sorted ascending; the tier that applies is
// the last one whose minimum is at or below qty. A quantity below the first
// tier's minimum, and an empty tier list, answer -1.
//
// The list is long enough that it is searched rather than scanned.
func Tier(mins []int, qty int) int {
	lo, hi := 0, len(mins)-1
	best := -1
	for lo < hi {
		mid := (lo + hi) / 2
		if mins[mid] <= qty {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}
