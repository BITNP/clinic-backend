package service

import (
	"context"
	"fmt"
	"time"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
)

const (
	reminderStatusPending   = "pending"
	reminderStatusSending   = "sending"
	reminderStatusSent      = "sent"
	reminderStatusCancelled = "cancelled"
)

// TicketReminderDiscovery inserts one durable reminder job when a confirmed
// ticket first enters the configured pre-shift window.
type TicketReminderDiscovery struct {
	DB       *database.DB
	LeadTime time.Duration
}

// Discover materializes due reminder jobs. The unique record/service-date
// identity makes repeated calls idempotent.
func (d *TicketReminderDiscovery) Discover(ctx context.Context, now time.Time) (int64, error) {
	now = now.UTC()
	until := now.Add(d.LeadTime)
	datePredicate := "d.date = r.appointment_time"
	if d.DB.Driver == database.DriverSQLite {
		datePredicate = "date(d.date) = date(r.appointment_time)"
	}
	result, err := d.DB.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO clinic_record_dingtalk_reminder
			 (record_id, service_date_id, next_attempt_at, attempts, status, claim_token, last_error, created_at, updated_at)
			 SELECT r.id, d.id, $1, 0, $2, '', '', $1, $1
			 FROM clinic_record r
			 JOIN clinic_service_date d ON d.room_id = r.room AND %s
			 WHERE d.start_time > $1
			   AND d.start_time <= $3
			   AND r.status = $4
			   AND COALESCE(TRIM(r."user"), '') <> ''
			 ON CONFLICT (record_id, service_date_id) DO UPDATE SET
			   status = excluded.status,
			   attempts = 0,
			   next_attempt_at = excluded.next_attempt_at,
			   lease_until = NULL,
			   claim_token = '',
			   last_error = '',
			   updated_at = excluded.updated_at
			 WHERE clinic_record_dingtalk_reminder.status = $5
			   AND clinic_record_dingtalk_reminder.sent_at IS NULL`,
			datePredicate,
		),
		now,
		reminderStatusPending,
		until,
		recordmodel.RecordStatusConfirmed,
		reminderStatusCancelled,
	)
	if err != nil {
		return 0, fmt.Errorf("discover dingtalk ticket reminders: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count dingtalk ticket reminders: %w", err)
	}
	return created, nil
}
