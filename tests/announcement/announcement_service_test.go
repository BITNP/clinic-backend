package announcement_test

import (
	"errors"
	"testing"
	"time"

	"clinic-backend/internal/announcement"
	"clinic-backend/internal/testutil"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAnnouncementServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open fake database: %v", err)
	}
	if err := db.AutoMigrate(&announcement.ClinicAnnouncement{}); err != nil {
		t.Fatalf("failed to migrate ClinicAnnouncement model: %v", err)
	}
	return db
}

func boolPtr(v bool) *bool { return &v }

func TestAnnouncementService_Create(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	before := time.Now().UTC()
	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "System Maintenance",
		Content:    "The clinic will be closed.",
		Tag:        "normal",
		Brief:      "Clinic closed",
		ExpireDate: testutil.FutureDate(7),
		Priority:   2,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}
	if a.CreatedTime.Before(before) {
		t.Errorf("expected CreatedTime to be set, got %v", a.CreatedTime)
	}
	if !a.LastEditedTime.Equal(a.CreatedTime) {
		t.Errorf("expected LastEditedTime == CreatedTime on create, got %v vs %v", a.LastEditedTime, a.CreatedTime)
	}
	if !a.Enabled {
		t.Error("expected announcement to be enabled by default")
	}
}

func TestAnnouncementService_Create_Disabled(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "Disabled",
		Content:    "c",
		Tag:        "normal",
		Enabled:    boolPtr(false),
		Brief:      "b",
		ExpireDate: testutil.FutureDate(3),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if a.Enabled {
		t.Error("expected returned announcement to be disabled")
	}

	stored, err := svc.GetByID(a.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if stored.Enabled {
		t.Error("expected stored announcement to stay disabled")
	}
}

func TestAnnouncementService_GetByID_NotFound(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	_, err := svc.GetByID(999)
	if !errors.Is(err, announcement.ErrAnnouncementNotFound) {
		t.Fatalf("expected ErrAnnouncementNotFound, got %v", err)
	}
}

func TestAnnouncementService_List_FilterAndSort(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	// Two active, one expired; varying priority and tag.
	seed := []announcement.CreateAnnouncementInput{
		{Title: "A", Content: "c", Tag: "normal", Brief: "b", ExpireDate: testutil.FutureDate(5), Priority: 1},
		{Title: "B", Content: "c", Tag: "normal", Brief: "b", ExpireDate: testutil.FutureDate(10), Priority: 3},
		{Title: "C", Content: "c", Tag: "pinned", Brief: "b", ExpireDate: testutil.FutureDate(-1), Priority: 5},
	}
	for _, in := range seed {
		if _, err := svc.Create(in); err != nil {
			t.Fatalf("seed create failed: %v", err)
		}
	}

	t.Run("all", func(t *testing.T) {
		items, total, err := svc.List(announcement.ListAnnouncementFilter{})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 3 || len(items) != 3 {
			t.Fatalf("expected 3 items, got %d (total %d)", len(items), total)
		}
		// priority desc: C(5), B(3), A(1)
		if items[0].Title != "C" || items[1].Title != "B" || items[2].Title != "A" {
			t.Errorf("unexpected order: %s, %s, %s", items[0].Title, items[1].Title, items[2].Title)
		}
	})

	t.Run("active_only", func(t *testing.T) {
		items, total, err := svc.List(announcement.ListAnnouncementFilter{ActiveOnly: true})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("expected 2 active items, got %d (total %d)", len(items), total)
		}
		for _, a := range items {
			if a.Title == "C" {
				t.Errorf("expired item C should not appear in active list")
			}
		}
	})

	t.Run("tag_filter", func(t *testing.T) {
		items, total, err := svc.List(announcement.ListAnnouncementFilter{Tag: "pinned"})
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if total != 1 || len(items) != 1 || items[0].Title != "C" {
			t.Fatalf("expected 1 alert item, got %d (total %d)", len(items), total)
		}
	})
}

func TestAnnouncementService_List_ActiveOnly_Enabled(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	seed := []announcement.CreateAnnouncementInput{
		{Title: "active", Content: "c", Tag: "normal", Brief: "b", ExpireDate: testutil.FutureDate(5)},
		{Title: "disabled", Content: "c", Tag: "normal", Brief: "b", Enabled: boolPtr(false), ExpireDate: testutil.FutureDate(5)},
		{Title: "disabled-expired", Content: "c", Tag: "normal", Brief: "b", Enabled: boolPtr(false), ExpireDate: testutil.FutureDate(-1)},
		{Title: "disabled-tos", Content: "c", Tag: "tos", Brief: "b", Enabled: boolPtr(false), ExpireDate: testutil.FutureDate(5)},
	}
	for _, in := range seed {
		if _, err := svc.Create(in); err != nil {
			t.Fatalf("seed create failed: %v", err)
		}
	}

	items, total, err := svc.List(announcement.ListAnnouncementFilter{ActiveOnly: true})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected only the enabled, unexpired item, got %d (total %d)", len(items), total)
	}
	if items[0].Title != "active" {
		t.Errorf("expected %q, got %q", "active", items[0].Title)
	}

	// Without ActiveOnly, disabled announcements stay visible to staff.
	all, allTotal, err := svc.List(announcement.ListAnnouncementFilter{})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if allTotal != 4 || len(all) != 4 {
		t.Fatalf("expected 4 items, got %d (total %d)", len(all), allTotal)
	}
}

func TestAnnouncementService_Update_Enabled(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "T",
		Content:    "c",
		Tag:        "normal",
		Brief:      "b",
		ExpireDate: testutil.FutureDate(3),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.Update(a.ID, announcement.UpdateAnnouncementInput{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Enabled {
		t.Error("expected announcement to be disabled")
	}

	updated, err = svc.Update(a.ID, announcement.UpdateAnnouncementInput{Enabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !updated.Enabled {
		t.Error("expected announcement to be enabled again")
	}
}

func TestAnnouncementService_List_Pagination(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	for i := 0; i < 5; i++ {
		if _, err := svc.Create(announcement.CreateAnnouncementInput{
			Title:      "T",
			Content:    "c",
			Tag:        "normal",
			Brief:      "b",
			ExpireDate: testutil.FutureDate(1),
			Priority:   uint(i),
		}); err != nil {
			t.Fatalf("seed create failed: %v", err)
		}
	}

	items, total, err := svc.List(announcement.ListAnnouncementFilter{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(items) != 2 {
		t.Fatalf("expected page size 2, got %d", len(items))
	}
}

func TestAnnouncementService_Update_PartialAndTimestamp(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "Old",
		Content:    "old content",
		Tag:        "normal",
		Brief:      "old brief",
		ExpireDate: testutil.FutureDate(3),
		Priority:   1,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	created := a.LastEditedTime

	time.Sleep(10 * time.Millisecond)
	newTitle := "New Title"
	updated, err := svc.Update(a.ID, announcement.UpdateAnnouncementInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Title != "New Title" {
		t.Errorf("expected title updated, got %q", updated.Title)
	}
	if updated.Content != "old content" {
		t.Errorf("expected content unchanged, got %q", updated.Content)
	}
	if !updated.LastEditedTime.After(created) {
		t.Errorf("expected LastEditedTime to advance, got %v (was %v)", updated.LastEditedTime, created)
	}
}

func TestAnnouncementService_Update_NotFound(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	newTitle := "x"
	_, err := svc.Update(999, announcement.UpdateAnnouncementInput{Title: &newTitle})
	if !errors.Is(err, announcement.ErrAnnouncementNotFound) {
		t.Fatalf("expected ErrAnnouncementNotFound, got %v", err)
	}
}

func TestAnnouncementService_Delete(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "X",
		Content:    "c",
		Tag:        "normal",
		Brief:      "b",
		ExpireDate: testutil.FutureDate(1),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := svc.Delete(a.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := svc.GetByID(a.ID); !errors.Is(err, announcement.ErrAnnouncementNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestAnnouncementService_Delete_NotFound(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	if err := svc.Delete(999); !errors.Is(err, announcement.ErrAnnouncementNotFound) {
		t.Fatalf("expected ErrAnnouncementNotFound, got %v", err)
	}
}

func TestAnnouncementService_Create_DefaultTag(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	a, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "Default Tag",
		Content:    "fake",
		Brief:      "fake",
		ExpireDate: testutil.FutureDate(1),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if a.Tag != announcement.AnnouncementTagNormal {
		t.Errorf("expected default tag %q, got %q", announcement.AnnouncementTagNormal, a.Tag)
	}
}

func TestAnnouncementService_Create_InvalidTag(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	_, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "Bad Tag",
		Content:    "fake",
		Tag:        "invalid",
		Brief:      "fake",
		ExpireDate: testutil.FutureDate(1),
	})
	if !errors.Is(err, announcement.ErrAnnouncementInvalidTag) {
		t.Fatalf("expected ErrAnnouncementInvalidTag, got %v", err)
	}
}

func TestAnnouncementService_GetTOS_Found(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	_, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "Terms of Service",
		Content:    "You agree to...",
		Tag:        "tos",
		Brief:      "TOS brief",
		ExpireDate: testutil.FutureDate(30),
	})
	if err != nil {
		t.Fatalf("seed tos failed: %v", err)
	}

	a, err := svc.GetTOS()
	if err != nil {
		t.Fatalf("GetTOS failed: %v", err)
	}
	if a.Tag != announcement.AnnouncementTagTOS {
		t.Errorf("expected tag tos, got %q", a.Tag)
	}
	if a.Title != "Terms of Service" {
		t.Errorf("expected title %q, got %q", "Terms of Service", a.Title)
	}
}

func TestAnnouncementService_GetTOS_NotFound(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	_, err := svc.GetTOS()
	if !errors.Is(err, announcement.ErrAnnouncementNotFound) {
		t.Fatalf("expected ErrAnnouncementNotFound, got %v", err)
	}
}

func TestAnnouncementService_Create_DuplicateTOS(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	_, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "TOS One",
		Content:    "terms",
		Tag:        "tos",
		Brief:      "tos",
		ExpireDate: testutil.FutureDate(1),
	})
	if err != nil {
		t.Fatalf("first tos create failed: %v", err)
	}

	_, err = svc.Create(announcement.CreateAnnouncementInput{
		Title:      "TOS Two",
		Content:    "terms",
		Tag:        "tos",
		Brief:      "tos",
		ExpireDate: testutil.FutureDate(1),
	})
	if !errors.Is(err, announcement.ErrAnnouncementTOSAlreadyExists) {
		t.Fatalf("expected ErrAnnouncementTOSAlreadyExists, got %v", err)
	}
}

func TestAnnouncementService_Update_DuplicateTOS(t *testing.T) {
	db := setupAnnouncementServiceDB(t)
	svc := announcement.NewAnnouncementService(db)

	a1, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "TOS One",
		Content:    "terms",
		Tag:        "tos",
		Brief:      "tos",
		ExpireDate: testutil.FutureDate(1),
	})
	if err != nil {
		t.Fatalf("first tos create failed: %v", err)
	}

	a2, err := svc.Create(announcement.CreateAnnouncementInput{
		Title:      "Normal",
		Content:    "fake",
		Tag:        "normal",
		Brief:      "fake",
		ExpireDate: testutil.FutureDate(1),
	})
	if err != nil {
		t.Fatalf("normal create failed: %v", err)
	}

	tos := announcement.AnnouncementTagTOS
	_, err = svc.Update(a2.ID, announcement.UpdateAnnouncementInput{Tag: &tos})
	if !errors.Is(err, announcement.ErrAnnouncementTOSAlreadyExists) {
		t.Fatalf("expected ErrAnnouncementTOSAlreadyExists, got %v", err)
	}

	// Updating the existing TOS to itself should succeed.
	updated, err := svc.Update(a1.ID, announcement.UpdateAnnouncementInput{Tag: &tos})
	if err != nil {
		t.Fatalf("re-setting tos on same announcement should succeed: %v", err)
	}
	if updated.Tag != announcement.AnnouncementTagTOS {
		t.Errorf("expected tag to remain tos, got %q", updated.Tag)
	}
}
