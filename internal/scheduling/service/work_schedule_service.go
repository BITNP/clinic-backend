package service

import (
	"errors"
	"fmt"
	"time"

	"clinic-backend/internal/database"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
	staffservice "clinic-backend/internal/staff/service"

	"gorm.io/gorm"
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
	db *gorm.DB
}

func NewWorkScheduleService(db *gorm.DB) *WorkScheduleService {
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

func (s *WorkScheduleService) validateWeekdayInTx(tx *gorm.DB, in WeekdayInput, skipStaff bool) (parsedWeekday, error) {
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
	var roomCount int64
	if err := tx.Model(&roommodel.ClinicRoom{}).Where("id = ?", in.RoomID).Count(&roomCount).Error; err != nil {
		return parsedWeekday{}, fmt.Errorf("check room: %w", err)
	}
	if roomCount == 0 {
		return parsedWeekday{}, ErrWorkScheduleRoomNotFound
	}
	if !skipStaff && len(in.StaffIDs) > 0 {
		for _, sid := range in.StaffIDs {
			var staffCount int64
			if err := tx.Model(&staffmodel.ClinicStaff{}).Where("id = ?", sid).Count(&staffCount).Error; err != nil {
				return parsedWeekday{}, fmt.Errorf("check staff %d: %w", sid, err)
			}
			if staffCount == 0 {
				return parsedWeekday{}, fmt.Errorf("staff %d: %w", sid, ErrWorkScheduleStaffNotFound)
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

func (s *WorkScheduleService) createWeekdaysInTx(tx *gorm.DB, scheduleID uint, inputs []WeekdayInput) error {
	for _, in := range inputs {
		pw, err := s.validateWeekdayInTx(tx, in, false)
		if err != nil {
			return err
		}
		wd := schedulingmodel.ClinicWorkScheduleWeekday{
			WorkScheduleID: scheduleID,
			Weekday:        pw.weekday,
			StartTime:      pw.startTime,
			EndTime:        pw.endTime,
			RoomID:         pw.roomID,
		}
		if err := tx.Create(&wd).Error; err != nil {
			return fmt.Errorf("create weekday: %w", err)
		}
		for _, sid := range pw.staffIDs {
			if err := tx.Create(&schedulingmodel.ClinicWorkScheduleStaff{
				WeekdayID:  wd.ID,
				StaffID:    sid,
				ScheduleID: scheduleID,
			}).Error; err != nil {
				return fmt.Errorf("create staff assignment: %w", err)
			}
		}
	}
	return nil
}

func (s *WorkScheduleService) hasEnabledSchedule(tx *gorm.DB, excludeID uint) (bool, error) {
	q := tx.Model(&schedulingmodel.ClinicWorkSchedule{}).Where("enabled = ?", true)
	if excludeID > 0 {
		q = q.Where("id != ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check enabled schedule: %w", err)
	}
	return count > 0, nil
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

	sch := schedulingmodel.ClinicWorkSchedule{
		Name:      in.Name,
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
		Enabled:   in.Enabled,
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if in.Enabled {
			enabled, err := s.hasEnabledSchedule(tx, 0)
			if err != nil {
				return err
			}
			if enabled {
				return ErrWorkScheduleAlreadyEnabled
			}
		}
		if err := tx.Create(&sch).Error; err != nil {
			if database.IsUniqueViolation(err) {
				return ErrWorkScheduleNameTaken
			}
			return fmt.Errorf("create work schedule: %w", err)
		}
		if err := s.createWeekdaysInTx(tx, sch.ID, in.Weekdays); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, err
	}
	return sch, nil
}

func (s *WorkScheduleService) GetByID(id uint) (schedulingmodel.ClinicWorkSchedule, error) {
	var sch schedulingmodel.ClinicWorkSchedule
	if err := s.db.
		Preload("Weekdays", func(db *gorm.DB) *gorm.DB {
			return db.Order("weekday ASC")
		}).
		Preload("Weekdays.Room").
		Preload("Weekdays.Staff").
		Preload("Weekdays.Staff.Staff").
		First(&sch, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return schedulingmodel.ClinicWorkSchedule{}, ErrWorkScheduleNotFound
		}
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("get work schedule %d: %w", id, err)
	}
	return sch, nil
}

func (s *WorkScheduleService) List(f ListWorkScheduleFilter) ([]schedulingmodel.ClinicWorkSchedule, int64, error) {
	q := s.db.Model(&schedulingmodel.ClinicWorkSchedule{})
	if f.Enabled != nil {
		q = q.Where("enabled = ?", *f.Enabled)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count work schedules: %w", err)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []schedulingmodel.ClinicWorkSchedule
	if err := q.
		Order("id ASC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list work schedules: %w", err)
	}
	return items, total, nil
}

func (s *WorkScheduleService) Update(id uint, in UpdateWorkScheduleInput) (schedulingmodel.ClinicWorkSchedule, error) {
	var sch schedulingmodel.ClinicWorkSchedule
	if err := s.db.First(&sch, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return schedulingmodel.ClinicWorkSchedule{}, ErrWorkScheduleNotFound
		}
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("get work schedule %d for update: %w", id, err)
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{}
		if in.Name != nil {
			updates["name"] = *in.Name
		}
		if in.StartDate != nil {
			updates["start_date"] = *in.StartDate
		}
		if in.EndDate != nil {
			updates["end_date"] = *in.EndDate
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
			updates["enabled"] = *in.Enabled
		}

		if in.StartDate != nil && in.EndDate != nil {
			if in.StartDate.After(*in.EndDate) {
				return ErrWorkScheduleInvalidDateRange
			}
		}
		if in.StartDate != nil && in.EndDate == nil {
			if in.StartDate.After(sch.EndDate) {
				return ErrWorkScheduleInvalidDateRange
			}
		}
		if in.StartDate == nil && in.EndDate != nil {
			if sch.StartDate.After(*in.EndDate) {
				return ErrWorkScheduleInvalidDateRange
			}
		}

		if len(updates) > 0 {
			if name, ok := updates["name"]; ok {
				var dup int64
				if err := tx.Model(&schedulingmodel.ClinicWorkSchedule{}).
					Where("name = ? AND id != ?", name, id).
					Count(&dup).Error; err != nil {
					return fmt.Errorf("check name duplicate: %w", err)
				}
				if dup > 0 {
					return ErrWorkScheduleNameTaken
				}
			}
			if err := tx.Model(&sch).Updates(updates).Error; err != nil {
				return fmt.Errorf("update work schedule %d: %w", id, err)
			}
		}

		if in.Weekdays != nil {
			var existingWDs []schedulingmodel.ClinicWorkScheduleWeekday
			if err := tx.Where("work_schedule_id = ?", id).Find(&existingWDs).Error; err != nil {
				return fmt.Errorf("find existing weekdays: %w", err)
			}
			for _, wd := range existingWDs {
				if err := tx.Where("weekday_id = ?", wd.ID).Delete(&schedulingmodel.ClinicWorkScheduleStaff{}).Error; err != nil {
					return fmt.Errorf("delete staff assignments: %w", err)
				}
				if err := tx.Delete(&wd).Error; err != nil {
					return fmt.Errorf("delete weekday: %w", err)
				}
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

	if err := s.db.
		Preload("Weekdays", func(db *gorm.DB) *gorm.DB {
			return db.Order("weekday ASC")
		}).
		Preload("Weekdays.Room").
		Preload("Weekdays.Staff").
		Preload("Weekdays.Staff.Staff").
		First(&sch, id).Error; err != nil {
		return schedulingmodel.ClinicWorkSchedule{}, fmt.Errorf("reload work schedule %d: %w", id, err)
	}
	return sch, nil
}

func (s *WorkScheduleService) Delete(id uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var sch schedulingmodel.ClinicWorkSchedule
		if err := tx.First(&sch, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkScheduleNotFound
			}
			return fmt.Errorf("get work schedule %d for delete: %w", id, err)
		}
		var wds []schedulingmodel.ClinicWorkScheduleWeekday
		if err := tx.Where("work_schedule_id = ?", id).Find(&wds).Error; err != nil {
			return fmt.Errorf("find weekdays: %w", err)
		}
		for _, wd := range wds {
			if err := tx.Where("weekday_id = ?", wd.ID).Delete(&schedulingmodel.ClinicWorkScheduleStaff{}).Error; err != nil {
				return fmt.Errorf("delete staff: %w", err)
			}
		}
		if err := tx.Where("work_schedule_id = ?", id).Delete(&schedulingmodel.ClinicWorkScheduleWeekday{}).Error; err != nil {
			return fmt.Errorf("delete weekdays: %w", err)
		}
		if err := tx.Delete(&sch).Error; err != nil {
			return fmt.Errorf("delete work schedule: %w", err)
		}
		return nil
	})
}

func (s *WorkScheduleService) AddStaff(scheduleID uint, in StaffAssignmentInput) (schedulingmodel.ClinicWorkScheduleStaff, error) {
	var sch schedulingmodel.ClinicWorkSchedule
	if err := s.db.First(&sch, scheduleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleNotFound
		}
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("find schedule: %w", err)
	}

	// Validate staff exists.
	var staffCount int64
	if err := s.db.Model(&staffmodel.ClinicStaff{}).Where("id = ?", in.StaffID).Count(&staffCount).Error; err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("check staff: %w", err)
	}
	if staffCount == 0 {
		return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleStaffNotFound
	}

	// Validate staff is in the schedule's work year.
	staffSvc := staffservice.NewStaffService(s.db)
	ok, err := staffSvc.IsValidForDate(in.StaffID, sch.StartDate)
	if err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, err
	}
	if !ok {
		return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleStaffNotInWorkYear
	}

	// Resolve the weekday slot.
	var wdID uint
	if in.WeekdayID > 0 {
		var wd schedulingmodel.ClinicWorkScheduleWeekday
		if err := s.db.Where("id = ? AND work_schedule_id = ?", in.WeekdayID, scheduleID).First(&wd).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleWeekdayNotFound
			}
			return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("find weekday: %w", err)
		}
		wdID = wd.ID
	} else if in.RoomID > 0 {
		if in.Weekday < 0 || in.Weekday > 6 {
			return schedulingmodel.ClinicWorkScheduleStaff{}, ErrWorkScheduleInvalidWeekday
		}
		var wd schedulingmodel.ClinicWorkScheduleWeekday
		err := s.db.Where("work_schedule_id = ? AND room_id = ? AND weekday = ?", scheduleID, in.RoomID, in.Weekday).
			First(&wd).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("find weekday: %w", err)
			}
			// Create a new slot with default times.
			startTime, _ := parseTimeOnly("18:30")
			endTime, _ := parseTimeOnly("21:00")
			wd = schedulingmodel.ClinicWorkScheduleWeekday{
				WorkScheduleID: scheduleID,
				Weekday:        in.Weekday,
				StartTime:      startTime,
				EndTime:        endTime,
				RoomID:         in.RoomID,
			}
			if err := s.db.Create(&wd).Error; err != nil {
				return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("create weekday: %w", err)
			}
		}
		wdID = wd.ID
	} else {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("must provide weekday_id or (room_id + weekday)")
	}

	// Check for existing duplicate assignment.
	var existing schedulingmodel.ClinicWorkScheduleStaff
	if err := s.db.Where("weekday_id = ? AND staff_id = ? AND schedule_id = ?", wdID, in.StaffID, scheduleID).
		Preload("Staff").
		First(&existing).Error; err == nil {
		return existing, nil
	}

	assign := schedulingmodel.ClinicWorkScheduleStaff{
		WeekdayID:  wdID,
		StaffID:    in.StaffID,
		ScheduleID: scheduleID,
	}
	if err := s.db.Create(&assign).Error; err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("add staff: %w", err)
	}
	if err := s.db.Preload("Staff").First(&assign, assign.ID).Error; err != nil {
		return schedulingmodel.ClinicWorkScheduleStaff{}, fmt.Errorf("reload assignment: %w", err)
	}
	return assign, nil
}

func (s *WorkScheduleService) ListValidStaff(scheduleID uint) ([]staffservice.StaffListItem, error) {
	var sch schedulingmodel.ClinicWorkSchedule
	if err := s.db.First(&sch, scheduleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkScheduleNotFound
		}
		return nil, fmt.Errorf("find schedule: %w", err)
	}
	staffSvc := staffservice.NewStaffService(s.db)
	return staffSvc.ListValidForYear(sch.StartDate.Year())
}

func (s *WorkScheduleService) RemoveStaff(scheduleID uint, in StaffAssignmentInput) error {
	var wd schedulingmodel.ClinicWorkScheduleWeekday
	if err := s.db.Where("id = ? AND work_schedule_id = ?", in.WeekdayID, scheduleID).First(&wd).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkScheduleWeekdayNotFound
		}
		return fmt.Errorf("find weekday: %w", err)
	}
	res := s.db.Where("weekday_id = ? AND staff_id = ? AND schedule_id = ?", in.WeekdayID, in.StaffID, scheduleID).
		Delete(&schedulingmodel.ClinicWorkScheduleStaff{})
	if err := res.Error; err != nil {
		return fmt.Errorf("remove staff: %w", err)
	}
	if res.RowsAffected == 0 {
		return ErrWorkScheduleStaffNotFound
	}
	return nil
}

func (s *WorkScheduleService) UpdateWeekday(scheduleID uint, in UpdateWeekdayInput) (schedulingmodel.ClinicWorkScheduleWeekday, error) {
	var sch schedulingmodel.ClinicWorkSchedule
	if err := s.db.First(&sch, scheduleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return schedulingmodel.ClinicWorkScheduleWeekday{}, ErrWorkScheduleNotFound
		}
		return schedulingmodel.ClinicWorkScheduleWeekday{}, fmt.Errorf("get work schedule %d: %w", scheduleID, err)
	}

	var wd schedulingmodel.ClinicWorkScheduleWeekday
	err := s.db.Transaction(func(tx *gorm.DB) error {
		pw, err := s.validateWeekdayInTx(tx, WeekdayInput{
			Weekday:   in.Weekday,
			StartTime: in.StartTime,
			EndTime:   in.EndTime,
			RoomID:    in.RoomID,
			StaffIDs:  []int{},
		}, true)
		if err != nil {
			return err
		}

		err = tx.Where("work_schedule_id = ? AND room_id = ? AND weekday = ?", scheduleID, in.RoomID, in.Weekday).
			First(&wd).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			wd = schedulingmodel.ClinicWorkScheduleWeekday{
				WorkScheduleID: scheduleID,
				RoomID:         in.RoomID,
				Weekday:        in.Weekday,
				StartTime:      pw.startTime,
				EndTime:        pw.endTime,
			}
			if err := tx.Create(&wd).Error; err != nil {
				return fmt.Errorf("create weekday: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("find weekday: %w", err)
		} else {
			wd.StartTime = pw.startTime
			wd.EndTime = pw.endTime
			if err := tx.Save(&wd).Error; err != nil {
				return fmt.Errorf("update weekday: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return schedulingmodel.ClinicWorkScheduleWeekday{}, err
	}
	return wd, nil
}

func (s *WorkScheduleService) ServiceAvailability(from, to time.Time, roomIDs []uint) ([]ServiceAvailabilityItem, error) {
	if len(roomIDs) == 0 {
		return []ServiceAvailabilityItem{}, nil
	}

	var schedule schedulingmodel.ClinicWorkSchedule
	if err := s.db.Where("enabled = ?", true).
		Preload("Weekdays", func(db *gorm.DB) *gorm.DB {
			return db.Where("room_id IN ?", roomIDs).Preload("Staff")
		}).
		First(&schedule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkScheduleNotFound
		}
		return nil, fmt.Errorf("find enabled schedule: %w", err)
	}

	staffMap := make(map[uint]map[int]bool)
	for _, wd := range schedule.Weekdays {
		if len(wd.Staff) == 0 {
			continue
		}
		if _, ok := staffMap[wd.RoomID]; !ok {
			staffMap[wd.RoomID] = make(map[int]bool)
		}
		staffMap[wd.RoomID][wd.Weekday] = true
	}

	fromDate := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	toDate := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)

	startDate := time.Date(schedule.StartDate.Year(), schedule.StartDate.Month(), schedule.StartDate.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(schedule.EndDate.Year(), schedule.EndDate.Month(), schedule.EndDate.Day(), 0, 0, 0, 0, time.UTC)

	var items []ServiceAvailabilityItem
	for d := fromDate; !d.After(toDate); d = d.AddDate(0, 0, 1) {
		inPeriod := !d.Before(startDate) && !d.After(endDate)
		weekday := int(d.Weekday())
		for _, roomID := range roomIDs {
			available := inPeriod && staffMap[roomID][weekday]
			items = append(items, ServiceAvailabilityItem{
				RoomID:    roomID,
				Date:      d.Format("2006-01-02"),
				Available: available,
			})
		}
	}
	return items, nil
}

func (s *WorkScheduleService) ListStaff(scheduleID uint) ([]schedulingmodel.ClinicWorkScheduleStaff, error) {
	if _, err := s.GetByID(scheduleID); err != nil {
		return nil, err
	}
	var result []schedulingmodel.ClinicWorkScheduleStaff
	if err := s.db.
		Where("schedule_id = ?", scheduleID).
		Preload("Weekday").
		Preload("Weekday.Room").
		Preload("Staff").
		Order("id ASC").
		Find(&result).Error; err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	return result, nil
}
