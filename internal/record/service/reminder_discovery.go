package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
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
	Journal  *journal.Service
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
	operationID := ""
	if d.Journal != nil {
		var err error
		operationID, err = journal.NewOperationID()
		if err != nil {
			return 0, fmt.Errorf("prepare dingtalk reminder journal: %w", err)
		}
	}

	var created int64
	err := database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(
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
				   AND clinic_record_dingtalk_reminder.sent_at IS NULL
				 RETURNING id, record_id, service_date_id`,
				datePredicate,
			),
			now,
			reminderStatusPending,
			until,
			recordmodel.RecordStatusConfirmed,
			reminderStatusCancelled,
		)
		if err != nil {
			return fmt.Errorf("discover dingtalk ticket reminders: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var reminderID, recordID, serviceDateID uint
			if err := rows.Scan(&reminderID, &recordID, &serviceDateID); err != nil {
				return fmt.Errorf("scan discovered dingtalk reminder: %w", err)
			}
			created++
			if d.Journal == nil {
				continue
			}
			eventID, err := journal.InsertTx(ctx, tx, journal.EventInput{
				OccurredAt:  now,
				ActorType:   journal.ActorSystem,
				ActorID:     "dingtalk_reminder_worker",
				Action:      "notification.requested",
				EntityType:  "record",
				EntityID:    fmt.Sprint(recordID),
				OperationID: operationID,
				Details: map[string]any{
					"version":         1,
					"reminder_id":     reminderID,
					"kind":            "record_service_window",
					"channel":         "dingtalk",
					"recipient":       map[string]any{"type": "customer"},
					"record_id":       recordID,
					"service_date_id": serviceDateID,
					"next_attempt_at": now.Format(time.RFC3339Nano),
				},
			})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(
				ctx,
				"UPDATE clinic_record_dingtalk_reminder SET requested_event_id = $1 WHERE id = $2",
				eventID,
				reminderID,
			); err != nil {
				return fmt.Errorf("link dingtalk reminder %d requested event: %w", reminderID, err)
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("discover dingtalk ticket reminders: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return created, nil
}
