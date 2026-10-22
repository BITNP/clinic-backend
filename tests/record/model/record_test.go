package model_test

import (
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRecordTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}

	if err := db.AutoMigrate(
		&roommodel.ClinicRoom{},
		&staffmodel.ClinicStaff{},
		&recordmodel.ClinicRecord{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
	); err != nil {
		t.Fatalf("failed to migrate record models: %v", err)
	}

	return db
}

func createTestRoomForRecord(t *testing.T, db *gorm.DB) roommodel.ClinicRoom {
	room := roommodel.ClinicRoom{Name: "Main Clinic", Address: "123 Campus Dr"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	return room
}

func createTestStaffForRecord(t *testing.T, db *gorm.DB) staffmodel.ClinicStaff {
	staff := staffmodel.ClinicStaff{ID: 1, AccountID: "cas:staff1", Realname: "Worker One"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("failed to create staff: %v", err)
	}
	return staff
}

func createTestRecord(t *testing.T, db *gorm.DB, roomID uint) recordmodel.ClinicRecord {
	record := recordmodel.ClinicRecord{
		Realname:        "Student A",
		PhoneNum:        "555-0100",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "Screen is broken",
		RoomID:          roomID,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("failed to create record: %v", err)
	}
	return record
}

func TestClinicRecord_CreateAndRetrieve(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	var retrieved recordmodel.ClinicRecord
	if err := db.First(&retrieved, record.ID).Error; err != nil {
		t.Fatalf("failed to retrieve record: %v", err)
	}

	if retrieved.Realname != record.Realname {
		t.Errorf("expected realname %q, got %q", record.Realname, retrieved.Realname)
	}
	if retrieved.PhoneNum != record.PhoneNum {
		t.Errorf("expected phone_num %q, got %q", record.PhoneNum, retrieved.PhoneNum)
	}
	if retrieved.Status != record.Status {
		t.Errorf("expected status %q, got %q", record.Status, retrieved.Status)
	}
	if !retrieved.AppointmentTime.Equal(record.AppointmentTime) {
		t.Errorf("expected appointment_time %v, got %v", record.AppointmentTime, retrieved.AppointmentTime)
	}
	if retrieved.QuestionDesc != record.QuestionDesc {
		t.Errorf("expected question_desc %q, got %q", record.QuestionDesc, retrieved.QuestionDesc)
	}
	if retrieved.RoomID != record.RoomID {
		t.Errorf("expected room %d, got %d", record.RoomID, retrieved.RoomID)
	}
}

func TestClinicRecord_Device(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	device := recordmodel.ClinicRecordDevice{
		RecordID:    record.ID,
		LaptopModel: "Dell XPS 13",
		Password:    "hunter2",
	}
	if err := db.Create(&device).Error; err != nil {
		t.Fatalf("failed to create device info: %v", err)
	}

	var retrieved recordmodel.ClinicRecordDevice
	if err := db.First(&retrieved, device.RecordID).Error; err != nil {
		t.Fatalf("failed to retrieve device info: %v", err)
	}

	if retrieved.LaptopModel != device.LaptopModel {
		t.Errorf("expected laptop_model %q, got %q", device.LaptopModel, retrieved.LaptopModel)
	}
	if retrieved.Password != device.Password {
		t.Errorf("expected password %q, got %q", device.Password, retrieved.Password)
	}
}

func TestClinicRecord_Arrival(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	arrival := recordmodel.ClinicRecordArrival{
		RecordID:   record.ID,
		ArriveTime: time.Now().UTC().Truncate(time.Second),
	}
	if err := db.Create(&arrival).Error; err != nil {
		t.Fatalf("failed to create arrival: %v", err)
	}

	var retrieved recordmodel.ClinicRecordArrival
	if err := db.First(&retrieved, arrival.RecordID).Error; err != nil {
		t.Fatalf("failed to retrieve arrival: %v", err)
	}

	if !retrieved.ArriveTime.Equal(arrival.ArriveTime) {
		t.Errorf("expected arrive_time %v, got %v", arrival.ArriveTime, retrieved.ArriveTime)
	}
}

func TestClinicRecord_Worker(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)
	staff := createTestStaffForRecord(t, db)

	worker := recordmodel.ClinicRecordWorker{
		RecordID:   record.ID,
		WorkerID:   uint(staff.ID),
		WorkerDesc: "Replaced the screen",
		FinishTime: time.Now().UTC().Truncate(time.Second),
	}
	if err := db.Create(&worker).Error; err != nil {
		t.Fatalf("failed to create worker record: %v", err)
	}

	var retrieved recordmodel.ClinicRecordWorker
	if err := db.First(&retrieved, worker.RecordID).Error; err != nil {
		t.Fatalf("failed to retrieve worker record: %v", err)
	}

	if retrieved.WorkerID != worker.WorkerID {
		t.Errorf("expected worker %d, got %d", worker.WorkerID, retrieved.WorkerID)
	}
	if retrieved.WorkerDesc != worker.WorkerDesc {
		t.Errorf("expected worker_desc %q, got %q", worker.WorkerDesc, retrieved.WorkerDesc)
	}
	if !retrieved.FinishTime.Equal(worker.FinishTime) {
		t.Errorf("expected finish_time %v, got %v", worker.FinishTime, retrieved.FinishTime)
	}
}

func TestClinicRecord_Rejection(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	rejection := recordmodel.ClinicRecordRejection{
		RecordID:     record.ID,
		RejectReason: "Missing required documentation",
	}
	if err := db.Create(&rejection).Error; err != nil {
		t.Fatalf("failed to create rejection: %v", err)
	}

	var retrieved recordmodel.ClinicRecordRejection
	if err := db.First(&retrieved, rejection.RecordID).Error; err != nil {
		t.Fatalf("failed to retrieve rejection: %v", err)
	}

	if retrieved.RejectReason != rejection.RejectReason {
		t.Errorf("expected reject_reason %q, got %q", rejection.RejectReason, retrieved.RejectReason)
	}
}

func TestClinicRecord_Referral(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	referral := recordmodel.ClinicRecordReferral{
		RecordID:       record.ID,
		ReferralReason: "Needs specialized hardware repair",
	}
	if err := db.Create(&referral).Error; err != nil {
		t.Fatalf("failed to create referral: %v", err)
	}

	var retrieved recordmodel.ClinicRecordReferral
	if err := db.First(&retrieved, referral.RecordID).Error; err != nil {
		t.Fatalf("failed to retrieve referral: %v", err)
	}

	if retrieved.ReferralReason != referral.ReferralReason {
		t.Errorf("expected referral_reason %q, got %q", referral.ReferralReason, retrieved.ReferralReason)
	}
}

func TestClinicRecord_NoOptionalTables(t *testing.T) {
	db := setupRecordTestDB(t)
	room := createTestRoomForRecord(t, db)
	record := createTestRecord(t, db, room.ID)

	var count int64
	if err := db.Model(&recordmodel.ClinicRecordDevice{}).Where("record_id = ?", record.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count device rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no device row for new record, got %d", count)
	}
}
