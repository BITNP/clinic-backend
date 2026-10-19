package service_test

import (
	"testing"
	"time"

	recordservice "clinic-backend/internal/record/service"
	taggingservice "clinic-backend/internal/tagging/service"
	"clinic-backend/internal/testutil"
)

func TestTicketService_Create_AssignsDefaultTag(t *testing.T) {
	db := testutil.SetupTagTestDB(t)
	tagSvc := taggingservice.NewRecordTagService(db)
	def, err := tagSvc.SeedDefault()
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	roomID := testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)

	svc := recordservice.NewTicketService(db, nil)
	svc.SetDefaultTagID(def.ID)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
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
