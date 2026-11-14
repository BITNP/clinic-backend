package service

import (
	"database/sql"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type rowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

type rowsQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

const recordColumns = `id, "user", realname, phone_num, status, appointment_time, question_desc, room, approver_id, tag_id, liquid_metal_status, action_seq`
const recordDeviceColumns = "record_id, laptop_model, password"
const recordWorkerColumns = "record_id, worker, worker_desc, finish_time"
const recordRejectionColumns = "record_id, reject_reason"
const recordReferralColumns = "record_id, referral_reason"
const recordArrivalColumns = "record_id, arrive_time"
const recordPhotoColumns = "id, record_id, object_key, content_type, size, position, created_at"
const serviceDateColumns = "id, capacity, room_id, date, start_time, end_time, title"
const roomColumns = "id, name, address, enabled, campus_id, type"

func scanRecord(row rowScanner) (recordmodel.ClinicRecord, error) {
	var record recordmodel.ClinicRecord
	var approverID sql.NullInt64
	err := row.Scan(
		&record.ID,
		&record.User,
		&record.Realname,
		&record.PhoneNum,
		&record.Status,
		&record.AppointmentTime,
		&record.QuestionDesc,
		&record.RoomID,
		&approverID,
		&record.TagID,
		&record.LiquidMetalStatusIndex,
		&record.ActionSeq,
	)
	if approverID.Valid {
		id := uint(approverID.Int64)
		record.ApproverID = &id
	}
	return record, err
}

func loadRecord(db rowQueryer, id uint) (recordmodel.ClinicRecord, error) {
	return scanRecord(db.QueryRow("SELECT "+recordColumns+" FROM clinic_record WHERE id = $1", id))
}

func scanRecordDevice(row rowScanner) (recordmodel.ClinicRecordDevice, error) {
	var device recordmodel.ClinicRecordDevice
	var password sql.NullString
	err := row.Scan(&device.RecordID, &device.LaptopModel, &password)
	device.Password = password.String
	return device, err
}

func scanRecordWorker(row rowScanner) (recordmodel.ClinicRecordWorker, error) {
	var worker recordmodel.ClinicRecordWorker
	var finishTime sql.NullTime
	err := row.Scan(&worker.RecordID, &worker.WorkerID, &worker.WorkerDesc, &finishTime)
	if finishTime.Valid {
		worker.FinishTime = finishTime.Time
	}
	return worker, err
}

func scanRecordRejection(row rowScanner) (recordmodel.ClinicRecordRejection, error) {
	var rejection recordmodel.ClinicRecordRejection
	err := row.Scan(&rejection.RecordID, &rejection.RejectReason)
	return rejection, err
}

func scanRecordReferral(row rowScanner) (recordmodel.ClinicRecordReferral, error) {
	var referral recordmodel.ClinicRecordReferral
	err := row.Scan(&referral.RecordID, &referral.ReferralReason)
	return referral, err
}

func scanRecordArrival(row rowScanner) (recordmodel.ClinicRecordArrival, error) {
	var arrival recordmodel.ClinicRecordArrival
	err := row.Scan(&arrival.RecordID, &arrival.ArriveTime)
	return arrival, err
}

func scanRecordPhoto(row rowScanner) (recordmodel.ClinicRecordPhoto, error) {
	var photo recordmodel.ClinicRecordPhoto
	err := row.Scan(
		&photo.ID,
		&photo.RecordID,
		&photo.ObjectKey,
		&photo.ContentType,
		&photo.Size,
		&photo.Position,
		&photo.CreatedAt,
	)
	return photo, err
}

func scanServiceDate(row rowScanner) (schedulingmodel.ClinicServiceDate, error) {
	var date schedulingmodel.ClinicServiceDate
	var roomID sql.NullInt64
	err := row.Scan(&date.ID, &date.Capacity, &roomID, &date.Date, &date.StartTime, &date.EndTime, &date.Title)
	if roomID.Valid {
		id := uint(roomID.Int64)
		date.RoomID = &id
	}
	return date, err
}

func scanRoom(row rowScanner) (roommodel.ClinicRoom, error) {
	var room roommodel.ClinicRoom
	var campusID sql.NullInt64
	err := row.Scan(&room.ID, &room.Name, &room.Address, &room.Enabled, &campusID, &room.Type)
	if campusID.Valid {
		id := uint(campusID.Int64)
		room.CampusID = &id
	}
	return room, err
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func nullableUint(v *uint) any {
	if v == nil {
		return nil
	}
	return *v
}
