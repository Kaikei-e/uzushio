`TestToCents` fails. `ToCents` turns an amount in currency units into whole
cents, and a ledger may not lose a fraction of a cent by dropping it: an
amount that lands exactly halfway between two cents belongs to the cent
further from zero, in both directions. So 0.125 is 13 cents and -0.125 is
-13 cents, while amounts that are not halfway simply go to the nearest
cent.

Make `ToCents` in `rounding.go` behave that way. Do not change the test.
