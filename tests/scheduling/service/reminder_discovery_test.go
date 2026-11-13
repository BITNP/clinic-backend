package service_test

import (
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
	staffmodel "clinic-backend/internal/staff/model"
	"context"
	"fmt"
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
	date := schedulingmodel.ClinicServiceDate{ID: 1, RoomID: &room, Date: day, StartTime: start.UTC(), EndTime: start.Add(time.Hour).UTC()}
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
	staff := staffmodel.ClinicStaff{ID: 2, AccountID: "staff", Role: "staff", QQNumber: "456"}
	noQQ := staffmodel.ClinicStaff{ID: 3, AccountID: "no-qq", Role: "admin"}
	for _, value := range []any{&staff, &noQQ, &schedulingmodel.ClinicWorkScheduleStaff{WeekdayID: 1, ScheduleID: 1, StaffID: 2}, &schedulingmodel.ClinicWorkScheduleStaff{WeekdayID: 1, ScheduleID: 1, StaffID: 3}, &schedulingmodel.ClinicWorkScheduleStaff{WeekdayID: 1, ScheduleID: 1, StaffID: 1}, &recordmodel.ClinicRecord{RoomID: 2, AppointmentTime: day, Status: recordmodel.RecordStatusPending}, &recordmodel.ClinicRecord{RoomID: room, AppointmentTime: day, Status: recordmodel.RecordStatusConfirmed}} {
		if err = db.Create(value).Error; err != nil {
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
	sentMessage := ""
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing gateway token")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		sentMessage = string(body)
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"status":"async","retcode":1,"data":{"delivery_id":"qq-1"}}`)), Header: make(http.Header)}, nil
	})}
	delivery := schedulingservice.ReminderDelivery{DB: db, Discovery: &d, Client: client, BaseURL: "http://gateway", Account: "qq", Token: "secret", Template: template.Must(template.New("message").Parse("{{.PendingCount}} {{.LeadTime}}")), Lease: time.Minute, Now: func() time.Time { return now }}
	job, err := delivery.Claim(context.Background(), now)
	if err != nil || job == nil {
		t.Fatalf("claim %v %v", job, err)
	}
	if err = delivery.Deliver(context.Background(), job, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sentMessage, `"message":"1 59m0s"`) {
		t.Fatalf("message = %s", sentMessage)
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
	rescheduled := schedulingmodel.ServiceDateReminder{ServiceDateID: date.ID, ServiceStartAt: date.StartTime, LeadTimeSeconds: 2700, StaffID: admin.ID, Status: "pending", NextAttemptAt: now}
	if err = db.Create(&rescheduled).Error; err != nil {
		t.Fatal(err)
	}
	rescheduledClaim, err := delivery.Claim(context.Background(), now)
	if err != nil || rescheduledClaim == nil {
		t.Fatalf("rescheduled claim %v %v", rescheduledClaim, err)
	}
	if err = db.Model(&schedulingmodel.ClinicServiceDate{}).Where("id = ?", date.ID).Update("start_time", date.StartTime.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err = delivery.Deliver(context.Background(), rescheduledClaim, now); err != nil {
		t.Fatal(err)
	}
	if err = db.First(&rescheduled, rescheduled.ID).Error; err != nil || rescheduled.Status != "cancelled" {
		t.Fatalf("rescheduled job=%+v err=%v", rescheduled, err)
	}
	if err = db.Model(&schedulingmodel.ClinicServiceDate{}).Where("id = ?", date.ID).Update("start_time", date.StartTime).Error; err != nil {
		t.Fatal(err)
	}
	retryJob := schedulingmodel.ServiceDateReminder{ServiceDateID: date.ID, ServiceStartAt: date.StartTime, LeadTimeSeconds: 1800, StaffID: admin.ID, Status: "pending", NextAttemptAt: now}
	if err = db.Create(&retryJob).Error; err != nil {
		t.Fatal(err)
	}
	delivery.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("offline") })}
	retryClaim, err := delivery.Claim(context.Background(), now)
	if err != nil || retryClaim == nil {
		t.Fatalf("retry claim %v %v", retryClaim, err)
	}
	if err = delivery.Deliver(context.Background(), retryClaim, now); err == nil {
		t.Fatal("expected failed submission")
	}
	if err = db.First(&retryJob, retryJob.ID).Error; err != nil || retryJob.Status != "pending" || retryJob.Attempts != 1 || !retryJob.NextAttemptAt.After(now) {
		t.Fatalf("retry job=%+v err=%v", retryJob, err)
	}
	lateJob := schedulingmodel.ServiceDateReminder{ServiceDateID: date.ID, ServiceStartAt: date.StartTime, LeadTimeSeconds: 1200, StaffID: admin.ID, Status: "pending", NextAttemptAt: now}
	if err = db.Create(&lateJob).Error; err != nil {
		t.Fatal(err)
	}
	lateClaim, err := delivery.Claim(context.Background(), now)
	if err != nil || lateClaim == nil {
		t.Fatalf("late claim %v %v", lateClaim, err)
	}
	delivery.Now = func() time.Time { return start }
	if err = delivery.Deliver(context.Background(), lateClaim, now); err != nil {
		t.Fatal(err)
	}
	if err = db.First(&lateJob, lateJob.ID).Error; err != nil || lateJob.Status != "cancelled" {
		t.Fatalf("late job=%+v err=%v", lateJob, err)
	}
	delivery.Now = func() time.Time { return now }
	if err = db.Model(&record).Update("status", recordmodel.RecordStatusConfirmed).Error; err != nil {
		t.Fatal(err)
	}
	retryClaim, err = delivery.Claim(context.Background(), retryJob.NextAttemptAt)
	if err != nil || retryClaim == nil {
		t.Fatalf("claim retry %v %v", retryClaim, err)
	}
	if err = delivery.Deliver(context.Background(), retryClaim, retryJob.NextAttemptAt); err != nil {
		t.Fatal(err)
	}
	if err = db.First(&retryJob, retryJob.ID).Error; err != nil || retryJob.Status != "cancelled" {
		t.Fatalf("cancelled job=%+v err=%v", retryJob, err)
	}
	if err = db.Model(&retryJob).Updates(map[string]any{"status": "pending", "next_attempt_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	oldClaim, err := delivery.Claim(context.Background(), now)
	if err != nil || oldClaim == nil {
		t.Fatalf("old claim %v %v", oldClaim, err)
	}
	newClaim, err := delivery.Claim(context.Background(), now.Add(2*time.Minute))
	if err != nil || newClaim == nil {
		t.Fatalf("expired claim %v %v", newClaim, err)
	}
	if err = delivery.Deliver(context.Background(), oldClaim, now); err == nil {
		t.Fatal("stale claim unexpectedly updated job")
	}
	if err = delivery.Deliver(context.Background(), newClaim, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, count, err := d.Eligible(context.Background(), date)
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
