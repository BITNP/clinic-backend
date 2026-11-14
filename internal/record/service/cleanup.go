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

		res, err = tx.Exec(
			"UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE appointment_time < $2 AND status = $3",
			recordmodel.RecordStatusCompleted, cutoff, recordmodel.RecordStatusInProgress,
		)
		if err != nil {
			return fmt.Errorf("mark completed: %w", err)
		}
		completedCount, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark completed: %w", err)
		}

		if completedCount > 0 {
			if _, err := tx.Exec(
				`UPDATE clinic_record_worker SET finish_time = $1
				 WHERE record_id IN (SELECT id FROM clinic_record WHERE appointment_time < $2 AND status = $3)`,
				time.Now().UTC(), cutoff, recordmodel.RecordStatusCompleted,
			); err != nil {
				return fmt.Errorf("update worker finish_time: %w", err)
			}
		}
		return nil
	})
	return
}
