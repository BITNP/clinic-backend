package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/calendar"
	recordmodel "clinic-backend/internal/record/model"
	taggingservice "clinic-backend/internal/tagging/service"

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
	tagSvc  *taggingservice.RecordTagService
	actions *RecordActionService
}

func NewAdminRecordService(db *gorm.DB) *AdminRecordService {
	return &AdminRecordService{db: db}
}

// SetRecordTagService attaches the in-memory record tag resolver used to
// populate the tag title on record views.
func (s *AdminRecordService) SetRecordTagService(tagSvc *taggingservice.RecordTagService) {
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
	// Realname filters records whose realname contains this text.
	Realname string
	Page     int
	PageSize int
}

// MaxAdminRecordPageSize bounds page sizes for record listings so a single
// request cannot ask the database for an arbitrarily large result.
const MaxAdminRecordPageSize = 100

type UpdateAdminRecordInput struct {
	WorkerDesc *string
}

type AdminRecordView struct {
	ID                uint    `json:"id"`
	User              string  `json:"user"`
	Realname          string  `json:"realname"`
	PhoneNum          string  `json:"phone_num"`
	Status            string  `json:"status"`
	AppointmentTime   string  `json:"appointment_time"`
	Description       string  `json:"description"`
	Campus            string  `json:"campus"`
	WorkerDesc        string  `json:"worker_desc"`
	PhotoCount        int64   `json:"photo_count"`
	RejectReason      string  `json:"reject_reason"`
	ReferralReason    string  `json:"referral_reason"`
	Model             string  `json:"model"`
	Password          string  `json:"password"`
	Tag               string  `json:"tag"`
	LiquidMetalStatus string  `json:"liquid_metal_status"`
	ArriveTime        *string `json:"arrive_time,omitempty"`
	FinishTime        *string `json:"finish_time,omitempty"`
	WorkerID          *uint   `json:"worker_id,omitempty"`
	WorkerRealname    string  `json:"worker_realname,omitempty"`
	ApproverID        *uint   `json:"approver_id,omitempty"`
	ApproverRealname  string  `json:"approver_realname,omitempty"`

	// Revert fields describe whether the requesting staff member can undo their
	// latest action on this record.
	Revertible      bool    `json:"revertible"`
	RevertibleUntil *string `json:"revertible_until,omitempty"`
	RevertibleCount int     `json:"revertible_count"`
	LastActionAt    *string `json:"last_action_at,omitempty"`
}

func (s *AdminRecordService) List(ctx context.Context, actorID int, f ListAdminRecordFilter) ([]AdminRecordView, int64, error) {
	q := s.db.Model(&recordmodel.ClinicRecord{})

	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.RoomID != nil {
		q = q.Where("room = ?", *f.RoomID)
	}
	if f.FromDate != nil {
		q = q.Where("appointment_time >= ?", calendar.DateInLocation(*f.FromDate, nil))
	}
	if f.ToDate != nil {
		q = q.Where("appointment_time <= ?", calendar.DateInLocation(*f.ToDate, nil))
	}
	if term := strings.TrimSpace(f.Realname); term != "" {
		// Escape LIKE metacharacters so the user's text is matched literally.
		// LOWER() keeps the search case-insensitive on both SQLite and Postgres.
		q = q.Where(`LOWER(realname) LIKE LOWER(?) ESCAPE '\'`, "%"+escapeLike(term)+"%")
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
	if f.PageSize > MaxAdminRecordPageSize {
		f.PageSize = MaxAdminRecordPageSize
	}
	offset := (f.Page - 1) * f.PageSize

	var records []recordmodel.ClinicRecord
	// Match the admin sidebar's newest-ID-first order before pagination so
	// newer tickets cannot be hidden behind older tickets with later appointments.
	if err := q.
		Order("id DESC").
		Offset(offset).
		Limit(f.PageSize).
		Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("list records: %w", err)
	}

	views, err := s.buildViewsTx(s.db, records)
	if err != nil {
		return nil, 0, err
	}
	if err := s.applyRevertibleList(ctx, actorID, records, views); err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// escapeLike escapes the characters that have special meaning in a LIKE
// pattern so a search term is treated as literal text.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *AdminRecordService) GetByID(ctx context.Context, actorID int, id uint) (AdminRecordView, error) {
	var rec recordmodel.ClinicRecord
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
	var rec recordmodel.ClinicRecord
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
			var worker recordmodel.ClinicRecordWorker
			if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					worker = recordmodel.ClinicRecordWorker{
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
