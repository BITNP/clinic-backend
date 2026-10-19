package testutil

import (
	"time"
)

func FutureDate(days int) time.Time {
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour).Truncate(24 * time.Hour)
}
