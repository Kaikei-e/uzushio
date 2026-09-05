`TestDayStart` fails. `DayStart` answers which working day a timestamp
belongs to for a site working in `loc`, and returns midnight at the start of
that day.

The timestamps come in from anywhere: recorded in UTC, recorded in another
zone, or already in `loc`. They all name the same instant, and the day that
instant belongs to is the day it falls on *as the clock in `loc` reads it* —
23:30 UTC is already the next day for a site nine hours ahead, and 02:15 UTC
is still the previous day for one five hours behind. The result must be in
`loc`.

Fix `DayStart` in `day.go`. Do not change the test.
