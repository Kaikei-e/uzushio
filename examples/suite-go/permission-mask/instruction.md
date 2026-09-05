`TestGrantAndHas` fails. Permissions are numbered from 0, and permission n
is bit n of the byte: permission 0 is the lowest bit, worth 1, and
permission 7 is the highest, worth 128. `Grant(0, n)` must therefore give
exactly `1 << n`, and `Has` must ask about exactly that bit and no other.

Both functions are currently one bit out, which also loses permission 7 off
the top of the byte. Fix them in `mask.go`. Do not change the test.
