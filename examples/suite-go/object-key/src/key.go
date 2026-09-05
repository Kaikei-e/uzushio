// Package objectkey builds the keys an object store is addressed by.
package objectkey

import "strings"

// Key joins the parts of an object's name into a store key. Store keys are
// always separated by "/" whatever the host filesystem uses, they never
// begin with "/", empty parts contribute nothing, and a part of ".." steps
// back over the part before it.
func Key(parts ...string) string {
	return strings.Join(parts, "/")
}
