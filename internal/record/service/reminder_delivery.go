package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
	recordmodel "clinic-backend/internal/record/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

const (
	maxTicketReminderAttempts = 5
	ticketReminderLease       = time.Minute
	ticketReminderMaxBackoff  = 10 * time.Minute
)

// TicketReminderDelivery claims and delivers persisted DingTalk ticket
// reminders.
type TicketReminderDelivery struct {
	DB       *database.DB
	Notifier TicketStatusNotifier
	Location *time.Location
	Journal  *journal.Service
}

// Claim leases one due reminder for this worker.
func (d *TicketReminderDelivery) Claim(ctx context.Context, now time.Time) (*recordmodel.ClinicRecordDingTalkReminder, error) {
	now = now.UTC()
	token, err := newTicketReminderClaimToken()
	if err != nil {
		return nil, err
	}

	var job recordmodel.ClinicRecordDingTalkReminder
	journaled := false
	err = database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
		query := "SELECT " + recordDingTalkReminderColumns +
			" FROM clinic_record_dingtalk_reminder" +
			" WHERE (status = $1 AND next_attempt_at <= $2)" +
			" OR (status = $3 AND lease_until <= $4)" +
			" ORDER BY next_attempt_at, id LIMIT 1"
		if d.DB.Driver == database.DriverPostgres {
			query += " FOR UPDATE SKIP LOCKED"
		}
		var err error
		job, err = scanRecordDingTalkReminder(tx.QueryRow(
			query,
			reminderStatusPending,
			now,
			reminderStatusSending,
			now,
		))
		if err != nil {
			return err
		}
		if job.Attempts >= maxTicketReminderAttempts {
			if _, err := tx.Exec(
				`UPDATE clinic_record_dingtalk_reminder
				 SET status = $1, lease_until = NULL, claim_token = '', last_error = $2, updated_at = $3
				 WHERE id = $4`,
				reminderStatusCancelled,
				"retry limit reached",
				now,
				job.ID,
			); err != nil {
				return fmt.Errorf("cancel exhausted reminder %d: %w", job.ID, err)
			}
			if d.Journal != nil {
				operationID, err := journal.NewOperationID()
				if err != nil {
					return fmt.Errorf("prepare exhausted reminder journal: %w", err)
				}
				if _, err := journal.InsertTx(ctx, tx, journal.EventInput{
					OccurredAt:      now,
					ActorType:       journal.ActorSystem,
					ActorID:         "dingtalk_reminder_worker",
					Action:          "notification.failed",
					EntityType:      "record",
					EntityID:        fmt.Sprint(job.RecordID),
					OperationID:     operationID,
					CausedByEventID: job.RequestedEventID,
					Details: map[string]any{
						"version":     1,
						"reminder_id": job.ID,
						"channel":     "dingtalk",
						"attempt":     job.Attempts,
						"reason":      "retry_limit_reached",
					},
				}); err != nil {
					return err
				}
				journaled = true
			}
			job.ID = 0
			return nil
		}

		previousStatus := job.Status
		lease := now.Add(ticketReminderLease)
		if _, err := tx.Exec(
			`UPDATE clinic_record_dingtalk_reminder
			 SET status = $1, lease_until = $2, claim_token = $3, attempts = attempts + 1, updated_at = $4
			 WHERE id = $5`,
			reminderStatusSending,
			lease,
			token,
			now,
			job.ID,
		); err != nil {
			return fmt.Errorf("claim reminder %d: %w", job.ID, err)
		}
		if d.Journal != nil {
			operationID, err := journal.NewOperationID()
			if err != nil {
				return fmt.Errorf("prepare dingtalk reminder attempt journal: %w", err)
			}
			reason := "scheduled"
			if previousStatus == reminderStatusSending {
				reason = "lease_expired"
			}
			eventID, err := journal.InsertTx(ctx, tx, journal.EventInput{
				OccurredAt:      now,
				ActorType:       journal.ActorSystem,
				ActorID:         "dingtalk_reminder_worker",
				Action:          "notification.attempt_started",
				EntityType:      "record",
				EntityID:        fmt.Sprint(job.RecordID),
				OperationID:     operationID,
				CausedByEventID: job.RequestedEventID,
				Details: map[string]any{
					"version":     1,
					"reminder_id": job.ID,
					"channel":     "dingtalk",
					"attempt":     job.Attempts + 1,
					"reason":      reason,
				},
			})
			if err != nil {
				return err
			}
			journaled = true
			if _, err := tx.ExecContext(
				ctx,
				"UPDATE clinic_record_dingtalk_reminder SET attempt_event_id = $1 WHERE id = $2",
				eventID,
				job.ID,
			); err != nil {
				return fmt.Errorf("link reminder %d attempt event: %w", job.ID, err)
			}
			job.AttemptEventID = &eventID
		}
		job.Status = reminderStatusSending
		job.LeaseUntil = &lease
		job.ClaimToken = token
		job.Attempts++
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim dingtalk ticket reminder: %w", err)
	}
	if journaled {
		bumpJournalSync(ctx, d.Journal, "dingtalk reminder claim")
	}
	if job.ID == 0 {
		return nil, nil
	}
	return &job, nil
}

// Deliver rechecks the ticket and service window, then sends one reminder.
func (d *TicketReminderDelivery) Deliver(
	ctx context.Context,
	job *recordmodel.ClinicRecordDingTalkReminder,
	now time.Time,
) error {
	now = now.UTC()
	cancelJob := func(reason string) error {
		return d.finish(
			ctx,
			job,
			reminderStatusCancelled,
			time.Time{},
			"",
			"notification.cancelled",
			map[string]any{"reason": reason},
		)
	}
	var record recordmodel.ClinicRecord
	var date schedulingmodel.ClinicServiceDate
	var roomName, roomAddress string
	var roomID sql.NullInt64
	err := d.DB.QueryRowContext(
		ctx,
		`SELECT r.id, COALESCE(r."user", ''), r.status, r.appointment_time, r.room,
		        d.id, d.room_id, d.date, d.start_time, d.end_time, d.title,
		        room.name, room.address
		 FROM clinic_record r
		 JOIN clinic_service_date d ON d.id = $1
		 JOIN clinic_room room ON room.id = r.room
		 WHERE r.id = $2`,
		job.ServiceDateID,
		job.RecordID,
	).Scan(
		&record.ID,
		&record.User,
		&record.Status,
		&record.AppointmentTime,
		&record.RoomID,
		&date.ID,
		&roomID,
		&date.Date,
		&date.StartTime,
		&date.EndTime,
		&date.Title,
		&roomName,
		&roomAddress,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cancelJob("record_or_service_date_missing")
	}
	if err != nil {
		return d.retry(ctx, job, now, time.Time{}, fmt.Errorf("load reminder ticket: %w", err), "reminder_context_unavailable")
	}
	if roomID.Valid {
		id := uint(roomID.Int64)
		date.RoomID = &id
	}
	loc := d.Location
	if loc == nil {
		loc = calendar.DefaultServiceDateLocation()
	}
	if record.Status != recordmodel.RecordStatusConfirmed {
		return cancelJob("record_no_longer_confirmed")
	}
	if date.RoomID == nil || *date.RoomID != record.RoomID ||
		!calendar.DateInLocation(date.Date, loc).Equal(calendar.DateInLocation(record.AppointmentTime, loc)) {
		return cancelJob("service_date_changed")
	}
	if !now.Before(date.StartTime) {
		return cancelJob("service_already_started")
	}
	if strings.TrimSpace(record.User) == "" {
		return cancelJob("recipient_address_missing")
	}
	if d.Notifier == nil {
		return d.retry(ctx, job, now, date.StartTime, fmt.Errorf("dingtalk notifier unavailable"), "notifier_unavailable")
	}
	err = d.Notifier.NotifyReminder(ctx, TicketStatusNotification{
		RecordID:     record.ID,
		User:         record.User,
		Status:       record.Status,
		RoomName:     roomName,
		RoomAddress:  roomAddress,
		ServiceStart: date.StartTime,
		ServiceEnd:   date.EndTime,
	})
	if err != nil {
		return d.retry(ctx, job, now, date.StartTime, err, "dingtalk_delivery_failed")
	}
	return d.finish(
		ctx,
		job,
		reminderStatusSent,
		now,
		"",
		"notification.sent",
		map[string]any{
			"channel":      "dingtalk",
			"recipient":    map[string]any{"type": "customer"},
			"confirmation": "message_center_send_success",
		},
	)
}

func (d *TicketReminderDelivery) retry(
	ctx context.Context,
	job *recordmodel.ClinicRecordDingTalkReminder,
	now time.Time,
	serviceStart time.Time,
	cause error,
	errorCode string,
) error {
	delay := time.Minute
	for n := 1; n < job.Attempts && delay < ticketReminderMaxBackoff; n++ {
		delay *= 2
	}
	if delay > ticketReminderMaxBackoff {
		delay = ticketReminderMaxBackoff
	}
	next := now.Add(delay)
	status := reminderStatusPending
	eventAction := "notification.retry_scheduled"
	reason := "gateway_delivery_failed"
	if job.Attempts >= maxTicketReminderAttempts {
		status = reminderStatusCancelled
		eventAction = "notification.failed"
		reason = "retry_limit_reached"
	} else if !serviceStart.IsZero() && !next.Before(serviceStart) {
		status = reminderStatusCancelled
		eventAction = "notification.failed"
		reason = "retry_deadline_reached"
	}
	details := map[string]any{
		"channel": "dingtalk",
		"reason":  reason,
		"error":   map[string]any{"code": errorCode, "message": "The DingTalk reminder delivery did not complete."},
	}
	if status == reminderStatusPending {
		details["next_attempt_at"] = next.UTC().Format(time.RFC3339Nano)
	}
	if err := d.finish(ctx, job, status, next, cause.Error(), eventAction, details); err != nil {
		return err
	}
	return cause
}

func (d *TicketReminderDelivery) finish(
	ctx context.Context,
	job *recordmodel.ClinicRecordDingTalkReminder,
	status string,
	nextAttemptAt time.Time,
	lastError string,
	eventAction string,
	eventDetails map[string]any,
) error {
	sets := []string{
		"status = $1",
		"last_error = $2",
		"updated_at = $3",
		"lease_until = NULL",
		"claim_token = ''",
	}
	args := []any{status, lastError, time.Now().UTC()}
	if !nextAttemptAt.IsZero() {
		args = append(args, nextAttemptAt)
		sets = append(sets, fmt.Sprintf("next_attempt_at = $%d", len(args)))
	}
	if status == reminderStatusSent {
		args = append(args, time.Now().UTC())
		sets = append(sets, fmt.Sprintf("sent_at = $%d", len(args)))
	}
	args = append(args, job.ID, reminderStatusSending, job.ClaimToken)
	idPos := len(args) - 2
	query := "UPDATE clinic_record_dingtalk_reminder SET " + strings.Join(sets, ", ") +
		fmt.Sprintf(
			" WHERE id = $%d AND status = $%d AND claim_token = $%d",
			idPos,
			idPos+1,
			idPos+2,
		)
	err := database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("finish reminder %d: %w", job.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check reminder %d update: %w", job.ID, err)
		}
		if affected != 1 {
			return fmt.Errorf("reminder %d claim expired", job.ID)
		}
		if d.Journal == nil || eventAction == "" {
			return nil
		}
		operationID, err := journal.NewOperationID()
		if err != nil {
			return fmt.Errorf("prepare dingtalk reminder outcome journal: %w", err)
		}
		details := make(map[string]any, len(eventDetails)+4)
		details["version"] = 1
		details["reminder_id"] = job.ID
		details["attempt"] = job.Attempts
		for key, value := range eventDetails {
			details[key] = value
		}
		_, err = journal.InsertTx(ctx, tx, journal.EventInput{
			OccurredAt:      time.Now().UTC(),
			ActorType:       journal.ActorSystem,
			ActorID:         "dingtalk_reminder_worker",
			Action:          eventAction,
			EntityType:      "record",
			EntityID:        fmt.Sprint(job.RecordID),
			OperationID:     operationID,
			CausedByEventID: job.AttemptEventID,
			Details:         details,
		})
		return err
	})
	if err != nil {
		return err
	}
	if eventAction != "" {
		bumpJournalSync(ctx, d.Journal, "dingtalk reminder outcome")
	}
	return nil
}

func newTicketReminderClaimToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate ticket reminder claim token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
