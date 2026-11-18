package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
)

func (s *AdminRecordService) CloseExpiredRecords(ctx context.Context, cutoff time.Time) (noShowCount, completedCount int64, err error) {
	noShowStatuses := []recordmodel.RecordStatus{
		recordmodel.RecordStatusPending,
		recordmodel.RecordStatusConfirmed,
		recordmodel.RecordStatusArrived,
	}

	err = database.WithTx(ctx, s.db.DB, func(tx *sql.Tx) error {
		args := make([]any, 0, len(noShowStatuses)+2)
		args = append(args, recordmodel.RecordStatusNoShow, cutoff)
		for _, status := range noShowStatuses {
			args = append(args, status)
		}
		res, err := tx.Exec(
			fmt.Sprintf(
				"UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE appointment_time < $2 AND status IN (%s)",
				database.Placeholders(3, len(noShowStatuses)),
			),
			args...,
		)
		if err != nil {
			return fmt.Errorf("mark no-show: %w", err)
		}
		noShowCount, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark no-show: %w", err)
		}

		rows, err := tx.Query(
			"UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE appointment_time < $2 AND status = $3 RETURNING id",
			recordmodel.RecordStatusCompleted, cutoff, recordmodel.RecordStatusInProgress,
		)
		if err != nil {
			return fmt.Errorf("mark completed: %w", err)
		}
		var ids []any
		for rows.Next() {
			var id uint
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("scan completed record: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close completed records: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read completed records: %w", err)
		}
		completedCount = int64(len(ids))
		finishedAt := time.Now().UTC()
		for start := 0; start < len(ids); start += 500 {
			end := min(start+500, len(ids))
			batch := ids[start:end]
			args := append([]any{finishedAt}, batch...)
			if _, err := tx.Exec(
				"UPDATE clinic_record_worker SET finish_time = $1 WHERE record_id IN ("+database.Placeholders(2, len(batch))+")",
				args...,
			); err != nil {
				return fmt.Errorf("update worker finish_time: %w", err)
			}
		}
		return nil
	})
	return
}
