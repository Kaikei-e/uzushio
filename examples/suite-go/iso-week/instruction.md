`TestLabel` fails around the turn of the year. `Label` reads the ISO week
number correctly but pairs it with the *calendar* year, and those two do
not always agree: 1 January 2021 is a Friday, and it belongs to week 53 of
2020, not to any week of 2021. The last days of December can likewise
belong to week 1 of the year after.

Report the week-numbering year that goes with the week. Fix `Label` in
`label.go`. Do not change the test.
