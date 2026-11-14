package service

import (
	"database/sql"
	"time"

	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type rowsQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

const serviceDateColumns = "id, capacity, room_id, date, start_time, end_time, title"
const workScheduleColumns = "id, name, start_date, end_date, enabled"
const workScheduleWeekdayColumns = "id, work_schedule_id, weekday, start_time, end_time, room_id"
const workScheduleStaffColumns = "id, weekday_id, staff_id, schedule_id"
const staffColumns = "id, account_id, realname, email, qq_number, role, version"
const roomColumns = "id, name, address, enabled, campus_id, type"
const roomWithCampusColumns = "r.id, r.name, r.address, r.enabled, r.campus_id, r.type, c.id, c.name"
const reminderColumns = "id, service_date_id, service_start_at, lead_time_seconds, staff_id, status, attempts, next_attempt_at, lease_until, claim_token, sent_at, last_error, created_at, updated_at"

func scanServiceDate(row rowScanner) (schedulingmodel.ClinicServiceDate, error) {
	var date schedulingmodel.ClinicServiceDate
	var roomID sql.NullInt64
	err := row.Scan(&date.ID, &date.Capacity, &roomID, &date.Date, &date.StartTime, &date.EndTime, &date.Title)
	if roomID.Valid {
		id := uint(roomID.Int64)
		date.RoomID = &id
	}
	return date, err
}

func scanWorkSchedule(row rowScanner) (schedulingmodel.ClinicWorkSchedule, error) {
	var schedule schedulingmodel.ClinicWorkSchedule
	err := row.Scan(&schedule.ID, &schedule.Name, &schedule.StartDate, &schedule.EndDate, &schedule.Enabled)
	return schedule, err
}

func scanWorkScheduleWeekday(row rowScanner) (schedulingmodel.ClinicWorkScheduleWeekday, error) {
	var weekday schedulingmodel.ClinicWorkScheduleWeekday
	err := row.Scan(
		&weekday.ID,
		&weekday.WorkScheduleID,
		&weekday.Weekday,
		&weekday.StartTime,
		&weekday.EndTime,
		&weekday.RoomID,
	)
	return weekday, err
}

func scanWorkScheduleStaff(row rowScanner) (schedulingmodel.ClinicWorkScheduleStaff, error) {
	var assignment schedulingmodel.ClinicWorkScheduleStaff
	err := row.Scan(&assignment.ID, &assignment.WeekdayID, &assignment.StaffID, &assignment.ScheduleID)
	return assignment, err
}

func scanStaff(row rowScanner) (staffmodel.ClinicStaff, error) {
	var staff staffmodel.ClinicStaff
	err := row.Scan(&staff.ID, &staff.AccountID, &staff.Realname, &staff.Email, &staff.QQNumber, &staff.Role, &staff.Version)
	return staff, err
}

func scanRoomWithCampus(row rowScanner) (roommodel.ClinicRoom, error) {
	var room roommodel.ClinicRoom
	var campusID sql.NullInt64
	var campusName sql.NullString
	err := row.Scan(&room.ID, &room.Name, &room.Address, &room.Enabled, &room.CampusID, &room.Type, &campusID, &campusName)
	if err != nil {
		return roommodel.ClinicRoom{}, err
	}
	if campusID.Valid {
		id := uint(campusID.Int64)
		room.Campus = &roommodel.ClinicCampus{ID: id, Name: campusName.String}
	}
	return room, nil
}

func scanRoom(row rowScanner) (roommodel.ClinicRoom, error) {
	var room roommodel.ClinicRoom
	var campusID sql.NullInt64
	err := row.Scan(&room.ID, &room.Name, &room.Address, &room.Enabled, &campusID, &room.Type)
	if campusID.Valid {
		id := uint(campusID.Int64)
		room.CampusID = &id
	}
	return room, err
}

func scanReminder(row rowScanner) (schedulingmodel.ServiceDateReminder, error) {
	var reminder schedulingmodel.ServiceDateReminder
	var leaseUntil, sentAt sql.NullTime
	err := row.Scan(
		&reminder.ID,
		&reminder.ServiceDateID,
		&reminder.ServiceStartAt,
		&reminder.LeadTimeSeconds,
		&reminder.StaffID,
		&reminder.Status,
		&reminder.Attempts,
		&reminder.NextAttemptAt,
		&leaseUntil,
		&reminder.ClaimToken,
		&sentAt,
		&reminder.LastError,
		&reminder.CreatedAt,
		&reminder.UpdatedAt,
	)
	if leaseUntil.Valid {
		reminder.LeaseUntil = &leaseUntil.Time
	}
	if sentAt.Valid {
		reminder.SentAt = &sentAt.Time
	}
	return reminder, err
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
