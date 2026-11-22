package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
	recordmodel "clinic-backend/internal/record/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
)

// ReminderDiscovery finds pending tickets and their assigned administrators.
type ReminderDiscovery struct {
	DB              *database.DB
	Location        *time.Location
	LeadTimes       []time.Duration
	LeadTimesGetter func() []time.Duration
	Guard           func(func() error) error
	Journal         *journal.Service
}

func (d *ReminderDiscovery) Discover(ctx context.Context, now time.Time) error {
	if d.Guard != nil {
		return d.Guard(func() error { return d.discover(ctx, now) })
	}
	return d.discover(ctx, now)
}

func (d *ReminderDiscovery) discover(ctx context.Context, now time.Time) error {
	leadTimes := d.LeadTimes
	if d.LeadTimesGetter != nil {
		leadTimes = d.LeadTimesGetter()
	}
	operationID := ""
	if d.Journal != nil {
		var err error
		operationID, err = journal.NewOperationID()
		if err != nil {
			return fmt.Errorf("prepare reminder discovery journal: %w", err)
		}
	}
	for _, lead := range leadTimes {
		from, until := ReminderStartWindow(now.UTC(), lead)
		rows, err := d.DB.QueryContext(
			ctx,
			"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE start_time >= $1 AND start_time < $2",
			from, until,
		)
		if err != nil {
			return fmt.Errorf("find service dates: %w", err)
		}
		dates := make([]schedulingmodel.ClinicServiceDate, 0)
		for rows.Next() {
			date, err := scanServiceDate(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("scan service date: %w", err)
			}
			dates = append(dates, date)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("find service dates: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("find service dates: %w", err)
		}

		for _, date := range dates {
			admins, count, err := d.Eligible(ctx, date)
			if err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			for _, admin := range admins {
				if strings.TrimSpace(admin.QQNumber) == "" {
					log.Printf("reminder: staff %d has no QQ number", admin.ID)
					continue
				}
				if err := database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
					var reminderID uint
					err := tx.QueryRowContext(
						ctx,
						`INSERT INTO clinic_service_date_reminder
						 (service_date_id, service_start_at, lead_time_seconds, staff_id, status, attempts, next_attempt_at, claim_token, last_error, created_at, updated_at)
						 VALUES ($1, $2, $3, $4, 'pending', 0, $5, '', '', $6, $6)
						 ON CONFLICT DO NOTHING
						 RETURNING id`,
						date.ID, date.StartTime, int64(lead/time.Second), admin.ID, now, now,
					).Scan(&reminderID)
					if errors.Is(err, sql.ErrNoRows) {
						return nil
					}
					if err != nil {
						return fmt.Errorf("create reminder job: %w", err)
					}
					if d.Journal == nil {
						return nil
					}
					if _, err := journal.InsertTx(ctx, tx, journal.EventInput{
						OccurredAt:  now,
						ActorType:   journal.ActorSystem,
						ActorID:     "reminder_worker",
						Action:      "notification.requested",
						EntityType:  "service_date",
						EntityID:    fmt.Sprint(date.ID),
						OperationID: operationID,
						Details: map[string]any{
							"version":           1,
							"reminder_id":       reminderID,
							"kind":              "service_date_pending_records",
							"channel":           "qq",
							"recipient":         map[string]any{"type": "staff", "id": admin.ID},
							"service_start_at":  date.StartTime.UTC().Format(time.RFC3339Nano),
							"lead_time_seconds": int64(lead / time.Second),
							"next_attempt_at":   now.UTC().Format(time.RFC3339Nano),
						},
					}); err != nil {
						return err
					}
					return nil
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Eligible returns currently assigned administrators and the pending ticket count.
func (d *ReminderDiscovery) Eligible(ctx context.Context, date schedulingmodel.ClinicServiceDate) ([]staffmodel.ClinicStaff, int64, error) {
	if date.RoomID == nil {
		return nil, 0, nil
	}
	var count int64
	if err := d.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM clinic_record WHERE appointment_time = $1 AND room = $2 AND status = $3",
		date.Date, *date.RoomID, recordmodel.RecordStatusPending,
	).Scan(&count); err != nil {
		return nil, 0, fmt.Errorf("count pending tickets: %w", err)
	}
	if count == 0 {
		return nil, 0, nil
	}

	scheduleRows, err := d.DB.QueryContext(
		ctx,
		"SELECT "+workScheduleColumns+" FROM clinic_work_schedule WHERE enabled = $1 AND start_date <= $2 AND end_date >= $3",
		true, date.Date, date.Date,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("find assigned admins: %w", err)
	}
	schedules := make([]schedulingmodel.ClinicWorkSchedule, 0)
	for scheduleRows.Next() {
		schedule, err := scanWorkSchedule(scheduleRows)
		if err != nil {
			scheduleRows.Close()
			return nil, 0, fmt.Errorf("scan assigned schedule: %w", err)
		}
		schedules = append(schedules, schedule)
	}
	if err := scheduleRows.Close(); err != nil {
		return nil, 0, fmt.Errorf("find assigned admins: %w", err)
	}
	if err := scheduleRows.Err(); err != nil {
		return nil, 0, fmt.Errorf("find assigned admins: %w", err)
	}
	if len(schedules) == 0 {
		return nil, count, nil
	}

	args := make([]any, 0, len(schedules)+2)
	for _, schedule := range schedules {
		args = append(args, schedule.ID)
	}
	args = append(args, *date.RoomID, int(date.Date.Weekday()))
	query := "SELECT wd.start_time, wd.end_time, " +
		"staff.id, staff.account_id, staff.realname, staff.email, staff.qq_number, staff.role, staff.version " +
		"FROM clinic_work_schedule_weekday wd " +
		"JOIN clinic_work_schedule_staff assignment ON assignment.weekday_id = wd.id " +
		"JOIN clinic_staff staff ON staff.id = assignment.staff_id " +
		"WHERE wd.work_schedule_id IN (" + database.Placeholders(1, len(schedules)) + ") " +
		"AND wd.room_id = $" + fmt.Sprint(len(args)-1) + " AND wd.weekday = $" + fmt.Sprint(len(args)) + " " +
		"ORDER BY wd.id, assignment.id"
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("find assigned admins: %w", err)
	}
	defer rows.Close()

	loc := d.Location
	if loc == nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	start := date.StartTime.In(loc)
	end := date.EndTime.In(loc)
	seconds := func(t time.Time) int {
		return t.Hour()*3600 + t.Minute()*60 + t.Second()
	}
	seen := make(map[int]bool)
	admins := make([]staffmodel.ClinicStaff, 0)
	for rows.Next() {
		var slotStart, slotEnd time.Time
		var staff staffmodel.ClinicStaff
		var realname, email sql.NullString
		if err := rows.Scan(
			&slotStart,
			&slotEnd,
			&staff.ID,
			&staff.AccountID,
			&realname,
			&email,
			&staff.QQNumber,
			&staff.Role,
			&staff.Version,
		); err != nil {
			return nil, 0, fmt.Errorf("scan assigned admin: %w", err)
		}
		staff.Realname = realname.String
		staff.Email = email.String
		if seconds(slotStart) > seconds(start) || seconds(slotEnd) < seconds(end) {
			continue
		}
		if staff.Role == "admin" && !seen[staff.ID] {
			seen[staff.ID] = true
			admins = append(admins, staff)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("find assigned admins: %w", err)
	}
	return admins, count, nil
}
