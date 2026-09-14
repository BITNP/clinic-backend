package services

import (
	"fmt"

	"clinic-backend/models"

	"gorm.io/gorm"
)

// DefaultRecordTagTitle is the title of the tag assigned to records that have
// not been tagged with any other rule. It is seeded at startup and used as the
// fallback tag id for new and migrated records.
const DefaultRecordTagTitle = "no_pending"

// RecordTagService stores the clinic_record_tag rows in memory at startup so
// record-tagging logic can resolve tags without hitting the database. The
// in-memory maps are written once during boot and only read afterwards.
type RecordTagService struct {
	db        *gorm.DB
	tags      []models.ClinicRecordTag
	byID      map[uint]models.ClinicRecordTag
	byTitle   map[string]models.ClinicRecordTag
	defaultID uint
}

func NewRecordTagService(db *gorm.DB) *RecordTagService {
	return &RecordTagService{db: db}
}

// SeedDefault ensures the default "no_pending" tag exists and returns it.
func (s *RecordTagService) SeedDefault() (models.ClinicRecordTag, error) {
	tag := models.ClinicRecordTag{Title: DefaultRecordTagTitle}
	if err := s.db.Where("title = ?", DefaultRecordTagTitle).FirstOrCreate(&tag).Error; err != nil {
		return models.ClinicRecordTag{}, fmt.Errorf("seed default record tag: %w", err)
	}
	return tag, nil
}

// Load reads all tags into memory and resolves the default tag id.
func (s *RecordTagService) Load() error {
	var tags []models.ClinicRecordTag
	if err := s.db.Order("id ASC").Find(&tags).Error; err != nil {
		return fmt.Errorf("load record tags: %w", err)
	}

	byID := make(map[uint]models.ClinicRecordTag, len(tags))
	byTitle := make(map[string]models.ClinicRecordTag, len(tags))
	for _, t := range tags {
		byID[t.ID] = t
		byTitle[t.Title] = t
	}

	s.tags = tags
	s.byID = byID
	s.byTitle = byTitle
	if t, ok := byTitle[DefaultRecordTagTitle]; ok {
		s.defaultID = t.ID
	}
	return nil
}

// BackfillRecords assigns the default tag id to records that do not have one.
func (s *RecordTagService) BackfillRecords() error {
	if err := s.db.Model(&models.ClinicRecord{}).
		Where("tag_id = ? OR tag_id IS NULL", 0).
		Update("tag_id", s.defaultID).Error; err != nil {
		return fmt.Errorf("backfill record tag_id: %w", err)
	}
	return nil
}

// DefaultID returns the cached id of the default tag.
func (s *RecordTagService) DefaultID() uint {
	return s.defaultID
}

// All returns every tag in id order, as loaded at startup.
func (s *RecordTagService) All() []models.ClinicRecordTag {
	return s.tags
}

// ByID returns the tag with the given id, if loaded.
func (s *RecordTagService) ByID(id uint) (models.ClinicRecordTag, bool) {
	t, ok := s.byID[id]
	return t, ok
}

// ByTitle returns the tag with the given title, if loaded.
func (s *RecordTagService) ByTitle(title string) (models.ClinicRecordTag, bool) {
	t, ok := s.byTitle[title]
	return t, ok
}
