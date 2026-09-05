// Package pagination cuts a slice of ids into cursor-addressed pages.
package pagination

// Page returns at most size ids starting at cursor, together with the cursor
// the caller passes back to get the following page. When the page reaches
// the end of ids there is no following page and the next cursor is -1.
func Page(ids []string, cursor, size int) ([]string, int) {
	end := cursor + size
	if end > len(ids) {
		end = len(ids)
	}
	next := end
	if next > len(ids) {
		next = -1
	}
	return ids[cursor:end], next
}
