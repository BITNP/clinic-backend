package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

// ErrServiceDateNotFound is returned when no service date matches the given id.
var ErrServiceDateNotFound = errors.New("service date not found")

// ErrServiceDateInUse is returned when a service date cannot be mutated because
// repair records already reference its room+date.
var ErrServiceDateInUse = errors.New("service date has existing records and cannot be modified")

// ErrServiceDateRoomNotFound is returned when the room referenced on create/update does not exist.
var ErrServiceDateRoomNotFound = errors.New("room does not exist")

// ServiceDateService contains the business logic for service date CRUD.
type ServiceDateService struct {
	db  *database.DB
	loc *time.Location
}

// NewServiceDateService creates a new ServiceDateService.
// If loc is nil, UTC+8 is used as the default timezone.
func NewServiceDateService(db *database.DB, loc *time.Location) *ServiceDateService {
	if loc == nil {
		loc = calendar.DefaultServiceDateLocation()
	}
	return &ServiceDateService{db: db, loc: loc}
}

// Location returns the timezone used for date comparisons.
func (s *ServiceDateService) Location() *time.Location {
	return s.loc
}

// CreateServiceDateInput carries the fields a caller may set on creation.
// Server-controlled fields (ID) are not included.
type CreateServiceDateInput struct {
	Capacity  uint
	RoomID    uint
	Date      time.Time
	StartTime time.Time
	EndTime   time.Time
	Title     string
}

// UpdateServiceDateInput uses pointers so omitted fields stay unchanged.
type UpdateServiceDateInput struct {
	Capacity  *uint
	RoomID    *uint
	Date      *time.Time
	StartTime *time.Time
	EndTime   *time.Time
	Title     *string
}

// ListServiceDateFilter controls listing behavior.
type ListServiceDateFilter struct {
	RoomID      *uint
	RoomIDs     []uint         // matches any of the given rooms; takes precedence over RoomID
	FromDate    time.Time      // inclusive lower bound on date, zero means unbounded
	ToDate      time.Time      // inclusive upper bound on date, zero means unbounded
	ActiveOnly  bool           // date >= today
	TodayLoc    *time.Location // timezone used for "today"; nil means use the service's configured location
	HasCapacity bool           // for students: booked count < capacity
	Page        int
	PageSize    int
}

func (s *ServiceDateService) Create(in CreateServiceDateInput) (schedulingmodel.ClinicServiceDate, error) {
	if err := s.assertRoomExists(in.RoomID); err != nil {
		return schedulingmodel.ClinicServiceDate{}, err
	}
	date := schedulingmodel.ClinicServiceDate{
		Capacity:  in.Capacity,
		RoomID:    &in.RoomID,
		Date:      calendar.DateInLocation(in.Date, s.loc),
		StartTime: in.StartTime,
		EndTime:   in.EndTime,
		Title:     in.Title,
	}
	date, err := scanServiceDate(s.db.QueryRow(
		`INSERT INTO clinic_service_date (capacity, room_id, date, start_time, end_time, title)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+serviceDateColumns,
		date.Capacity, *date.RoomID, date.Date, date.StartTime, date.EndTime, date.Title,
	))
	if err != nil {
		return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("create service date: %w", err)
	}
	return date, nil
}

func (s *ServiceDateService) GetByID(id uint) (schedulingmodel.ClinicServiceDate, error) {
	date, err := scanServiceDate(s.db.QueryRow(
		"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingmodel.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("get service date %d: %w", id, err)
	}
	booked, err := s.bookedCount(date.RoomID, date.Date, calendar.DateInLocation(time.Now(), s.loc))
	if err != nil {
		return schedulingmodel.ClinicServiceDate{}, err
	}
	date.Count = booked
	return date, nil
}

// assertRoomExists verifies the given room exists, returning ErrServiceDateRoomNotFound otherwise.
func (s *ServiceDateService) assertRoomExists(roomID uint) error {
	var exists bool
	if err := s.db.QueryRow("SELECT EXISTS (SELECT 1 FROM clinic_room WHERE id = $1)", roomID).Scan(&exists); err != nil {
		return fmt.Errorf("check room %d: %w", roomID, err)
	}
	if !exists {
		return fmt.Errorf("room %d: %w", roomID, ErrServiceDateRoomNotFound)
	}
	return nil
}

// GetByDateAndRoom returns the service date for the given (roomID, date) or
// ErrServiceDateNotFound if none exists. Used by the ticket flow to verify the
// clinic is open on a requested appointment day.
func (s *ServiceDateService) GetByDateAndRoom(roomID uint, date time.Time) (schedulingmodel.ClinicServiceDate, error) {
	serviceDate, err := scanServiceDate(s.db.QueryRow(
		"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE room_id = $1 AND date = $2 ORDER BY id LIMIT 1",
		roomID, calendar.DateInLocation(date, s.loc),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingmodel.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("get service date room=%d date=%s: %w", roomID, date.Format("2006-01-02"), err)
	}
	return serviceDate, nil
}

type rowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// recordsExistFor returns true if any repair record references the given room+date.
func (s *ServiceDateService) recordsExistFor(db rowQueryer, roomID uint, date time.Time) (bool, error) {
	var exists bool
	if err := db.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM clinic_record WHERE room = $1 AND appointment_time = $2)",
		roomID, calendar.DateInLocation(date, s.loc),
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("count records for service date: %w", err)
	}
	return exists, nil
}

// ticketedCountExcludeStatuses returns the statuses that do not count toward a
// service date's ticketed total on the given day. rejected, referred, and
// no_show tickets never count; from the day after today onward, completed
// tickets are also excluded (an upcoming date only shows still-open tickets).
// Both day and today must be normalized calendar days (see DateInLocation).
func TicketedCountExcludeStatuses(day, today time.Time) []recordmodel.RecordStatus {
	excluded := []recordmodel.RecordStatus{
		recordmodel.RecordStatusRejected,
		recordmodel.RecordStatusReferred,
		recordmodel.RecordStatusNoShow,
	}
	if day.After(today) {
		excluded = append(excluded, recordmodel.RecordStatusCompleted)
	}
	return excluded
}

// bookedCount returns the number of ticketed records for the given room+date,
// excluding statuses per TicketedCountExcludeStatuses based on whether the day
// is today/earlier or still ahead.
func (s *ServiceDateService) bookedCount(roomID *uint, date time.Time, today time.Time) (int64, error) {
	day := calendar.DateInLocation(date, s.loc)
	excluded := TicketedCountExcludeStatuses(day, today)
	args := make([]any, 0, len(excluded)+2)
	args = append(args, roomID, day)
	for _, status := range excluded {
		args = append(args, status)
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM clinic_record WHERE room = $1 AND appointment_time = $2 AND status NOT IN (%s)",
		database.Placeholders(3, len(excluded)),
	)
	var count int64
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count booked: %w", err)
	}
	return count, nil
}

type serviceDateRoomKey struct {
	roomID uint
	date   time.Time
}

// bookedCounts loads booking counts for a page of service dates in one grouped
// query. Records from rejected, referred, and no-show tickets are excluded
// first; completed tickets are subtracted only for future dates.
func (s *ServiceDateService) bookedCounts(dates []schedulingmodel.ClinicServiceDate, today time.Time) (map[serviceDateRoomKey]int64, error) {
	wanted := make(map[serviceDateRoomKey]struct{}, len(dates))
	roomIDs := make([]uint, 0, len(dates))
	days := make([]time.Time, 0, len(dates))
	seenRooms := make(map[uint]struct{}, len(dates))
	seenDays := make(map[time.Time]struct{}, len(dates))
	for _, date := range dates {
		if date.RoomID == nil {
			continue
		}
		day := calendar.DateInLocation(date.Date, s.loc)
		key := serviceDateRoomKey{roomID: *date.RoomID, date: day}
		wanted[key] = struct{}{}
		if _, ok := seenRooms[key.roomID]; !ok {
			seenRooms[key.roomID] = struct{}{}
			roomIDs = append(roomIDs, key.roomID)
		}
		if _, ok := seenDays[key.date]; !ok {
			seenDays[key.date] = struct{}{}
			days = append(days, key.date)
		}
	}
	counts := make(map[serviceDateRoomKey]int64, len(wanted))
	if len(wanted) == 0 {
		return counts, nil
	}

	args := []any{
		recordmodel.RecordStatusCompleted,
		recordmodel.RecordStatusRejected,
		recordmodel.RecordStatusReferred,
		recordmodel.RecordStatusNoShow,
	}
	roomStart := len(args) + 1
	for _, roomID := range roomIDs {
		args = append(args, roomID)
	}
	dayStart := len(args) + 1
	for _, day := range days {
		args = append(args, day)
	}
	query := fmt.Sprintf(
		`SELECT room, appointment_time, COUNT(*), SUM(CASE WHEN status = $1 THEN 1 ELSE 0 END)
		 FROM clinic_record
		 WHERE status NOT IN ($2, $3, $4)
		   AND room IN (%s)
		   AND appointment_time IN (%s)
		 GROUP BY room, appointment_time`,
		database.Placeholders(roomStart, len(roomIDs)),
		database.Placeholders(dayStart, len(days)),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("count booked for service dates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var roomID uint
		var day time.Time
		var total, completed int64
		if err := rows.Scan(&roomID, &day, &total, &completed); err != nil {
			return nil, fmt.Errorf("scan booked count: %w", err)
		}
		key := serviceDateRoomKey{roomID: roomID, date: calendar.DateInLocation(day, s.loc)}
		if _, ok := wanted[key]; !ok {
			continue
		}
		if key.date.After(today) {
			total -= completed
		}
		counts[key] = total
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count booked for service dates: %w", err)
	}
	return counts, nil
}

func (s *ServiceDateService) List(f ListServiceDateFilter) ([]schedulingmodel.ClinicServiceDate, int64, error) {
	where := []string{"1 = 1"}
	var args []any
	if len(f.RoomIDs) > 0 {
		for _, roomID := range f.RoomIDs {
			args = append(args, roomID)
		}
		where = append(where, "room_id IN ("+database.Placeholders(1, len(f.RoomIDs))+")")
	} else if f.RoomID != nil {
		args = append(args, *f.RoomID)
		where = append(where, fmt.Sprintf("room_id = $%d", len(args)))
	}
	if !f.FromDate.IsZero() {
		args = append(args, calendar.DateInLocation(f.FromDate, s.loc))
		where = append(where, fmt.Sprintf("date >= $%d", len(args)))
	}
	if !f.ToDate.IsZero() {
		args = append(args, calendar.DateInLocation(f.ToDate, s.loc))
		where = append(where, fmt.Sprintf("date <= $%d", len(args)))
	}
	loc := s.loc
	if f.TodayLoc != nil {
		loc = f.TodayLoc
	}
	if f.ActiveOnly {
		args = append(args, calendar.DateInLocation(time.Now(), loc))
		where = append(where, fmt.Sprintf("date >= $%d", len(args)))
	}
	predicate := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_service_date"+predicate, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count service dates: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	args = append(args, f.PageSize, offset)
	query := fmt.Sprintf(
		"SELECT %s FROM clinic_service_date%s ORDER BY date ASC, start_time ASC LIMIT $%d OFFSET $%d",
		serviceDateColumns, predicate, len(args)-1, len(args),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list service dates: %w", err)
	}
	defer rows.Close()

	items := make([]schedulingmodel.ClinicServiceDate, 0)
	for rows.Next() {
		date, err := scanServiceDate(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan service date: %w", err)
		}
		items = append(items, date)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list service dates: %w", err)
	}

	today := calendar.DateInLocation(time.Now(), loc)
	counts, err := s.bookedCounts(items, today)
	if err != nil {
		return nil, 0, err
	}
	filtered := items[:0]
	for _, date := range items {
		var booked int64
		if date.RoomID != nil {
			booked = counts[serviceDateRoomKey{
				roomID: *date.RoomID,
				date:   calendar.DateInLocation(date.Date, s.loc),
			}]
		}
		date.Count = booked
		if !f.HasCapacity || booked < int64(date.Capacity) {
			filtered = append(filtered, date)
		}
	}
	return filtered, total, nil
}

func (s *ServiceDateService) Update(id uint, in UpdateServiceDateInput) (schedulingmodel.ClinicServiceDate, error) {
	date, err := scanServiceDate(s.db.QueryRow(
		"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingmodel.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("get service date %d for update: %w", id, err)
	}

	originalRoomID := date.RoomID
	originalDate := calendar.DateInLocation(date.Date, s.loc)
	effectiveRoomID := originalRoomID
	effectiveDate := originalDate
	if in.RoomID != nil {
		if err := s.assertRoomExists(*in.RoomID); err != nil {
			return schedulingmodel.ClinicServiceDate{}, err
		}
		effectiveRoomID = in.RoomID
	}
	if in.Date != nil {
		effectiveDate = calendar.DateInLocation(*in.Date, s.loc)
	}

	roomChanged := in.RoomID != nil && (originalRoomID == nil || *effectiveRoomID != *originalRoomID)
	dateChanged := !effectiveDate.Equal(originalDate)
	if roomChanged || dateChanged {
		if originalRoomID != nil {
			inUse, err := s.recordsExistFor(s.db, *originalRoomID, originalDate)
			if err != nil {
				return schedulingmodel.ClinicServiceDate{}, err
			}
			if inUse {
				return schedulingmodel.ClinicServiceDate{}, ErrServiceDateInUse
			}
		}
		if effectiveRoomID != nil {
			inUse, err := s.recordsExistFor(s.db, *effectiveRoomID, effectiveDate)
			if err != nil {
				return schedulingmodel.ClinicServiceDate{}, err
			}
			if inUse {
				return schedulingmodel.ClinicServiceDate{}, ErrServiceDateInUse
			}
		}
	}

	if in.Capacity != nil {
		booked, err := s.bookedCount(effectiveRoomID, effectiveDate, calendar.DateInLocation(time.Now(), s.loc))
		if err != nil {
			return schedulingmodel.ClinicServiceDate{}, err
		}
		if *in.Capacity < uint(booked) {
			return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("capacity cannot be less than current booked count %d: %w", booked, ErrServiceDateInUse)
		}
	}

	var updates []string
	var args []any
	add := func(column string, value any) {
		args = append(args, value)
		updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if in.Capacity != nil {
		add("capacity", *in.Capacity)
	}
	if in.RoomID != nil {
		add("room_id", *in.RoomID)
	}
	if in.Date != nil {
		add("date", calendar.DateInLocation(*in.Date, s.loc))
	}
	if in.StartTime != nil {
		add("start_time", *in.StartTime)
	}
	if in.EndTime != nil {
		add("end_time", *in.EndTime)
	}
	if in.Title != nil {
		add("title", *in.Title)
	}

	if len(updates) == 0 {
		return date, nil
	}
	args = append(args, id)
	date, err = scanServiceDate(s.db.QueryRow(
		"UPDATE clinic_service_date SET "+strings.Join(updates, ", ")+" WHERE id = $"+fmt.Sprint(len(args))+" RETURNING "+serviceDateColumns,
		args...,
	))
	if err != nil {
		return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("update service date %d: %w", id, err)
	}
	return date, nil
}

func (s *ServiceDateService) Delete(id uint) error {
	date, err := scanServiceDate(s.db.QueryRow(
		"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServiceDateNotFound
		}
		return fmt.Errorf("get service date %d for delete: %w", id, err)
	}

	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		if date.RoomID != nil {
			inUse, err := s.recordsExistFor(tx, *date.RoomID, date.Date)
			if err != nil {
				return err
			}
			if inUse {
				return ErrServiceDateInUse
			}
		}
		if _, err := tx.Exec("DELETE FROM clinic_service_date WHERE id = $1", id); err != nil {
			return fmt.Errorf("delete service date %d: %w", id, err)
		}
		return nil
	})
}
