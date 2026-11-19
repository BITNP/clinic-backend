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
}

// Claim leases one due reminder for this worker.
func (d *TicketReminderDelivery) Claim(ctx context.Context, now time.Time) (*recordmodel.ClinicRecordDingTalkReminder, error) {
	now = now.UTC()
	token, err := newTicketReminderClaimToken()
	if err != nil {
		return nil, err
	}

	var job recordmodel.ClinicRecordDingTalkReminder
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
			job.ID = 0
			return nil
		}

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
		return d.finish(ctx, job, reminderStatusCancelled, time.Time{}, "")
	}
	if err != nil {
		return d.retry(ctx, job, now, time.Time{}, fmt.Errorf("load reminder ticket: %w", err))
	}
	if roomID.Valid {
		id := uint(roomID.Int64)
		date.RoomID = &id
	}
	loc := d.Location
	if loc == nil {
		loc = calendar.DefaultServiceDateLocation()
	}
	if record.Status != recordmodel.RecordStatusConfirmed ||
		date.RoomID == nil || *date.RoomID != record.RoomID ||
		!calendar.DateInLocation(date.Date, loc).Equal(calendar.DateInLocation(record.AppointmentTime, loc)) ||
		!now.Before(date.StartTime) ||
		strings.TrimSpace(record.User) == "" {
		return d.finish(ctx, job, reminderStatusCancelled, time.Time{}, "")
	}
	if d.Notifier == nil {
		return d.retry(ctx, job, now, date.StartTime, fmt.Errorf("ticket reminder notifier is not configured"))
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
		return d.retry(ctx, job, now, date.StartTime, err)
	}
	return d.finish(ctx, job, reminderStatusSent, now, "")
}

func (d *TicketReminderDelivery) retry(
	ctx context.Context,
	job *recordmodel.ClinicRecordDingTalkReminder,
	now time.Time,
	serviceStart time.Time,
	cause error,
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
	if job.Attempts >= maxTicketReminderAttempts ||
		(!serviceStart.IsZero() && !next.Before(serviceStart)) {
		status = reminderStatusCancelled
	}
	if err := d.finish(ctx, job, status, next, cause.Error()); err != nil {
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
	result, err := d.DB.ExecContext(ctx, query, args...)
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
	return nil
}

func newTicketReminderClaimToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate ticket reminder claim token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
