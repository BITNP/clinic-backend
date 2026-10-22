package service_test

import (
	"strings"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	taggingmodel "clinic-backend/internal/tagging/model"
	taggingservice "clinic-backend/internal/tagging/service"
	"clinic-backend/internal/testutil"
)

func TestRecordTagService_SeedCatalog(t *testing.T) {
	db := testutil.SetupTagTestDB(t)
	svc := taggingservice.NewRecordTagService(db)

	def, err := svc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
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
	if err := db.Model(&taggingmodel.ClinicRecordTag{}).Count(&count).Error; err != nil {
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
	// The default tag is seeded separately and is not part of the CSV catalog.
	if int(count) != len(titles)+1 {
		t.Errorf("expected %d tags, got %d", len(titles)+1, count)
	}

	stale := taggingmodel.ClinicRecordTag{Title: "legacy", ApplyRule: "legacy rule"}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatalf("create stale tag: %v", err)
	}
	rec := recordmodel.ClinicRecord{
		Realname:        "A",
		PhoneNum:        "p",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "x",
		RoomID:          1,
		TagID:           stale.ID,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}

	if err := svc.SeedCatalog(); err != nil {
		t.Fatalf("reseed catalog with stale tag: %v", err)
	}
	var got recordmodel.ClinicRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if got.TagID != def.ID {
		t.Errorf("expected stale record moved to default tag %d, got %d", def.ID, got.TagID)
	}
	var staleCount int64
	if err := db.Model(&taggingmodel.ClinicRecordTag{}).Where("id = ?", stale.ID).Count(&staleCount).Error; err != nil {
		t.Fatalf("count stale tag: %v", err)
	}
	if staleCount != 0 {
		t.Errorf("expected stale tag to be deleted, found %d rows", staleCount)
	}
}

func TestRecordTagService_Prompt(t *testing.T) {
	svc := taggingservice.NewRecordTagService(testutil.SetupTagTestDB(t))

	prompt := svc.Prompt()
	if prompt == "" {
		t.Fatal("expected a non-empty embedded prompt")
	}
	for _, want := range []string{"电脑维修报修分类助手", "【分类目标】", "【输出要求】"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestRecordTagService_SeedAndLoad(t *testing.T) {
	db := testutil.SetupTagTestDB(t)
	svc := taggingservice.NewRecordTagService(db)

	def, err := svc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if def.Title != taggingservice.DefaultRecordTagTitle {
		t.Errorf("expected title %q, got %q", taggingservice.DefaultRecordTagTitle, def.Title)
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
	if _, ok := svc.ByTitle(taggingservice.DefaultRecordTagTitle); !ok {
		t.Errorf("expected default tag in memory")
	}
	if _, ok := svc.ByID(def.ID); !ok {
		t.Errorf("expected default tag by id in memory")
	}
}

func TestRecordTagService_BackfillRecords(t *testing.T) {
	db := testutil.SetupTagTestDB(t)
	svc := taggingservice.NewRecordTagService(db)

	def, err := svc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	untagged := recordmodel.ClinicRecord{
		Realname:        "A",
		PhoneNum:        "p",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: time.Now().UTC().Truncate(24 * time.Hour),
		QuestionDesc:    "x",
		RoomID:          1,
	}
	if err := db.Create(&untagged).Error; err != nil {
		t.Fatalf("seed untagged record: %v", err)
	}
	other := taggingmodel.ClinicRecordTag{Title: "special", ApplyRule: "1"}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("seed other tag: %v", err)
	}
	tagged := recordmodel.ClinicRecord{
		Realname:        "B",
		PhoneNum:        "p",
		Status:          recordmodel.RecordStatusPending,
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

	var gotU recordmodel.ClinicRecord
	if err := db.First(&gotU, untagged.ID).Error; err != nil {
		t.Fatalf("retrieve untagged: %v", err)
	}
	if gotU.TagID != def.ID {
		t.Errorf("expected backfilled tag_id %d, got %d", def.ID, gotU.TagID)
	}
	var gotT recordmodel.ClinicRecord
	if err := db.First(&gotT, tagged.ID).Error; err != nil {
		t.Fatalf("retrieve tagged: %v", err)
	}
	if gotT.TagID != other.ID {
		t.Errorf("expected tagged record unchanged (%d), got %d", other.ID, gotT.TagID)
	}
}
