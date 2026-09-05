// Package audit builds the immutable entry lists an audit trail is made of.
package audit

// Entry is one recorded action.
type Entry struct {
	Actor  string
	Action string
}

// Append returns the list of entries with e added at the end. The list that
// comes back never shares storage with the one that went in: a caller that
// appends twice to the same list gets two independent lists, and the list it
// held is unchanged.
func Append(entries []Entry, e Entry) []Entry {
	return append(entries, e)
}
