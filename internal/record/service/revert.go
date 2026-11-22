package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
	recordmodel "clinic-backend/internal/record/model"
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
func (s *AdminRecordService) snapshotTx(tx *sql.Tx, rec recordmodel.ClinicRecord) (RecordSnapshot, error) {
	snap := RecordSnapshot{Status: rec.Status, RoomID: rec.RoomID}
	if rec.ApproverID != nil {
		value := *rec.ApproverID
		snap.ApproverID = &value
	}

	arrival, err := scanRecordArrival(tx.QueryRow(
		"SELECT "+recordArrivalColumns+" FROM clinic_record_arrival WHERE record_id = $1",
		rec.ID,
	))
	if err == nil {
		value := arrival.ArriveTime
		snap.ArriveTime = &value
	} else if !errors.Is(err, sql.ErrNoRows) {
		return snap, fmt.Errorf("snapshot arrival for record %d: %w", rec.ID, err)
	}

	worker, err := scanRecordWorker(tx.QueryRow(
		"SELECT "+recordWorkerColumns+" FROM clinic_record_worker WHERE record_id = $1",
		rec.ID,
	))
	if err == nil {
		snap.Worker = &WorkerSnapshot{
			WorkerID:   worker.WorkerID,
			WorkerDesc: worker.WorkerDesc,
			FinishTime: worker.FinishTime,
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return snap, fmt.Errorf("snapshot worker for record %d: %w", rec.ID, err)
	}

	rejection, err := scanRecordRejection(tx.QueryRow(
		"SELECT "+recordRejectionColumns+" FROM clinic_record_rejection WHERE record_id = $1",
		rec.ID,
	))
	if err == nil {
		value := rejection.RejectReason
		snap.RejectReason = &value
	} else if !errors.Is(err, sql.ErrNoRows) {
		return snap, fmt.Errorf("snapshot rejection for record %d: %w", rec.ID, err)
	}

	referral, err := scanRecordReferral(tx.QueryRow(
		"SELECT "+recordReferralColumns+" FROM clinic_record_referral WHERE record_id = $1",
		rec.ID,
	))
	if err == nil {
		value := referral.ReferralReason
		snap.ReferralReason = &value
	} else if !errors.Is(err, sql.ErrNoRows) {
		return snap, fmt.Errorf("snapshot referral for record %d: %w", rec.ID, err)
	}

	return snap, nil
}

// applyRevertibleOne marks a single view with the actor's revert options.
func (s *AdminRecordService) applyRevertibleOne(ctx context.Context, actorID int, rec recordmodel.ClinicRecord, v *AdminRecordView) error {
	if s.actions == nil {
		return nil
	}
	window := s.actions.Window()
	windows, err := s.actions.windows(ctx, actorID, time.Now().UTC(), window)
	if err != nil {
		return err
	}
	if w, ok := windows[rec.ID]; ok {
		applyRevertible(w, rec, window, v)
	}
	return nil
}

// applyRevertibleList marks a page of views with the actor's revert options
// using a single window read.
func (s *AdminRecordService) applyRevertibleList(ctx context.Context, actorID int, recs []recordmodel.ClinicRecord, views []AdminRecordView) error {
	if s.actions == nil || len(recs) == 0 {
		return nil
	}
	window := s.actions.Window()
	windows, err := s.actions.windows(ctx, actorID, time.Now().UTC(), window)
	if err != nil {
		return err
	}
	for i := range recs {
		if w, ok := windows[recs[i].ID]; ok {
			applyRevertible(w, recs[i], window, &views[i])
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
	if rec.ActionSeq != w.Latest.ActionSeq || rec.ActionToken != w.Latest.ActionToken {
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
	window := s.actions.Window()
	windows, err := s.actions.windows(ctx, actorID, now, window)
	if err != nil {
		return AdminRecordView{}, fmt.Errorf("load latest action for record %d: %w", id, err)
	}
	entryWindow, ok := windows[id]
	if !ok {
		return AdminRecordView{}, ErrRevertUnavailable
	}
	entry := entryWindow.Latest
	if now.Sub(entry.CreatedAt) > window {
		return AdminRecordView{}, ErrRevertWindowExpired
	}

	var record recordmodel.ClinicRecord
	var view AdminRecordView
	var operationID string
	if s.journal != nil {
		var err error
		operationID, err = journal.NewOperationID()
		if err != nil {
			return AdminRecordView{}, fmt.Errorf("prepare record revert journal: %w", err)
		}
	}
	err = database.WithTx(ctx, s.db.DB, func(tx *sql.Tx) error {
		var err error
		record, err = loadRecordForUpdate(ctx, tx, s.db.Driver, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		if err != nil {
			return fmt.Errorf("get record %d for revert: %w", id, err)
		}
		if record.ActionSeq != entry.ActionSeq || record.ActionToken != entry.ActionToken {
			return fmt.Errorf("revert record %d: %w", id, ErrRevertSuperseded)
		}
		beforeSnap, err := s.snapshotTx(tx, record)
		if err != nil {
			return err
		}
		if err := s.restoreSnapshotTx(tx, &record, entry.Prev, entry.PrevSeq, entry.PrevToken); err != nil {
			return err
		}
		record, err = loadRecord(tx, id)
		if err != nil {
			return fmt.Errorf("reload record %d after revert: %w", id, err)
		}
		if s.journal != nil {
			afterSnap, err := s.snapshotTx(tx, record)
			if err != nil {
				return err
			}
			changes := recordSnapshotChanges(beforeSnap, afterSnap)
			if _, err := journal.InsertTx(ctx, tx, journal.EventInput{
				OccurredAt:  now,
				ActorType:   journal.ActorStaff,
				ActorID:     staffActorID(actorID),
				Action:      "record.reverted",
				EntityType:  "record",
				EntityID:    recordEntityID(record.ID),
				OperationID: operationID,
				Details: map[string]any{
					"version":            1,
					"reverted_event_id":  entry.JournalEventID,
					"reverted_action":    recordEventAction(entry.Action),
					"reverted_operation": entry.OperationID,
					"changes":            changes,
				},
			}); err != nil {
				return err
			}
		}
		view, err = s.buildViewTx(tx, record)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	if err := s.actions.Consume(ctx, entry); err != nil {
		log.Printf("revert record %d: consume action: %v", id, err)
	}
	if err := s.applyRevertibleOne(ctx, actorID, record, &view); err != nil {
		return AdminRecordView{}, err
	}
	return view, nil
}

// restoreSnapshotTx writes a snapshot back onto a record and its side tables.
func (s *AdminRecordService) restoreSnapshotTx(tx *sql.Tx, rec *recordmodel.ClinicRecord, snap RecordSnapshot, prevSeq uint, prevToken string) error {
	roomID := snap.RoomID
	if roomID == 0 {
		roomID = rec.RoomID
	}
	if _, err := tx.Exec(
		"UPDATE clinic_record SET status = $1, room = $2, approver_id = $3, action_seq = $4, action_token = $5 WHERE id = $6",
		snap.Status, roomID, nullableUint(snap.ApproverID), prevSeq, prevToken, rec.ID,
	); err != nil {
		return fmt.Errorf("restore record %d: %w", rec.ID, err)
	}

	if snap.ArriveTime == nil {
		if _, err := tx.Exec("DELETE FROM clinic_record_arrival WHERE record_id = $1", rec.ID); err != nil {
			return fmt.Errorf("restore record %d arrival: %w", rec.ID, err)
		}
	} else if err := upsertArrivalTx(tx, rec.ID, *snap.ArriveTime); err != nil {
		return err
	}

	if snap.Worker == nil {
		if _, err := tx.Exec("DELETE FROM clinic_record_worker WHERE record_id = $1", rec.ID); err != nil {
			return fmt.Errorf("restore record %d worker: %w", rec.ID, err)
		}
	} else if err := upsertWorkerTx(tx, rec.ID, *snap.Worker); err != nil {
		return err
	}

	if snap.RejectReason == nil {
		if _, err := tx.Exec("DELETE FROM clinic_record_rejection WHERE record_id = $1", rec.ID); err != nil {
			return fmt.Errorf("restore record %d rejection: %w", rec.ID, err)
		}
	} else if err := upsertRejectionTx(tx, rec.ID, *snap.RejectReason); err != nil {
		return err
	}

	if snap.ReferralReason == nil {
		if _, err := tx.Exec("DELETE FROM clinic_record_referral WHERE record_id = $1", rec.ID); err != nil {
			return fmt.Errorf("restore record %d referral: %w", rec.ID, err)
		}
	} else if err := upsertReferralTx(tx, rec.ID, *snap.ReferralReason); err != nil {
		return err
	}

	return nil
}

func upsertArrivalTx(tx *sql.Tx, recordID uint, arriveTime time.Time) error {
	if _, err := tx.Exec(
		`INSERT INTO clinic_record_arrival (record_id, arrive_time) VALUES ($1, $2)
		 ON CONFLICT (record_id) DO UPDATE SET arrive_time = excluded.arrive_time`,
		recordID, arriveTime,
	); err != nil {
		return fmt.Errorf("upsert record %d arrival: %w", recordID, err)
	}
	return nil
}

func upsertWorkerTx(tx *sql.Tx, recordID uint, snap WorkerSnapshot) error {
	if _, err := tx.Exec(
		`INSERT INTO clinic_record_worker (record_id, worker, worker_desc, finish_time) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (record_id) DO UPDATE SET worker = excluded.worker, worker_desc = excluded.worker_desc, finish_time = excluded.finish_time`,
		recordID, snap.WorkerID, snap.WorkerDesc, snap.FinishTime,
	); err != nil {
		return fmt.Errorf("upsert record %d worker: %w", recordID, err)
	}
	return nil
}

func upsertRejectionTx(tx *sql.Tx, recordID uint, reason string) error {
	if _, err := tx.Exec(
		`INSERT INTO clinic_record_rejection (record_id, reject_reason) VALUES ($1, $2)
		 ON CONFLICT (record_id) DO UPDATE SET reject_reason = excluded.reject_reason`,
		recordID, reason,
	); err != nil {
		return fmt.Errorf("upsert record %d rejection: %w", recordID, err)
	}
	return nil
}

func upsertReferralTx(tx *sql.Tx, recordID uint, reason string) error {
	if _, err := tx.Exec(
		`INSERT INTO clinic_record_referral (record_id, referral_reason) VALUES ($1, $2)
		 ON CONFLICT (record_id) DO UPDATE SET referral_reason = excluded.referral_reason`,
		recordID, reason,
	); err != nil {
		return fmt.Errorf("upsert record %d referral: %w", recordID, err)
	}
	return nil
}
