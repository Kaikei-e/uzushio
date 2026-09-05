`TestReadingKeepsARecord` fails. The cache evicts the record that has gone
longest without being *used*, and reading a record is using it: a record
read a moment ago must outlive one that was written earlier and not touched
since.

`Put` already refreshes a record. `Get` does not, so the cache is really
evicting in arrival order. Fix `Get` in `lru.go` so that a successful read
counts as a use, while a read that finds nothing changes nothing. Do not
change the test.
