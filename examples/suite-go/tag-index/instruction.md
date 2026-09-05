The tests panic. `Index` is documented as usable straight out of its zero
value — callers write `var ix Index` and start adding — but `Add` writes
into a map that nothing has built, and writing to a nil map panics.

Make `Add` in `index.go` build whatever it needs the first time it is
called, and keep on appending after that: ids stay in the order they were
added, and a tag that was never added still reads back as nothing. Reading
before writing must not break the writing that follows. Do not change the
test.
