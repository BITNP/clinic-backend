package service_test

import (
	recordmodel "clinic-backend/internal/record/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	staffmodel "clinic-backend/internal/staff/model"
	"context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReminderDiscoveryCreatesOneJobForAssignedAdmin(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&schedulingmodel.ClinicServiceDate{}, &schedulingmodel.ServiceDateReminder{}, &schedulingmodel.ClinicWorkSchedule{}, &schedulingmodel.ClinicWorkScheduleWeekday{}, &schedulingmodel.ClinicWorkScheduleStaff{}, &staffmodel.ClinicStaff{}, &recordmodel.ClinicRecord{}); err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("UTC+8", 8*3600)
	start := time.Date(2026, 11, 12, 18, 30, 0, 0, loc)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	room := uint(1)
	date := schedulingmodel.ClinicServiceDate{ID: 1, RoomID: &room, Date: day, StartTime: start, EndTime: start.Add(time.Hour)}
	admin := staffmodel.ClinicStaff{ID: 1, AccountID: "a", Role: "admin", QQNumber: "123"}
	schedule := schedulingmodel.ClinicWorkSchedule{ID: 1, Name: "schedule", StartDate: day, EndDate: day, Enabled: true}
	slot := schedulingmodel.ClinicWorkScheduleWeekday{ID: 1, WorkScheduleID: 1, Weekday: int(day.Weekday()), RoomID: room, StartTime: time.Date(1970, 1, 1, 18, 0, 0, 0, time.UTC), EndTime: time.Date(1970, 1, 1, 20, 0, 0, 0, time.UTC)}
	assignment := schedulingmodel.ClinicWorkScheduleStaff{WeekdayID: 1, ScheduleID: 1, StaffID: 1}
	record := recordmodel.ClinicRecord{RoomID: room, AppointmentTime: day, Status: recordmodel.RecordStatusPending}
	for _, v := range []any{&date, &admin, &schedule, &slot, &assignment, &record} {
		if err = db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	d := schedulingservice.ReminderDiscovery{DB: db, Location: loc, LeadTimes: []time.Duration{time.Hour}}
	now := start.Add(-time.Hour).Add(time.Second)
	for range 2 {
		if err = d.Discover(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	var jobs []schedulingmodel.ServiceDateReminder
	if err = db.Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].StaffID != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	if err = db.Model(&record).Update("status", recordmodel.RecordStatusConfirmed).Error; err != nil {
		t.Fatal(err)
	}
	_, count, err := d.Eligible(context.Background(), date)
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
