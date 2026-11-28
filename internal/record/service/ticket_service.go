package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	taggingservice "clinic-backend/internal/tagging/service"
)

// Ticket validation errors. Each carries the exact Chinese message the wire
// protocol returns to the mini-program as {"msg": "..."} on HTTP 400.
var (
	ErrTicketDateClosed  = errors.New("该日期诊所停止营业")
	ErrTicketNoCapacity  = errors.New("该日期已无剩余容量")
	ErrTicketOneWorking  = errors.New("您的未完成工单多于一个")
	ErrTicketPastTime    = errors.New("无法选择过去的时间")
	ErrTicketRoomMissing = errors.New("campus 不存在")
	ErrTicketUserBanned  = errors.New("您已被暂停预约")
	ErrTicketNotFound    = errors.New("record not found")
	ErrTicketForbidden   = errors.New("forbidden")
	ErrTicketInvalidDate = errors.New("invalid appointment_time")
)

// TicketService implements the customer-facing booking flow.
type TicketService struct {
	db           *database.DB
	loc          *time.Location
	defaultTagID uint
	tagger       TicketTagger
	photoSvc     *PhotoService
	banSvc       *UserTicketBanService
}

// ticketPhotoCleanupTimeout bounds post-delete photo cleanup so a slow object
// store cannot keep the request goroutine alive indefinitely.
const ticketPhotoCleanupTimeout = 10 * time.Second

// TicketTagger asynchronously classifies a newly created record. Enqueue must
// not block; callers fall back to the default tag if tagging cannot keep up.
type TicketTagger interface {
	Enqueue(recordID uint, text, model, extra string)
}

func NewTicketService(db *database.DB, loc *time.Location) *TicketService {
	if loc == nil {
		loc = time.UTC
	}
	return &TicketService{
		db:     db,
		loc:    loc,
		banSvc: NewUserTicketBanService(db),
	}
}

// SetDefaultTagID sets the tag id assigned to newly created records.
func (s *TicketService) SetDefaultTagID(id uint) {
	s.defaultTagID = id
}

// SetTagger enables asynchronous tagging of newly created records.
func (s *TicketService) SetTagger(t TicketTagger) {
	s.tagger = t
}

// SetPhotoService enables photo cleanup when a ticket is deleted.
func (s *TicketService) SetPhotoService(photoSvc *PhotoService) {
	s.photoSvc = photoSvc
}

// todayCutoff returns 00:00:00 UTC for the current calendar day in the service
// timezone. This matches the canonical UTC-midnight storage used for dates.
func (s *TicketService) todayCutoff() time.Time {
	return calendar.DateInLocation(time.Now(), s.loc)
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
func (s *TicketService) Create(in CreateTicketInput) (recordmodel.ClinicRecord, error) {
	room, err := s.lookupRoom(in.Campus)
	if err != nil {
		return recordmodel.ClinicRecord{}, err
	}

	if in.AppointmentTime.IsZero() {
		return recordmodel.ClinicRecord{}, ErrTicketInvalidDate
	}
	appointmentDate := calendar.DateInLocation(in.AppointmentTime, s.loc)

	banned, err := s.banSvc.IsBanned(in.User, room.CampusID, time.Now())
	if err != nil {
		return recordmodel.ClinicRecord{}, err
	}
	if banned {
		return recordmodel.ClinicRecord{}, ErrTicketUserBanned
	}

	if err := s.validateCreate(in.User, room.ID, appointmentDate); err != nil {
		return recordmodel.ClinicRecord{}, err
	}

	var record recordmodel.ClinicRecord
	err = database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var err error
		record, err = scanRecord(tx.QueryRow(
			`INSERT INTO clinic_record
			 ("user", realname, phone_num, status, appointment_time, question_desc, room, approver_id, tag_id, liquid_metal_status, action_seq)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, $8, 0, 0)
			 RETURNING `+recordColumns,
			in.User,
			in.Realname,
			in.PhoneNum,
			recordmodel.RecordStatusPending,
			appointmentDate,
			in.Description,
			room.ID,
			s.defaultTagID,
		))
		if err != nil {
			return fmt.Errorf("create ticket: %w", err)
		}
		if _, err := tx.Exec(
			"INSERT INTO clinic_record_device (record_id, laptop_model, password) VALUES ($1, $2, $3)",
			record.ID, in.Model, in.Password,
		); err != nil {
			return fmt.Errorf("create ticket device: %w", err)
		}
		return nil
	})
	if err != nil {
		return recordmodel.ClinicRecord{}, err
	}
	if s.tagger != nil {
		s.tagger.Enqueue(record.ID, taggerText(in.Description, in.Model), in.Model, in.Description)
	}
	return record, nil
}

// taggerText builds the text sent to the tagger: the customer's problem
// description plus, when present, the laptop model.
func taggerText(description, model string) string {
	text := strings.TrimSpace(description)
	if m := strings.TrimSpace(model); m != "" {
		text += "\n笔记本型号: " + m
	}
	return text
}

// validateCreate runs steps 1-4 in order. Returns the first failure, if any.
func (s *TicketService) validateCreate(username string, roomID uint, date time.Time) error {
	// STEP 1 — open-date check.
	if _, err := s.lookupServiceDate(roomID, date); err != nil {
		if errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
			return ErrTicketDateClosed
		}
		return err
	}

	// STEP 2 — capacity. Statuses that do not count toward a day's ticketed
	// total (see ticketedCountExcludeStatuses) are excluded from the count.
	excluded := schedulingservice.TicketedCountExcludeStatuses(date, s.todayCutoff())
	args := make([]any, 0, len(excluded)+2)
	args = append(args, roomID, date)
	for _, status := range excluded {
		args = append(args, status)
	}
	var existing int64
	if err := s.db.QueryRow(
		fmt.Sprintf(
			"SELECT COUNT(*) FROM clinic_record WHERE room = $1 AND appointment_time = $2 AND status NOT IN (%s)",
			database.Placeholders(3, len(excluded)),
		),
		args...,
	).Scan(&existing); err != nil {
		return fmt.Errorf("count existing for capacity: %w", err)
	}

	serviceDate, err := s.lookupServiceDate(roomID, date)
	if err != nil {
		return ErrTicketDateClosed
	}
	if uint(existing) >= serviceDate.Capacity {
		return ErrTicketNoCapacity
	}

	// STEP 3 — at most one working record per user with a future appointment.
	workingStatuses := recordmodel.WorkingStatuses()
	workingArgs := make([]any, 0, len(workingStatuses)+2)
	workingArgs = append(workingArgs, username)
	for _, status := range workingStatuses {
		workingArgs = append(workingArgs, status)
	}
	workingArgs = append(workingArgs, s.todayCutoff())
	var workingCount int64
	if err := s.db.QueryRow(
		fmt.Sprintf(
			`SELECT COUNT(*) FROM clinic_record WHERE "user" = $1 AND status IN (%s) AND appointment_time >= $%d`,
			database.Placeholders(2, len(workingStatuses)),
			len(workingArgs),
		),
		workingArgs...,
	).Scan(&workingCount); err != nil {
		return fmt.Errorf("count working for user: %w", err)
	}
	if workingCount >= 1 {
		return ErrTicketOneWorking
	}

	// STEP 4 — no past appointment_time.
	if date.Before(s.todayCutoff()) {
		return ErrTicketPastTime
	}
	return nil
}

// List returns the user's records, hiding expired-but-still-active bookings.
// Order: id DESC (newest first). Paginated.
func (s *TicketService) List(username string, f ListTicketFilter) ([]recordmodel.ClinicRecord, int64, error) {
	statuses := recordmodel.WorkingStatuses()
	args := make([]any, 0, len(statuses)+2)
	args = append(args, username)
	for _, status := range statuses {
		args = append(args, status)
	}
	args = append(args, s.todayCutoff())
	where := fmt.Sprintf(
		`"user" = $1 AND NOT (status IN (%s) AND appointment_time < $%d)`,
		database.Placeholders(2, len(statuses)),
		len(args),
	)

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_record WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tickets: %w", err)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize
	args = append(args, f.PageSize, offset)
	rows, err := s.db.Query(
		fmt.Sprintf(
			"SELECT %s FROM clinic_record WHERE %s ORDER BY id DESC LIMIT $%d OFFSET $%d",
			recordColumns, where, len(args)-1, len(args),
		),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()
	items := make([]recordmodel.ClinicRecord, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan ticket: %w", err)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	return items, total, nil
}

// Finished returns the user's closed repair records.
// Order: id DESC. Paginated.
func (s *TicketService) Finished(username string, f ListTicketFilter) ([]recordmodel.ClinicRecord, int64, error) {
	statuses := recordmodel.FinishedStatuses()
	args := make([]any, 0, len(statuses)+1)
	args = append(args, username)
	for _, status := range statuses {
		args = append(args, status)
	}
	where := fmt.Sprintf(`"user" = $1 AND status IN (%s)`, database.Placeholders(2, len(statuses)))

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_record WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count finished tickets: %w", err)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize
	args = append(args, f.PageSize, offset)
	rows, err := s.db.Query(
		fmt.Sprintf(
			"SELECT %s FROM clinic_record WHERE %s ORDER BY id DESC LIMIT $%d OFFSET $%d",
			recordColumns, where, len(args)-1, len(args),
		),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list finished tickets: %w", err)
	}
	defer rows.Close()
	items := make([]recordmodel.ClinicRecord, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan finished ticket: %w", err)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list finished tickets: %w", err)
	}
	return items, total, nil
}

// Working returns the user's single in-progress record, if any. The invariant
// "at most one" is enforced by Step 3 of Create; if two exist (e.g. created
// before a rule change) we take the smallest id and log a warning per spec
// line 224-227.
func (s *TicketService) Working(username string) (*recordmodel.ClinicRecord, error) {
	statuses := recordmodel.WorkingStatuses()
	args := make([]any, 0, len(statuses)+1)
	args = append(args, username)
	for _, status := range statuses {
		args = append(args, status)
	}
	where := fmt.Sprintf(`"user" = $1 AND status IN (%s)`, database.Placeholders(2, len(statuses)))
	var count int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_record WHERE "+where, args...).Scan(&count); err != nil {
		return nil, fmt.Errorf("count working: %w", err)
	}
	if count == 0 {
		return nil, nil
	}
	if count > 1 {
		log.Printf("warning: user %s has %d working records; returning the first by id", username, count)
	}
	record, err := scanRecord(s.db.QueryRow(
		"SELECT "+recordColumns+" FROM clinic_record WHERE "+where+" ORDER BY id ASC LIMIT 1",
		args...,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get working: %w", err)
	}
	return &record, nil
}

// GetForUser fetches a record by id, requiring ownership. Returns
// ErrTicketNotFound if missing, ErrTicketForbidden if it belongs to another user.
func (s *TicketService) GetForUser(id uint, username string) (recordmodel.ClinicRecord, error) {
	record, err := loadRecord(s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return recordmodel.ClinicRecord{}, ErrTicketNotFound
		}
		return recordmodel.ClinicRecord{}, fmt.Errorf("get ticket %d: %w", id, err)
	}
	if record.User != username {
		return recordmodel.ClinicRecord{}, ErrTicketForbidden
	}
	return record, nil
}

// UpdateForUser applies client-allowed field updates, re-running validation
// steps 1, 2, 4 if appointment_time or campus changes (spec line 266-269).
// status/worker_description/reject_reason are never accepted from clients.
func (s *TicketService) UpdateForUser(id uint, username string, in UpdateTicketInput) (recordmodel.ClinicRecord, error) {
	record, err := s.GetForUser(id, username)
	if err != nil {
		return recordmodel.ClinicRecord{}, err
	}

	roomID := record.RoomID
	if in.Campus != nil && *in.Campus != "" {
		room, err := s.lookupRoom(*in.Campus)
		if err != nil {
			return recordmodel.ClinicRecord{}, err
		}
		roomID = room.ID
	}

	date := record.AppointmentTime
	if in.AppointmentTime != nil {
		date = calendar.DateInLocation(*in.AppointmentTime, s.loc)
	}

	roomOrDateChanged := (in.Campus != nil && *in.Campus != "") || in.AppointmentTime != nil
	if roomOrDateChanged {
		if err := s.validateUpdate(username, record.ID, roomID, date); err != nil {
			return recordmodel.ClinicRecord{}, err
		}
	}

	var updates []string
	var args []any
	add := func(column string, value any) {
		args = append(args, value)
		updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if in.Realname != nil {
		add("realname", *in.Realname)
	}
	if in.PhoneNum != nil {
		add("phone_num", *in.PhoneNum)
	}
	if in.Campus != nil && *in.Campus != "" {
		add("room", roomID)
	}
	if in.AppointmentTime != nil {
		add("appointment_time", date)
	}
	if in.Description != nil {
		add("question_desc", *in.Description)
	}
	if len(updates) > 0 {
		args = append(args, id)
		record, err = scanRecord(s.db.QueryRow(
			"UPDATE clinic_record SET "+strings.Join(updates, ", ")+" WHERE id = $"+fmt.Sprint(len(args))+" RETURNING "+recordColumns,
			args...,
		))
		if err != nil {
			return recordmodel.ClinicRecord{}, fmt.Errorf("update ticket %d: %w", id, err)
		}
	}

	// Side-table update for model + password.
	if in.Model != nil || in.Password != nil {
		_, err := scanRecordDevice(s.db.QueryRow(
			"SELECT "+recordDeviceColumns+" FROM clinic_record_device WHERE record_id = $1",
			record.ID,
		))
		if err == nil {
			var deviceUpdates []string
			var deviceArgs []any
			if in.Model != nil {
				deviceArgs = append(deviceArgs, *in.Model)
				deviceUpdates = append(deviceUpdates, fmt.Sprintf("laptop_model = $%d", len(deviceArgs)))
			}
			if in.Password != nil {
				deviceArgs = append(deviceArgs, *in.Password)
				deviceUpdates = append(deviceUpdates, fmt.Sprintf("password = $%d", len(deviceArgs)))
			}
			deviceArgs = append(deviceArgs, record.ID)
			if _, err := s.db.Exec(
				"UPDATE clinic_record_device SET "+strings.Join(deviceUpdates, ", ")+" WHERE record_id = $"+fmt.Sprint(len(deviceArgs)),
				deviceArgs...,
			); err != nil {
				return recordmodel.ClinicRecord{}, fmt.Errorf("update ticket %d device: %w", id, err)
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err := s.db.Exec(
				"INSERT INTO clinic_record_device (record_id, laptop_model, password) VALUES ($1, $2, $3)",
				record.ID, deref(in.Model), deref(in.Password),
			); err != nil {
				return recordmodel.ClinicRecord{}, fmt.Errorf("create ticket %d device: %w", id, err)
			}
		} else {
			return recordmodel.ClinicRecord{}, fmt.Errorf("get device for ticket %d: %w", id, err)
		}
	}

	record, err = loadRecord(s.db, id)
	if err != nil {
		return recordmodel.ClinicRecord{}, fmt.Errorf("reload ticket %d: %w", id, err)
	}
	return record, nil
}

// validateUpdate runs the open-date, capacity, and no-past-time checks for an
// update. Capacity excludes the record itself.
func (s *TicketService) validateUpdate(username string, recordID, roomID uint, date time.Time) error {
	if _, err := s.lookupServiceDate(roomID, date); err != nil {
		if errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
			return ErrTicketDateClosed
		}
		return err
	}
	today := s.todayCutoff()
	if date.Before(today) {
		return ErrTicketPastTime
	}

	excluded := schedulingservice.TicketedCountExcludeStatuses(date, today)
	args := make([]any, 0, len(excluded)+3)
	args = append(args, roomID, date)
	for _, status := range excluded {
		args = append(args, status)
	}
	args = append(args, recordID)
	var existing int64
	if err := s.db.QueryRow(
		fmt.Sprintf(
			"SELECT COUNT(*) FROM clinic_record WHERE room = $1 AND appointment_time = $2 AND status NOT IN (%s) AND id != $%d",
			database.Placeholders(3, len(excluded)),
			len(args),
		),
		args...,
	).Scan(&existing); err != nil {
		return fmt.Errorf("count existing for capacity: %w", err)
	}
	serviceDate, err := s.lookupServiceDate(roomID, date)
	if err != nil {
		return ErrTicketDateClosed
	}
	if uint(existing) >= serviceDate.Capacity {
		return ErrTicketNoCapacity
	}
	return nil
}

// DeleteForUser verifies ownership and that the ServiceDate row still exists
// for the record's appointment day + room (spec line 276-299), then deletes the
// record and its side-table rows.
func (s *TicketService) DeleteForUser(id uint, username string) error {
	record, err := s.GetForUser(id, username)
	if err != nil {
		return err
	}

	if _, err := s.lookupServiceDate(record.RoomID, record.AppointmentTime); err != nil {
		if errors.Is(err, schedulingservice.ErrServiceDateNotFound) {
			return ErrTicketDateClosed
		}
		return err
	}

	if err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		deletes := []struct {
			table string
			label string
		}{
			{"clinic_record_device", "device"},
			{"clinic_record_arrival", "arrival"},
			{"clinic_record_worker", "worker"},
			{"clinic_record_rejection", "rejection"},
			{"clinic_record_referral", "referral"},
		}
		for _, item := range deletes {
			if _, err := tx.Exec("DELETE FROM "+item.table+" WHERE record_id = $1", record.ID); err != nil {
				return fmt.Errorf("delete %s for ticket %d: %w", item.label, id, err)
			}
		}
		if _, err := tx.Exec("DELETE FROM clinic_record WHERE id = $1", id); err != nil {
			return fmt.Errorf("delete ticket %d: %w", id, err)
		}
		return nil
	}); err != nil {
		return err
	}

	s.deleteRecordPhotos(record.ID)
	return nil
}

// deleteRecordPhotos removes photo rows and objects after the record row has
// been committed. Cleanup failures are logged because the record deletion has
// already succeeded and cannot be retried by the client.
func (s *TicketService) deleteRecordPhotos(recordID uint) {
	if s.photoSvc == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), ticketPhotoCleanupTimeout)
	defer cancel()
	if err := s.photoSvc.DeleteByRecord(ctx, recordID); err != nil {
		log.Printf("delete photos for ticket %d: %v", recordID, err)
	}
}

func (s *TicketService) lookupRoom(name string) (roommodel.ClinicRoom, error) {
	if name == "" {
		return roommodel.ClinicRoom{}, ErrTicketRoomMissing
	}
	room, err := scanRoom(s.db.QueryRow(
		"SELECT "+roomColumns+" FROM clinic_room WHERE name = $1 ORDER BY id LIMIT 1",
		name,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roommodel.ClinicRoom{}, ErrTicketRoomMissing
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("lookup room %q: %w", name, err)
	}
	return room, nil
}

func (s *TicketService) lookupServiceDate(roomID uint, date time.Time) (schedulingmodel.ClinicServiceDate, error) {
	serviceDate, err := scanServiceDate(s.db.QueryRow(
		"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE room_id = $1 AND date = $2 ORDER BY id LIMIT 1",
		roomID, calendar.DateInLocation(date, s.loc),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingmodel.ClinicServiceDate{}, schedulingservice.ErrServiceDateNotFound
		}
		return schedulingmodel.ClinicServiceDate{}, fmt.Errorf("lookup service date room=%d date=%s: %w", roomID, date.Format("2006-01-02"), err)
	}
	return serviceDate, nil
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
	LiquidMetalStatus string `json:"liquid_metal_status"`
}

// View assembles the wire representation of a record by joining the Room name
// and the optional side tables (device, worker, rejection).
func (s *TicketService) View(rec recordmodel.ClinicRecord) (TicketView, error) {
	var roomName string
	if err := s.db.QueryRow("SELECT name FROM clinic_room WHERE id = $1", rec.RoomID).Scan(&roomName); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TicketView{}, fmt.Errorf("load room for ticket %d: %w", rec.ID, err)
	}

	device, err := scanRecordDevice(s.db.QueryRow(
		"SELECT "+recordDeviceColumns+" FROM clinic_record_device WHERE record_id = $1",
		rec.ID,
	))
	hasDevice := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TicketView{}, fmt.Errorf("load device for ticket %d: %w", rec.ID, err)
	}

	worker, err := scanRecordWorker(s.db.QueryRow(
		"SELECT "+recordWorkerColumns+" FROM clinic_record_worker WHERE record_id = $1",
		rec.ID,
	))
	hasWorker := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TicketView{}, fmt.Errorf("load worker for ticket %d: %w", rec.ID, err)
	}

	rejection, err := scanRecordRejection(s.db.QueryRow(
		"SELECT "+recordRejectionColumns+" FROM clinic_record_rejection WHERE record_id = $1",
		rec.ID,
	))
	hasRejection := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TicketView{}, fmt.Errorf("load rejection for ticket %d: %w", rec.ID, err)
	}

	view := TicketView{
		URL:               fmt.Sprintf("/api/tickets/%d", rec.ID),
		User:              rec.User,
		Status:            string(rec.Status),
		Realname:          rec.Realname,
		PhoneNum:          rec.PhoneNum,
		Campus:            roomName,
		AppointmentTime:   rec.AppointmentTime.UTC().Format("2006-01-02"),
		Description:       rec.QuestionDesc,
		LiquidMetalStatus: string(taggingservice.LiquidMetalStatusFromIndex(rec.LiquidMetalStatusIndex)),
	}
	if hasDevice {
		view.Model = device.LaptopModel
		view.Password = device.Password
	}
	if hasWorker {
		view.WorkerDescription = worker.WorkerDesc
	}
	if hasRejection {
		view.RejectReason = rejection.RejectReason
	}
	return view, nil
}
