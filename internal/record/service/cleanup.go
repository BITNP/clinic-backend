package service

import (
	"context"
	"fmt"
	"time"

	recordmodel "clinic-backend/internal/record/model"

	"gorm.io/gorm"
)

func (s *AdminRecordService) CloseExpiredRecords(ctx context.Context, cutoff time.Time) (noShowCount, completedCount int64, err error) {
	noShowStatuses := []recordmodel.RecordStatus{
		recordmodel.RecordStatusPending,
		recordmodel.RecordStatusConfirmed,
		recordmodel.RecordStatusArrived,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&recordmodel.ClinicRecord{}).
			Where("appointment_time < ? AND status IN ?", cutoff, noShowStatuses).
			Updates(map[string]any{
				"status":     recordmodel.RecordStatusNoShow,
				"action_seq": gorm.Expr("action_seq + 1"),
			})
		if r.Error != nil {
			return fmt.Errorf("mark no-show: %w", r.Error)
		}
		noShowCount = r.RowsAffected

		r = tx.Model(&recordmodel.ClinicRecord{}).
			Where("appointment_time < ? AND status = ?", cutoff, recordmodel.RecordStatusInProgress).
			Updates(map[string]any{
				"status":     recordmodel.RecordStatusCompleted,
				"action_seq": gorm.Expr("action_seq + 1"),
			})
		if r.Error != nil {
			return fmt.Errorf("mark completed: %w", r.Error)
		}
		completedCount = r.RowsAffected

		if completedCount > 0 {
			sub := tx.Model(&recordmodel.ClinicRecord{}).
				Select("id").
				Where("appointment_time < ? AND status = ?", cutoff, recordmodel.RecordStatusCompleted)
			if err := tx.Model(&recordmodel.ClinicRecordWorker{}).
				Where("record_id IN (?)", sub).
				Update("finish_time", time.Now().UTC()).Error; err != nil {
				return fmt.Errorf("update worker finish_time: %w", err)
			}
		}

		return nil
	})
	return
}
