package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"clinic-backend/models"

	"gorm.io/gorm"
)

var (
	ErrRecordNotFound          = errors.New("record not found")
	ErrRecordInvalidTransition = errors.New("invalid status transition")
	ErrRevertUnavailable       = errors.New("no revertible action")
	ErrRevertWindowExpired     = errors.New("revert window expired")
	ErrRevertSuperseded        = errors.New("action has been superseded")
)

type AdminRecordService struct {
	db      *gorm.DB
	tagSvc  *RecordTagService
	actions *RecordActionService
}

func NewAdminRecordService(db *gorm.DB) *AdminRecordService {
	return &AdminRecordService{db: db}
}

// SetRecordTagService attaches the in-memory record tag resolver used to
// populate the tag title on record views.
func (s *AdminRecordService) SetRecordTagService(tagSvc *RecordTagService) {
	s.tagSvc = tagSvc
}

// SetRecordActionService attaches the per-operator action stack used to
// journal and revert record operations. When unset, reverting is unavailable.
func (s *AdminRecordService) SetRecordActionService(actions *RecordActionService) {
	s.actions = actions
}

type ListAdminRecordFilter struct {
	Status   string
	RoomID   *uint
	FromDate *time.Time
	ToDate   *time.Time
	Page     int
	PageSize int
}

type UpdateAdminRecordInput struct {
	WorkerDesc *string
}

type AdminRecordView struct {
	ID              uint    `json:"id"`
	User            string  `json:"user"`
	Realname        string  `json:"realname"`
	PhoneNum        string  `json:"phone_num"`
	Status          string  `json:"status"`
	AppointmentTime string  `json:"appointment_time"`
	Description     string  `json:"description"`
	Campus          string  `json:"campus"`
	WorkerDesc      string  `json:"worker_desc"`
	RejectReason    string  `json:"reject_reason"`
	ReferralReason  string  `json:"referral_reason"`
	Model           string  `json:"model"`
	Password        string  `json:"password"`
	Tag             string  `json:"tag"`
	ArriveTime      *string `json:"arrive_time,omitempty"`
	FinishTime      *string `json:"finish_time,omitempty"`
	WorkerID        *uint   `json:"worker_id,omitempty"`
	ApproverID      *uint   `json:"approver_id,omitempty"`

	// Revert fields describe whether the requesting staff member can undo their
	// latest action on this record.
	Revertible      bool    `json:"revertible"`
	RevertibleUntil *string `json:"revertible_until,omitempty"`
	RevertibleCount int     `json:"revertible_count"`
	LastActionAt    *string `json:"last_action_at,omitempty"`
}

func (s *AdminRecordService) List(ctx context.Context, actorID int, f ListAdminRecordFilter) ([]AdminRecordView, int64, error) {
	q := s.db.Model(&models.ClinicRecord{})

	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.RoomID != nil {
		q = q.Where("room = ?", *f.RoomID)
	}
	if f.FromDate != nil {
		q = q.Where("appointment_time >= ?", DateInLocation(*f.FromDate, nil))
	}
	if f.ToDate != nil {
		q = q.Where("appointment_time <= ?", DateInLocation(*f.ToDate, nil))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count records: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var records []models.ClinicRecord
	if err := q.
		Order("appointment_time DESC, id DESC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("list records: %w", err)
	}

	views := make([]AdminRecordView, 0, len(records))
	for _, rec := range records {
		v, err := s.buildView(rec)
		if err != nil {
			return nil, 0, err
		}
		views = append(views, v)
	}
	if err := s.applyRevertibleList(ctx, actorID, records, views); err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

func (s *AdminRecordService) GetByID(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}
	v, err := s.buildView(rec)
	if err != nil {
		return AdminRecordView{}, err
	}
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) Update(ctx context.Context, actorID int, id uint, in UpdateAdminRecordInput) (AdminRecordView, error) {
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d for update: %w", id, err)
	}

	var v AdminRecordView
	var entry RecordActionEntry
	changed := in.WorkerDesc != nil
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d for update: %w", id, err)
		}
		prevSeq := rec.ActionSeq
		snap, err := s.snapshotTx(tx, rec)
		if err != nil {
			return err
		}

		if in.WorkerDesc != nil {
			var worker models.ClinicRecordWorker
			if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					worker = models.ClinicRecordWorker{
						RecordID:   rec.ID,
						WorkerDesc: *in.WorkerDesc,
					}
					if err := tx.Create(&worker).Error; err != nil {
						return fmt.Errorf("create record %d worker: %w", id, err)
					}
				} else {
					return fmt.Errorf("get record %d worker: %w", id, err)
				}
			} else {
				if err := tx.Model(&worker).Update("worker_desc", *in.WorkerDesc).Error; err != nil {
					return fmt.Errorf("update record %d worker: %w", id, err)
				}
			}
			if err := tx.Model(&rec).Update("action_seq", gorm.Expr("action_seq + 1")).Error; err != nil {
				return fmt.Errorf("bump record %d action_seq: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		if changed {
			entry = RecordActionEntry{
				RecordID:  rec.ID,
				ActorID:   actorID,
				Action:    RecordActionUpdate,
				PrevSeq:   prevSeq,
				ActionSeq: rec.ActionSeq,
				CreatedAt: time.Now().UTC(),
				Prev:      snap,
			}
		}
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	if changed {
		s.recordAction(ctx, entry)
	}
	if err := s.applyRevertibleOne(ctx, actorID, rec, &v); err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkConfirmed(ctx context.Context, id uint, approverID uint) (AdminRecordView, error) {
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != models.RecordStatusPending {
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
			"status":      models.RecordStatusConfirmed,
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
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != models.RecordStatusConfirmed {
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
			"status":     models.RecordStatusArrived,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark arrived record %d: %w", id, err)
		}

		var arrival models.ClinicRecordArrival
		if err := tx.Where("record_id = ?", rec.ID).First(&arrival).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d arrival: %w", id, err)
			}
			arrival = models.ClinicRecordArrival{
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
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != models.RecordStatusConfirmed && rec.Status != models.RecordStatusArrived {
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
			"status":     models.RecordStatusInProgress,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark in_progress record %d: %w", id, err)
		}

		var worker models.ClinicRecordWorker
		if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d worker: %w", id, err)
			}
			worker = models.ClinicRecordWorker{
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
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != models.RecordStatusInProgress {
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
			"status":     models.RecordStatusCompleted,
			"action_seq": gorm.Expr("action_seq + 1"),
		}).Error; err != nil {
			return fmt.Errorf("mark completed record %d: %w", id, err)
		}

		var worker models.ClinicRecordWorker
		if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d worker: %w", id, err)
			}
			worker = models.ClinicRecordWorker{
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
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != models.RecordStatusPending {
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
			"status":      models.RecordStatusRejected,
			"approver_id": approverID,
			"action_seq":  gorm.Expr("action_seq + 1"),
		}
		if err := tx.Model(&rec).Updates(updates).Error; err != nil {
			return fmt.Errorf("mark rejected record %d: %w", id, err)
		}

		var rejection models.ClinicRecordRejection
		if err := tx.Where("record_id = ?", rec.ID).First(&rejection).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d rejection: %w", id, err)
			}
			rejection = models.ClinicRecordRejection{
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
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	switch rec.Status {
	case models.RecordStatusRejected, models.RecordStatusCompleted, models.RecordStatusReferred, models.RecordStatusNoShow:
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
			"status":      models.RecordStatusReferred,
			"approver_id": approverID,
			"action_seq":  gorm.Expr("action_seq + 1"),
		}
		if err := tx.Model(&rec).Updates(updates).Error; err != nil {
			return fmt.Errorf("mark referred record %d: %w", id, err)
		}

		var referral models.ClinicRecordReferral
		if err := tx.Where("record_id = ?", rec.ID).First(&referral).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d referral: %w", id, err)
			}
			referral = models.ClinicRecordReferral{
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
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}

	if rec.Status != models.RecordStatusConfirmed {
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
			"status":     models.RecordStatusNoShow,
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

func (s *AdminRecordService) CloseExpiredRecords(ctx context.Context, cutoff time.Time) (noShowCount, completedCount int64, err error) {
	noShowStatuses := []models.RecordStatus{
		models.RecordStatusPending,
		models.RecordStatusConfirmed,
		models.RecordStatusArrived,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&models.ClinicRecord{}).
			Where("appointment_time < ? AND status IN ?", cutoff, noShowStatuses).
			Updates(map[string]any{
				"status":     models.RecordStatusNoShow,
				"action_seq": gorm.Expr("action_seq + 1"),
			})
		if r.Error != nil {
			return fmt.Errorf("mark no-show: %w", r.Error)
		}
		noShowCount = r.RowsAffected

		r = tx.Model(&models.ClinicRecord{}).
			Where("appointment_time < ? AND status = ?", cutoff, models.RecordStatusInProgress).
			Updates(map[string]any{
				"status":     models.RecordStatusCompleted,
				"action_seq": gorm.Expr("action_seq + 1"),
			})
		if r.Error != nil {
			return fmt.Errorf("mark completed: %w", r.Error)
		}
		completedCount = r.RowsAffected

		if completedCount > 0 {
			sub := tx.Model(&models.ClinicRecord{}).
				Select("id").
				Where("appointment_time < ? AND status = ?", cutoff, models.RecordStatusCompleted)
			if err := tx.Model(&models.ClinicRecordWorker{}).
				Where("record_id IN (?)", sub).
				Update("finish_time", time.Now().UTC()).Error; err != nil {
				return fmt.Errorf("update worker finish_time: %w", err)
			}
		}

		return nil
	})
	return
}

func (s *AdminRecordService) buildView(rec models.ClinicRecord) (AdminRecordView, error) {
	return s.buildViewTx(s.db, rec)
}

func (s *AdminRecordService) buildViewTx(tx *gorm.DB, rec models.ClinicRecord) (AdminRecordView, error) {
	var room models.ClinicRoom
	if err := tx.Select("name").Where("id = ?", rec.RoomID).First(&room).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, fmt.Errorf("load room for record %d: %w", rec.ID, err)
		}
	}

	var device models.ClinicRecordDevice
	hasDevice := true
	if err := tx.Where("record_id = ?", rec.ID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasDevice = false
		} else {
			return AdminRecordView{}, fmt.Errorf("load device for record %d: %w", rec.ID, err)
		}
	}

	var worker models.ClinicRecordWorker
	hasWorker := true
	if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasWorker = false
		} else {
			return AdminRecordView{}, fmt.Errorf("load worker for record %d: %w", rec.ID, err)
		}
	}

	var rejection models.ClinicRecordRejection
	hasRejection := true
	if err := tx.Where("record_id = ?", rec.ID).First(&rejection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasRejection = false
		} else {
			return AdminRecordView{}, fmt.Errorf("load rejection for record %d: %w", rec.ID, err)
		}
	}

	var referral models.ClinicRecordReferral
	hasReferral := true
	if err := tx.Where("record_id = ?", rec.ID).First(&referral).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasReferral = false
		} else {
			return AdminRecordView{}, fmt.Errorf("load referral for record %d: %w", rec.ID, err)
		}
	}

	var arrival models.ClinicRecordArrival
	hasArrival := true
	if err := tx.Where("record_id = ?", rec.ID).First(&arrival).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasArrival = false
		} else {
			return AdminRecordView{}, fmt.Errorf("load arrival for record %d: %w", rec.ID, err)
		}
	}

	v := AdminRecordView{
		ID:              rec.ID,
		User:            rec.User,
		Realname:        rec.Realname,
		PhoneNum:        rec.PhoneNum,
		Status:          string(rec.Status),
		AppointmentTime: rec.AppointmentTime.UTC().Format("2006-01-02"),
		Description:     rec.QuestionDesc,
		Campus:          room.Name,
		ApproverID:      rec.ApproverID,
	}
	if s.tagSvc != nil {
		if tag, ok := s.tagSvc.ByID(rec.TagID); ok {
			v.Tag = tag.Title
		}
	}
	if hasDevice {
		v.Model = device.LaptopModel
		v.Password = device.Password
	}
	if hasWorker {
		v.WorkerDesc = worker.WorkerDesc
		v.WorkerID = &worker.WorkerID
		if !worker.FinishTime.IsZero() {
			v.FinishTime = new(worker.FinishTime.UTC().Format(time.RFC3339))
		}
	}
	if hasRejection {
		v.RejectReason = rejection.RejectReason
	}
	if hasReferral {
		v.ReferralReason = referral.ReferralReason
	}
	if hasArrival {
		v.ArriveTime = new(arrival.ArriveTime.UTC().Format(time.RFC3339))
	}
	return v, nil
}

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
func (s *AdminRecordService) snapshotTx(tx *gorm.DB, rec models.ClinicRecord) (RecordSnapshot, error) {
	snap := RecordSnapshot{Status: rec.Status}
	if rec.ApproverID != nil {
		v := *rec.ApproverID
		snap.ApproverID = &v
	}

	var arrival models.ClinicRecordArrival
	if err := tx.Where("record_id = ?", rec.ID).First(&arrival).Error; err == nil {
		t := arrival.ArriveTime
		snap.ArriveTime = &t
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot arrival for record %d: %w", rec.ID, err)
	}

	var worker models.ClinicRecordWorker
	if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err == nil {
		snap.Worker = &WorkerSnapshot{
			WorkerID:   worker.WorkerID,
			WorkerDesc: worker.WorkerDesc,
			FinishTime: worker.FinishTime,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot worker for record %d: %w", rec.ID, err)
	}

	var rejection models.ClinicRecordRejection
	if err := tx.Where("record_id = ?", rec.ID).First(&rejection).Error; err == nil {
		r := rejection.RejectReason
		snap.RejectReason = &r
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot rejection for record %d: %w", rec.ID, err)
	}

	var referral models.ClinicRecordReferral
	if err := tx.Where("record_id = ?", rec.ID).First(&referral).Error; err == nil {
		r := referral.ReferralReason
		snap.ReferralReason = &r
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return snap, fmt.Errorf("snapshot referral for record %d: %w", rec.ID, err)
	}

	return snap, nil
}

// applyRevertibleOne marks a single view with the actor's revert options.
func (s *AdminRecordService) applyRevertibleOne(ctx context.Context, actorID int, rec models.ClinicRecord, v *AdminRecordView) error {
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
func (s *AdminRecordService) applyRevertibleList(ctx context.Context, actorID int, recs []models.ClinicRecord, views []AdminRecordView) error {
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
func applyRevertible(w RecordWindow, rec models.ClinicRecord, window time.Duration, v *AdminRecordView) {
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

	var rec models.ClinicRecord
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
func (s *AdminRecordService) restoreSnapshotTx(tx *gorm.DB, rec *models.ClinicRecord, snap RecordSnapshot, prevSeq uint) error {
	updates := map[string]any{
		"status":      snap.Status,
		"approver_id": snap.ApproverID,
		"action_seq":  prevSeq,
	}
	if err := tx.Model(rec).Updates(updates).Error; err != nil {
		return fmt.Errorf("restore record %d: %w", rec.ID, err)
	}

	if snap.ArriveTime == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordArrival{}).Error; err != nil {
			return fmt.Errorf("restore record %d arrival: %w", rec.ID, err)
		}
	} else if err := upsertArrivalTx(tx, rec.ID, *snap.ArriveTime); err != nil {
		return err
	}

	if snap.Worker == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordWorker{}).Error; err != nil {
			return fmt.Errorf("restore record %d worker: %w", rec.ID, err)
		}
	} else if err := upsertWorkerTx(tx, rec.ID, *snap.Worker); err != nil {
		return err
	}

	if snap.RejectReason == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordRejection{}).Error; err != nil {
			return fmt.Errorf("restore record %d rejection: %w", rec.ID, err)
		}
	} else if err := upsertRejectionTx(tx, rec.ID, *snap.RejectReason); err != nil {
		return err
	}

	if snap.ReferralReason == nil {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordReferral{}).Error; err != nil {
			return fmt.Errorf("restore record %d referral: %w", rec.ID, err)
		}
	} else if err := upsertReferralTx(tx, rec.ID, *snap.ReferralReason); err != nil {
		return err
	}

	return nil
}

func upsertArrivalTx(tx *gorm.DB, recordID uint, arriveTime time.Time) error {
	var arrival models.ClinicRecordArrival
	if err := tx.Where("record_id = ?", recordID).First(&arrival).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d arrival: %w", recordID, err)
		}
		if err := tx.Create(&models.ClinicRecordArrival{RecordID: recordID, ArriveTime: arriveTime}).Error; err != nil {
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
	var worker models.ClinicRecordWorker
	if err := tx.Where("record_id = ?", recordID).First(&worker).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d worker: %w", recordID, err)
		}
		if err := tx.Create(&models.ClinicRecordWorker{
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
	var rejection models.ClinicRecordRejection
	if err := tx.Where("record_id = ?", recordID).First(&rejection).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d rejection: %w", recordID, err)
		}
		if err := tx.Create(&models.ClinicRecordRejection{RecordID: recordID, RejectReason: reason}).Error; err != nil {
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
	var referral models.ClinicRecordReferral
	if err := tx.Where("record_id = ?", recordID).First(&referral).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get record %d referral: %w", recordID, err)
		}
		if err := tx.Create(&models.ClinicRecordReferral{RecordID: recordID, ReferralReason: reason}).Error; err != nil {
			return fmt.Errorf("create record %d referral: %w", recordID, err)
		}
		return nil
	}
	if err := tx.Model(&referral).Update("referral_reason", reason).Error; err != nil {
		return fmt.Errorf("update record %d referral: %w", recordID, err)
	}
	return nil
}
