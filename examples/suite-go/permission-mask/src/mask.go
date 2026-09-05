// Package perm packs a document's permissions into one byte.
package perm

// Has reports whether the mask grants permission number n. Permissions are
// numbered from 0, so permission 0 is the lowest bit and permission 7 is
// the highest bit of the byte.
func Has(mask uint8, n int) bool {
	return mask&(1<<(n+1)) != 0
}

// Grant returns the mask with permission n added. Permissions already in
// the mask stay in it.
func Grant(mask uint8, n int) uint8 {
	return mask | 1<<(n+1)
}
