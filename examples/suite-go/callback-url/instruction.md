`TestURL` fails. `URL` pastes the two values straight into the query, so a
value carrying a space, an "&", an "=" or a "#" changes the shape of the
URL: the parsed result ends up with three parameters, or with a value cut
short at the character that was not escaped.

Build the query so that the URL always parses back into exactly the two
parameters it was given, each with its value unchanged, whatever the value
contains. Fix `URL` in `callback.go`. Do not change the test.
