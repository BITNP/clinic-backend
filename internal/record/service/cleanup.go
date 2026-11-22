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
	}
	completedStatuses := []recordmodel.RecordStatus{
		recordmodel.RecordStatusArrived,
		recordmodel.RecordStatusInProgress,
	}
	var noShowRecords []recordmodel.ClinicRecord

	err = database.WithTx(ctx, s.db.DB, func(tx *sql.Tx) error {
		args := make([]any, 0, len(noShowStatuses)+2)
		args = append(args, recordmodel.RecordStatusNoShow, cutoff)
		for _, status := range noShowStatuses {
			args = append(args, status)
		}
		rows, err := tx.Query(
			fmt.Sprintf(
				`UPDATE clinic_record
				 SET status = $1, action_seq = action_seq + 1
				 WHERE appointment_time < $2 AND status IN (%s)
				 RETURNING id, COALESCE("user", ''), appointment_time, room`,
				database.Placeholders(3, len(noShowStatuses)),
			),
			args...,
		)
		if err != nil {
			return fmt.Errorf("mark no-show: %w", err)
		}
		for rows.Next() {
			record := recordmodel.ClinicRecord{Status: recordmodel.RecordStatusNoShow}
			if err := rows.Scan(&record.ID, &record.User, &record.AppointmentTime, &record.RoomID); err != nil {
				rows.Close()
				return fmt.Errorf("scan no-show record: %w", err)
			}
			noShowRecords = append(noShowRecords, record)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close no-show records: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read no-show records: %w", err)
		}
		noShowCount = int64(len(noShowRecords))

		completedArgs := []any{recordmodel.RecordStatusCompleted, cutoff}
		for _, status := range completedStatuses {
			completedArgs = append(completedArgs, status)
		}
		rows, err = tx.Query(
			"UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE appointment_time < $2 AND status IN ("+database.Placeholders(3, len(completedStatuses))+") RETURNING id",
			completedArgs...,
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
	if err != nil {
		return noShowCount, completedCount, err
	}
	for _, record := range noShowRecords {
		s.notifyTicketStatusChange(ctx, record, "")
	}
	return
}
