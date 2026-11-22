package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	notificationclient "clinic-backend/internal/notification/client"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

type fakeDingTalkStatusSender struct {
	requests []notificationclient.SendRequest
	err      error
}

func (f *fakeDingTalkStatusSender) SendAndCheckStatus(
	_ context.Context,
	input notificationclient.SendRequest,
) (notificationclient.SendResult, notificationclient.MessageStatus, error) {
	f.requests = append(f.requests, input)
	if f.err != nil {
		return notificationclient.SendResult{}, notificationclient.MessageStatus{}, f.err
	}
	return notificationclient.SendResult{MsgID: "message-1"}, notificationclient.MessageStatus{
		MsgID: "message-1",
		Detail: []notificationclient.MessageDetail{{
			Platform:         "dingTalk",
			Status:           "send_success",
			SendSuccessCount: 1,
		}},
	}, nil
}

type fakeTicketStatusNotifier struct {
	notifications []recordservice.TicketStatusNotification
	reminders     []recordservice.TicketStatusNotification
	err           error
}

func (f *fakeTicketStatusNotifier) Notify(_ context.Context, notification recordservice.TicketStatusNotification) error {
	f.notifications = append(f.notifications, notification)
	return f.err
}

func (f *fakeTicketStatusNotifier) NotifyReminder(_ context.Context, notification recordservice.TicketStatusNotification) error {
	f.reminders = append(f.reminders, notification)
	return f.err
}

func seedTicketNotificationServiceDate(
	t *testing.T,
	db *gorm.DB,
	roomID uint,
	record *recordmodel.ClinicRecord,
) {
	t.Helper()
	day := time.Date(
		record.AppointmentTime.Year(),
		record.AppointmentTime.Month(),
		record.AppointmentTime.Day(),
		0, 0, 0, 0, time.UTC,
	)
	if err := db.Model(&recordmodel.ClinicRecord{}).
		Where("id = ?", record.ID).
		Update("appointment_time", day).Error; err != nil {
		t.Fatalf("normalize record appointment date: %v", err)
	}
	record.AppointmentTime = day
	start := day.Add(10 * time.Hour)
	serviceDate := schedulingmodel.ClinicServiceDate{
		Capacity:  10,
		RoomID:    &roomID,
		Date:      day,
		StartTime: start,
		EndTime:   start.Add(2 * time.Hour),
		Title:     "test",
	}
	if err := db.Create(&serviceDate).Error; err != nil {
		t.Fatalf("seed service date: %v", err)
	}
}

func TestDingTalkTicketNotifierFormatsStatusMessages(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	start := time.Date(2026, 11, 20, 18, 30, 0, 0, location)
	end := time.Date(2026, 11, 20, 21, 0, 0, 0, location)
	const allFields = "{{.HEDR}}|{{.DATE}}|{{.ID}}|{{.ROOM}}|{{.LOCA}}|{{.TRRO}}|{{.TRLO}}|{{.REAS}}|{{.QQAC}}"
	cfg := recordservice.DingTalkTicketNotifierConfig{
		Title:             "状态通知",
		Header:            "测试协会",
		QQAccount:         "123456",
		TransferRoom:      "转诊诊室",
		TransferAddress:   "转诊地址",
		ConfirmedTemplate: allFields,
		RejectedTemplate:  allFields,
		ReferredTemplate:  allFields,
		NoShowTemplate:    allFields,
		ReminderTemplate:  "{{.ROOM}}",
	}
	tests := []struct {
		name   string
		status recordmodel.RecordStatus
		reason string
	}{
		{
			name:   "confirmed",
			status: recordmodel.RecordStatusConfirmed,
		},
		{
			name:   "rejected",
			status: recordmodel.RecordStatusRejected,
			reason: "预约时间不在服务时段",
		},
		{
			name:   "referred",
			status: recordmodel.RecordStatusReferred,
			reason: "需要主板级维修",
		},
		{
			name:   "no show",
			status: recordmodel.RecordStatusNoShow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeDingTalkStatusSender{}
			notifier, err := recordservice.NewDingTalkTicketNotifier(sender, location, cfg)
			if err != nil {
				t.Fatalf("NewDingTalkTicketNotifier() error = %v", err)
			}
			err = notifier.Notify(context.Background(), recordservice.TicketStatusNotification{
				RecordID:     42,
				User:         "ding-user-42",
				Status:       tt.status,
				Reason:       tt.reason,
				RoomName:     "徐特立图书馆",
				RoomAddress:  "徐特立图书馆三楼311G研讨室",
				ServiceStart: start,
				ServiceEnd:   end,
			})
			if err != nil {
				t.Fatalf("Notify() error = %v", err)
			}
			if len(sender.requests) != 1 {
				t.Fatalf("requests = %d, want 1", len(sender.requests))
			}
			request := sender.requests[0]
			if request.Receivers != "ding-user-42" {
				t.Errorf("Receivers = %q, want ding-user-42", request.Receivers)
			}
			wantContent := "测试协会|2026-11-20的18:30-21:00|#42|徐特立图书馆|徐特立图书馆三楼311G研讨室|转诊诊室|转诊地址|" +
				tt.reason + "|123456"
			if request.Title != "状态通知" || request.Content != wantContent {
				t.Errorf("message = %q / %q", request.Title, request.Content)
			}
			if request.MessageType != "text" || request.Level != "normal" {
				t.Errorf("message options = %q / %q", request.MessageType, request.Level)
			}
		})
	}
}

func TestDingTalkTicketNotifierRejectsInvalidTemplate(t *testing.T) {
	cfg := recordservice.DingTalkTicketNotifierConfig{
		Title:             "状态通知",
		Header:            "测试协会",
		ConfirmedTemplate: "{{.UNKNOWN}}",
		RejectedTemplate:  "rejected",
		ReferredTemplate:  "referred",
		NoShowTemplate:    "no_show",
		ReminderTemplate:  "reminder",
	}
	if _, err := recordservice.NewDingTalkTicketNotifier(&fakeDingTalkStatusSender{}, time.UTC, cfg); err == nil {
		t.Fatal("expected invalid template error")
	}
}

func TestDingTalkTicketNotifierDecodesEscapedNewlines(t *testing.T) {
	cfg := recordservice.DingTalkTicketNotifierConfig{
		Title:             "状态通知",
		Header:            "测试协会",
		ConfirmedTemplate: `第一行\n{{.ROOM}}`,
		RejectedTemplate:  "rejected",
		ReferredTemplate:  "referred",
		NoShowTemplate:    "no_show",
		ReminderTemplate:  "reminder",
	}
	sender := &fakeDingTalkStatusSender{}
	notifier, err := recordservice.NewDingTalkTicketNotifier(sender, time.UTC, cfg)
	if err != nil {
		t.Fatalf("NewDingTalkTicketNotifier() error = %v", err)
	}
	err = notifier.Notify(context.Background(), recordservice.TicketStatusNotification{
		RecordID:     1,
		User:         "ding-user",
		Status:       recordmodel.RecordStatusConfirmed,
		RoomName:     "测试诊室",
		ServiceStart: time.Date(2026, 11, 20, 18, 30, 0, 0, time.UTC),
		ServiceEnd:   time.Date(2026, 11, 20, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if got := sender.requests[0].Content; got != "第一行\n测试诊室" {
		t.Errorf("Content = %q", got)
	}
}

func TestDingTalkTicketNotifierSendsReminderTemplate(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	cfg := recordservice.DingTalkTicketNotifierConfig{
		Title:             "状态通知",
		Header:            "测试协会",
		ConfirmedTemplate: "confirmed",
		RejectedTemplate:  "rejected",
		ReferredTemplate:  "referred",
		NoShowTemplate:    "no_show",
		ReminderTemplate:  "提醒：{{.DATE}} {{.ROOM}} {{.LOCA}}",
	}
	sender := &fakeDingTalkStatusSender{}
	notifier, err := recordservice.NewDingTalkTicketNotifier(sender, location, cfg)
	if err != nil {
		t.Fatalf("NewDingTalkTicketNotifier() error = %v", err)
	}
	err = notifier.NotifyReminder(context.Background(), recordservice.TicketStatusNotification{
		RecordID:     1,
		User:         "ding-user",
		RoomName:     "徐特立图书馆",
		RoomAddress:  "三楼311G",
		ServiceStart: time.Date(2026, 11, 20, 18, 30, 0, 0, location),
		ServiceEnd:   time.Date(2026, 11, 20, 21, 0, 0, 0, location),
	})
	if err != nil {
		t.Fatalf("NotifyReminder() error = %v", err)
	}
	if got := sender.requests[0].Content; got != "提醒：2026-11-20的18:30-21:00 徐特立图书馆 三楼311G" {
		t.Errorf("Content = %q", got)
	}
}

func TestTicketTransitionsNotifySupportedStatuses(t *testing.T) {
	tests := []struct {
		name       string
		initial    recordmodel.RecordStatus
		status     recordmodel.RecordStatus
		reason     string
		transition func(context.Context, *recordservice.AdminRecordService, uint) error
	}{
		{
			name:    "confirmed",
			initial: recordmodel.RecordStatusPending,
			status:  recordmodel.RecordStatusConfirmed,
			transition: func(ctx context.Context, svc *recordservice.AdminRecordService, id uint) error {
				_, err := svc.MarkConfirmed(ctx, id, 7)
				return err
			},
		},
		{
			name:    "rejected",
			initial: recordmodel.RecordStatusPending,
			status:  recordmodel.RecordStatusRejected,
			reason:  "日程冲突",
			transition: func(ctx context.Context, svc *recordservice.AdminRecordService, id uint) error {
				_, err := svc.MarkRejected(ctx, id, "日程冲突", 7)
				return err
			},
		},
		{
			name:    "referred",
			initial: recordmodel.RecordStatusPending,
			status:  recordmodel.RecordStatusReferred,
			reason:  "建议返厂",
			transition: func(ctx context.Context, svc *recordservice.AdminRecordService, id uint) error {
				_, err := svc.MarkReferred(ctx, id, "建议返厂", 7)
				return err
			},
		},
		{
			name:    "no show",
			initial: recordmodel.RecordStatusConfirmed,
			status:  recordmodel.RecordStatusNoShow,
			transition: func(ctx context.Context, svc *recordservice.AdminRecordService, id uint) error {
				_, err := svc.MarkNoShow(ctx, 7, id)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupAdminRecordTestDB(t)
			room := seedActionRoom(t, db)
			record := seedActionRecord(t, db, tt.initial, room.ID)
			record.User = "ding-user-" + tt.name
			if err := db.Model(&record).Update("user", record.User).Error; err != nil {
				t.Fatal(err)
			}
			seedTicketNotificationServiceDate(t, db, room.ID, &record)

			notifier := &fakeTicketStatusNotifier{}
			svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
			svc.SetTicketStatusNotifier(notifier)
			if err := tt.transition(context.Background(), svc, record.ID); err != nil {
				t.Fatalf("transition: %v", err)
			}
			if len(notifier.notifications) != 1 {
				t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
			}
			got := notifier.notifications[0]
			if got.RecordID != record.ID || got.User != record.User || got.Status != tt.status || got.Reason != tt.reason {
				t.Errorf("notification = %+v", got)
			}
			if got.RoomName != room.Name || got.ServiceStart.IsZero() || got.ServiceEnd.IsZero() {
				t.Errorf("notification details = %+v", got)
			}
		})
	}
}

func TestConfirmToRoomNotifiesSelectedRoom(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	source := seedActionRoom(t, db)
	destination := roommodel.ClinicRoom{Name: "Destination Room", Address: "Destination Address", Enabled: true}
	if err := db.Create(&destination).Error; err != nil {
		t.Fatal(err)
	}
	record := seedActionRecord(t, db, recordmodel.RecordStatusPending, source.ID)
	seedTicketNotificationServiceDate(t, db, destination.ID, &record)
	notifier := &fakeTicketStatusNotifier{}
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	svc.SetTicketStatusNotifier(notifier)

	view, err := svc.MarkConfirmedToRoom(context.Background(), record.ID, &destination.ID, 7)
	if err != nil {
		t.Fatalf("MarkConfirmedToRoom() error = %v", err)
	}
	if view.Campus != destination.Name {
		t.Fatalf("view campus = %q, want %q", view.Campus, destination.Name)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
	got := notifier.notifications[0]
	if got.RoomName != destination.Name || got.RoomAddress != destination.Address {
		t.Fatalf("notification room = %q, %q; want %q, %q", got.RoomName, got.RoomAddress, destination.Name, destination.Address)
	}
}

func TestTicketTransitionSkipsUnsupportedStatus(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	record := seedActionRecord(t, db, recordmodel.RecordStatusConfirmed, room.ID)
	notifier := &fakeTicketStatusNotifier{}
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	svc.SetTicketStatusNotifier(notifier)

	if _, err := svc.MarkArrived(context.Background(), 7, record.ID); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("notifications = %+v, want none", notifier.notifications)
	}
}

func TestTicketNotificationFailureDoesNotFailTransition(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := seedActionRoom(t, db)
	record := seedActionRecord(t, db, recordmodel.RecordStatusPending, room.ID)
	seedTicketNotificationServiceDate(t, db, room.ID, &record)
	notifier := &fakeTicketStatusNotifier{err: errors.New("message center unavailable")}
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	svc.SetTicketStatusNotifier(notifier)

	view, err := svc.MarkConfirmed(context.Background(), record.ID, 7)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if view.Status != string(recordmodel.RecordStatusConfirmed) {
		t.Fatalf("status = %s, want confirmed", view.Status)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
}

func TestCloseExpiredRecordsNotifiesNoShowGuests(t *testing.T) {
	db := setupAdminRecordTestDB(t)
	room := roommodel.ClinicRoom{Name: "Test Room"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatal(err)
	}
	cutoff := yesterdayCutoff()
	var lastRecord recordmodel.ClinicRecord
	for _, tc := range []struct {
		user   string
		status recordmodel.RecordStatus
	}{
		{user: "pending-user", status: recordmodel.RecordStatusPending},
		{user: "confirmed-user", status: recordmodel.RecordStatusConfirmed},
	} {
		record := recordmodel.ClinicRecord{
			User:            tc.user,
			Realname:        "r",
			PhoneNum:        "p",
			Status:          tc.status,
			AppointmentTime: cutoff.AddDate(0, 0, -1),
			QuestionDesc:    "x",
			RoomID:          room.ID,
		}
		if err := db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
		lastRecord = record
	}
	seedTicketNotificationServiceDate(t, db, room.ID, &lastRecord)

	notifier := &fakeTicketStatusNotifier{}
	svc := recordservice.NewAdminRecordService(testutil.RawDB(t, db))
	svc.SetTicketStatusNotifier(notifier)
	noShow, completed, err := svc.CloseExpiredRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if noShow != 2 || completed != 0 {
		t.Fatalf("counts = no_show %d, completed %d", noShow, completed)
	}
	if len(notifier.notifications) != 2 {
		t.Fatalf("notifications = %d, want 2", len(notifier.notifications))
	}
	for _, notification := range notifier.notifications {
		if notification.Status != recordmodel.RecordStatusNoShow {
			t.Errorf("status = %s, want no_show", notification.Status)
		}
		if notification.RoomName != room.Name || notification.ServiceStart.IsZero() {
			t.Errorf("notification details = %+v", notification)
		}
	}
}
