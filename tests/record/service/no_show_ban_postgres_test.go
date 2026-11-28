package service_test

import (
	"context"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"
)

func TestNoShowBanPostgresRevert(t *testing.T) {
	_, gormDB := setupRecordPostgres(t)
	campus := roommodel.ClinicCampus{
		Name: "postgres-no-show",
		NoShowBanPolicy: roommodel.NoShowBanPolicy{
			Enabled: true, DurationHours: 24, Level: roommodel.NoShowBanLevelCampus,
		},
	}
	if err := gormDB.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	room := roommodel.ClinicRoom{Name: "postgres-no-show-room", Enabled: true, CampusID: &campus.ID}
	if err := gormDB.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	rec := recordmodel.ClinicRecord{
		User: "postgres-user", Realname: "r", PhoneNum: "p",
		Status:          recordmodel.RecordStatusConfirmed,
		AppointmentTime: time.Now().UTC().AddDate(0, 0, 1),
		QuestionDesc:    "x", RoomID: room.ID,
	}
	if err := gormDB.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, gormDB))
	svc.SetRecordActionService(recordservice.NewRecordActionService(newFakeActionStore(), 5*time.Minute))

	if _, err := svc.MarkNoShow(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("mark no-show: %v", err)
	}
	var ban recordmodel.ClinicUserTicketBan
	if err := gormDB.Where("user_id = ?", rec.User).First(&ban).Error; err != nil {
		t.Fatalf("load ban: %v", err)
	}
	if ban.CampusID == nil || *ban.CampusID != campus.ID {
		t.Fatalf("ban campus = %v, want %d", ban.CampusID, campus.ID)
	}
	if _, err := svc.Revert(context.Background(), 7, rec.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	var count int64
	if err := gormDB.Model(&recordmodel.ClinicUserTicketBan{}).
		Where("user_id = ?", rec.User).
		Count(&count).Error; err != nil {
		t.Fatalf("count bans: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected ban removal after revert, got %d", count)
	}
}
