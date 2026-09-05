The tests in `parse_test.go` fail. `Parse` reads a carrier's settings line,
a semicolon-separated list of `key=value` pairs, into a map. Three rules are
not being kept:

- only the *first* "=" of a segment separates the key from the value, so
  `note=split=allowed` maps "note" to "split=allowed";
- a segment that is empty or only spaces is skipped, so a trailing
  semicolon, a doubled semicolon and an empty line are all fine and yield no
  entry;
- a segment that carries no "=" at all is a fault: `Parse` returns a nil map
  and an error naming that segment. `errSegment` already builds that error.

A later pair with the same key replaces an earlier one, and an empty value
is a legal value. Fix `Parse` in `parse.go`. Do not change the test.
