// Package ledger converts posted amounts into the whole cents a ledger
// stores.
package ledger

// ToCents converts an amount expressed in currency units into whole cents.
// A value exactly halfway between two cents rounds away from zero.
func ToCents(amount float64) int64 {
	return int64(amount * 100)
}
