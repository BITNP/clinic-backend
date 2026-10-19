package service

import (
	"errors"
	"fmt"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"

	"gorm.io/gorm"
)

// ErrRoomNotFound is returned when no room matches the given id.
var ErrRoomNotFound = errors.New("room not found")

// ErrRoomNameTaken is returned on create/update when the room name already exists.
var ErrRoomNameTaken = errors.New("room name already taken")

// ErrRoomInUse is returned when a room cannot be deleted because
// repair records or service dates still reference it.
var ErrRoomInUse = errors.New("room has existing service dates or records and cannot be deleted")

// ErrRoomHasUnfinishedRecords is returned when a room cannot be disabled
// because it still has tickets in an unfinished (working) state.
var ErrRoomHasUnfinishedRecords = errors.New("room has unfinished tickets and cannot be disabled")

// RoomService contains the business logic for clinic room CRUD.
type RoomService struct {
	db *gorm.DB
}

func NewRoomService(db *gorm.DB) *RoomService {
	return &RoomService{db: db}
}

// CreateRoomInput carries the fields a caller may set on creation.
// Server-controlled fields (ID) are not included.
type CreateRoomInput struct {
	Name    string
	Address string
	Enabled *bool // nil defaults to true
}

// UpdateRoomInput uses pointers so omitted fields stay unchanged.
type UpdateRoomInput struct {
	Name    *string
	Address *string
	Enabled *bool
}

// ListRoomFilter controls listing behavior.
type ListRoomFilter struct {
	Name        string // substring match (ILIKE/LIKE)
	EnabledOnly bool   // true: only enabled rooms (used by client routes)
	Page        int
	PageSize    int
}

func (s *RoomService) Create(in CreateRoomInput) (roommodel.ClinicRoom, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	r := roommodel.ClinicRoom{
		Name:    in.Name,
		Address: in.Address,
		Enabled: enabled,
	}
	if err := s.db.Create(&r).Error; err != nil {
		if database.IsUniqueViolation(err) {
			return roommodel.ClinicRoom{}, ErrRoomNameTaken
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("create room: %w", err)
	}
	return r, nil
}

func (s *RoomService) GetByID(id uint) (roommodel.ClinicRoom, error) {
	var r roommodel.ClinicRoom
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return roommodel.ClinicRoom{}, ErrRoomNotFound
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("get room %d: %w", id, err)
	}
	return r, nil
}

func (s *RoomService) GetByName(name string) (roommodel.ClinicRoom, error) {
	var r roommodel.ClinicRoom
	if err := s.db.Where("name = ?", name).First(&r).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return roommodel.ClinicRoom{}, ErrRoomNotFound
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("get room by name %q: %w", name, err)
	}
	return r, nil
}

func (s *RoomService) List(f ListRoomFilter) ([]roommodel.ClinicRoom, int64, error) {
	q := s.db.Model(&roommodel.ClinicRoom{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.EnabledOnly {
		q = q.Where("enabled = ?", true)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count rooms: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []roommodel.ClinicRoom
	if err := q.
		Order("id ASC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list rooms: %w", err)
	}
	return items, total, nil
}

func (s *RoomService) Update(id uint, in UpdateRoomInput) (roommodel.ClinicRoom, error) {
	var r roommodel.ClinicRoom
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&r, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRoomNotFound
			}
			return fmt.Errorf("get room %d for update: %w", id, err)
		}

		// Disabling a room is refused while it still has unfinished tickets.
		if in.Enabled != nil && !*in.Enabled {
			unfinished, err := s.hasUnfinishedRecords(tx, id)
			if err != nil {
				return err
			}
			if unfinished {
				return ErrRoomHasUnfinishedRecords
			}
		}

		updates := map[string]any{}
		if in.Name != nil {
			updates["name"] = *in.Name
		}
		if in.Address != nil {
			updates["address"] = *in.Address
		}
		if in.Enabled != nil {
			updates["enabled"] = *in.Enabled
		}

		if len(updates) > 0 {
			if err := tx.Model(&r).Updates(updates).Error; err != nil {
				if database.IsUniqueViolation(err) {
					return ErrRoomNameTaken
				}
				return fmt.Errorf("update room %d: %w", id, err)
			}
		}

		if err := tx.First(&r, id).Error; err != nil {
			return fmt.Errorf("reload room %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return roommodel.ClinicRoom{}, err
	}
	return r, nil
}

func (s *RoomService) Delete(id uint) error {
	var r roommodel.ClinicRoom
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRoomNotFound
		}
		return fmt.Errorf("get room %d for delete: %w", id, err)
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// A room with unfinished tickets cannot be removed.
		unfinished, err := s.hasUnfinishedRecords(tx, id)
		if err != nil {
			return err
		}
		if unfinished {
			return ErrRoomHasUnfinishedRecords
		}

		inUse, err := s.isRoomInUse(tx, id)
		if err != nil {
			return err
		}
		if inUse {
			return ErrRoomInUse
		}
		if err := tx.Delete(&r).Error; err != nil {
			return fmt.Errorf("delete room %d: %w", id, err)
		}
		return nil
	})
}

// hasUnfinishedRecords reports whether any ticket referencing the room is still
// in an unfinished (working) state. Shared by the room disable/remove guards.
func (s *RoomService) hasUnfinishedRecords(tx *gorm.DB, roomID uint) (bool, error) {
	var n int64
	if err := tx.Model(&recordmodel.ClinicRecord{}).
		Where("room = ? AND status IN ?", roomID, recordmodel.WorkingStatuses()).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("count unfinished records for room %d: %w", roomID, err)
	}
	return n > 0, nil
}

// isRoomInUse reports whether any service date or repair record references the room.
func (s *RoomService) isRoomInUse(tx *gorm.DB, roomID uint) (bool, error) {
	var dates int64
	if err := tx.Model(&schedulingmodel.ClinicServiceDate{}).
		Where("room_id = ?", roomID).
		Count(&dates).Error; err != nil {
		return false, fmt.Errorf("count service dates for room %d: %w", roomID, err)
	}
	if dates > 0 {
		return true, nil
	}

	var records int64
	if err := tx.Model(&recordmodel.ClinicRecord{}).
		Where("room = ?", roomID).
		Count(&records).Error; err != nil {
		return false, fmt.Errorf("count records for room %d: %w", roomID, err)
	}
	return records > 0, nil
}
