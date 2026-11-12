package service_test

import (
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	staffmodel "clinic-backend/internal/staff/model"
	"context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"testing"
	"text/template"
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
	if err = db.Create(&roommodel.ClinicRoom{ID: room, Name: "clinic", Address: "A"}).Error; err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing gateway token")
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"status":"async","retcode":1,"data":{"delivery_id":"qq-1"}}`)), Header: make(http.Header)}, nil
	})}
	delivery := schedulingservice.ReminderDelivery{DB: db, Discovery: &d, Client: client, BaseURL: "http://gateway", Account: "qq", Token: "secret", Template: template.Must(template.New("message").Parse("{{.PendingCount}}")), Lease: time.Minute}
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("claim %v %v", job, err)
	}
	if err = delivery.Deliver(context.Background(), job, now); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
	if err = db.First(&jobs[0], job.ID).Error; err != nil || jobs[0].Status != "sent" {
		t.Fatalf("job=%+v err=%v", jobs[0], err)
	}
	if next, err := delivery.Claim(context.Background(), now.Add(time.Minute)); err != nil || next != nil {
		t.Fatalf("second claim=%+v err=%v", next, err)
	}
	if err = db.Model(&record).Update("status", recordmodel.RecordStatusConfirmed).Error; err != nil {
		t.Fatal(err)
	}
	_, count, err := d.Eligible(context.Background(), date)
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
