package calendar

import (
	"time"
)

// DefaultServiceDateLocation returns UTC+8, used when no timezone is supplied.
func DefaultServiceDateLocation() *time.Location {
	return time.FixedZone("UTC+8", 8*60*60)
}

// DateInLocation returns the local calendar day of t in loc as 00:00:00 UTC.
// This treats the stored date as a wall-clock date in the configured clinic
// timezone; for example 2026-07-20 00:00:00+00:00 is interpreted as the UTC+8
// date 2026-07-20, regardless of any offset on the input.
func DateInLocation(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = DefaultServiceDateLocation()
	}
	lt := t.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.UTC)
}
