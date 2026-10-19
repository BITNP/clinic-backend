package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	recordmodel "clinic-backend/internal/record/model"

	"gorm.io/gorm"
)

// recordAction appends a completed action to the acting staff member's stack.
// A failed push is logged but not surfaced: the record change has already been
// committed, and losing the journal only makes the action non-revertible.
func (s *AdminRecordService) recordAction(ctx context.Context, entry RecordActionEntry) {
	if s.actions == nil {
		return
	}
	if err := s.actions.Record(ctx, entry); err != nil {
		log.Printf("record action journal: %v", err)
	}
}

// snapshotTx captures every piece of mutable state that a record action may
// overwrite, so the action can later be reverted by restoring it.
func (s *AdminRecordService) snapshotTx(tx *gorm.DB, rec recordmodel.ClinicRecord) (RecordSnapshot, error) {
	snap := RecordSnapshot{Status: rec.Status}
	if rec.ApproverID != nil {
		v := *rec.ApproverID
		snap.ApproverID = &v
	}

	var arrival recordmodel.ClinicRecordArrival
	if err := tx.Where("record_id = ?", rec.ID).First(&arrival).Error; err == nil {
		t := arrival.ArriveTime
		snap.ArriveTime = &t
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot arrival for record %d: %w", rec.ID, err)
	}

	var worker recordmodel.ClinicRecordWorker
	if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err == nil {
		snap.Worker = &WorkerSnapshot{
			WorkerID:   worker.WorkerID,
			WorkerDesc: worker.WorkerDesc,
			FinishTime: worker.FinishTime,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot worker for record %d: %w", rec.ID, err)
	}

	var rejection recordmodel.ClinicRecordRejection
	if err := tx.Where("record_id = ?", rec.ID).First(&rejection).Error; err == nil {
		r := rejection.RejectReason
		snap.RejectReason = &r
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot rejection for record %d: %w", rec.ID, err)
	}

	var referral recordmodel.ClinicRecordReferral
	if err := tx.Where("record_id = ?", rec.ID).First(&referral).Error; err == nil {
		r := referral.ReferralReason
		snap.ReferralReason = &r
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot referral for record %d: %w", rec.ID, err)
	}

	return snap, nil
}

// applyRevertibleOne marks a single view with the actor's revert options.
func (s *AdminRecordService) applyRevertibleOne(ctx context.Context, actorID int, rec recordmodel.ClinicRecord, v *AdminRecordView) error {
	if s.actions == nil {
		return nil
	}
	windows, err := s.actions.Windows(ctx, actorID, time.Now().UTC())
	if err != nil {
		return err
	}
	if w, ok := windows[rec.ID]; ok {
		applyRevertible(w, rec, s.actions.Window(), v)
	}
	return nil
}

// applyRevertibleList marks a page of views with the actor's revert options
// using a single window read.
func (s *AdminRecordService) applyRevertibleList(ctx context.Context, actorID int, recs []recordmodel.ClinicRecord, views []AdminRecordView) error {
	if s.actions == nil || len(recs) == 0 {
		return nil
	}
	windows, err := s.actions.Windows(ctx, actorID, time.Now().UTC())
	if err != nil {
		return err
	}
	for i := range recs {
		if w, ok := windows[recs[i].ID]; ok {
			applyRevertible(w, recs[i], s.actions.Window(), &views[i])
		}
	}
	return nil
}

// applyRevertible fills the revert fields on v. The action is revertible only
// while the record's sequence still matches the recorded action, i.e. nothing
// else has touched the record since.
func applyRevertible(w RecordWindow, rec recordmodel.ClinicRecord, window time.Duration, v *AdminRecordView) {
	v.RevertibleCount = w.Count
	createdAt := w.Latest.CreatedAt
	if !createdAt.IsZero() {
		v.LastActionAt = new(createdAt.UTC().Format(time.RFC3339))
	}
	if rec.ActionSeq != w.Latest.ActionSeq {
		return
	}
	until := createdAt.Add(window)
	v.Revertible = true
	v.RevertibleUntil = new(until.UTC().Format(time.RFC3339))
}

// Revert undoes the acting staff member's latest action on a record, restoring
// the state captured before that action. Reverts are strict LIFO: an action can
// only be undone while it is the newest one on the record and still inside the
// sliding window.
func (s *AdminRecordService) Revert(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	if s.actions == nil {
		return AdminRecordView{}, ErrRevertUnavailable
	}
	now := time.Now().UTC()
	entry, ok, err := s.actions.Latest(ctx, actorID, id, now)
	if err != nil {
		return AdminRecordView{}, fmt.Errorf("load latest action for record %d: %w", id, err)
	}
	if !ok {
		return AdminRecordView{}, ErrRevertUnavailable
	}
	if now.Sub(entry.CreatedAt) > s.actions.Window() {
		return AdminRecordView{}, ErrRevertWindowExpired
	}

	var rec recordmodel.ClinicRecord
	var v AdminRecordView
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRecordNotFound
			}
			return fmt.Errorf("get record %d for revert: %w", id, err)
		}
		if rec.ActionSeq != entry.ActionSeq {
			return fmt.Errorf("revert record %d: %w", id, ErrRevertSuperseded)
		}
		if err := s.restoreSnapshotTx(tx, &rec, entry.Prev, entry.PrevSeq); err != nil {
			return err
		}
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d after revert: %w", id, err)
		}
		var buildErr error
		v, buildErr = s.buildViewTx(tx, rec)
		return buildErr
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	if err := s.actions.Consume(ctx, entry); err != nil {
		log.Printf("revert record %d: consume action: %v", id, err)
	}
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

// restoreSnapshotTx writes a snapshot back onto a record and its side tables.
func (s *AdminRecordService) restoreSnapshotTx(tx *gorm.DB, rec *recordmodel.ClinicRecord, snap RecordSnapshot, prevSeq uint) error {
	updates := map[string]any{
		"status":      snap.Status,
		"approver_id": snap.ApproverID,
		"action_seq":  prevSeq,
	}
	if err := tx.Model(rec).Updates(updates).Error; err != nil {
		return fmt.Errorf("restore record %d: %w", rec.ID, err)
	}

	if snap.ArriveTime == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&recordmodel.ClinicRecordArrival{}).Error; err != nil {
			return fmt.Errorf("restore record %d arrival: %w", rec.ID, err)
		}
	} else if err := upsertArrivalTx(tx, rec.ID, *snap.ArriveTime); err != nil {
		return err
	}

	if snap.Worker == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&recordmodel.ClinicRecordWorker{}).Error; err != nil {
			return fmt.Errorf("restore record %d worker: %w", rec.ID, err)
		}
	} else if err := upsertWorkerTx(tx, rec.ID, *snap.Worker); err != nil {
		return err
	}

	if snap.RejectReason == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&recordmodel.ClinicRecordRejection{}).Error; err != nil {
			return fmt.Errorf("restore record %d rejection: %w", rec.ID, err)
		}
	} else if err := upsertRejectionTx(tx, rec.ID, *snap.RejectReason); err != nil {
		return err
	}

	if snap.ReferralReason == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&recordmodel.ClinicRecordReferral{}).Error; err != nil {
			return fmt.Errorf("restore record %d referral: %w", rec.ID, err)
		}
	} else if err := upsertReferralTx(tx, rec.ID, *snap.ReferralReason); err != nil {
		return err
	}

	return nil
}

func upsertArrivalTx(tx *gorm.DB, recordID uint, arriveTime time.Time) error {
	var arrival recordmodel.ClinicRecordArrival
	if err := tx.Where("record_id = ?", recordID).First(&arrival).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d arrival: %w", recordID, err)
		}
		if err := tx.Create(&recordmodel.ClinicRecordArrival{RecordID: recordID, ArriveTime: arriveTime}).Error; err != nil {
			return fmt.Errorf("create record %d arrival: %w", recordID, err)
		}
		return nil
	}
	if err := tx.Model(&arrival).Update("arrive_time", arriveTime).Error; err != nil {
		return fmt.Errorf("update record %d arrival: %w", recordID, err)
	}
	return nil
}

func upsertWorkerTx(tx *gorm.DB, recordID uint, snap WorkerSnapshot) error {
	var worker recordmodel.ClinicRecordWorker
	if err := tx.Where("record_id = ?", recordID).First(&worker).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d worker: %w", recordID, err)
		}
		if err := tx.Create(&recordmodel.ClinicRecordWorker{
			RecordID:   recordID,
			WorkerID:   snap.WorkerID,
			WorkerDesc: snap.WorkerDesc,
			FinishTime: snap.FinishTime,
		}).Error; err != nil {
			return fmt.Errorf("create record %d worker: %w", recordID, err)
		}
		return nil
	}
	if err := tx.Model(&worker).Updates(map[string]any{
		"worker":      snap.WorkerID,
		"worker_desc": snap.WorkerDesc,
		"finish_time": snap.FinishTime,
	}).Error; err != nil {
		return fmt.Errorf("update record %d worker: %w", recordID, err)
	}
	return nil
}

func upsertRejectionTx(tx *gorm.DB, recordID uint, reason string) error {
	var rejection recordmodel.ClinicRecordRejection
	if err := tx.Where("record_id = ?", recordID).First(&rejection).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d rejection: %w", recordID, err)
		}
		if err := tx.Create(&recordmodel.ClinicRecordRejection{RecordID: recordID, RejectReason: reason}).Error; err != nil {
			return fmt.Errorf("create record %d rejection: %w", recordID, err)
		}
		return nil
	}
	if err := tx.Model(&rejection).Update("reject_reason", reason).Error; err != nil {
		return fmt.Errorf("update record %d rejection: %w", recordID, err)
	}
	return nil
}

func upsertReferralTx(tx *gorm.DB, recordID uint, reason string) error {
	var referral recordmodel.ClinicRecordReferral
	if err := tx.Where("record_id = ?", recordID).First(&referral).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d referral: %w", recordID, err)
		}
		if err := tx.Create(&recordmodel.ClinicRecordReferral{RecordID: recordID, ReferralReason: reason}).Error; err != nil {
			return fmt.Errorf("create record %d referral: %w", recordID, err)
		}
		return nil
	}
	if err := tx.Model(&referral).Update("referral_reason", reason).Error; err != nil {
		return fmt.Errorf("update record %d referral: %w", recordID, err)
	}
	return nil
}
