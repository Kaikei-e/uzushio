// Package shift works out which working day a timestamp belongs to.
package shift

import "time"

// DayStart returns midnight at the beginning of the calendar day that t
// falls on for a site working in loc. The instant t is the same instant
// whatever zone it was recorded in, so the day it belongs to is the one it
// falls on as loc reads the clock.
func DayStart(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
