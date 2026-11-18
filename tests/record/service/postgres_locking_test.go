package service_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	schedulingmodel "clinic-backend/internal/scheduling/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupRecordPostgres(t *testing.T) (*sql.DB, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("CLINIC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CLINIC_TEST_POSTGRES_DSN is not set")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := fmt.Sprintf("clinic_record_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db, PreferSimpleProtocol: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	migrateAdminRecordTestModels(t, gormDB)
	return db, gormDB
}

// waitForBlockedRecordLock waits until another session is actually blocked by
// the transaction, rather than relying on scheduling or a fixed sleep.
func waitForBlockedRecordLock(t *testing.T, db *sql.DB, blockerPID int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("record operation did not wait for the row lock")
}

func TestRecordTransitionsPostgresLocking(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	room := seedActionRoom(t, gormDB)
	rec := seedActionRecord(t, gormDB, recordmodel.RecordStatusPending, room.ID)
	svc := recordservice.NewAdminRecordService(database.Wrap(db, database.DriverPostgres))
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE id = $2", recordmodel.RecordStatusNoShow, rec.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := svc.MarkConfirmed(context.Background(), rec.ID, 7); result <- err }()
	waitForBlockedRecordLock(t, db, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, recordservice.ErrRecordInvalidTransition) {
		t.Fatalf("confirm after competing update: %v", err)
	}
	got := reloadActionRecord(t, gormDB, rec.ID)
	if got.Status != recordmodel.RecordStatusNoShow || got.ActionSeq != 1 {
		t.Fatalf("competing update overwritten: %+v", got)
	}
}

func TestRecordActionTokenPostgresMigration(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	room := seedActionRoom(t, gormDB)
	rec := seedActionRecord(t, gormDB, recordmodel.RecordStatusPending, room.ID)
	if _, err := db.Exec("ALTER TABLE clinic_record DROP COLUMN action_token"); err != nil {
		t.Fatal(err)
	}
	if err := gormDB.AutoMigrate(&recordmodel.ClinicRecord{}); err != nil {
		t.Fatal(err)
	}
	got := reloadActionRecord(t, gormDB, rec.ID)
	if got.ActionToken != "" || got.Status != rec.Status || got.User != rec.User {
		t.Fatalf("record after token migration: %+v", got)
	}
}

func TestRecordRevertPostgresLocking(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	room := seedActionRoom(t, gormDB)
	rec := seedActionRecord(t, gormDB, recordmodel.RecordStatusPending, room.ID)
	store := newFakeActionStore()
	svc := recordservice.NewAdminRecordService(database.Wrap(db, database.DriverPostgres))
	svc.SetRecordActionService(recordservice.NewRecordActionService(store, 5*time.Minute))
	if _, err := svc.MarkConfirmed(context.Background(), rec.ID, 7); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE clinic_record SET status = $1, action_seq = action_seq + 1 WHERE id = $2", recordmodel.RecordStatusNoShow, rec.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := svc.Revert(context.Background(), 7, rec.ID); result <- err }()
	waitForBlockedRecordLock(t, db, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, recordservice.ErrRevertSuperseded) {
		t.Fatalf("revert after competing update: %v", err)
	}
	got := reloadActionRecord(t, gormDB, rec.ID)
	if got.Status != recordmodel.RecordStatusNoShow || got.ActionSeq != 2 {
		t.Fatalf("competing update overwritten: %+v", got)
	}
}

func TestRecordRevertPostgresRejectsReusedSequence(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	room := seedActionRoom(t, gormDB)
	rec := seedActionRecord(t, gormDB, recordmodel.RecordStatusPending, room.ID)
	store := newFakeActionStore()
	svc := recordservice.NewAdminRecordService(database.Wrap(db, database.DriverPostgres))
	svc.SetRecordActionService(recordservice.NewRecordActionService(store, 5*time.Minute))
	if _, err := svc.MarkConfirmed(context.Background(), rec.ID, 7); err != nil {
		t.Fatal(err)
	}
	// Hold the row while a stale undo reads the old journal entry. The changed
	// token represents another action that reused the same sequence number.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE clinic_record SET action_token = $1 WHERE id = $2", "new-action-token", rec.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := svc.Revert(context.Background(), 7, rec.ID); result <- err }()
	waitForBlockedRecordLock(t, db, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, recordservice.ErrRevertSuperseded) {
		t.Fatalf("stale revert with reused sequence: %v", err)
	}
	got := reloadActionRecord(t, gormDB, rec.ID)
	if got.Status != recordmodel.RecordStatusConfirmed || got.ActionSeq != 1 || got.ActionToken != "new-action-token" {
		t.Fatalf("newer action overwritten: %+v", got)
	}
}

func TestRecordEditPostgresLocking(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	room := seedActionRoom(t, gormDB)
	rec := seedActionRecord(t, gormDB, recordmodel.RecordStatusInProgress, room.ID)
	if err := gormDB.Create(&recordmodel.ClinicRecordWorker{RecordID: rec.ID, WorkerID: 7, WorkerDesc: "original"}).Error; err != nil {
		t.Fatal(err)
	}
	store := newFakeActionStore()
	svc := recordservice.NewAdminRecordService(database.Wrap(db, database.DriverPostgres))
	svc.SetRecordActionService(recordservice.NewRecordActionService(store, 5*time.Minute))
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE clinic_record SET action_seq = action_seq + 1 WHERE id = $1", rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE clinic_record_worker SET worker_desc = $1 WHERE record_id = $2", "intervening", rec.ID); err != nil {
		t.Fatal(err)
	}
	description := "new description"
	result := make(chan error, 1)
	go func() {
		_, err := svc.Update(context.Background(), 7, rec.ID, recordservice.UpdateAdminRecordInput{WorkerDesc: &description})
		result <- err
	}()
	waitForBlockedRecordLock(t, db, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("edit after competing update: %v", err)
	}
	view, err := svc.Revert(context.Background(), 7, rec.ID)
	if err != nil || view.WorkerDesc != "intervening" {
		t.Fatalf("revert should restore intervening edit: %+v, %v", view, err)
	}
}

func TestCleanupPostgresPreservesHistoricalFinishTime(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	room := seedActionRoom(t, gormDB)
	cutoff := time.Now().UTC()
	oldFinish := cutoff.Add(-48 * time.Hour).Truncate(time.Microsecond)
	for _, status := range []recordmodel.RecordStatus{recordmodel.RecordStatusCompleted, recordmodel.RecordStatusInProgress} {
		rec := seedActionRecord(t, gormDB, status, room.ID)
		if err := gormDB.Model(&rec).Update("appointment_time", cutoff.AddDate(0, 0, -1)).Error; err != nil {
			t.Fatal(err)
		}
		if err := gormDB.Create(&recordmodel.ClinicRecordWorker{RecordID: rec.ID, WorkerID: 7, FinishTime: oldFinish}).Error; err != nil {
			t.Fatal(err)
		}
	}
	arrived := seedActionRecord(t, gormDB, recordmodel.RecordStatusArrived, room.ID)
	if err := gormDB.Model(&arrived).Update("appointment_time", cutoff.AddDate(0, 0, -1)).Error; err != nil {
		t.Fatal(err)
	}
	svc := recordservice.NewAdminRecordService(database.Wrap(db, database.DriverPostgres))
	_, count, err := svc.CloseExpiredRecords(context.Background(), cutoff)
	if err != nil || count != 2 {
		t.Fatalf("cleanup: completed=%d, err=%v", count, err)
	}
	if got := reloadActionRecord(t, gormDB, arrived.ID); got.Status != recordmodel.RecordStatusCompleted {
		t.Fatalf("arrived record not completed: %+v", got)
	}
	var workers []recordmodel.ClinicRecordWorker
	if err := gormDB.Order("record_id").Find(&workers).Error; err != nil {
		t.Fatal(err)
	}
	if len(workers) != 2 || !workers[0].FinishTime.Equal(oldFinish) || workers[1].FinishTime.Equal(oldFinish) {
		t.Fatalf("unexpected finish times: %+v", workers)
	}
}

func TestCampusSelectionAndRecordFilterPostgres(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	if err := gormDB.AutoMigrate(&schedulingmodel.ClinicServiceDate{}); err != nil {
		t.Fatal(err)
	}
	raw := database.Wrap(db, database.DriverPostgres)
	campusSvc := roomservice.NewCampusService(raw)
	roomSvc := roomservice.NewRoomService(raw)
	campus, err := campusSvc.Create(roomservice.CreateCampusInput{Name: "campus", SelectionPolicy: roommodel.SelectionPolicyBalanced})
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint
	day := calendar.DateInLocation(time.Now().AddDate(0, 0, 1), nil)
	for i, name := range []string{"room-a", "room-b"} {
		room, err := roomSvc.Create(roomservice.CreateRoomInput{Name: name, Address: "here", CampusID: campus.ID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, room.ID)
		date := schedulingmodel.ClinicServiceDate{RoomID: &room.ID, Date: day, StartTime: day, EndTime: day.Add(time.Hour), Capacity: 3, Title: "test"}
		if err := gormDB.Create(&date).Error; err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 2-i; n++ {
			seedActionRecord(t, gormDB, recordmodel.RecordStatusPending, room.ID)
		}
	}
	selected, err := roomSvc.PreferredRoom(context.Background(), campus, day, roomservice.ListRoomFilter{})
	if err != nil || selected == nil || *selected != ids[1] {
		t.Fatalf("balanced selection: %v, %v", selected, err)
	}
	policy := roommodel.SelectionPolicyFulfill
	campus, err = campusSvc.Update(campus.ID, roomservice.UpdateCampusInput{SelectionPolicy: &policy})
	if err != nil {
		t.Fatal(err)
	}
	selected, err = roomSvc.PreferredRoom(context.Background(), campus, day, roomservice.ListRoomFilter{})
	if err != nil || selected == nil || *selected != ids[0] {
		t.Fatalf("fulfill selection: %v, %v", selected, err)
	}
	room, err := roomSvc.GetByID(ids[0])
	if err != nil || room.Campus == nil || room.Campus.SelectionPolicy != policy {
		t.Fatalf("room campus policy: %+v, %v", room, err)
	}
	recordSvc := recordservice.NewAdminRecordService(raw)
	items, total, err := recordSvc.List(context.Background(), 1, recordservice.ListAdminRecordFilter{CampusID: &campus.ID})
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("campus records: count=%d items=%d err=%v", total, len(items), err)
	}
}
