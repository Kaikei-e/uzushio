`TestSection` fails from the second section on. The renderer keeps one
builder and writes every section into it, so the second call returns the
first section followed by the second, and the third returns all three.

A call to `Section` must return that section and nothing else, whether the
renderer has rendered sections before or not — a renderer that has already
been used has to give the same answer as a fresh one. Fix `Section` in
`render.go`. Do not change the test.
