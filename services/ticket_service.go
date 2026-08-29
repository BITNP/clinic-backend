package services

import (
	"errors"
	"fmt"
	"log"
	"time"

	"clinic-backend/models"

	"gorm.io/gorm"
)

// Ticket validation errors. Each carries the exact Chinese message the wire
// protocol returns to the mini-program as {"msg": "..."} on HTTP 400.
var (
	ErrTicketDateClosed  = errors.New("该日期诊所停止营业")
	ErrTicketNoCapacity  = errors.New("该日期已无剩余容量")
	ErrTicketOneWorking  = errors.New("您的未完成工单多于一个")
	ErrTicketPastTime    = errors.New("无法选择过去的时间")
	ErrTicketRoomMissing = errors.New("campus 不存在")
	ErrTicketNotFound    = errors.New("record not found")
	ErrTicketForbidden   = errors.New("forbidden")
	ErrTicketInvalidDate = errors.New("invalid appointment_time")
)

// workingStatuses and finishedStatuses partition the status enum into the
// "active" and "closed" sets used by the List/Working/Finished queries. This
// mirrors the working=[0,1,2,4,5] / finished=[3,6,7,8,9] split from the Django
// spec, modulo the statuses that have no equivalent in the local enum (walk-in
// unresolved, registered, defer-tomorrow) — those are intentionally omitted
// since the customer flow never produces them.
var (
	workingStatuses = []models.RecordStatus{
		models.RecordStatusPending,
		models.RecordStatusConfirmed,
		models.RecordStatusArrived,
		models.RecordStatusInProgress,
	}
	finishedStatuses = []models.RecordStatus{
		models.RecordStatusRejected,
		models.RecordStatusCompleted,
		models.RecordStatusReferred,
		models.RecordStatusNoShow,
	}
)

// Rejected statuses don't count toward capacity (spec line 159).
var capacityExcludeStatuses = []models.RecordStatus{models.RecordStatusRejected}

// TicketService implements the customer-facing booking flow.
type TicketService struct {
	db  *gorm.DB
	loc *time.Location
}

func NewTicketService(db *gorm.DB, loc *time.Location) *TicketService {
	if loc == nil {
		loc = time.UTC
	}
	return &TicketService{db: db, loc: loc}
}

// todayCutoff returns 00:00:00 UTC for the current calendar day in the service
// timezone. This matches the canonical UTC-midnight storage used for dates.
func (s *TicketService) todayCutoff() time.Time {
	return DateInLocation(time.Now(), s.loc)
}

// CreateTicketInput is the customer-writable subset of a record. Server-controlled
// fields (status, worker_description, reject_reason, etc.) are not present.
type CreateTicketInput struct {
	User            string
	Realname        string
	PhoneNum        string
	Campus          string // room.name; resolved to RoomID inside Create
	AppointmentTime time.Time
	Description     string
	Model           string
	Password        string
}

// UpdateTicketInput is the same shape with pointers for optional fields. The
// handler never accepts status/worker_description/reject_reason from clients.
type UpdateTicketInput struct {
	Realname        *string
	PhoneNum        *string
	Campus          *string
	AppointmentTime *time.Time
	Description     *string
	Model           *string
	Password        *string
}

type ListTicketFilter struct {
	Page     int
	PageSize int
}

// Create runs the 4 validation steps from ticket_api.md:150-183 in exact order
// and inserts the record inside a transaction. Also persists the optional
// ClinicRecordDevice side-table row holding model + password.
func (s *TicketService) Create(in CreateTicketInput) (models.ClinicRecord, error) {
	room, err := s.lookupRoom(in.Campus)
	if err != nil {
		return models.ClinicRecord{}, err
	}

	if in.AppointmentTime.IsZero() {
		return models.ClinicRecord{}, ErrTicketInvalidDate
	}
	date := DateInLocation(in.AppointmentTime, s.loc)

	if err := s.validateCreate(in.User, room.ID, date); err != nil {
		return models.ClinicRecord{}, err
	}

	rec := models.ClinicRecord{
		User:            in.User,
		Realname:        in.Realname,
		PhoneNum:        in.PhoneNum,
		Status:          models.RecordStatusPending,
		AppointmentTime: date,
		QuestionDesc:    in.Description,
		RoomID:          room.ID,
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&rec).Error; err != nil {
			return fmt.Errorf("create ticket: %w", err)
		}
		device := models.ClinicRecordDevice{
			RecordID:    rec.ID,
			LaptopModel: in.Model,
			Password:    in.Password,
		}
		if err := tx.Create(&device).Error; err != nil {
			return fmt.Errorf("create ticket device: %w", err)
		}
		return nil
	})
	if err != nil {
		return models.ClinicRecord{}, err
	}
	return rec, nil
}

// validateCreate runs steps 1-4 in order. Returns the first failure, if any.
func (s *TicketService) validateCreate(username string, roomID uint, date time.Time) error {
	// STEP 1 — open-date check.
	if _, err := s.lookupServiceDate(roomID, date); err != nil {
		if errors.Is(err, ErrServiceDateNotFound) {
			return ErrTicketDateClosed
		}
		return err
	}

	// STEP 2 — capacity (excluding rejected).
	var existing int64
	if err := s.db.Model(&models.ClinicRecord{}).
		Where("room = ? AND appointment_time = ? AND status NOT IN ?",
			roomID, date, capacityExcludeStatuses).
		Count(&existing).Error; err != nil {
		return fmt.Errorf("count existing for capacity: %w", err)
	}

	var d models.ClinicServiceDate
	if err := s.db.Where("room_id = ? AND date = ?", roomID, date).First(&d).Error; err != nil {
		return ErrTicketDateClosed
	}
	if uint(existing) >= d.Capacity {
		return ErrTicketNoCapacity
	}

	// STEP 3 — at most one working record per user with a future appointment.
	var workingCount int64
	today := s.todayCutoff()
	if err := s.db.Model(&models.ClinicRecord{}).
		Where(`"user" = ? AND status IN ? AND appointment_time >= ?`, username, workingStatuses, today).
		Count(&workingCount).Error; err != nil {
		return fmt.Errorf("count working for user: %w", err)
	}
	if workingCount >= 1 {
		return ErrTicketOneWorking
	}

	// STEP 4 — no past appointment_time.
	if date.Before(today) {
		return ErrTicketPastTime
	}

	return nil
}

// List returns the user's records, hiding expired-but-still-active bookings.
// Order: id DESC (newest first). Paginated.
func (s *TicketService) List(username string, f ListTicketFilter) ([]models.ClinicRecord, int64, error) {
	q := s.db.Model(&models.ClinicRecord{}).Where(`"user" = ?`, username)
	today := s.todayCutoff()
	q = q.Where("NOT (status IN ? AND appointment_time < ?)", workingStatuses, today)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count tickets: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []models.ClinicRecord
	if err := q.Order("id DESC").Offset(offset).Limit(f.PageSize).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	return items, total, nil
}

// Finished returns the user's closed records (status in finishedStatuses).
// Order: id DESC. Paginated.
func (s *TicketService) Finished(username string, f ListTicketFilter) ([]models.ClinicRecord, int64, error) {
	q := s.db.Model(&models.ClinicRecord{}).
		Where(`"user" = ? AND status IN ?`, username, finishedStatuses)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count finished tickets: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	var items []models.ClinicRecord
	if err := q.Order("id DESC").Offset(offset).Limit(f.PageSize).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list finished tickets: %w", err)
	}
	return items, total, nil
}

// Working returns the user's single in-progress record, if any. The invariant
// "at most one" is enforced by Step 3 of Create; if two exist (e.g. created
// before a rule change) we take the smallest id and log a warning per spec
// line 224-227.
func (s *TicketService) Working(username string) (*models.ClinicRecord, error) {
	var count int64
	if err := s.db.Model(&models.ClinicRecord{}).
		Where(`"user" = ? AND status IN ?`, username, workingStatuses).
		Count(&count).Error; err != nil {
		return nil, fmt.Errorf("count working: %w", err)
	}
	if count == 0 {
		return nil, nil
	}
	if count > 1 {
		log.Printf("warning: user %s has %d working records; returning the first by id", username, count)
	}
	var rec models.ClinicRecord
	if err := s.db.Where(`"user" = ? AND status IN ?`, username, workingStatuses).
		Order("id ASC").First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get working: %w", err)
	}
	return &rec, nil
}

// GetForUser fetches a record by id, requiring ownership. Returns
// ErrTicketNotFound if missing, ErrTicketForbidden if it belongs to another user.
func (s *TicketService) GetForUser(id uint, username string) (models.ClinicRecord, error) {
	var rec models.ClinicRecord
	if err := s.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicRecord{}, ErrTicketNotFound
		}
		return models.ClinicRecord{}, fmt.Errorf("get ticket %d: %w", id, err)
	}
	if rec.User != username {
		return models.ClinicRecord{}, ErrTicketForbidden
	}
	return rec, nil
}

// UpdateForUser applies client-allowed field updates, re-running validation
// steps 1, 2, 4 if appointment_time or campus changes (spec line 266-269).
// status/worker_description/reject_reason are never accepted from clients.
func (s *TicketService) UpdateForUser(id uint, username string, in UpdateTicketInput) (models.ClinicRecord, error) {
	rec, err := s.GetForUser(id, username)
	if err != nil {
		return models.ClinicRecord{}, err
	}

	roomID := rec.RoomID
	if in.Campus != nil && *in.Campus != "" {
		room, err := s.lookupRoom(*in.Campus)
		if err != nil {
			return models.ClinicRecord{}, err
		}
		roomID = room.ID
	}

	date := rec.AppointmentTime
	if in.AppointmentTime != nil {
		date = DateInLocation(*in.AppointmentTime, s.loc)
	}

	// Re-validate steps 1, 2, 4 if room or date changed.
	roomOrDateChanged := (in.Campus != nil && *in.Campus != "") || in.AppointmentTime != nil
	if roomOrDateChanged {
		if err := s.validateUpdate(username, rec.ID, roomID, date); err != nil {
			return models.ClinicRecord{}, err
		}
	}

	updates := map[string]any{}
	if in.Realname != nil {
		updates["realname"] = *in.Realname
	}
	if in.PhoneNum != nil {
		updates["phone_num"] = *in.PhoneNum
	}
	if in.Campus != nil && *in.Campus != "" {
		updates["room"] = roomID
	}
	if in.AppointmentTime != nil {
		updates["appointment_time"] = date
	}
	if in.Description != nil {
		updates["question_desc"] = *in.Description
	}

	if len(updates) > 0 {
		if err := s.db.Model(&rec).Updates(updates).Error; err != nil {
			return models.ClinicRecord{}, fmt.Errorf("update ticket %d: %w", id, err)
		}
	}

	// Side-table update for model + password.
	if in.Model != nil || in.Password != nil {
		var device models.ClinicRecordDevice
		err := s.db.Where("record_id = ?", rec.ID).First(&device).Error
		if err == nil {
			dUpdates := map[string]any{}
			if in.Model != nil {
				dUpdates["laptop_model"] = *in.Model
			}
			if in.Password != nil {
				dUpdates["password"] = *in.Password
			}
			if len(dUpdates) > 0 {
				if err := s.db.Model(&device).Updates(dUpdates).Error; err != nil {
					return models.ClinicRecord{}, fmt.Errorf("update ticket %d device: %w", id, err)
				}
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			device := models.ClinicRecordDevice{
				RecordID:    rec.ID,
				LaptopModel: deref(in.Model),
				Password:    deref(in.Password),
			}
			if err := s.db.Create(&device).Error; err != nil {
				return models.ClinicRecord{}, fmt.Errorf("create ticket %d device: %w", id, err)
			}
		} else {
			return models.ClinicRecord{}, fmt.Errorf("get device for ticket %d: %w", id, err)
		}
	}

	if err := s.db.First(&rec, id).Error; err != nil {
		return models.ClinicRecord{}, fmt.Errorf("reload ticket %d: %w", id, err)
	}
	return rec, nil
}

// validateUpdate runs the open-date, capacity, and no-past-time checks for an
// update. Capacity excludes the record itself.
func (s *TicketService) validateUpdate(username string, recordID, roomID uint, date time.Time) error {
	// STEP 1 — open-date.
	if _, err := s.lookupServiceDate(roomID, date); err != nil {
		if errors.Is(err, ErrServiceDateNotFound) {
			return ErrTicketDateClosed
		}
		return err
	}

	// STEP 4 — no past time.
	today := s.todayCutoff()
	if date.Before(today) {
		return ErrTicketPastTime
	}

	// STEP 2 — capacity excluding this record itself.
	var existing int64
	if err := s.db.Model(&models.ClinicRecord{}).
		Where("room = ? AND appointment_time = ? AND status NOT IN ? AND id != ?",
			roomID, date, capacityExcludeStatuses, recordID).
		Count(&existing).Error; err != nil {
		return fmt.Errorf("count existing for capacity: %w", err)
	}
	var d models.ClinicServiceDate
	if err := s.db.Where("room_id = ? AND date = ?", roomID, date).First(&d).Error; err != nil {
		return ErrTicketDateClosed
	}
	if uint(existing) >= d.Capacity {
		return ErrTicketNoCapacity
	}
	return nil
}

// DeleteForUser verifies ownership and that the ServiceDate row still exists
// for the record's appointment day + room (spec line 276-299), then deletes the
// record and its side-table rows.
func (s *TicketService) DeleteForUser(id uint, username string) error {
	rec, err := s.GetForUser(id, username)
	if err != nil {
		return err
	}

	if _, err := s.lookupServiceDate(rec.RoomID, rec.AppointmentTime); err != nil {
		if errors.Is(err, ErrServiceDateNotFound) {
			return ErrTicketDateClosed
		}
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordDevice{}).Error; err != nil {
			return fmt.Errorf("delete device for ticket %d: %w", id, err)
		}
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordArrival{}).Error; err != nil {
			return fmt.Errorf("delete arrival for ticket %d: %w", id, err)
		}
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordWorker{}).Error; err != nil {
			return fmt.Errorf("delete worker for ticket %d: %w", id, err)
		}
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordRejection{}).Error; err != nil {
			return fmt.Errorf("delete rejection for ticket %d: %w", id, err)
		}
		if err := tx.Where("record_id = ?", rec.ID).Delete(&models.ClinicRecordReferral{}).Error; err != nil {
			return fmt.Errorf("delete referral for ticket %d: %w", id, err)
		}
		if err := tx.Delete(&rec).Error; err != nil {
			return fmt.Errorf("delete ticket %d: %w", id, err)
		}
		return nil
	})
}

func (s *TicketService) lookupRoom(name string) (models.ClinicRoom, error) {
	if name == "" {
		return models.ClinicRoom{}, ErrTicketRoomMissing
	}
	var r models.ClinicRoom
	if err := s.db.Where("name = ?", name).First(&r).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicRoom{}, ErrTicketRoomMissing
		}
		return models.ClinicRoom{}, fmt.Errorf("lookup room %q: %w", name, err)
	}
	return r, nil
}

func (s *TicketService) lookupServiceDate(roomID uint, date time.Time) (models.ClinicServiceDate, error) {
	var d models.ClinicServiceDate
	if err := s.db.Where("room_id = ? AND date = ?", roomID, DateInLocation(date, s.loc)).First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicServiceDate{}, ErrServiceDateNotFound
		}
		return models.ClinicServiceDate{}, fmt.Errorf("lookup service date room=%d date=%s: %w", roomID, date.Format("2006-01-02"), err)
	}
	return d, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TicketView is the customer-facing wire representation of a record. It matches
// the 12 fields exposed by RecordSerializerWechat (spec lines 99-114). The wire
// key is `url` (a hyperlinked-identity leftover); `id` is not exposed.
type TicketView struct {
	URL               string `json:"url"`
	User              string `json:"user"`
	Status            string `json:"status"`
	Realname          string `json:"realname"`
	PhoneNum          string `json:"phone_num"`
	Campus            string `json:"campus"`
	AppointmentTime   string `json:"appointment_time"`
	Description       string `json:"description"`
	WorkerDescription string `json:"worker_description"`
	Model             string `json:"model"`
	RejectReason      string `json:"reject_reason"`
	Password          string `json:"password"`
}

// View assembles the wire representation of a record by joining the Room name
// and the optional side tables (device, worker, rejection).
func (s *TicketService) View(rec models.ClinicRecord) (TicketView, error) {
	var room models.ClinicRoom
	if err := s.db.Select("name").Where("id = ?", rec.RoomID).First(&room).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return TicketView{}, fmt.Errorf("load room for ticket %d: %w", rec.ID, err)
		}
	}

	var device models.ClinicRecordDevice
	hasDevice := true
	if err := s.db.Where("record_id = ?", rec.ID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasDevice = false
		} else {
			return TicketView{}, fmt.Errorf("load device for ticket %d: %w", rec.ID, err)
		}
	}

	var worker models.ClinicRecordWorker
	hasWorker := true
	if err := s.db.Where("record_id = ?", rec.ID).First(&worker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasWorker = false
		} else {
			return TicketView{}, fmt.Errorf("load worker for ticket %d: %w", rec.ID, err)
		}
	}

	var rejection models.ClinicRecordRejection
	hasRejection := true
	if err := s.db.Where("record_id = ?", rec.ID).First(&rejection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hasRejection = false
		} else {
			return TicketView{}, fmt.Errorf("load rejection for ticket %d: %w", rec.ID, err)
		}
	}

	v := TicketView{
		URL:             fmt.Sprintf("/api/tickets/%d", rec.ID),
		User:            rec.User,
		Status:          string(rec.Status),
		Realname:        rec.Realname,
		PhoneNum:        rec.PhoneNum,
		Campus:          room.Name,
		AppointmentTime: rec.AppointmentTime.UTC().Format("2006-01-02"),
		Description:     rec.QuestionDesc,
	}
	if hasDevice {
		v.Model = device.LaptopModel
		v.Password = device.Password
	}
	if hasWorker {
		v.WorkerDescription = worker.WorkerDesc
	}
	if hasRejection {
		v.RejectReason = rejection.RejectReason
	}
	return v, nil
}
