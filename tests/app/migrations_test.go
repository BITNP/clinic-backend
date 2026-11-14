package app_test

import (
	"testing"

	"clinic-backend/internal/app"
	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type legacyClinicStaff struct {
	ID       uint   `gorm:"primaryKey;column:id"`
	PhoneNum string `gorm:"column:phone_num"`
}

func (legacyClinicStaff) TableName() string {
	return "clinic_staff"
}

type legacyClinicRoom struct {
	ID      uint   `gorm:"primaryKey;column:id"`
	Name    string `gorm:"size:64;not null;uniqueIndex;column:name"`
	Address string `gorm:"size:256;not null;column:address"`
	Enabled bool   `gorm:"not null;column:enabled"`
}

func (legacyClinicRoom) TableName() string {
	return "clinic_room"
}

func setupMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestMigrateBeforeAutoMigrate_RenamesStaffPhoneColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&legacyClinicStaff{}); err != nil {
		t.Fatalf("migrate legacy staff: %v", err)
	}
	staff := legacyClinicStaff{PhoneNum: "12345"}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("seed staff: %v", err)
	}

	if err := app.MigrateBeforeAutoMigrate(db); err != nil {
		t.Fatalf("migrate before auto: %v", err)
	}
	if !db.Migrator().HasColumn(&staffmodel.ClinicStaff{}, "email") {
		t.Error("expected clinic_staff.email to exist")
	}
	if db.Migrator().HasColumn(&staffmodel.ClinicStaff{}, "phone_num") {
		t.Error("expected clinic_staff.phone_num to be renamed")
	}

	var email string
	if err := db.Raw("SELECT email FROM clinic_staff WHERE id = ?", staff.ID).Scan(&email).Error; err != nil {
		t.Fatalf("load email: %v", err)
	}
	if email != staff.PhoneNum {
		t.Errorf("email = %q, want %q", email, staff.PhoneNum)
	}

	if err := app.MigrateBeforeAutoMigrate(db); err != nil {
		t.Fatalf("second migrate before auto: %v", err)
	}
}

func TestAutoMigrate_UpgradesLegacyRoomTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&legacyClinicRoom{}); err != nil {
		t.Fatalf("migrate legacy room: %v", err)
	}
	rooms := []legacyClinicRoom{
		{Name: "Room A", Address: "addr-a", Enabled: true},
		{Name: "Room B", Address: "addr-b", Enabled: true},
	}
	for i := range rooms {
		if err := db.Create(&rooms[i]).Error; err != nil {
			t.Fatalf("seed legacy room: %v", err)
		}
	}

	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &roommodel.ClinicRoom{}); err != nil {
		t.Fatalf("upgrade room schema: %v", err)
	}
	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("migrate room defaults: %v", err)
	}

	var campus roommodel.ClinicCampus
	if err := db.Where("name = ?", "默认校区").First(&campus).Error; err != nil {
		t.Fatalf("find default campus: %v", err)
	}
	for _, legacy := range rooms {
		var got roommodel.ClinicRoom
		if err := db.First(&got, legacy.ID).Error; err != nil {
			t.Fatalf("load room %d: %v", legacy.ID, err)
		}
		if got.CampusID == nil || *got.CampusID != campus.ID {
			t.Errorf("room %d campus_id = %v, want %d", legacy.ID, got.CampusID, campus.ID)
		}
		if got.Type != roommodel.RoomTypeNormal {
			t.Errorf("room %d type = %q, want %q", legacy.ID, got.Type, roommodel.RoomTypeNormal)
		}
	}
}

func TestMigrateAfterAutoMigrate_AssignsDefaultCampus(t *testing.T) {
	db := setupMigrationTestDB(t)

	rooms := []roommodel.ClinicRoom{
		{Name: "Room A", Address: "addr-a", Enabled: true},
		{Name: "Room B", Address: "addr-b", Enabled: true},
	}
	for i := range rooms {
		if err := db.Create(&rooms[i]).Error; err != nil {
			t.Fatalf("seed room: %v", err)
		}
	}

	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("migrate room defaults: %v", err)
	}

	var campus roommodel.ClinicCampus
	if err := db.Where("name = ?", "默认校区").First(&campus).Error; err != nil {
		t.Fatalf("find default campus: %v", err)
	}
	for _, room := range rooms {
		var got roommodel.ClinicRoom
		if err := db.First(&got, room.ID).Error; err != nil {
			t.Fatalf("load room %d: %v", room.ID, err)
		}
		if got.CampusID == nil || *got.CampusID != campus.ID {
			t.Errorf("room %d campus_id = %v, want %d", room.ID, got.CampusID, campus.ID)
		}
		if got.Type != roommodel.RoomTypeNormal {
			t.Errorf("room %d type = %q, want %q", room.ID, got.Type, roommodel.RoomTypeNormal)
		}
	}
}

func TestMigrateAfterAutoMigrate_IsIdempotent(t *testing.T) {
	db := setupMigrationTestDB(t)

	room := roommodel.ClinicRoom{Name: "Room A", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	var campusCount int64
	if err := db.Model(&roommodel.ClinicCampus{}).Count(&campusCount).Error; err != nil {
		t.Fatalf("count campuses: %v", err)
	}
	if campusCount != 1 {
		t.Errorf("campus count = %d, want 1", campusCount)
	}
}

func TestMigrateAfterAutoMigrate_AssignsRoomsCreatedLater(t *testing.T) {
	db := setupMigrationTestDB(t)

	first := roommodel.ClinicRoom{Name: "Room A", Address: "addr-a", Enabled: true}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("seed first room: %v", err)
	}
	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	second := roommodel.ClinicRoom{Name: "Room B", Address: "addr-b", Enabled: true}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("seed second room: %v", err)
	}
	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	var got roommodel.ClinicRoom
	if err := db.First(&got, second.ID).Error; err != nil {
		t.Fatalf("load second room: %v", err)
	}
	if got.CampusID == nil {
		t.Fatal("expected second room to be assigned to the default campus")
	}
}

func TestMigrateAfterAutoMigrate_BackfillsType(t *testing.T) {
	db := setupMigrationTestDB(t)

	room := roommodel.ClinicRoom{Name: "Room A", Address: "addr", Enabled: true}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := db.Exec("UPDATE clinic_room SET type = '' WHERE id = ?", room.ID).Error; err != nil {
		t.Fatalf("clear room type: %v", err)
	}

	if err := app.MigrateAfterAutoMigrate(db); err != nil {
		t.Fatalf("migrate room defaults: %v", err)
	}

	var got roommodel.ClinicRoom
	if err := db.First(&got, room.ID).Error; err != nil {
		t.Fatalf("load room: %v", err)
	}
	if got.Type != roommodel.RoomTypeNormal {
		t.Errorf("type = %q, want %q", got.Type, roommodel.RoomTypeNormal)
	}
}
