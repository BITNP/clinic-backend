package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
	recordmodel "clinic-backend/internal/record/model"
)

var ErrRoomUnavailable = errors.New("selected room is unavailable")

type transitionSpec struct {
	verb                 string
	action               RecordAction
	actorID              int
	notificationReason   string
	notificationTransfer *bool
	allowed              func(recordmodel.RecordStatus) bool
	update               func(*sql.Tx, recordmodel.ClinicRecord, time.Time) error
	augmentEntry         func(*RecordActionEntry)
}

func (s *AdminRecordService) runTransition(ctx context.Context, id uint, spec transitionSpec) (AdminRecordView, error) {
	var record recordmodel.ClinicRecord
	var view AdminRecordView
	var entry RecordActionEntry
	var operationID string
	var journalEventID uint64
	if s.journal != nil {
		var err error
		operationID, err = journal.NewOperationID()
		if err != nil {
			return AdminRecordView{}, fmt.Errorf("prepare record transition journal: %w", err)
		}
	}
	err := database.WithTx(ctx, s.db.DB, func(tx *sql.Tx) error {
		var err error
		record, err = loadRecordForUpdate(ctx, tx, s.db.Driver, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		if err != nil {
			return fmt.Errorf("lock record %d: %w", id, err)
		}
		if !spec.allowed(record.Status) {
			return fmt.Errorf("%s record %d: %w (current: %s)", spec.verb, id, ErrRecordInvalidTransition, record.Status)
		}
		prevSeq := record.ActionSeq
		prevToken := record.ActionToken
		snap, err := s.snapshotTx(tx, record)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		token, err := newRecordActionToken()
		if err != nil {
			return err
		}
		if err := spec.update(tx, record, now); err != nil {
			return fmt.Errorf("%s record %d: %w", spec.verb, id, err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE clinic_record SET action_token = $1 WHERE id = $2", token, id); err != nil {
			return fmt.Errorf("set record %d action token: %w", id, err)
		}
		record, err = loadRecord(tx, id)
		if err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		afterSnap, err := s.snapshotTx(tx, record)
		if err != nil {
			return err
		}
		if s.journal != nil {
			journalEventID, err = journal.InsertTx(ctx, tx, journal.EventInput{
				OccurredAt:  now,
				ActorType:   journal.ActorStaff,
				ActorID:     staffActorID(spec.actorID),
				Action:      recordEventAction(spec.action),
				EntityType:  "record",
				EntityID:    recordEntityID(record.ID),
				OperationID: operationID,
				Details:     recordSnapshotDetails(snap, afterSnap),
			})
			if err != nil {
				return err
			}
		}
		entry = RecordActionEntry{
			RecordID:       record.ID,
			ActorID:        spec.actorID,
			Action:         spec.action,
			PrevSeq:        prevSeq,
			ActionSeq:      record.ActionSeq,
			CreatedAt:      now,
			Prev:           snap,
			JournalEventID: journalEventID,
			OperationID:    operationID,
			ActionToken:    token,
			PrevToken:      prevToken,
		}
		if spec.augmentEntry != nil {
			spec.augmentEntry(&entry)
		}
		view, err = s.buildViewTx(tx, record)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	s.recordAction(ctx, entry)
	roomTransfer := spec.notificationTransfer != nil && *spec.notificationTransfer
	s.notifyTicketStatusChange(ctx, record, spec.notificationReason, roomTransfer, journalEventID)
	if err := s.applyRevertibleOne(ctx, spec.actorID, record, &view); err != nil {
		return AdminRecordView{}, err
	}
	return view, nil
}

func setRecordStatusTx(tx *sql.Tx, id uint, status recordmodel.RecordStatus, approverID *uint, setApprover bool) error {
	if setApprover {
		if _, err := tx.Exec(
			"UPDATE clinic_record SET status = $1, approver_id = $2, action_seq = action_seq + 1 WHERE id = $3",
			status, nullableUint(approverID), id,
		); err != nil {
			return err
		}
		return nil
	}
	if _, err := tx.Exec(
		"UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE id = $2",
		status, id,
	); err != nil {
		return err
	}
	return nil
}

func onlyStatus(want recordmodel.RecordStatus) func(recordmodel.RecordStatus) bool {
	return func(status recordmodel.RecordStatus) bool { return status == want }
}

func anyStatus(allowed ...recordmodel.RecordStatus) func(recordmodel.RecordStatus) bool {
	return func(status recordmodel.RecordStatus) bool {
		for _, candidate := range allowed {
			if status == candidate {
				return true
			}
		}
		return false
	}
}

func (s *AdminRecordService) MarkConfirmed(ctx context.Context, id uint, approverID uint) (AdminRecordView, error) {
	return s.MarkConfirmedToRoom(ctx, id, nil, approverID)
}

// MarkConfirmedToRoom confirms a pending record and can atomically move it to
// the enabled room selected by the acting staff member.
func (s *AdminRecordService) MarkConfirmedToRoom(
	ctx context.Context,
	id uint,
	roomID *uint,
	approverID uint,
) (AdminRecordView, error) {
	roomTransfer := false
	return s.runTransition(ctx, id, transitionSpec{
		verb:                 "confirm",
		action:               RecordActionConfirm,
		actorID:              int(approverID),
		notificationTransfer: &roomTransfer,
		allowed:              onlyStatus(recordmodel.RecordStatusPending),
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, _ time.Time) error {
			if roomID != nil {
				roomTransfer = *roomID != record.RoomID
				if err := validateEnabledRoomTx(tx, *roomID); err != nil {
					return err
				}
				if _, err := tx.Exec(
					"UPDATE clinic_record SET status = $1, approver_id = $2, room = $3, action_seq = action_seq + 1 WHERE id = $4",
					recordmodel.RecordStatusConfirmed, approverID, *roomID, record.ID,
				); err != nil {
					return err
				}
				return nil
			}
			return setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusConfirmed, &approverID, true)
		},
	})
}

func validateEnabledRoomTx(tx *sql.Tx, roomID uint) error {
	var selectedID uint
	err := tx.QueryRow(
		"SELECT id FROM clinic_room WHERE id = $1 AND enabled = TRUE",
		roomID,
	).Scan(&selectedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRoomUnavailable
	}
	if err != nil {
		return fmt.Errorf("validate room %d: %w", roomID, err)
	}
	return nil
}

func (s *AdminRecordService) MarkArrived(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	return s.runTransition(ctx, id, transitionSpec{
		verb:    "arrive",
		action:  RecordActionArrive,
		actorID: actorID,
		allowed: onlyStatus(recordmodel.RecordStatusConfirmed),
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, now time.Time) error {
			if err := setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusArrived, nil, false); err != nil {
				return err
			}
			if _, err := tx.Exec(
				"INSERT INTO clinic_record_arrival (record_id, arrive_time) VALUES ($1, $2) ON CONFLICT (record_id) DO NOTHING",
				record.ID, now,
			); err != nil {
				return fmt.Errorf("create record %d arrival: %w", record.ID, err)
			}
			return nil
		},
	})
}

func (s *AdminRecordService) MarkInProgress(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	return s.runTransition(ctx, id, transitionSpec{
		verb:    "in-progress",
		action:  RecordActionInProgress,
		actorID: actorID,
		allowed: anyStatus(recordmodel.RecordStatusConfirmed, recordmodel.RecordStatusArrived),
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, _ time.Time) error {
			if err := setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusInProgress, nil, false); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO clinic_record_worker (record_id, worker, worker_desc, finish_time) VALUES ($1, $2, '', NULL)
				 ON CONFLICT (record_id) DO UPDATE SET worker = excluded.worker`,
				record.ID, uint(actorID),
			); err != nil {
				return fmt.Errorf("upsert record %d worker: %w", record.ID, err)
			}
			return nil
		},
	})
}

func (s *AdminRecordService) MarkCompleted(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	return s.runTransition(ctx, id, transitionSpec{
		verb:    "complete",
		action:  RecordActionComplete,
		actorID: actorID,
		allowed: onlyStatus(recordmodel.RecordStatusInProgress),
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, now time.Time) error {
			if err := setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusCompleted, nil, false); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO clinic_record_worker (record_id, worker, worker_desc, finish_time) VALUES ($1, 0, '', $2)
				 ON CONFLICT (record_id) DO UPDATE SET finish_time = excluded.finish_time`,
				record.ID, now,
			); err != nil {
				return fmt.Errorf("upsert record %d worker finish_time: %w", record.ID, err)
			}
			return nil
		},
	})
}

func (s *AdminRecordService) MarkRejected(ctx context.Context, id uint, reason string, approverID uint) (AdminRecordView, error) {
	return s.runTransition(ctx, id, transitionSpec{
		verb:               "reject",
		action:             RecordActionReject,
		actorID:            int(approverID),
		notificationReason: reason,
		allowed:            onlyStatus(recordmodel.RecordStatusPending),
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, _ time.Time) error {
			if err := setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusRejected, &approverID, true); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO clinic_record_rejection (record_id, reject_reason) VALUES ($1, $2)
				 ON CONFLICT (record_id) DO UPDATE SET reject_reason = excluded.reject_reason`,
				record.ID, reason,
			); err != nil {
				return fmt.Errorf("upsert record %d rejection: %w", record.ID, err)
			}
			return nil
		},
	})
}

func (s *AdminRecordService) MarkReferred(ctx context.Context, id uint, reason string, approverID uint) (AdminRecordView, error) {
	return s.runTransition(ctx, id, transitionSpec{
		verb:               "refer",
		action:             RecordActionRefer,
		actorID:            int(approverID),
		notificationReason: reason,
		allowed: func(status recordmodel.RecordStatus) bool {
			switch status {
			case recordmodel.RecordStatusRejected, recordmodel.RecordStatusCompleted, recordmodel.RecordStatusReferred, recordmodel.RecordStatusNoShow:
				return false
			default:
				return true
			}
		},
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, _ time.Time) error {
			if err := setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusReferred, &approverID, true); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO clinic_record_referral (record_id, referral_reason) VALUES ($1, $2)
				 ON CONFLICT (record_id) DO UPDATE SET referral_reason = excluded.referral_reason`,
				record.ID, reason,
			); err != nil {
				return fmt.Errorf("upsert record %d referral: %w", record.ID, err)
			}
			return nil
		},
	})
}

func (s *AdminRecordService) MarkNoShow(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var banSnapshot *NoShowBanActionSnapshot
	return s.runTransition(ctx, id, transitionSpec{
		verb:    "no-show",
		action:  RecordActionNoShow,
		actorID: actorID,
		allowed: onlyStatus(recordmodel.RecordStatusConfirmed),
		update: func(tx *sql.Tx, record recordmodel.ClinicRecord, now time.Time) error {
			if err := setRecordStatusTx(tx, record.ID, recordmodel.RecordStatusNoShow, nil, false); err != nil {
				return err
			}
			snapshot, err := s.applyNoShowBanTx(ctx, tx, record, now)
			banSnapshot = snapshot
			return err
		},
		augmentEntry: func(entry *RecordActionEntry) {
			entry.NoShowBan = banSnapshot
		},
	})
}
