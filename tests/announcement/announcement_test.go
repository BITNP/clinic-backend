package announcement_test

import (
	"testing"
	"time"

	announcementmodel "clinic-backend/internal/announcement"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAnnouncementTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}

	if err := db.AutoMigrate(&announcementmodel.ClinicAnnouncement{}); err != nil {
		t.Fatalf("failed to migrate ClinicAnnouncement model: %v", err)
	}

	return db
}

func TestClinicAnnouncement_CreateAndRetrieve(t *testing.T) {
	db := setupAnnouncementTestDB(t)

	now := time.Now().UTC().Truncate(time.Second)
	expire := now.Add(7 * 24 * time.Hour).Truncate(24 * time.Hour)

	announcement := announcementmodel.ClinicAnnouncement{
		Title:          "System Maintenance",
		Content:        "The clinic will be closed for maintenance.",
		Tag:            "normal",
		CreatedTime:    now,
		LastEditedTime: now,
		ExpireDate:     expire,
		Priority:       1,
		Brief:          "Clinic closed for maintenance",
	}

	if err := db.Create(&announcement).Error; err != nil {
		t.Fatalf("failed to create announcement: %v", err)
	}

	if announcement.ID == 0 {
		t.Fatal("expected auto-incremented ID to be assigned")
	}

	var retrieved announcementmodel.ClinicAnnouncement
	if err := db.First(&retrieved, announcement.ID).Error; err != nil {
		t.Fatalf("failed to retrieve announcement: %v", err)
	}

	if retrieved.Title != announcement.Title {
		t.Errorf("expected title %q, got %q", announcement.Title, retrieved.Title)
	}
	if retrieved.Content != announcement.Content {
		t.Errorf("expected content %q, got %q", announcement.Content, retrieved.Content)
	}
	if retrieved.Tag != announcement.Tag {
		t.Errorf("expected tag %q, got %q", announcement.Tag, retrieved.Tag)
	}
	if !retrieved.CreatedTime.Equal(announcement.CreatedTime) {
		t.Errorf("expected createdTime %v, got %v", announcement.CreatedTime, retrieved.CreatedTime)
	}
	if !retrieved.LastEditedTime.Equal(announcement.LastEditedTime) {
		t.Errorf("expected lastEditedTime %v, got %v", announcement.LastEditedTime, retrieved.LastEditedTime)
	}
	if !retrieved.ExpireDate.Equal(announcement.ExpireDate) {
		t.Errorf("expected expireDate %v, got %v", announcement.ExpireDate, retrieved.ExpireDate)
	}
	if retrieved.Priority != announcement.Priority {
		t.Errorf("expected priority %d, got %d", announcement.Priority, retrieved.Priority)
	}
	if retrieved.Brief != announcement.Brief {
		t.Errorf("expected brief %q, got %q", announcement.Brief, retrieved.Brief)
	}
}

func TestClinicAnnouncement_TableName(t *testing.T) {
	var announcement announcementmodel.ClinicAnnouncement
	if got := announcement.TableName(); got != "clinic_announcement" {
		t.Errorf("expected table name %q, got %q", "clinic_announcement", got)
	}
}
