package service

import "time"

// ReminderStartWindow returns the service-start interval due for discovery.
// The overlap tolerates small delays between successive minute scans.
func ReminderStartWindow(now time.Time, lead time.Duration) (time.Time, time.Time) {
	return now.Add(lead - 90*time.Second), now.Add(lead)
}

// ReminderCompletionWindow returns the service-end interval due for completion
// discovery. The bounded lookback lets a briefly unavailable worker catch up
// without repeatedly scanning the full service history.
func ReminderCompletionWindow(now time.Time, delay time.Duration) (time.Time, time.Time) {
	until := now.UTC().Add(-delay)
	return until.Add(-24 * time.Hour), until
}
