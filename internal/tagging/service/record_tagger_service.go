package service

import (
	"database/sql"
	"fmt"

	"clinic-backend/internal/database"
	taggingmodel "clinic-backend/internal/tagging/model"
)

// DefaultRecordTagTitle is the title of the tag assigned to records that have
// not been tagged with any other rule. It is seeded at startup and used as the
// fallback tag id for new and migrated records.
const DefaultRecordTagTitle = "no_pending"

// RecordTagService stores the clinic_record_tag rows in memory at startup so
// record-tagging logic can resolve them without hitting the database. The
// in-memory values are written once during boot and only read afterwards. The
// tag-set prompt is embedded from data/record_tagger_prompt.txt.
type RecordTagService struct {
	db        *database.DB
	tags      []taggingmodel.ClinicRecordTag
	byID      map[uint]taggingmodel.ClinicRecordTag
	byTitle   map[string]taggingmodel.ClinicRecordTag
	defaultID uint
}

func NewRecordTagService(db *database.DB) *RecordTagService {
	return &RecordTagService{db: db}
}

const recordTagColumns = "id, title, description, apply_rule"

func scanRecordTag(row interface{ Scan(...any) error }) (taggingmodel.ClinicRecordTag, error) {
	var tag taggingmodel.ClinicRecordTag
	var description, applyRule sql.NullString
	err := row.Scan(&tag.ID, &tag.Title, &description, &applyRule)
	tag.Description = description.String
	tag.ApplyRule = applyRule.String
	return tag, err
}

// SeedDefault ensures the default "no_pending" tag exists and returns it.
func (s *RecordTagService) SeedDefault() (taggingmodel.ClinicRecordTag, error) {
	if _, err := s.db.Exec(
		`INSERT INTO clinic_record_tag (title, description, apply_rule)
		 VALUES ($1, '', '') ON CONFLICT (title) DO NOTHING`,
		DefaultRecordTagTitle,
	); err != nil {
		return taggingmodel.ClinicRecordTag{}, fmt.Errorf("seed default record tag: %w", err)
	}
	tag, err := scanRecordTag(s.db.QueryRow(
		"SELECT "+recordTagColumns+" FROM clinic_record_tag WHERE title = $1 ORDER BY id LIMIT 1",
		DefaultRecordTagTitle,
	))
	if err != nil {
		return taggingmodel.ClinicRecordTag{}, fmt.Errorf("seed default record tag: %w", err)
	}
	return tag, nil
}

// Load reads all tags into memory and resolves the default tag id.
func (s *RecordTagService) Load() error {
	rows, err := s.db.Query("SELECT " + recordTagColumns + " FROM clinic_record_tag ORDER BY id ASC")
	if err != nil {
		return fmt.Errorf("load record tags: %w", err)
	}
	defer rows.Close()

	tags := make([]taggingmodel.ClinicRecordTag, 0)
	for rows.Next() {
		tag, err := scanRecordTag(rows)
		if err != nil {
			return fmt.Errorf("scan record tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load record tags: %w", err)
	}

	byID := make(map[uint]taggingmodel.ClinicRecordTag, len(tags))
	byTitle := make(map[string]taggingmodel.ClinicRecordTag, len(tags))
	for _, tag := range tags {
		byID[tag.ID] = tag
		byTitle[tag.Title] = tag
	}

	s.tags = tags
	s.byID = byID
	s.byTitle = byTitle
	if tag, ok := byTitle[DefaultRecordTagTitle]; ok {
		s.defaultID = tag.ID
	}
	return nil
}

// BackfillRecords assigns the default tag id to records that do not have one.
func (s *RecordTagService) BackfillRecords() error {
	if _, err := s.db.Exec(
		"UPDATE clinic_record SET tag_id = $1 WHERE tag_id = 0 OR tag_id IS NULL",
		s.defaultID,
	); err != nil {
		return fmt.Errorf("backfill record tag_id: %w", err)
	}
	return nil
}

// DefaultID returns the cached id of the default tag.
func (s *RecordTagService) DefaultID() uint {
	return s.defaultID
}

// All returns every tag in id order, as loaded at startup.
func (s *RecordTagService) All() []taggingmodel.ClinicRecordTag {
	return s.tags
}

// ByID returns the tag with the given id, if loaded.
func (s *RecordTagService) ByID(id uint) (taggingmodel.ClinicRecordTag, bool) {
	tag, ok := s.byID[id]
	return tag, ok
}

// ByTitle returns the tag with the given title, if loaded.
func (s *RecordTagService) ByTitle(title string) (taggingmodel.ClinicRecordTag, bool) {
	tag, ok := s.byTitle[title]
	return tag, ok
}

// Prompt returns the embedded tag-set prompt.
func (s *RecordTagService) Prompt() string {
	return RecordTaggerPrompt()
}
