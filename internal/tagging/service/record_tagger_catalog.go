package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"clinic-backend/internal/database"
)

// SeedCatalog ensures every built-in record tag from the embedded CSV exists
// with the current description and apply_rule. Tags removed from the CSV are
// deleted after their records are moved back to the default tag.
func (s *RecordTagService) SeedCatalog() error {
	rows, err := RecordTagRows()
	if err != nil {
		return fmt.Errorf("seed record tag catalog: %w", err)
	}

	titles := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		titles = append(titles, row.Title)
	}
	titles = append(titles, DefaultRecordTagTitle)

	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, err := tx.Exec(
				`INSERT INTO clinic_record_tag (title, description, apply_rule)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (title) DO UPDATE SET description = excluded.description, apply_rule = excluded.apply_rule`,
				row.Title, row.Description, row.ApplyRule,
			); err != nil {
				return fmt.Errorf("upsert record tags: %w", err)
			}
		}

		var defaultID uint
		if err := tx.QueryRow(
			"SELECT id FROM clinic_record_tag WHERE title = $1 ORDER BY id LIMIT 1",
			DefaultRecordTagTitle,
		).Scan(&defaultID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("seed record tag catalog: default tag not found")
			}
			return fmt.Errorf("resolve default record tag: %w", err)
		}

		args := make([]any, 0, len(titles)+1)
		args = append(args, defaultID)
		for _, title := range titles {
			args = append(args, title)
		}
		if _, err := tx.Exec(
			"UPDATE clinic_record SET tag_id = $1 WHERE tag_id IN (SELECT id FROM clinic_record_tag WHERE title NOT IN ("+
				database.Placeholders(2, len(titles))+"))",
			args...,
		); err != nil {
			return fmt.Errorf("reassign records from removed tags: %w", err)
		}

		deleteArgs := make([]any, len(titles))
		for i, title := range titles {
			deleteArgs[i] = title
		}
		if _, err := tx.Exec(
			"DELETE FROM clinic_record_tag WHERE title NOT IN ("+database.Placeholders(1, len(titles))+")",
			deleteArgs...,
		); err != nil {
			return fmt.Errorf("delete removed record tags: %w", err)
		}
		return nil
	})
}
