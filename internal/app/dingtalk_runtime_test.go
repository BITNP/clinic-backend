package app

import (
	"context"
	"testing"
	"time"

	configservice "clinic-backend/internal/config/service"
	notificationclient "clinic-backend/internal/notification/client"
	recordservice "clinic-backend/internal/record/service"
)

type fakeDingTalkStatusSender struct {
	calls    int
	requests []notificationclient.SendRequest
}

func (f *fakeDingTalkStatusSender) SendAndCheckStatus(
	_ context.Context,
	input notificationclient.SendRequest,
) (notificationclient.SendResult, notificationclient.MessageStatus, error) {
	f.calls++
	f.requests = append(f.requests, input)
	return notificationclient.SendResult{MsgID: "message-1"}, notificationclient.MessageStatus{MsgID: "message-1"}, nil
}

func dingTalkRuntimeSettings() configservice.RuntimeSettings {
	return configservice.RuntimeSettings{
		DingTalk: configservice.DingTalkRuntimeSettings{
			Timeout:              10 * time.Second,
			ReminderLeadTime:     30 * time.Minute,
			ReminderPollInterval: time.Minute,
			NotificationTitle:    configservice.DefaultDingTalkNotificationTitle,
			NotificationHeader:   configservice.DefaultDingTalkNotificationHeader,
			ConfirmedTemplate:    configservice.DefaultDingTalkConfirmedTemplate,
			RejectedTemplate:     configservice.DefaultDingTalkRejectedTemplate,
			ReferredTemplate:     configservice.DefaultDingTalkReferredTemplate,
			TransferTemplate:     configservice.DefaultDingTalkTransferTemplate,
			NoShowTemplate:       configservice.DefaultDingTalkNoShowTemplate,
			NoShowNoBanTemplate:  configservice.DefaultDingTalkNoShowNoBanTemplate,
			ReminderTemplate:     configservice.DefaultDingTalkReminderTemplate,
		},
	}
}

func TestDingTalkRuntimeManagerActivatesOnlyPreparedSettings(t *testing.T) {
	sender := &fakeDingTalkStatusSender{}
	var clientConfig notificationclient.Config
	manager := newDingTalkRuntimeManager(time.UTC)
	manager.senderFactory = func(cfg notificationclient.Config) (recordservice.DingTalkStatusSender, error) {
		clientConfig = cfg
		return sender, nil
	}

	settings := dingTalkRuntimeSettings()
	if err := manager.Initialize(settings); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	notification := recordservice.TicketStatusNotification{
		RecordID:     42,
		User:         "2026001",
		Status:       "confirmed",
		RoomName:     "A101",
		RoomAddress:  "A building",
		ServiceStart: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC),
		ServiceEnd:   time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC),
	}
	if _, err := manager.Notify(context.Background(), notification); err != nil {
		t.Fatalf("disabled Notify() error = %v", err)
	}
	if _, err := manager.NotifyReminder(context.Background(), notification); err == nil {
		t.Fatal("disabled NotifyReminder() error = nil, want retryable failure")
	}
	if sender.calls != 0 {
		t.Fatalf("disabled sender calls = %d, want 0", sender.calls)
	}

	enabled := settings
	enabled.DingTalk.Enabled = true
	enabled.DingTalk.BaseURL = "https://message-center.example"
	enabled.DingTalk.AppCode = "clinic"
	enabled.DingTalk.AppKey = "app-key"
	enabled.DingTalk.AppSecret = "app-secret"
	enabled.DingTalk.RedirectEnabled = true
	enabled.DingTalk.RedirectReceiver = "dev-user"
	if err := manager.Prepare(enabled); err != nil {
		t.Fatalf("Prepare(enabled) error = %v", err)
	}
	if _, err := manager.Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify() before Activate error = %v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("sender calls before Activate = %d, want 0", sender.calls)
	}

	manager.Activate(enabled)
	if clientConfig.ReceiverOverride != "dev-user" {
		t.Errorf("receiver override = %q, want dev-user", clientConfig.ReceiverOverride)
	}
	delivery, err := manager.Notify(context.Background(), notification)
	if err != nil {
		t.Fatalf("enabled Notify() error = %v", err)
	}
	if delivery.MessageID != "message-1" || delivery.Skipped {
		t.Fatalf("delivery = %+v", delivery)
	}
	if sender.calls != 1 || len(sender.requests) != 1 {
		t.Fatalf("sender calls = %d, requests = %d; want 1 and 1", sender.calls, len(sender.requests))
	}

	invalid := enabled
	invalid.DingTalk.AppSecret = ""
	if err := manager.Prepare(invalid); err == nil {
		t.Fatal("Prepare(invalid) error = nil, want validation error")
	}
	if _, err := manager.Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify() after rejected Prepare error = %v", err)
	}
	if sender.calls != 2 {
		t.Fatalf("active sender calls = %d, want 2", sender.calls)
	}

	disabled := enabled
	disabled.DingTalk.Enabled = false
	if err := manager.Prepare(disabled); err != nil {
		t.Fatalf("Prepare(disabled) error = %v", err)
	}
	manager.Activate(disabled)
	if _, err := manager.Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify() after disable error = %v", err)
	}
	if sender.calls != 2 {
		t.Fatalf("sender calls after disable = %d, want 2", sender.calls)
	}
}
