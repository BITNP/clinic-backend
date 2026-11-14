package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/database"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
	staffservice "clinic-backend/internal/staff/service"
)

var (
	ErrWorkScheduleNotFound           = errors.New("work schedule not found")
	ErrWorkScheduleNameTaken          = errors.New("work schedule name already taken")
	ErrWorkScheduleAlreadyEnabled     = errors.New("there is already an enabled work schedule")
	ErrWorkScheduleInvalidDateRange   = errors.New("start_date must not be after end_date")
	ErrWorkScheduleInvalidWeekday     = errors.New("weekday must be between 0 and 6")
	ErrWorkScheduleInvalidTimeWindow  = errors.New("start_time must be before end_time")
	ErrWorkScheduleRoomNotFound       = errors.New("room not found")
	ErrWorkScheduleStaffNotFound      = errors.New("staff not found")
	ErrWorkScheduleStaffNotInWorkYear = errors.New("staff is not assigned to the work year of the schedule")
	ErrWorkScheduleWeekdayNotFound    = errors.New("weekday not found")
)

type WorkScheduleService struct {
	db *database.DB
}

func NewWorkScheduleService(db *database.DB) *WorkScheduleService {
	return &WorkScheduleService{db: db}
}

type WeekdayInput struct {
	Weekday   int
	StartTime string
	EndTime   string
	RoomID    uint
	StaffIDs  []int
}

type CreateWorkScheduleInput struct {
	Name      string
	StartDate time.Time
	EndDate   time.Time
	Enabled   bool
	Weekdays  []WeekdayInput
}

type UpdateWorkScheduleInput struct {
	Name      *string
	StartDate *time.Time
	EndDate   *time.Time
	Enabled   *bool
	Weekdays  []WeekdayInput
}

type StaffAssignmentInput struct {
	WeekdayID uint
	StaffID   int
	RoomID    uint
	Weekday   int
}

type UpdateWeekdayInput struct {
	RoomID    uint
	Weekday   int
	StartTime string
	EndTime   string
}

type ListWorkScheduleFilter struct {
	Enabled  *bool
	Page     int
	PageSize int
}

type ServiceAvailabilityItem struct {
	RoomID    uint   `json:"room_id"`
	Date      string `json:"date"`
	Available bool   `json:"available"`
}

func parseTimeOnly(s string) (time.Time, error) {
	layouts := []string{"15:04:05", "15:04"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(1970, 1, 1, t.Hour(), t.Minute(), t.Second(), 0, time.UTC), nil
		}
	}
	return time.Time{}, errors.New("time must be in HH:MM:SS or HH:MM format")
}

func (s *WorkScheduleService) validateWeekdayInTx(tx *sql.Tx, in WeekdayInput, skipStaff bool) (parsedWeekday, error) {
	if in.Weekday < 0 || in.Weekday > 6 {
		return parsedWeekday{}, ErrWorkScheduleInvalidWeekday
	}
	startTime, err := parseTimeOnly(in.StartTime)
	if err != nil {
		return parsedWeekday{}, fmt.Errorf("start_time: %w", err)
	}
	endTime, err := parseTimeOnly(in.EndTime)
	if err != nil {
		return parsedWeekday{}, fmt.Errorf("end_time: %w", err)
	}
	if !startTime.Before(endTime) {
		return parsedWeekday{}, ErrWorkScheduleInvalidTimeWindow
	}
	var roomExists bool
	if err := tx.QueryRow("SELECT EXISTS (SELECT 1 FROM clinic_room WHERE id = $1)", in.RoomID).Scan(&roomExists); err != nil {
		return parsedWeekday{}, fmt.Errorf("check room: %w", err)
	}
	if !roomExists {
		return parsedWeekday{}, ErrWorkScheduleRoomNotFound
	}
	if !skipStaff {
		for _, staffID := range in.StaffIDs {
			var staffExists bool
			if err := tx.QueryRow("SELECT EXISTS (SELECT 1 FROM clinic_staff WHERE id = $1)", staffID).Scan(&staffExists); err != nil {
				return parsedWeekday{}, fmt.Errorf("check staff %d: %w", staffID, err)
			}
			if !staffExists {
				return parsedWeekday{}, fmt.Errorf("staff %d: %w", staffID, ErrWorkScheduleStaffNotFound)
			}
		}
	}
	return parsedWeekday{
		weekday:   in.Weekday,
		startTime: startTime,
		endTime:   endTime,
		roomID:    in.RoomID,
		staffIDs:  in.StaffIDs,
	}, nil
}

type parsedWeekday struct {
	weekday   int
	startTime time.Time
	endTime   time.Time
	roomID    uint
	staffIDs  []int
}

func (s *WorkScheduleService) createWeekdaysInTx(tx *sql.Tx, scheduleID uint, inputs []WeekdayInput) error {
	for _, in := range inputs {
		parsed, err := s.validateWeekdayInTx(tx, in, false)
		if err != nil {
			return err
		}
		weekday, err := scanWorkScheduleWeekday(tx.QueryRow(
			`INSERT INTO clinic_work_schedule_weekday (work_schedule_id, weekday, start_time, end_time, room_id)
			 VALUES ($1, $2, $3, $4, $5) RETURNING `+workScheduleWeekdayColumns,
			scheduleID, parsed.weekday, parsed.startTime, parsed.endTime, parsed.roomID,
		))
		if err != nil {
			return fmt.Errorf("create weekday: %w", err)
		}
		for _, staffID := range parsed.staffIDs {
			if _, err := tx.Exec(
				"INSERT INTO clinic_work_schedule_staff (weekday_id, staff_id, schedule_id) VALUES ($1, $2, $3)",
				weekday.ID, staffID, scheduleID,
			); err != nil {
				return fmt.Errorf("create staff assignment: %w", err)
			}
		}
	}
	return nil
}

func (s *WorkScheduleService) hasEnabledSchedule(tx *sql.Tx, excludeID uint) (bool, error) {
	query := "SELECT EXISTS (SELECT 1 FROM clinic_work_schedule WHERE enabled = $1"
	args := []any{true}
	if excludeID > 0 {
		args = append(args, excludeID)
		query += fmt.Sprintf(" AND id != $%d", len(args))
	}
	query += ")"
	var exists bool
	if err := tx.QueryRow(query, args...).Scan(&exists); err != nil {
		return false, fmt.Errorf("check enabled schedule: %w", err)
	}
	return exists, nil
}

func (s *WorkScheduleService) Create(in CreateWorkScheduleInput) (schedulingmodel.ClinicWorkSchedule, error) {
	if len(in.Name) > 128 {
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("name must be 128 characters or fewer")
	}
	if in.Name == "" {
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("name is required")
	}
	if in.StartDate.After(in.EndDate) {
		return schedulingmodel.ClinicWorkSchedule{}, ErrWorkScheduleInvalidDateRange
	}

	var schedule schedulingmodel.ClinicWorkSchedule
	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		if in.Enabled {
			enabled, err := s.hasEnabledSchedule(tx, 0)
			if err != nil {
				return err
			}
			if enabled {
				return ErrWorkScheduleAlreadyEnabled
			}
		}
		var err error
		schedule, err = scanWorkSchedule(tx.QueryRow(
			`INSERT INTO clinic_work_schedule (name, start_date, end_date, enabled)
			 VALUES ($1, $2, $3, $4) RETURNING `+workScheduleColumns,
			in.Name, in.StartDate, in.EndDate, in.Enabled,
		))
		if err != nil {
			if database.IsUniqueViolation(err) {
				return ErrWorkScheduleNameTaken
			}
			return fmt.Errorf("create work schedule: %w", err)
		}
		return s.createWeekdaysInTx(tx, schedule.ID, in.Weekdays)
	})
	if err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, err
	}
	return schedule, nil
}

func (s *WorkScheduleService) GetByID(id uint) (schedulingmodel.ClinicWorkSchedule, error) {
	schedule, err := s.getByID(s.db, id)
	if err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, err
	}
	if err := s.loadWorkScheduleGraph(s.db, &schedule); err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("get work schedule %d: %w", id, err)
	}
	return schedule, nil
}

func (s *WorkScheduleService) getByID(db rowQueryer, id uint) (schedulingmodel.ClinicWorkSchedule, error) {
	schedule, err := scanWorkSchedule(db.QueryRow(
		"SELECT "+workScheduleColumns+" FROM clinic_work_schedule WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingmodel.ClinicWorkSchedule{}, ErrWorkScheduleNotFound
		}
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("get work schedule %d: %w", id, err)
	}
	return schedule, nil
}

func (s *WorkScheduleService) loadWorkScheduleGraph(db rowsQueryer, schedule *schedulingmodel.ClinicWorkSchedule) error {
	rows, err := db.Query(
		"SELECT "+workScheduleWeekdayColumns+" FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1 ORDER BY weekday ASC",
		schedule.ID,
	)
	if err != nil {
		return fmt.Errorf("load weekdays: %w", err)
	}
	defer rows.Close()

	weekdays := make([]schedulingmodel.ClinicWorkScheduleWeekday, 0)
	for rows.Next() {
		weekday, err := scanWorkScheduleWeekday(rows)
		if err != nil {
			return fmt.Errorf("scan weekday: %w", err)
		}
		weekdays = append(weekdays, weekday)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load weekdays: %w", err)
	}
	if err := s.loadWeekdayRooms(db, weekdays); err != nil {
		return err
	}
	if err := s.loadWeekdayStaff(db, weekdays); err != nil {
		return err
	}
	schedule.Weekdays = weekdays
	return nil
}

func (s *WorkScheduleService) loadWeekdayRooms(db rowsQueryer, weekdays []schedulingmodel.ClinicWorkScheduleWeekday) error {
	roomIDs := make([]uint, 0, len(weekdays))
	seen := make(map[uint]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		if _, ok := seen[weekday.RoomID]; ok {
			continue
		}
		seen[weekday.RoomID] = struct{}{}
		roomIDs = append(roomIDs, weekday.RoomID)
	}
	if len(roomIDs) == 0 {
		return nil
	}
	args := make([]any, len(roomIDs))
	for i, id := range roomIDs {
		args[i] = id
	}
	rows, err := db.Query(
		"SELECT "+roomWithCampusColumns+" FROM clinic_room r LEFT JOIN clinic_campus c ON c.id = r.campus_id WHERE r.id IN ("+database.Placeholders(1, len(roomIDs))+")",
		args...,
	)
	if err != nil {
		return fmt.Errorf("load weekday rooms: %w", err)
	}
	defer rows.Close()

	rooms := make(map[uint]roommodel.ClinicRoom, len(roomIDs))
	for rows.Next() {
		room, err := scanRoomWithCampus(rows)
		if err != nil {
			return fmt.Errorf("scan weekday room: %w", err)
		}
		rooms[room.ID] = room
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load weekday rooms: %w", err)
	}
	for i := range weekdays {
		if room, ok := rooms[weekdays[i].RoomID]; ok {
			weekdays[i].Room = room
		}
	}
	return nil
}

func (s *WorkScheduleService) loadWeekdayStaff(db rowsQueryer, weekdays []schedulingmodel.ClinicWorkScheduleWeekday) error {
	weekdayIDs := make([]uint, len(weekdays))
	for i, weekday := range weekdays {
		weekdayIDs[i] = weekday.ID
	}
	if len(weekdayIDs) == 0 {
		return nil
	}
	args := make([]any, len(weekdayIDs))
	for i, id := range weekdayIDs {
		args[i] = id
	}
	rows, err := db.Query(
		"SELECT "+workScheduleStaffColumns+" FROM clinic_work_schedule_staff WHERE weekday_id IN ("+database.Placeholders(1, len(weekdayIDs))+") ORDER BY id ASC",
		args...,
	)
	if err != nil {
		return fmt.Errorf("load weekday staff: %w", err)
	}
	defer rows.Close()

	byWeekday := make(map[uint][]schedulingmodel.ClinicWorkScheduleStaff, len(weekdays))
	staffIDs := make([]int, 0)
	seenStaff := make(map[int]struct{})
	for rows.Next() {
		assignment, err := scanWorkScheduleStaff(rows)
		if err != nil {
			return fmt.Errorf("scan weekday staff: %w", err)
		}
		byWeekday[assignment.WeekdayID] = append(byWeekday[assignment.WeekdayID], assignment)
		if _, ok := seenStaff[assignment.StaffID]; !ok {
			seenStaff[assignment.StaffID] = struct{}{}
			staffIDs = append(staffIDs, assignment.StaffID)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load weekday staff: %w", err)
	}
	if len(staffIDs) == 0 {
		return nil
	}
	staff, err := s.loadStaffByIDs(db, staffIDs)
	if err != nil {
		return err
	}
	for i := range weekdays {
		assignments := byWeekday[weekdays[i].ID]
		for j := range assignments {
			assignments[j].Staff = staff[assignments[j].StaffID]
		}
		weekdays[i].Staff = assignments
	}
	return nil
}

func (s *WorkScheduleService) loadStaffByIDs(db rowsQueryer, staffIDs []int) (map[int]staffmodel.ClinicStaff, error) {
	args := make([]any, len(staffIDs))
	for i, id := range staffIDs {
		args[i] = id
	}
	rows, err := db.Query(
		"SELECT "+staffColumns+" FROM clinic_staff WHERE id IN ("+database.Placeholders(1, len(staffIDs))+")",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("load staff: %w", err)
	}
	defer rows.Close()
	staff := make(map[int]staffmodel.ClinicStaff, len(staffIDs))
	for rows.Next() {
		member, err := scanStaff(rows)
		if err != nil {
			return nil, fmt.Errorf("scan staff: %w", err)
		}
		staff[member.ID] = member
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load staff: %w", err)
	}
	return staff, nil
}

func (s *WorkScheduleService) List(f ListWorkScheduleFilter) ([]schedulingmodel.ClinicWorkSchedule, int64, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.Enabled != nil {
		args = append(args, *f.Enabled)
		where = append(where, fmt.Sprintf("enabled = $%d", len(args)))
	}
	predicate := " WHERE " + strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_work_schedule"+predicate, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count work schedules: %w", err)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	args = append(args, f.PageSize, offset)
	rows, err := s.db.Query(
		fmt.Sprintf(
			"SELECT %s FROM clinic_work_schedule%s ORDER BY id ASC LIMIT $%d OFFSET $%d",
			workScheduleColumns, predicate, len(args)-1, len(args),
		),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list work schedules: %w", err)
	}
	defer rows.Close()
	items := make([]schedulingmodel.ClinicWorkSchedule, 0)
	for rows.Next() {
		schedule, err := scanWorkSchedule(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan work schedule: %w", err)
		}
		items = append(items, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list work schedules: %w", err)
	}
	return items, total, nil
}

func (s *WorkScheduleService) Update(id uint, in UpdateWorkScheduleInput) (schedulingmodel.ClinicWorkSchedule, error) {
	schedule, err := s.getByID(s.db, id)
	if err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, err
	}

	err = database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var updates []string
		var args []any
		add := func(column string, value any) {
			args = append(args, value)
			updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
		}
		if in.Name != nil {
			add("name", *in.Name)
		}
		if in.StartDate != nil {
			add("start_date", *in.StartDate)
		}
		if in.EndDate != nil {
			add("end_date", *in.EndDate)
		}
		if in.Enabled != nil {
			if *in.Enabled {
				enabled, err := s.hasEnabledSchedule(tx, id)
				if err != nil {
					return err
				}
				if enabled {
					return ErrWorkScheduleAlreadyEnabled
				}
			}
			add("enabled", *in.Enabled)
		}

		if in.StartDate != nil && in.EndDate != nil && in.StartDate.After(*in.EndDate) {
			return ErrWorkScheduleInvalidDateRange
		}
		if in.StartDate != nil && in.EndDate == nil && in.StartDate.After(schedule.EndDate) {
			return ErrWorkScheduleInvalidDateRange
		}
		if in.StartDate == nil && in.EndDate != nil && schedule.StartDate.After(*in.EndDate) {
			return ErrWorkScheduleInvalidDateRange
		}

		if in.Name != nil {
			var duplicate bool
			if err := tx.QueryRow(
				"SELECT EXISTS (SELECT 1 FROM clinic_work_schedule WHERE name = $1 AND id != $2)",
				*in.Name, id,
			).Scan(&duplicate); err != nil {
				return fmt.Errorf("check name duplicate: %w", err)
			}
			if duplicate {
				return ErrWorkScheduleNameTaken
			}
		}
		if len(updates) > 0 {
			args = append(args, id)
			if _, err := tx.Exec(
				"UPDATE clinic_work_schedule SET "+strings.Join(updates, ", ")+" WHERE id = $"+fmt.Sprint(len(args)),
				args...,
			); err != nil {
				return fmt.Errorf("update work schedule %d: %w", id, err)
			}
		}

		if in.Weekdays != nil {
			var ids []uint
			rows, err := tx.Query("SELECT id FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1", id)
			if err != nil {
				return fmt.Errorf("find existing weekdays: %w", err)
			}
			for rows.Next() {
				var weekdayID uint
				if err := rows.Scan(&weekdayID); err != nil {
					rows.Close()
					return fmt.Errorf("scan existing weekday: %w", err)
				}
				ids = append(ids, weekdayID)
			}
			if err := rows.Close(); err != nil {
				return fmt.Errorf("find existing weekdays: %w", err)
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("find existing weekdays: %w", err)
			}
			if len(ids) > 0 {
				assignArgs := make([]any, len(ids))
				for i, weekdayID := range ids {
					assignArgs[i] = weekdayID
				}
				if _, err := tx.Exec(
					"DELETE FROM clinic_work_schedule_staff WHERE weekday_id IN ("+database.Placeholders(1, len(ids))+")",
					assignArgs...,
				); err != nil {
					return fmt.Errorf("delete staff assignments: %w", err)
				}
			}
			if _, err := tx.Exec("DELETE FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1", id); err != nil {
				return fmt.Errorf("delete weekdays: %w", err)
			}
			if err := s.createWeekdaysInTx(tx, id, in.Weekdays); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, err
	}

	schedule, err = s.getByID(s.db, id)
	if err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("reload work schedule %d: %w", id, err)
	}
	if err := s.loadWorkScheduleGraph(s.db, &schedule); err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("reload work schedule %d: %w", id, err)
	}
	return schedule, nil
}

func (s *WorkScheduleService) Delete(id uint) error {
	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		_, err := s.getByID(tx, id)
		if err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1", id)
		if err != nil {
			return fmt.Errorf("find weekdays: %w", err)
		}
		var ids []uint
		for rows.Next() {
			var weekdayID uint
			if err := rows.Scan(&weekdayID); err != nil {
				rows.Close()
				return fmt.Errorf("scan weekday: %w", err)
			}
			ids = append(ids, weekdayID)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("find weekdays: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("find weekdays: %w", err)
		}
		if len(ids) > 0 {
			args := make([]any, len(ids))
			for i, weekdayID := range ids {
				args[i] = weekdayID
			}
			if _, err := tx.Exec(
				"DELETE FROM clinic_work_schedule_staff WHERE weekday_id IN ("+database.Placeholders(1, len(ids))+")",
				args...,
			); err != nil {
				return fmt.Errorf("delete staff: %w", err)
			}
		}
		if _, err := tx.Exec("DELETE FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1", id); err != nil {
			return fmt.Errorf("delete weekdays: %w", err)
		}
		if _, err := tx.Exec("DELETE FROM clinic_work_schedule WHERE id = $1", id); err != nil {
			return fmt.Errorf("delete work schedule: %w", err)
		}
		return nil
	})
}

func (s *WorkScheduleService) AddStaff(scheduleID uint, in StaffAssignmentInput) (schedulingmodel.ClinicWorkScheduleStaff, error) {
	schedule, err := s.getByID(s.db, scheduleID)
	if err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, err
	}

	var staffExists bool
	if err := s.db.QueryRow("SELECT EXISTS (SELECT 1 FROM clinic_staff WHERE id = $1)", in.StaffID).Scan(&staffExists); err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("check staff: %w", err)
	}
	if !staffExists {
		return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleStaffNotFound
	}

	staffSvc := staffservice.NewStaffService(s.db)
	valid, err := staffSvc.IsValidForDate(in.StaffID, schedule.StartDate)
	if err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, err
	}
	if !valid {
		return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleStaffNotInWorkYear
	}

	var weekdayID uint
	if in.WeekdayID > 0 {
		weekday, err := scanWorkScheduleWeekday(s.db.QueryRow(
			"SELECT "+workScheduleWeekdayColumns+" FROM clinic_work_schedule_weekday WHERE id = $1 AND work_schedule_id = $2",
			in.WeekdayID, scheduleID,
		))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleWeekdayNotFound
			}
			return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("find weekday: %w", err)
		}
		weekdayID = weekday.ID
	} else if in.RoomID > 0 {
		if in.Weekday < 0 || in.Weekday > 6 {
			return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleInvalidWeekday
		}
		weekday, err := scanWorkScheduleWeekday(s.db.QueryRow(
			"SELECT "+workScheduleWeekdayColumns+" FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1 AND room_id = $2 AND weekday = $3 ORDER BY id LIMIT 1",
			scheduleID, in.RoomID, in.Weekday,
		))
		if errors.Is(err, sql.ErrNoRows) {
			startTime, _ := parseTimeOnly("18:30")
			endTime, _ := parseTimeOnly("21:00")
			weekday, err = scanWorkScheduleWeekday(s.db.QueryRow(
				`INSERT INTO clinic_work_schedule_weekday (work_schedule_id, weekday, start_time, end_time, room_id)
				 VALUES ($1, $2, $3, $4, $5) RETURNING `+workScheduleWeekdayColumns,
				scheduleID, in.Weekday, startTime, endTime, in.RoomID,
			))
			if err != nil {
				return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("create weekday: %w", err)
			}
		} else if err != nil {
			return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("find weekday: %w", err)
		}
		weekdayID = weekday.ID
	} else {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("must provide weekday_id or (room_id + weekday)")
	}

	existing, err := scanWorkScheduleStaff(s.db.QueryRow(
		"SELECT "+workScheduleStaffColumns+" FROM clinic_work_schedule_staff WHERE weekday_id = $1 AND staff_id = $2 AND schedule_id = $3",
		weekdayID, in.StaffID, scheduleID,
	))
	if err == nil {
		if err := s.attachAssignmentStaff(s.db, &existing); err != nil {
			return schedulingmodel.ClinicWorkScheduleStaff{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("check existing assignment: %w", err)
	}

	assignment, err := scanWorkScheduleStaff(s.db.QueryRow(
		`INSERT INTO clinic_work_schedule_staff (weekday_id, staff_id, schedule_id)
		 VALUES ($1, $2, $3) RETURNING `+workScheduleStaffColumns,
		weekdayID, in.StaffID, scheduleID,
	))
	if err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("add staff: %w", err)
	}
	if err := s.attachAssignmentStaff(s.db, &assignment); err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("reload assignment: %w", err)
	}
	return assignment, nil
}

func (s *WorkScheduleService) attachAssignmentStaff(db rowQueryer, assignment *schedulingmodel.ClinicWorkScheduleStaff) error {
	staff, err := scanStaff(db.QueryRow(
		"SELECT "+staffColumns+" FROM clinic_staff WHERE id = $1",
		assignment.StaffID,
	))
	if err != nil {
		return fmt.Errorf("load assignment staff: %w", err)
	}
	assignment.Staff = staff
	return nil
}

func (s *WorkScheduleService) ListValidStaff(scheduleID uint) ([]staffservice.StaffListItem, error) {
	var startDate time.Time
	if err := s.db.QueryRow("SELECT start_date FROM clinic_work_schedule WHERE id = $1", scheduleID).Scan(&startDate); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorkScheduleNotFound
		}
		return nil, fmt.Errorf("find schedule: %w", err)
	}
	staffSvc := staffservice.NewStaffService(s.db)
	return staffSvc.ListValidForYear(startDate.Year())
}

func (s *WorkScheduleService) RemoveStaff(scheduleID uint, in StaffAssignmentInput) error {
	var weekdayID uint
	if err := s.db.QueryRow(
		"SELECT id FROM clinic_work_schedule_weekday WHERE id = $1 AND work_schedule_id = $2",
		in.WeekdayID, scheduleID,
	).Scan(&weekdayID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkScheduleWeekdayNotFound
		}
		return fmt.Errorf("find weekday: %w", err)
	}
	res, err := s.db.Exec(
		"DELETE FROM clinic_work_schedule_staff WHERE weekday_id = $1 AND staff_id = $2 AND schedule_id = $3",
		in.WeekdayID, in.StaffID, scheduleID,
	)
	if err != nil {
		return fmt.Errorf("remove staff: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check removed staff: %w", err)
	}
	if n == 0 {
		return ErrWorkScheduleStaffNotFound
	}
	return nil
}

func (s *WorkScheduleService) UpdateWeekday(scheduleID uint, in UpdateWeekdayInput) (schedulingmodel.ClinicWorkScheduleWeekday, error) {
	var scheduleIDValue uint
	if err := s.db.QueryRow("SELECT id FROM clinic_work_schedule WHERE id = $1", scheduleID).Scan(&scheduleIDValue); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingmodel.ClinicWorkScheduleWeekday{}, ErrWorkScheduleNotFound
		}
		return schedulingmodel.ClinicWorkScheduleWeekday{}, fmt.Errorf("get work schedule %d: %w", scheduleID, err)
	}

	var weekday schedulingmodel.ClinicWorkScheduleWeekday
	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		parsed, err := s.validateWeekdayInTx(tx, WeekdayInput{
			Weekday:   in.Weekday,
			StartTime: in.StartTime,
			EndTime:   in.EndTime,
			RoomID:    in.RoomID,
			StaffIDs:  []int{},
		}, true)
		if err != nil {
			return err
		}

		weekday, err = scanWorkScheduleWeekday(tx.QueryRow(
			"SELECT "+workScheduleWeekdayColumns+" FROM clinic_work_schedule_weekday WHERE work_schedule_id = $1 AND room_id = $2 AND weekday = $3 ORDER BY id LIMIT 1",
			scheduleID, in.RoomID, in.Weekday,
		))
		if errors.Is(err, sql.ErrNoRows) {
			weekday, err = scanWorkScheduleWeekday(tx.QueryRow(
				`INSERT INTO clinic_work_schedule_weekday (work_schedule_id, weekday, start_time, end_time, room_id)
				 VALUES ($1, $2, $3, $4, $5) RETURNING `+workScheduleWeekdayColumns,
				scheduleID, in.Weekday, parsed.startTime, parsed.endTime, in.RoomID,
			))
			if err != nil {
				return fmt.Errorf("create weekday: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("find weekday: %w", err)
		} else {
			weekday, err = scanWorkScheduleWeekday(tx.QueryRow(
				"UPDATE clinic_work_schedule_weekday SET start_time = $1, end_time = $2 WHERE id = $3 RETURNING "+workScheduleWeekdayColumns,
				parsed.startTime, parsed.endTime, weekday.ID,
			))
			if err != nil {
				return fmt.Errorf("update weekday: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return schedulingmodel.ClinicWorkScheduleWeekday{}, err
	}
	return weekday, nil
}

func (s *WorkScheduleService) ServiceAvailability(from, to time.Time, roomIDs []uint) ([]ServiceAvailabilityItem, error) {
	if len(roomIDs) == 0 {
		return []ServiceAvailabilityItem{}, nil
	}

	schedule, err := scanWorkSchedule(s.db.QueryRow(
		"SELECT "+workScheduleColumns+" FROM clinic_work_schedule WHERE enabled = $1 ORDER BY id LIMIT 1",
		true,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find enabled schedule: %w", err)
	}

	args := make([]any, 0, len(roomIDs)+1)
	args = append(args, schedule.ID)
	for _, id := range roomIDs {
		args = append(args, id)
	}
	rows, err := s.db.Query(
		"SELECT wd.room_id, wd.weekday FROM clinic_work_schedule_weekday wd JOIN clinic_work_schedule_staff staff ON staff.weekday_id = wd.id WHERE wd.work_schedule_id = $1 AND wd.room_id IN ("+database.Placeholders(2, len(roomIDs))+")",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("load schedule availability: %w", err)
	}
	defer rows.Close()

	staffMap := make(map[uint]map[int]bool)
	for rows.Next() {
		var roomID uint
		var weekday int
		if err := rows.Scan(&roomID, &weekday); err != nil {
			return nil, fmt.Errorf("scan schedule availability: %w", err)
		}
		if _, ok := staffMap[roomID]; !ok {
			staffMap[roomID] = make(map[int]bool)
		}
		staffMap[roomID][weekday] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load schedule availability: %w", err)
	}

	fromDate := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	toDate := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	startDate := time.Date(schedule.StartDate.Year(), schedule.StartDate.Month(), schedule.StartDate.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(schedule.EndDate.Year(), schedule.EndDate.Month(), schedule.EndDate.Day(), 0, 0, 0, 0, time.UTC)

	items := make([]ServiceAvailabilityItem, 0)
	for date := fromDate; !date.After(toDate); date = date.AddDate(0, 0, 1) {
		inPeriod := !date.Before(startDate) && !date.After(endDate)
		weekday := int(date.Weekday())
		for _, roomID := range roomIDs {
			items = append(items, ServiceAvailabilityItem{
				RoomID:    roomID,
				Date:      date.Format("2006-01-02"),
				Available: inPeriod && staffMap[roomID][weekday],
			})
		}
	}
	return items, nil
}

func (s *WorkScheduleService) ListStaff(scheduleID uint) ([]schedulingmodel.ClinicWorkScheduleStaff, error) {
	exists, err := s.scheduleExists(scheduleID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrWorkScheduleNotFound
	}
	rows, err := s.db.Query(
		"SELECT "+workScheduleStaffColumns+" FROM clinic_work_schedule_staff WHERE schedule_id = $1 ORDER BY id ASC",
		scheduleID,
	)
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	defer rows.Close()

	assignments := make([]schedulingmodel.ClinicWorkScheduleStaff, 0)
	for rows.Next() {
		assignment, err := scanWorkScheduleStaff(rows)
		if err != nil {
			return nil, fmt.Errorf("scan staff assignment: %w", err)
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	if err := s.loadAssignmentRelations(assignments); err != nil {
		return nil, err
	}
	return assignments, nil
}

func (s *WorkScheduleService) scheduleExists(scheduleID uint) (bool, error) {
	var exists bool
	if err := s.db.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM clinic_work_schedule WHERE id = $1)",
		scheduleID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check work schedule %d: %w", scheduleID, err)
	}
	return exists, nil
}

func (s *WorkScheduleService) loadAssignmentRelations(assignments []schedulingmodel.ClinicWorkScheduleStaff) error {
	if len(assignments) == 0 {
		return nil
	}
	weekdayIDs := make([]uint, 0, len(assignments))
	seenWeekdays := make(map[uint]struct{}, len(assignments))
	staffIDs := make([]int, 0, len(assignments))
	seenStaff := make(map[int]struct{}, len(assignments))
	for _, assignment := range assignments {
		if _, ok := seenWeekdays[assignment.WeekdayID]; !ok {
			seenWeekdays[assignment.WeekdayID] = struct{}{}
			weekdayIDs = append(weekdayIDs, assignment.WeekdayID)
		}
		if _, ok := seenStaff[assignment.StaffID]; !ok {
			seenStaff[assignment.StaffID] = struct{}{}
			staffIDs = append(staffIDs, assignment.StaffID)
		}
	}

	weekdayArgs := make([]any, len(weekdayIDs))
	for i, id := range weekdayIDs {
		weekdayArgs[i] = id
	}
	weekdayRows, err := s.db.Query(
		"SELECT "+workScheduleWeekdayColumns+" FROM clinic_work_schedule_weekday WHERE id IN ("+database.Placeholders(1, len(weekdayIDs))+")",
		weekdayArgs...,
	)
	if err != nil {
		return fmt.Errorf("load assignment weekdays: %w", err)
	}
	weekdays := make(map[uint]schedulingmodel.ClinicWorkScheduleWeekday, len(weekdayIDs))
	for weekdayRows.Next() {
		weekday, err := scanWorkScheduleWeekday(weekdayRows)
		if err != nil {
			weekdayRows.Close()
			return fmt.Errorf("scan assignment weekday: %w", err)
		}
		weekdays[weekday.ID] = weekday
	}
	if err := weekdayRows.Close(); err != nil {
		return fmt.Errorf("load assignment weekdays: %w", err)
	}
	if err := weekdayRows.Err(); err != nil {
		return fmt.Errorf("load assignment weekdays: %w", err)
	}

	roomIDs := make([]uint, 0, len(weekdays))
	seenRooms := make(map[uint]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		if _, ok := seenRooms[weekday.RoomID]; ok {
			continue
		}
		seenRooms[weekday.RoomID] = struct{}{}
		roomIDs = append(roomIDs, weekday.RoomID)
	}
	rooms, err := s.loadRooms(roomIDs)
	if err != nil {
		return err
	}
	for id, weekday := range weekdays {
		weekday.Room = rooms[weekday.RoomID]
		weekdays[id] = weekday
	}

	staff, err := s.loadStaffByIDs(s.db, staffIDs)
	if err != nil {
		return err
	}
	for i := range assignments {
		assignments[i].Weekday = weekdays[assignments[i].WeekdayID]
		assignments[i].Staff = staff[assignments[i].StaffID]
	}
	return nil
}

func (s *WorkScheduleService) loadRooms(roomIDs []uint) (map[uint]roommodel.ClinicRoom, error) {
	if len(roomIDs) == 0 {
		return map[uint]roommodel.ClinicRoom{}, nil
	}
	args := make([]any, len(roomIDs))
	for i, id := range roomIDs {
		args[i] = id
	}
	rows, err := s.db.Query(
		"SELECT "+roomColumns+" FROM clinic_room WHERE id IN ("+database.Placeholders(1, len(roomIDs))+")",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("load rooms: %w", err)
	}
	defer rows.Close()
	rooms := make(map[uint]roommodel.ClinicRoom, len(roomIDs))
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("scan room: %w", err)
		}
		rooms[room.ID] = room
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load rooms: %w", err)
	}
	return rooms, nil
}
