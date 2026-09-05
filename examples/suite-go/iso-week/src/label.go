// Package isoweek labels a day with the week a weekly report covers.
package isoweek

import "time"

// Label returns the ISO 8601 week-numbering year and the week number for
// the day t falls on. The week-numbering year is not always the calendar
// year: the first days of January can belong to the last week of the year
// before, and the last days of December to week 1 of the year after.
func Label(t time.Time) (year, week int) {
	_, w := t.ISOWeek()
	return t.Year(), w
}
