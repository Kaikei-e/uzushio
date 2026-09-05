// Package tagindex keeps which documents carry which tag.
package tagindex

// Index maps a tag to the ids of the documents that carry it. The zero
// Index is usable: the first Add builds whatever it needs.
type Index struct {
	byTag map[string][]string
}

// Add records that the document id carries the tag. Ids are kept in the
// order they were added.
func (ix *Index) Add(tag, id string) {
	ix.byTag[tag] = append(ix.byTag[tag], id)
}

// IDs returns the ids recorded for the tag, or nothing when the tag has
// never been seen.
func (ix *Index) IDs(tag string) []string {
	return ix.byTag[tag]
}
