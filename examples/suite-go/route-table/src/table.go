// Package routes resolves a request's method and path to a handler name.
package routes

// Table holds the registered routes, keyed by method and path.
type Table struct {
	byRoute map[string]string
}

// New returns an empty table.
func New() *Table { return &Table{byRoute: map[string]string{}} }

// Add registers handler for the given method and path. The method is
// matched without regard to case; the path is matched exactly.
func (t *Table) Add(method, path, handler string) {
	t.byRoute[method+" "+path] = handler
}

// Lookup returns the handler registered for the method and path.
func (t *Table) Lookup(method, path string) (string, bool) {
	h, ok := t.byRoute[method+" "+path]
	return h, ok
}
