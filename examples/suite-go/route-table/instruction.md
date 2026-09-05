`TestLookupIgnoresMethodCase` fails. A route is registered under a method
and a path, and HTTP methods are matched without regard to case: a route
added as "GET" must be found by "get" or "Get", and a route added as "post"
must be found by "POST". Paths are matched exactly, so a route added for
"/Orders" must *not* be found by "/orders".

The two sides currently build their key differently. Fix `table.go` so that
`Add` and `Lookup` agree, folding the method's case and leaving the path
alone. Do not change the test.
