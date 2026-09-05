`TestInterval` fails. `Interval` is handed a rate in polls per minute and
must return the wait between two of them: 60 polls a minute is one second
apart, 1 poll a minute is one minute apart, 4 is fifteen seconds, and 7 is
a minute divided by seven with nothing lost to rounding.

A `time.Duration` counts nanoseconds, and the code is building one out of a
bare number of its own. Fix `Interval` in `interval.go`. A rate that is not
positive still answers zero. Do not change the test.
