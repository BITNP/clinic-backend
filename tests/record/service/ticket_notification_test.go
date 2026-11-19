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
	"clinic-backend/internal/testutil"
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
	err           error
}

func (f *fakeTicketStatusNotifier) Notify(_ context.Context, notification recordservice.TicketStatusNotification) error {
	f.notifications = append(f.notifications, notification)
	return f.err
}

func TestDingTalkTicketNotifierFormatsStatusMessages(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	appointment := time.Date(2026, 11, 20, 0, 0, 0, 0, location)
	tests := []struct {
		name    string
		status  recordmodel.RecordStatus
		reason  string
		content string
	}{
		{
			name:    "confirmed",
			status:  recordmodel.RecordStatusConfirmed,
			content: "您的工单 #00000042（预约日期 2026-11-20）已确认。",
		},
		{
			name:   "rejected",
			status: recordmodel.RecordStatusRejected,
			reason: "预约时间不在服务时段",
			content: "您的工单 #00000042（预约日期 2026-11-20）已驳回。\n" +
				"驳回原因：预约时间不在服务时段",
		},
		{
			name:   "referred",
			status: recordmodel.RecordStatusReferred,
			reason: "需要主板级维修",
			content: "您的工单 #00000042（预约日期 2026-11-20）建议返厂。\n" +
				"说明：需要主板级维修",
		},
		{
			name:    "no show",
			status:  recordmodel.RecordStatusNoShow,
			content: "您的工单 #00000042（预约日期 2026-11-20）因未到诊所已关闭。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeDingTalkStatusSender{}
			notifier := recordservice.NewDingTalkTicketNotifier(sender, location)
			err := notifier.Notify(context.Background(), recordservice.TicketStatusNotification{
				RecordID:        42,
				User:            "ding-user-42",
				Status:          tt.status,
				AppointmentTime: appointment,
				Reason:          tt.reason,
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
			if request.Title != "电脑诊所工单状态更新" || request.Content != tt.content {
				t.Errorf("message = %q / %q", request.Title, request.Content)
			}
			if request.MessageType != "text" || request.Level != "normal" {
				t.Errorf("message options = %q / %q", request.MessageType, request.Level)
			}
		})
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
		})
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
	}

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
	}
}
