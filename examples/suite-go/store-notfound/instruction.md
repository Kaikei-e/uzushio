`TestLookupOrMissingUsesTheDefault` fails. `LookupOr` should answer with
its default when the record is missing, and pass every other failure back
to its caller. `Lookup` returns its failures *wrapped*, with the id folded
into the message, so the sentinel is not the error value that comes back —
it is inside it.

Fix `LookupOr` in `store.go` so a missing record uses the default while an
empty id still comes back as an error. Do not change the test.
