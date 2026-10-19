package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	recordmodel "clinic-backend/internal/record/model"

	"gorm.io/gorm"
)

func (s *AdminRecordService) MarkConfirmed(ctx context.Context, id uint, approverID uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != recordmodel.RecordStatusPending {
		return AdminRecordView{}, fmt.Errorf("confirm record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"status":      recordmodel.RecordStatusConfirmed,
			"approver_id": approverID,
			"action_seq":  gorm.Expr("action_seq + 1"),
		}
		if err := tx.Model(&rec).Updates(updates).Error; err != nil {
			return fmt.Errorf("confirm record %d: %w", id, err)
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   int(approverID),
			Action:    RecordActionConfirm,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, entry.ActorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkArrived(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != recordmodel.RecordStatusConfirmed {
		return AdminRecordView{}, fmt.Errorf("arrive record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		if err := tx.Model(&rec).Updates(map[string]any{
			"status":     recordmodel.RecordStatusArrived,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark arrived record %d: %w", id, err)
		}

		var arrival recordmodel.ClinicRecordArrival
		if err := tx.Where("record_id = ?", rec.ID).First(&arrival).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d arrival: %w", id, err)
			}
			arrival = recordmodel.ClinicRecordArrival{
				RecordID:   rec.ID,
				ArriveTime: time.Now().UTC(),
			}
			if err := tx.Create(&arrival).Error; err != nil {
				return fmt.Errorf("create record %d arrival: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   actorID,
			Action:    RecordActionArrive,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkInProgress(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != recordmodel.RecordStatusConfirmed && rec.Status != recordmodel.RecordStatusArrived {
		return AdminRecordView{}, fmt.Errorf("in-progress record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		if err := tx.Model(&rec).Updates(map[string]any{
			"status":     recordmodel.RecordStatusInProgress,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark in_progress record %d: %w", id, err)
		}

		var worker recordmodel.ClinicRecordWorker
		if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d worker: %w", id, err)
			}
			worker = recordmodel.ClinicRecordWorker{
				RecordID: rec.ID,
				WorkerID: uint(actorID),
			}
			if err := tx.Create(&worker).Error; err != nil {
				return fmt.Errorf("create record %d worker: %w", id, err)
			}
		} else {
			if err := tx.Model(&worker).Update("worker", uint(actorID)).Error; err != nil {
				return fmt.Errorf("update record %d worker: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   actorID,
			Action:    RecordActionInProgress,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkCompleted(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != recordmodel.RecordStatusInProgress {
		return AdminRecordView{}, fmt.Errorf("complete record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		if err := tx.Model(&rec).Updates(map[string]any{
			"status":     recordmodel.RecordStatusCompleted,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark completed record %d: %w", id, err)
		}

		var worker recordmodel.ClinicRecordWorker
		if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d worker: %w", id, err)
			}
			worker = recordmodel.ClinicRecordWorker{
				RecordID:   rec.ID,
				FinishTime: time.Now().UTC(),
			}
			if err := tx.Create(&worker).Error; err != nil {
				return fmt.Errorf("create record %d worker: %w", id, err)
			}
		} else {
			if err := tx.Model(&worker).Update("finish_time", time.Now().UTC()).Error; err != nil {
				return fmt.Errorf("update record %d worker finish_time: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   actorID,
			Action:    RecordActionComplete,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkRejected(ctx context.Context, id uint, reason string, approverID uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != recordmodel.RecordStatusPending {
		return AdminRecordView{}, fmt.Errorf("reject record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"status":      recordmodel.RecordStatusRejected,
			"approver_id": approverID,
			"action_seq":  gorm.Expr("action_seq + 1"),
		}
		if err := tx.Model(&rec).Updates(updates).Error; err != nil {
			return fmt.Errorf("mark rejected record %d: %w", id, err)
		}

		var rejection recordmodel.ClinicRecordRejection
		if err := tx.Where("record_id = ?", rec.ID).First(&rejection).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d rejection: %w", id, err)
			}
			rejection = recordmodel.ClinicRecordRejection{
				RecordID:     rec.ID,
				RejectReason: reason,
			}
			if err := tx.Create(&rejection).Error; err != nil {
				return fmt.Errorf("create record %d rejection: %w", id, err)
			}
		} else {
			if err := tx.Model(&rejection).Update("reject_reason", reason).Error; err != nil {
				return fmt.Errorf("update record %d rejection: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   int(approverID),
			Action:    RecordActionReject,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, entry.ActorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkReferred(ctx context.Context, id uint, reason string, approverID uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	switch rec.Status {
	case recordmodel.RecordStatusRejected, recordmodel.RecordStatusCompleted, recordmodel.RecordStatusReferred, recordmodel.RecordStatusNoShow:
		return AdminRecordView{}, fmt.Errorf("refer record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"status":      recordmodel.RecordStatusReferred,
			"approver_id": approverID,
			"action_seq":  gorm.Expr("action_seq + 1"),
		}
		if err := tx.Model(&rec).Updates(updates).Error; err != nil {
			return fmt.Errorf("mark referred record %d: %w", id, err)
		}

		var referral recordmodel.ClinicRecordReferral
		if err := tx.Where("record_id = ?", rec.ID).First(&referral).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d referral: %w", id, err)
			}
			referral = recordmodel.ClinicRecordReferral{
				RecordID:       rec.ID,
				ReferralReason: reason,
			}
			if err := tx.Create(&referral).Error; err != nil {
				return fmt.Errorf("create record %d referral: %w", id, err)
			}
		} else {
			if err := tx.Model(&referral).Update("referral_reason", reason).Error; err != nil {
				return fmt.Errorf("update record %d referral_reason: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   int(approverID),
			Action:    RecordActionRefer,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, entry.ActorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkNoShow(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != recordmodel.RecordStatusConfirmed {
		return AdminRecordView{}, fmt.Errorf("no-show record %d: %w (current: %s)", id, ErrRecordInvalidTransition, rec.Status)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		if err := tx.Model(&rec).Updates(map[string]any{
			"status":     recordmodel.RecordStatusNoShow,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark no-show record %d: %w", id, err)
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		entry = RecordActionEntry{
			RecordID:  rec.ID,
			ActorID:   actorID,
			Action:    RecordActionNoShow,
			PrevSeq:   prevSeq,
			ActionSeq: rec.ActionSeq,
			CreatedAt: time.Now().UTC(),
			Prev:      snap,
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}
