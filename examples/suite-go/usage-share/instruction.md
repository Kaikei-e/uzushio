`TestShare` fails: `Share(1, 2)` answers 0 instead of 50. The division is
being done in whole numbers, so everything below 100% collapses to a
multiple of 100.

Make `Share` in `share.go` compute the percentage in floating point, so
that 1 of 3 is 33.333…, 3 of 4 is 75, and 7 of 2 is 350. A total of zero
still answers 0 rather than dividing by zero. Do not change the test.
