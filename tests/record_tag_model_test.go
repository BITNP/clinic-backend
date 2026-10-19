package tests

import (
	"testing"
	"time"

	"clinic-backend/models"
	"clinic-backend/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.ClinicRoom{},
		&models.ClinicServiceDate{},
		&models.ClinicRecord{},
		&models.ClinicRecordDevice{},
		&models.ClinicRecordTag{},
		&models.ClinicRecordTagPrompt{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestClinicRecordTag_CreateAndRetrieve(t *testing.T) {
	db := setupTagTestDB(t)

	tag := models.ClinicRecordTag{
		Title:       "no_pending",
		Description: "record awaiting processing",
		ApplyRule:   "status == pending",
	}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if tag.ID == 0 {
		t.Fatal("expected id assigned")
	}

	var got models.ClinicRecordTag
	if err := db.First(&got, tag.ID).Error; err != nil {
		t.Fatalf("retrieve tag: %v", err)
	}
	if got.Title != tag.Title || got.Description != tag.Description || got.ApplyRule != tag.ApplyRule {
		t.Errorf("tag mismatch: %+v", got)
	}
}

func TestClinicRecord_PersistsTagID(t *testing.T) {
	db := setupTagTestDB(t)

	rec := models.ClinicRecord{
		Realname:        "Student A",
		PhoneNum:        "555-0100",
		Status:          models.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "broken",
		RoomID:          1,
		TagID:           7,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}

	var got models.ClinicRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("retrieve record: %v", err)
	}
	if got.TagID != 7 {
		t.Errorf("expected tag_id 7, got %d", got.TagID)
	}
}

func TestRecordTagService_SeedCatalog(t *testing.T) {
	db := setupTagTestDB(t)
	svc := services.NewRecordTagService(db)

	if err := svc.SeedCatalog(); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if err := svc.SeedCatalog(); err != nil {
		t.Fatalf("reseed catalog: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	var count int64
	if err := db.Model(&models.ClinicRecordTag{}).Count(&count).Error; err != nil {
		t.Fatalf("count tags: %v", err)
	}

	titles := []string{
		"清灰换硅脂", "风扇故障", "重装系统", "硬盘内存", "网络问题", "SW安装",
		"软件安装", "安装双系统", "空间整理", "系统驱动", "软件故障", "电池供电",
		"硬件故障", "其它问题", "G15", "蛟龙16",
	}
	for _, title := range titles {
		if _, ok := svc.ByTitle(title); !ok {
			t.Errorf("expected tag %q in memory after load", title)
		}
	}
	if int(count) != len(titles) {
		t.Errorf("expected %d tags, got %d", len(titles), count)
	}
}

func TestRecordTagService_SeedPrompt(t *testing.T) {
	db := setupTagTestDB(t)
	svc := services.NewRecordTagService(db)

	if err := svc.SeedPrompt(); err != nil {
		t.Fatalf("seed prompt: %v", err)
	}
	if err := svc.SeedPrompt(); err != nil {
		t.Fatalf("reseed prompt: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	var count int64
	if err := db.Model(&models.ClinicRecordTagPrompt{}).Count(&count).Error; err != nil {
		t.Fatalf("count prompts: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 prompt row, got %d", count)
	}
	if svc.Prompt() == "" {
		t.Error("expected non-empty prompt after load")
	}
}

func TestRecordTagService_SeedAndLoad(t *testing.T) {
	db := setupTagTestDB(t)
	svc := services.NewRecordTagService(db)

	def, err := svc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if def.Title != services.DefaultRecordTagTitle {
		t.Errorf("expected title %q, got %q", services.DefaultRecordTagTitle, def.Title)
	}

	// Seeding again must be idempotent and return the same id.
	again, err := svc.SeedDefault()
	if err != nil {
		t.Fatalf("reseed default: %v", err)
	}
	if again.ID != def.ID {
		t.Errorf("expected idempotent seed id %d, got %d", def.ID, again.ID)
	}

	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if svc.DefaultID() != def.ID {
		t.Errorf("expected default id %d, got %d", def.ID, svc.DefaultID())
	}
	if _, ok := svc.ByTitle(services.DefaultRecordTagTitle); !ok {
		t.Errorf("expected default tag in memory")
	}
	if _, ok := svc.ByID(def.ID); !ok {
		t.Errorf("expected default tag by id in memory")
	}
}

func TestRecordTagService_BackfillRecords(t *testing.T) {
	db := setupTagTestDB(t)
	svc := services.NewRecordTagService(db)

	def, err := svc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	untagged := models.ClinicRecord{
		Realname:        "A",
		PhoneNum:        "p",
		Status:          models.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "x",
		RoomID:          1,
	}
	if err := db.Create(&untagged).Error; err != nil {
		t.Fatalf("seed untagged record: %v", err)
	}
	other := models.ClinicRecordTag{Title: "special", ApplyRule: "1"}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("seed other tag: %v", err)
	}
	tagged := models.ClinicRecord{
		Realname:        "B",
		PhoneNum:        "p",
		Status:          models.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "x",
		RoomID:          1,
		TagID:           other.ID,
	}
	if err := db.Create(&tagged).Error; err != nil {
		t.Fatalf("seed tagged record: %v", err)
	}

	if err := svc.BackfillRecords(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var gotU models.ClinicRecord
	if err := db.First(&gotU, untagged.ID).Error; err != nil {
		t.Fatalf("retrieve untagged: %v", err)
	}
	if gotU.TagID != def.ID {
		t.Errorf("expected backfilled tag_id %d, got %d", def.ID, gotU.TagID)
	}
	var gotT models.ClinicRecord
	if err := db.First(&gotT, tagged.ID).Error; err != nil {
		t.Fatalf("retrieve tagged: %v", err)
	}
	if gotT.TagID != other.ID {
		t.Errorf("expected tagged record unchanged (%d), got %d", other.ID, gotT.TagID)
	}
}

func TestTicketService_Create_AssignsDefaultTag(t *testing.T) {
	db := setupTagTestDB(t)
	tagSvc := services.NewRecordTagService(db)
	def, err := tagSvc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	roomID := seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)

	svc := services.NewTicketService(db, nil)
	svc.SetDefaultTagID(def.ID)

	rec, err := svc.Create(services.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: futureTruncatedDate(7),
		Description:     "d",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.TagID != def.ID {
		t.Errorf("expected tag_id %d, got %d", def.ID, rec.TagID)
	}
	if roomID == 0 {
		t.Fatal("expected room id")
	}
}
