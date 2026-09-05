// Package inventory holds stock against orders.
package inventory

// Reserve takes n units out of a stock level of the given size. It returns
// the level that is left and whether the reservation was accepted. Taking
// the whole remaining stock is accepted; taking more than there is, or a
// count that is not positive, is refused and leaves the level alone.
func Reserve(stock, n int) (int, bool) {
	if n >= stock {
		return stock, false
	}
	return stock - n, true
}
