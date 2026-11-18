package service_test

import (
	"testing"

	roomservice "clinic-backend/internal/room/service"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

func newRoomService(t *testing.T, db *gorm.DB) *roomservice.RoomService {
	t.Helper()
	return roomservice.NewRoomService(testutil.RawDB(t, db))
}

func newCampusService(t *testing.T, db *gorm.DB) *roomservice.CampusService {
	t.Helper()
	return roomservice.NewCampusService(testutil.RawDB(t, db))
}
