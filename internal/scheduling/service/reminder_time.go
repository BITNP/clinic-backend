package service

import "time"

// ReminderStartWindow returns the service-start interval due for discovery.
// The overlap tolerates small delays between successive minute scans.
func ReminderStartWindow(now time.Time, lead time.Duration) (time.Time, time.Time) {
	return now.Add(lead - 90*time.Second), now.Add(lead)
}
