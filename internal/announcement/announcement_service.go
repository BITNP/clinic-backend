package announcement

import (
	"errors"
	"fmt"
	"time"

	"clinic-backend/internal/calendar"

	"gorm.io/gorm"
)

var (
	ErrAnnouncementNotFound         = errors.New("announcement not found")
	ErrAnnouncementInvalidTag       = errors.New("invalid announcement tag")
	ErrAnnouncementTOSAlreadyExists = errors.New("a terms-of-service announcement already exists")
)

// AnnouncementService contains the business logic for announcement CRUD.
type AnnouncementService struct {
	db *gorm.DB
}

func NewAnnouncementService(db *gorm.DB) *AnnouncementService {
	return &AnnouncementService{db: db}
}

// CreateAnnouncementInput carries the fields a caller may set on creation.
// Server-controlled fields (ID, CreatedTime, LastEditedTime) are not included.
// A nil Enabled means the announcement starts enabled.
type CreateAnnouncementInput struct {
	Title      string
	Content    string
	Tag        AnnouncementTag
	Enabled    *bool
	Brief      string
	ExpireDate time.Time
	Priority   uint
}

// UpdateAnnouncementInput uses pointers so omitted fields stay unchanged.
type UpdateAnnouncementInput struct {
	Title      *string
	Content    *string
	Tag        *AnnouncementTag
	Enabled    *bool
	Brief      *string
	ExpireDate *time.Time
	Priority   *uint
}

// ListAnnouncementFilter controls listing behavior.
type ListAnnouncementFilter struct {
	Tag        AnnouncementTag
	ActiveOnly bool
	Page       int
	PageSize   int
}

func (s *AnnouncementService) Create(in CreateAnnouncementInput) (ClinicAnnouncement, error) {
	if in.Tag == "" {
		in.Tag = AnnouncementTagNormal
	}
	if !in.Tag.Valid() {
		return ClinicAnnouncement{}, ErrAnnouncementInvalidTag
	}
	if in.Tag == AnnouncementTagTOS {
		var count int64
		if err := s.db.Model(&ClinicAnnouncement{}).Where("tag = ?", AnnouncementTagTOS).Count(&count).Error; err != nil {
			return ClinicAnnouncement{}, fmt.Errorf("check existing tos: %w", err)
		}
		if count > 0 {
			return ClinicAnnouncement{}, ErrAnnouncementTOSAlreadyExists
		}
	}

	now := time.Now().UTC()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	a := ClinicAnnouncement{
		Title:          in.Title,
		Content:        in.Content,
		Tag:            in.Tag,
		Brief:          in.Brief,
		ExpireDate:     calendar.DateInLocation(in.ExpireDate, nil),
		Priority:       in.Priority,
		CreatedTime:    now,
		LastEditedTime: now,
	}
	if err := s.db.Create(&a).Error; err != nil {
		return ClinicAnnouncement{}, fmt.Errorf("create announcement: %w", err)
	}
	// GORM writes the column default (true) instead of the zero value when a
	// field declares a default, so a disabled announcement has to be stored
	// explicitly after the insert.
	if !enabled {
		if err := s.db.Model(&a).UpdateColumn("enabled", false).Error; err != nil {
			return ClinicAnnouncement{}, fmt.Errorf("disable announcement %d: %w", a.ID, err)
		}
		a.Enabled = false
	}
	return a, nil
}

func (s *AnnouncementService) GetByID(id uint) (ClinicAnnouncement, error) {
	var a ClinicAnnouncement
	if err := s.db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ClinicAnnouncement{}, ErrAnnouncementNotFound
		}
		return ClinicAnnouncement{}, fmt.Errorf("get announcement %d: %w", id, err)
	}
	return a, nil
}

func (s *AnnouncementService) GetTOS() (ClinicAnnouncement, error) {
	var a ClinicAnnouncement
	if err := s.db.Where("tag = ?", AnnouncementTagTOS).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ClinicAnnouncement{}, ErrAnnouncementNotFound
		}
		return ClinicAnnouncement{}, fmt.Errorf("get tos: %w", err)
	}
	return a, nil
}

func (s *AnnouncementService) List(f ListAnnouncementFilter) ([]ClinicAnnouncement, int64, error) {
	q := s.db.Model(&ClinicAnnouncement{})
	if f.Tag != "" {
		q = q.Where("tag = ?", f.Tag)
	}
	if f.ActiveOnly {
		// A disabled announcement is never active, and its expiration date only
		// applies while it is enabled. Terms of service never expire.
		q = q.Where("enabled = ?", true).
			Where("(tag = ? OR expire_date >= ?)", AnnouncementTagTOS, calendar.DateInLocation(time.Now(), nil))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count announcements: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []ClinicAnnouncement
	if err := q.
		Order("priority DESC, created_time DESC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list announcements: %w", err)
	}
	return items, total, nil
}

func (s *AnnouncementService) Update(id uint, in UpdateAnnouncementInput) (ClinicAnnouncement, error) {
	var a ClinicAnnouncement
	if err := s.db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ClinicAnnouncement{}, ErrAnnouncementNotFound
		}
		return ClinicAnnouncement{}, fmt.Errorf("get announcement %d for update: %w", id, err)
	}

	if in.Tag != nil {
		if !in.Tag.Valid() {
			return ClinicAnnouncement{}, ErrAnnouncementInvalidTag
		}
		if *in.Tag == AnnouncementTagTOS && a.Tag != AnnouncementTagTOS {
			var count int64
			if err := s.db.Model(&ClinicAnnouncement{}).
				Where("tag = ? AND id != ?", AnnouncementTagTOS, id).
				Count(&count).Error; err != nil {
				return ClinicAnnouncement{}, fmt.Errorf("check existing tos: %w", err)
			}
			if count > 0 {
				return ClinicAnnouncement{}, ErrAnnouncementTOSAlreadyExists
			}
		}
	}

	updates := map[string]any{}
	if in.Title != nil {
		updates["title"] = *in.Title
	}
	if in.Content != nil {
		updates["content"] = *in.Content
	}
	if in.Tag != nil {
		updates["tag"] = *in.Tag
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if in.Brief != nil {
		updates["brief"] = *in.Brief
	}
	if in.ExpireDate != nil {
		updates["expire_date"] = calendar.DateInLocation(*in.ExpireDate, nil)
	}
	if in.Priority != nil {
		updates["priority"] = *in.Priority
	}
	updates["last_edited_time"] = time.Now().UTC()

	if len(updates) > 1 {
		if err := s.db.Model(&a).Updates(updates).Error; err != nil {
			return ClinicAnnouncement{}, fmt.Errorf("update announcement %d: %w", id, err)
		}
	}

	if err := s.db.First(&a, id).Error; err != nil {
		return ClinicAnnouncement{}, fmt.Errorf("reload announcement %d: %w", id, err)
	}
	return a, nil
}

func (s *AnnouncementService) Delete(id uint) error {
	res := s.db.Delete(&ClinicAnnouncement{}, id)
	if err := res.Error; err != nil {
		return fmt.Errorf("delete announcement %d: %w", id, err)
	}
	if res.RowsAffected == 0 {
		return ErrAnnouncementNotFound
	}
	return nil
}
