// Package hello is the smallest possible target for a CMoA run: one
// function with a bug that the test catches.
package hello

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a - b
}
