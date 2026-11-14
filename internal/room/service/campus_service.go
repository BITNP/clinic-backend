package service

import (
	"errors"
	"fmt"

	"clinic-backend/internal/database"
	roommodel "clinic-backend/internal/room/model"

	"gorm.io/gorm"
)

// ErrCampusNotFound is returned when no campus matches the given id.
var ErrCampusNotFound = errors.New("campus not found")

// ErrCampusNameTaken is returned on create/update when the campus name already exists.
var ErrCampusNameTaken = errors.New("campus name already taken")

// ErrCampusInUse is returned when a campus cannot be deleted because rooms still reference it.
var ErrCampusInUse = errors.New("campus has rooms and cannot be deleted")

// CampusService contains the business logic for clinic campus CRUD.
type CampusService struct {
	db *gorm.DB
}

func NewCampusService(db *gorm.DB) *CampusService {
	return &CampusService{db: db}
}

// CreateCampusInput carries the fields a caller may set on creation.
type CreateCampusInput struct {
	Name string
}

// UpdateCampusInput uses pointers so omitted fields stay unchanged.
type UpdateCampusInput struct {
	Name *string
}

// ListCampusFilter controls listing behavior.
type ListCampusFilter struct {
	Name     string // substring match
	Page     int
	PageSize int
}

func (s *CampusService) Create(in CreateCampusInput) (roommodel.ClinicCampus, error) {
	c := roommodel.ClinicCampus{Name: in.Name}
	if err := s.db.Create(&c).Error; err != nil {
		if database.IsUniqueViolation(err) {
			return roommodel.ClinicCampus{}, ErrCampusNameTaken
		}
		return roommodel.ClinicCampus{}, fmt.Errorf("create campus: %w", err)
	}
	return c, nil
}

func (s *CampusService) GetByID(id uint) (roommodel.ClinicCampus, error) {
	var c roommodel.ClinicCampus
	if err := s.db.First(&c, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return roommodel.ClinicCampus{}, ErrCampusNotFound
		}
		return roommodel.ClinicCampus{}, fmt.Errorf("get campus %d: %w", id, err)
	}
	return c, nil
}

func (s *CampusService) List(f ListCampusFilter) ([]roommodel.ClinicCampus, int64, error) {
	q := s.db.Model(&roommodel.ClinicCampus{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count campuses: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []roommodel.ClinicCampus
	if err := q.
		Order("id ASC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list campuses: %w", err)
	}
	return items, total, nil
}

func (s *CampusService) Update(id uint, in UpdateCampusInput) (roommodel.ClinicCampus, error) {
	var c roommodel.ClinicCampus
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&c, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCampusNotFound
			}
			return fmt.Errorf("get campus %d for update: %w", id, err)
		}

		updates := map[string]any{}
		if in.Name != nil {
			updates["name"] = *in.Name
		}
		if len(updates) == 0 {
			return nil
		}
		if err := tx.Model(&c).Updates(updates).Error; err != nil {
			if database.IsUniqueViolation(err) {
				return ErrCampusNameTaken
			}
			return fmt.Errorf("update campus %d: %w", id, err)
		}
		if err := tx.First(&c, id).Error; err != nil {
			return fmt.Errorf("reload campus %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return roommodel.ClinicCampus{}, err
	}
	return c, nil
}

func (s *CampusService) Delete(id uint) error {
	var c roommodel.ClinicCampus
	if err := s.db.First(&c, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCampusNotFound
		}
		return fmt.Errorf("get campus %d for delete: %w", id, err)
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var rooms int64
		if err := tx.Model(&roommodel.ClinicRoom{}).
			Where("campus_id = ?", id).
			Count(&rooms).Error; err != nil {
			return fmt.Errorf("count rooms for campus %d: %w", id, err)
		}
		if rooms > 0 {
			return ErrCampusInUse
		}
		if err := tx.Delete(&c).Error; err != nil {
			return fmt.Errorf("delete campus %d: %w", id, err)
		}
		return nil
	})
}
