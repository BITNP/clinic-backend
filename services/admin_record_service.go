package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clinic-backend/models"

	"gorm.io/gorm"
)

var (
	ErrRecordNotFound          = errors.New("record not found")
	ErrRecordInvalidTransition = errors.New("invalid status transition")
)

type AdminRecordService struct {
	db     *gorm.DB
	tagSvc *RecordTagService
}

func NewAdminRecordService(db *gorm.DB) *AdminRecordService {
	return &AdminRecordService{db: db}
}

// SetRecordTagService attaches the in-memory record tag resolver used to
// populate the tag title on record views.
func (s *AdminRecordService) SetRecordTagService(tagSvc *RecordTagService) {
	s.tagSvc = tagSvc
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
}

func (s *AdminRecordService) List(f ListAdminRecordFilter) ([]AdminRecordView, int64, error) {
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
	return views, total, nil
}

func (s *AdminRecordService) GetByID(id uint) (AdminRecordView, error) {
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d: %w", id, err)
	}
	return s.buildView(rec)
}

func (s *AdminRecordService) Update(id uint, in UpdateAdminRecordInput) (AdminRecordView, error) {
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminRecordView{}, ErrRecordNotFound
		}
		return AdminRecordView{}, fmt.Errorf("get record %d for update: %w", id, err)
	}

	var v AdminRecordView
	err := s.db.Transaction(func(tx *gorm.DB) error {
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
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkConfirmed(id uint, approverID uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"status":      models.RecordStatusConfirmed,
			"approver_id": approverID,
		}
		if err := tx.Model(&rec).Updates(updates).Error; err != nil {
			return fmt.Errorf("confirm record %d: %w", id, err)
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkArrived(id uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&rec).Update("status", models.RecordStatusArrived).Error; err != nil {
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
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkInProgress(id uint, workerID uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&rec).Update("status", models.RecordStatusInProgress).Error; err != nil {
			return fmt.Errorf("mark in_progress record %d: %w", id, err)
		}

		var worker models.ClinicRecordWorker
		if err := tx.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get record %d worker: %w", id, err)
			}
			worker = models.ClinicRecordWorker{
				RecordID: rec.ID,
				WorkerID: workerID,
			}
			if err := tx.Create(&worker).Error; err != nil {
				return fmt.Errorf("create record %d worker: %w", id, err)
			}
		} else {
			if err := tx.Model(&worker).Update("worker", workerID).Error; err != nil {
				return fmt.Errorf("update record %d worker: %w", id, err)
			}
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkCompleted(id uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&rec).Update("status", models.RecordStatusCompleted).Error; err != nil {
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
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkRejected(id uint, reason string, approverID uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"status":      models.RecordStatusRejected,
			"approver_id": approverID,
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
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkReferred(id uint, reason string, approverID uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"status":      models.RecordStatusReferred,
			"approver_id": approverID,
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
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
		return AdminRecordView{}, err
	}
	return v, nil
}

func (s *AdminRecordService) MarkNoShow(id uint) (AdminRecordView, error) {
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&rec).Update("status", models.RecordStatusNoShow).Error; err != nil {
			return fmt.Errorf("mark no-show record %d: %w", id, err)
		}

		if err := tx.First(&rec, id).Error; err != nil {
			return fmt.Errorf("reload record %d: %w", id, err)
		}
		var err error
		v, err = s.buildViewTx(tx, rec)
		return err
	})
	if err != nil {
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
			Update("status", models.RecordStatusNoShow)
		if r.Error != nil {
			return fmt.Errorf("mark no-show: %w", r.Error)
		}
		noShowCount = r.RowsAffected

		r = tx.Model(&models.ClinicRecord{}).
			Where("appointment_time < ? AND status = ?", cutoff, models.RecordStatusInProgress).
			Update("status", models.RecordStatusCompleted)
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
