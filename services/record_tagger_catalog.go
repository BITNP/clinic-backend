package services

import (
	"fmt"

	recordsql "clinic-backend/sql"
)

// SeedCatalog ensures every built-in record tag exists with the current
// description and apply_rule. The statements live in the standalone sql/
// package so they can also be run directly against the database.
func (s *RecordTagService) SeedCatalog() error {
	if err := s.db.Exec(recordsql.RecordTagCatalog).Error; err != nil {
		return fmt.Errorf("seed record tag catalog: %w", err)
	}
	return nil
}

// SeedPrompt ensures the singleton tag-set prompt exists with the current
// value. The statement lives in the standalone sql/ package so it can also be
// run directly against the database.
func (s *RecordTagService) SeedPrompt() error {
	if err := s.db.Exec(recordsql.RecordTagPrompt).Error; err != nil {
		return fmt.Errorf("seed record tag prompt: %w", err)
	}
	return nil
}
