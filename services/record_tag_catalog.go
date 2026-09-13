package services

import (
	_ "embed"
	"fmt"
)

// recordTagCatalogSQL is the seed script for the built-in record tags. It is
// embedded and executed once at startup; the ON CONFLICT clause makes it
// idempotent across both SQLite and Postgres. Each tag's description is a
// natural-language spec for an agent to choose from; apply_rule keeps an
// extended, comma-separated keyword list for plain text matching (a "model:"
// prefix means match against the laptop model rather than the description).
//
//go:embed record_tag_catalog.sql
var recordTagCatalogSQL string

// SeedCatalog ensures every built-in record tag exists with the current
// description and apply_rule.
func (s *RecordTagService) SeedCatalog() error {
	if err := s.db.Exec(recordTagCatalogSQL).Error; err != nil {
		return fmt.Errorf("seed record tag catalog: %w", err)
	}
	return nil
}
