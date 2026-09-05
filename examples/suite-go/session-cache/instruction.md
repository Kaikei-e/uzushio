The tests in `cache_test.go` panic. `HasRole` answers a question about a
session that may not be there, and every way of it not being there has to
answer false rather than crash the handler asking:

- the cache pointer itself is nil;
- the cache was built as a zero value and holds no map;
- the id was never stored;
- the id was stored with a nil session.

A session that is present answers as it does today. Fix `HasRole` in
`cache.go`. Do not change the test.
