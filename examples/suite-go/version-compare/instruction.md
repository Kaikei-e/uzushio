`TestCompare` fails. `Compare` compares the two versions as plain text, so
"1.10.0" sorts before "1.9.0" — as text "1" is less than "9" — and
"10.0.0" sorts before "2.0.0".

Compare the three components as numbers instead, most significant first:
major, then minor, then patch, stopping at the first that differs. Return
-1, 0 or 1 as documented. Every version handed in has all three components
and each is a plain decimal number. Fix `Compare` in `compare.go`. Do not
change the test.
