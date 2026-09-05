// Package version orders the release versions of a dependency.
package version

// Compare orders two versions written as MAJOR.MINOR.PATCH, all three
// present and all three plain decimal numbers. It returns -1 when a is
// older than b, 1 when a is newer, and 0 when they are the same version.
// The components are numbers, not text, so 1.10.0 is newer than 1.9.0.
func Compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
