package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	taggingservice "clinic-backend/internal/tagging/service"
)

var (
	ErrRecordNotFound          = errors.New("record not found")
	ErrRecordInvalidTransition = errors.New("invalid status transition")
	ErrRevertUnavailable       = errors.New("no revertible action")
	ErrRevertWindowExpired     = errors.New("revert window expired")
	ErrRevertSuperseded        = errors.New("action has been superseded")
)

type AdminRecordService struct {
	db      *database.DB
	tagSvc  *taggingservice.RecordTagService
	actions *RecordActionService
}

func NewAdminRecordService(db *database.DB) *AdminRecordService {
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
	where := []string{"1 = 1"}
	var args []any
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.RoomID != nil {
		args = append(args, *f.RoomID)
		where = append(where, fmt.Sprintf("room = $%d", len(args)))
	}
	if f.FromDate != nil {
		args = append(args, calendar.DateInLocation(*f.FromDate, nil))
		where = append(where, fmt.Sprintf("appointment_time >= $%d", len(args)))
	}
	if f.ToDate != nil {
		args = append(args, calendar.DateInLocation(*f.ToDate, nil))
		where = append(where, fmt.Sprintf("appointment_time <= $%d", len(args)))
	}
	if term := strings.TrimSpace(f.Realname); term != "" {
		// Escape LIKE metacharacters so the user's text is matched literally.
		// LOWER() keeps the search case-insensitive on both SQLite and Postgres.
		args = append(args, "%"+escapeLike(term)+"%")
		where = append(where, fmt.Sprintf(`LOWER(realname) LIKE LOWER($%d) ESCAPE '\'`, len(args)))
	}
	predicate := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clinic_record"+predicate, args...).Scan(&total); err != nil {
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
	args = append(args, f.PageSize, offset)

	// Match the admin sidebar's newest-ID-first order before pagination so
	// newer tickets cannot be hidden behind older tickets with later appointments.
	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(
			"SELECT %s FROM clinic_record%s ORDER BY id DESC LIMIT $%d OFFSET $%d",
			recordColumns, predicate, len(args)-1, len(args),
		),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()
	records := make([]recordmodel.ClinicRecord, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan record: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
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
	record, err := loadRecord(s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}
	view, err := s.buildView(record)
	if err != nil {
		return AdminRecordView{}, err
	}
	if err := s.applyRevertibleOne(ctx, actorID, record, &view); err != nil {
		return AdminRecordView{}, err
	}
	return view, nil
}

func (s *AdminRecordService) Update(ctx context.Context, actorID int, id uint, in UpdateAdminRecordInput) (AdminRecordView, error) {
	if _, err := loadRecord(s.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d for update: %w", id, err)
	}

	var record recordmodel.ClinicRecord
	var view AdminRecordView
	var entry RecordActionEntry
	changed := in.WorkerDesc != nil
	err := database.WithTx(ctx, s.db.DB, func(tx *sql.Tx) error {
		var err error
		record, err = loadRecord(tx, id)
		if err != nil {
			return fmt.Errorf("reload record %d for update: %w", id, err)
		}
		prevSeq := record.ActionSeq
		snap, err := s.snapshotTx(tx, record)
		if err != nil {
			return err
		}

		if in.WorkerDesc != nil {
			worker, err := scanRecordWorker(tx.QueryRow(
				"SELECT "+recordWorkerColumns+" FROM clinic_record_worker WHERE record_id = $1",
				record.ID,
			))
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.Exec(
					"INSERT INTO clinic_record_worker (record_id, worker, worker_desc, finish_time) VALUES ($1, 0, $2, NULL)",
					record.ID, *in.WorkerDesc,
				); err != nil {
					return fmt.Errorf("create record %d worker: %w", id, err)
				}
			} else if err != nil {
				return fmt.Errorf("get record %d worker: %w", id, err)
			} else {
				if _, err := tx.Exec(
					"UPDATE clinic_record_worker SET worker_desc = $1 WHERE record_id = $2",
					*in.WorkerDesc, worker.RecordID,
				); err != nil {
					return fmt.Errorf("update record %d worker: %w", id, err)
				}
			}
			if _, err := tx.Exec("UPDATE clinic_record SET action_seq = action_seq + 1 WHERE id = $1", id); err != nil {
				return fmt.Errorf("bump record %d action_seq: %w", id, err)
			}
		}

		record, err = loadRecord(tx, id)
		if err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		if changed {
			entry = RecordActionEntry{
				RecordID:  record.ID,
				ActorID:   actorID,
				Action:    RecordActionUpdate,
				PrevSeq:   prevSeq,
				ActionSeq: record.ActionSeq,
				CreatedAt: time.Now().UTC(),
				Prev:      snap,
			}
		}
		view, err = s.buildViewTx(tx, record)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	if changed {
		s.recordAction(ctx, entry)
	}
	if err := s.applyRevertibleOne(ctx, actorID, record, &view); err != nil {
		return AdminRecordView{}, err
	}
	return view, nil
}
