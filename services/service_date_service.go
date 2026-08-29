package services

import (
	"errors"
	"fmt"
	"time"

	"clinic-backend/models"

	"gorm.io/gorm"
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
	db  *gorm.DB
	loc *time.Location
}

// defaultServiceDateLocation returns UTC+8, used when no timezone is supplied.
func defaultServiceDateLocation() *time.Location {
	return time.FixedZone("UTC+8", 8*60*60)
}

// DateInLocation returns the local calendar day of t in loc as 00:00:00 UTC.
// This treats the stored date as a wall-clock date in the configured clinic
// timezone; for example 2026-07-20 00:00:00+00:00 is interpreted as the UTC+8
// date 2026-07-20, regardless of any offset on the input.
func DateInLocation(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = defaultServiceDateLocation()
	}
	lt := t.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.UTC)
}

// NewServiceDateService creates a new ServiceDateService.
// If loc is nil, UTC+8 is used as the default timezone.
func NewServiceDateService(db *gorm.DB, loc *time.Location) *ServiceDateService {
	if loc == nil {
		loc = defaultServiceDateLocation()
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
	FromDate    time.Time      // inclusive lower bound on date, zero means unbounded
	ActiveOnly  bool           // date >= today
	TodayLoc    *time.Location // timezone used for "today"; nil means use the service's configured location
	HasCapacity bool           // for students: booked count < capacity
	Page        int
	PageSize    int
}

func (s *ServiceDateService) Create(in CreateServiceDateInput) (models.ClinicServiceDate, error) {
	if err := s.assertRoomExists(in.RoomID); err != nil {
		return models.ClinicServiceDate{}, err
	}
	d := models.ClinicServiceDate{
		Capacity:  in.Capacity,
		RoomID:    &in.RoomID,
		Date:      DateInLocation(in.Date, s.loc),
		StartTime: in.StartTime,
		EndTime:   in.EndTime,
		Title:     in.Title,
	}
	if err := s.db.Create(&d).Error; err != nil {
		return models.ClinicServiceDate{}, fmt.Errorf("create service date: %w", err)
	}
	return d, nil
}

func (s *ServiceDateService) GetByID(id uint) (models.ClinicServiceDate, error) {
	var d models.ClinicServiceDate
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return models.ClinicServiceDate{}, fmt.Errorf("get service date %d: %w", id, err)
	}
	booked, err := s.bookedCount(d.RoomID, d.Date)
	if err != nil {
		return models.ClinicServiceDate{}, err
	}
	d.Count = booked
	return d, nil
}

// assertRoomExists verifies the given room exists, returning ErrServiceDateRoomNotFound otherwise.
func (s *ServiceDateService) assertRoomExists(roomID uint) error {
	var n int64
	if err := s.db.Model(&models.ClinicRoom{}).Where("id = ?", roomID).Count(&n).Error; err != nil {
		return fmt.Errorf("check room %d: %w", roomID, err)
	}
	if n == 0 {
		return fmt.Errorf("room %d: %w", roomID, ErrServiceDateRoomNotFound)
	}
	return nil
}

// GetByDateAndRoom returns the service date for the given (roomID, date) or
// ErrServiceDateNotFound if none exists. Used by the ticket flow to verify the
// clinic is open on a requested appointment day.
func (s *ServiceDateService) GetByDateAndRoom(roomID uint, date time.Time) (models.ClinicServiceDate, error) {
	var d models.ClinicServiceDate
	if err := s.db.Where("room_id = ? AND date = ?", roomID, DateInLocation(date, s.loc)).First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return models.ClinicServiceDate{}, fmt.Errorf("get service date room=%d date=%s: %w", roomID, date.Format("2006-01-02"), err)
	}
	return d, nil
}

// recordsExistFor returns true if any repair record references the given room+date.
func (s *ServiceDateService) recordsExistFor(tx *gorm.DB, roomID uint, date time.Time) (bool, error) {
	var n int64
	q := tx.Model(&models.ClinicRecord{}).
		Where("room = ? AND appointment_time = ?", roomID, DateInLocation(date, s.loc))
	if err := q.Count(&n).Error; err != nil {
		return false, fmt.Errorf("count records for service date: %w", err)
	}
	return n > 0, nil
}

// bookedCount returns the number of active (non-rejected, non-noshow) records
// for the given room+date combination.
func (s *ServiceDateService) bookedCount(roomID *uint, date time.Time) (int64, error) {
	var n int64
	q := s.db.Model(&models.ClinicRecord{}).
		Where("room = ? AND appointment_time = ? AND status NOT IN ?",
			roomID, DateInLocation(date, s.loc),
			[]models.RecordStatus{models.RecordStatusRejected, models.RecordStatusNoShow})
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count booked: %w", err)
	}
	return n, nil
}

func (s *ServiceDateService) List(f ListServiceDateFilter) ([]models.ClinicServiceDate, int64, error) {
	q := s.db.Model(&models.ClinicServiceDate{})
	if f.RoomID != nil {
		q = q.Where("room_id = ?", *f.RoomID)
	}
	if !f.FromDate.IsZero() {
		q = q.Where("date >= ?", DateInLocation(f.FromDate, s.loc))
	}
	if f.ActiveOnly {
		loc := s.loc
		if f.TodayLoc != nil {
			loc = f.TodayLoc
		}
		q = q.Where("date >= ?", DateInLocation(time.Now(), loc))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count service dates: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []models.ClinicServiceDate
	if err := q.
		Order("date ASC, start_time ASC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list service dates: %w", err)
	}

	filtered := items[:0]
	for _, d := range items {
		booked, err := s.bookedCount(d.RoomID, d.Date)
		if err != nil {
			return nil, 0, fmt.Errorf("count booked for service date %d: %w", d.ID, err)
		}
		d.Count = booked
		if !f.HasCapacity || booked < int64(d.Capacity) {
			filtered = append(filtered, d)
		}
	}
	items = filtered
	return items, total, nil
}

func (s *ServiceDateService) Update(id uint, in UpdateServiceDateInput) (models.ClinicServiceDate, error) {
	var d models.ClinicServiceDate
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return models.ClinicServiceDate{}, fmt.Errorf("get service date %d for update: %w", id, err)
	}

	originalRoomID := *d.RoomID
	originalDate := DateInLocation(d.Date, s.loc)
	effectiveRoomID := originalRoomID
	effectiveDate := originalDate
	if in.RoomID != nil {
		if err := s.assertRoomExists(*in.RoomID); err != nil {
			return models.ClinicServiceDate{}, err
		}
		effectiveRoomID = *in.RoomID
	}
	if in.Date != nil {
		effectiveDate = DateInLocation(*in.Date, s.loc)
	}

	roomChanged := effectiveRoomID != originalRoomID
	dateChanged := !effectiveDate.Equal(originalDate)
	if roomChanged || dateChanged {
		inUse, err := s.recordsExistFor(s.db, originalRoomID, originalDate)
		if err != nil {
			return models.ClinicServiceDate{}, err
		}
		if inUse {
			return models.ClinicServiceDate{}, ErrServiceDateInUse
		}
		inUse, err = s.recordsExistFor(s.db, effectiveRoomID, effectiveDate)
		if err != nil {
			return models.ClinicServiceDate{}, err
		}
		if inUse {
			return models.ClinicServiceDate{}, ErrServiceDateInUse
		}
	}

	if in.Capacity != nil {
		booked, err := s.bookedCount(&effectiveRoomID, effectiveDate)
		if err != nil {
			return models.ClinicServiceDate{}, err
		}
		if *in.Capacity < uint(booked) {
			return models.ClinicServiceDate{}, fmt.Errorf("capacity cannot be less than current booked count %d: %w", booked, ErrServiceDateInUse)
		}
	}

	updates := map[string]any{}
	if in.Capacity != nil {
		updates["capacity"] = *in.Capacity
	}
	if in.RoomID != nil {
		updates["room_id"] = *in.RoomID
	}
	if in.Date != nil {
		updates["date"] = DateInLocation(*in.Date, s.loc)
	}
	if in.StartTime != nil {
		updates["start_time"] = *in.StartTime
	}
	if in.EndTime != nil {
		updates["end_time"] = *in.EndTime
	}
	if in.Title != nil {
		updates["title"] = *in.Title
	}

	if len(updates) > 0 {
		if err := s.db.Model(&d).Updates(updates).Error; err != nil {
			return models.ClinicServiceDate{}, fmt.Errorf("update service date %d: %w", id, err)
		}
	}

	if err := s.db.First(&d, id).Error; err != nil {
		return models.ClinicServiceDate{}, fmt.Errorf("reload service date %d: %w", id, err)
	}
	return d, nil
}

func (s *ServiceDateService) Delete(id uint) error {
	var d models.ClinicServiceDate
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrServiceDateNotFound
		}
		return fmt.Errorf("get service date %d for delete: %w", id, err)
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		inUse, err := s.recordsExistFor(tx, *d.RoomID, d.Date)
		if err != nil {
			return err
		}
		if inUse {
			return ErrServiceDateInUse
		}
		if err := tx.Delete(&d).Error; err != nil {
			return fmt.Errorf("delete service date %d: %w", id, err)
		}
		return nil
	})
}
