package service_test

import (
	schedulingservice "clinic-backend/internal/scheduling/service"
	"testing"
	"time"
)

func TestReminderStartWindowAcrossMidnight(t *testing.T) {
	loc := time.FixedZone("clinic", 8*60*60)
	now := time.Date(2026, 11, 12, 23, 59, 30, 0, loc)
	from, until := schedulingservice.ReminderStartWindow(now, time.Hour)
	if !from.Equal(time.Date(2026, 11, 13, 0, 58, 0, 0, loc)) || !until.Equal(time.Date(2026, 11, 13, 0, 59, 30, 0, loc)) {
		t.Fatalf("window = [%v, %v)", from, until)
	}
}
