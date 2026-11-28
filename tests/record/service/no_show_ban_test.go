package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

func seedNoShowPolicyRoom(t *testing.T, db *gorm.DB, name string, policy roommodel.NoShowBanPolicy) roommodel.ClinicRoom {
	t.Helper()
	campus := roommodel.ClinicCampus{Name: name + " Campus", NoShowBanPolicy: policy}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	room := roommodel.ClinicRoom{Name: name + " Room", Enabled: true, CampusID: &campus.ID}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	return room
}

func TestMarkNoShowAppliesCampusBanPolicy(t *testing.T) {
	tests := []struct {
		name       string
		policy     roommodel.NoShowBanPolicy
		wantCampus bool
		wantBan    bool
	}{
		{
			name: "campus ban",
			policy: roommodel.NoShowBanPolicy{
				Enabled: true, DurationHours: 48, Level: roommodel.NoShowBanLevelCampus,
			},
			wantCampus: true,
			wantBan:    true,
		},
		{
			name: "global ban",
			policy: roommodel.NoShowBanPolicy{
				Enabled: true, DurationHours: 24, Level: roommodel.NoShowBanLevelGlobal,
			},
			wantBan: true,
		},
		{
			name: "disabled",
			policy: roommodel.NoShowBanPolicy{
				Enabled: false, DurationHours: 48, Level: roommodel.NoShowBanLevelCampus,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAdminRecordTestDB(t)
			room := seedNoShowPolicyRoom(t, db, tc.name, tc.policy)
			rec := seedActionRecord(t, db, recordmodel.RecordStatusConfirmed, room.ID)
			svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
			start := time.Now().UTC()

			if _, err := svc.MarkNoShow(context.Background(), 7, rec.ID); err != nil {
				t.Fatalf("mark no-show: %v", err)
			}
			var bans []recordmodel.ClinicUserTicketBan
			if err := db.Find(&bans).Error; err != nil {
				t.Fatalf("load bans: %v", err)
			}
			if !tc.wantBan {
				if len(bans) != 0 {
					t.Fatalf("expected no ban, got %+v", bans)
				}
				return
			}
			if len(bans) != 1 {
				t.Fatalf("expected one ban, got %+v", bans)
			}
			ban := bans[0]
			if ban.UserID != rec.User || ban.Reason != noShowBanReasonForTest() {
				t.Fatalf("unexpected ban: %+v", ban)
			}
			if tc.wantCampus {
				if ban.CampusID == nil || *ban.CampusID == 0 {
					t.Fatalf("expected campus ban, got %+v", ban)
				}
			} else if ban.CampusID != nil {
				t.Fatalf("expected global ban, got %+v", ban)
			}
			minExpiry := start.Add(time.Duration(tc.policy.DurationHours)*time.Hour - time.Minute)
			maxExpiry := time.Now().UTC().Add(time.Duration(tc.policy.DurationHours)*time.Hour + time.Minute)
			if ban.ExpirateAt.Before(minExpiry) || ban.ExpirateAt.After(maxExpiry) {
				t.Fatalf("expiry %v not within policy window [%v, %v]", ban.ExpirateAt, minExpiry, maxExpiry)
			}
		})
	}
}

func TestAutomaticNoShowAppliesCampusBanPolicy(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedNoShowPolicyRoom(t, db, "automatic", roommodel.NoShowBanPolicy{
		Enabled: true, DurationHours: 12, Level: roommodel.NoShowBanLevelCampus,
	})
	past := time.Now().UTC().AddDate(0, 0, -2)
	rec := recordmodel.ClinicRecord{
		User: "automatic-user", Realname: "r", PhoneNum: "p",
		Status: recordmodel.RecordStatusConfirmed, AppointmentTime: past,
		QuestionDesc: "x", RoomID: room.ID,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	if _, _, err := svc.CloseExpiredRecords(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("close expired records: %v", err)
	}
	var ban recordmodel.ClinicUserTicketBan
	if err := db.Where("user_id = ?", rec.User).First(&ban).Error; err != nil {
		t.Fatalf("load automatic ban: %v", err)
	}
	if ban.CampusID == nil || ban.Reason != noShowBanReasonForTest() {
		t.Fatalf("automatic ban = %+v", ban)
	}
}

func TestNoShowBanExtendsShorterBanOnly(t *testing.T) {
	tests := []struct {
		name          string
		existingHours int64
		wantExtend    bool
	}{
		{name: "extends shorter", existingHours: 1, wantExtend: true},
		{name: "keeps longer", existingHours: 96},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAdminRecordTestDB(t)
			room := seedNoShowPolicyRoom(t, db, tc.name, roommodel.NoShowBanPolicy{
				Enabled: true, DurationHours: 48, Level: roommodel.NoShowBanLevelCampus,
			})
			rec := seedActionRecord(t, db, recordmodel.RecordStatusConfirmed, room.ID)
			existingExpiry := time.Now().UTC().Add(time.Duration(tc.existingHours) * time.Hour)
			existing := recordmodel.ClinicUserTicketBan{
				UserID: rec.User, CampusID: room.CampusID, Reason: "manual reason", ExpirateAt: existingExpiry,
			}
			if err := db.Create(&existing).Error; err != nil {
				t.Fatalf("seed ban: %v", err)
			}
			svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
			if _, err := svc.MarkNoShow(context.Background(), 7, rec.ID); err != nil {
				t.Fatalf("mark no-show: %v", err)
			}
			var got recordmodel.ClinicUserTicketBan
			if err := db.First(&got, existing.ID).Error; err != nil {
				t.Fatalf("load ban: %v", err)
			}
			if tc.wantExtend {
				if !got.ExpirateAt.After(existingExpiry) || got.Reason != noShowBanReasonForTest() {
					t.Fatalf("expected extended no-show ban, got %+v", got)
				}
			} else if !got.ExpirateAt.Equal(existingExpiry) || got.Reason != "manual reason" {
				t.Fatalf("longer manual ban changed: %+v", got)
			}
		})
	}
}

func TestNoShowBanRejectsInvalidStoredPolicy(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedNoShowPolicyRoom(t, db, "invalid", roommodel.NoShowBanPolicy{
		Enabled: true, DurationHours: 0, Level: roommodel.NoShowBanLevelCampus,
	})
	if err := db.Exec("UPDATE clinic_campus SET no_show_ban_duration_hours = 0 WHERE id = ?", *room.CampusID).Error; err != nil {
		t.Fatalf("corrupt policy: %v", err)
	}
	rec := seedActionRecord(t, db, recordmodel.RecordStatusConfirmed, room.ID)
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	if _, err := svc.MarkNoShow(context.Background(), 7, rec.ID); !errors.Is(err, recordservice.ErrNoShowBanPolicyInvalid) {
		t.Fatalf("expected invalid policy, got %v", err)
	}
	var got recordmodel.ClinicRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if got.Status != recordmodel.RecordStatusConfirmed {
		t.Fatalf("no-show transition should roll back, got %s", got.Status)
	}
}

func noShowBanReasonForTest() string {
	return "预约后未到访诊所，已暂停预约"
}
