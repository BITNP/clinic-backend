package services

import (
	"errors"
	"fmt"

	"clinic-backend/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SeedCatalog ensures every built-in record tag from the embedded CSV exists
// with the current description and apply_rule. Tags removed from the CSV are
// deleted after their records are moved back to the default tag.
func (s *RecordTagService) SeedCatalog() error {
	rows, err := RecordTagRows()
	if err != nil {
		return fmt.Errorf("seed record tag catalog: %w", err)
	}

	tags := make([]models.ClinicRecordTag, len(rows))
	titles := make([]string, 0, len(rows)+1)
	for i, row := range rows {
		tags[i] = models.ClinicRecordTag{
			Title:       row.Title,
			Description: row.Description,
			ApplyRule:   row.ApplyRule,
		}
		titles = append(titles, row.Title)
	}
	titles = append(titles, DefaultRecordTagTitle)

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "title"}},
			DoUpdates: clause.AssignmentColumns([]string{"description", "apply_rule"}),
		}).Create(&tags).Error; err != nil {
			return fmt.Errorf("upsert record tags: %w", err)
		}

		var defaultID uint
		if err := tx.Model(&models.ClinicRecordTag{}).
			Where("title = ?", DefaultRecordTagTitle).
			Limit(1).
			Pluck("id", &defaultID).Error; err != nil {
			return fmt.Errorf("resolve default record tag: %w", err)
		}
		if defaultID == 0 {
			return errors.New("seed record tag catalog: default tag not found")
		}

		staleIDs := tx.Model(&models.ClinicRecordTag{}).
			Select("id").
			Where("title NOT IN ?", titles)
		if err := tx.Model(&models.ClinicRecord{}).
			Where("tag_id IN (?)", staleIDs).
			Update("tag_id", defaultID).Error; err != nil {
			return fmt.Errorf("reassign records from removed tags: %w", err)
		}
		if err := tx.Where("title NOT IN ?", titles).
			Delete(&models.ClinicRecordTag{}).Error; err != nil {
			return fmt.Errorf("delete removed record tags: %w", err)
		}
		return nil
	})
}
