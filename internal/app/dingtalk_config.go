package app

import (
	"fmt"
	"strings"
	"time"

	configservice "clinic-backend/internal/config/service"
	notificationclient "clinic-backend/internal/notification/client"
	recordservice "clinic-backend/internal/record/service"
)

const (
	defaultDingTalkTimeout              = 10 * time.Second
	defaultDingTalkReminderLeadTime     = 30 * time.Minute
	defaultDingTalkReminderPollInterval = time.Minute
)

// DingTalkNotificationConfig contains the configurable guest notification
// templates and their organization-specific values.
type DingTalkNotificationConfig struct {
	Title             string
	Header            string
	QQAccount         string
	TransferRoom      string
	TransferAddress   string
	ConfirmedTemplate string
	RejectedTemplate  string
	ReferredTemplate  string
	NoShowTemplate    string
	ReminderTemplate  string
}

// DingTalkReminderConfig controls guest pre-shift reminder discovery.
type DingTalkReminderConfig struct {
	LeadTime     time.Duration
	PollInterval time.Duration
}

// DingTalkConfig contains the message-center settings used for DingTalk
// notifications.
type DingTalkConfig struct {
	Enabled          bool
	BaseURL          string
	AppCode          string
	AppKey           string
	AppSecret        string
	Timeout          time.Duration
	RedirectEnabled  bool
	RedirectReceiver string
	Notification     DingTalkNotificationConfig
	Reminder         DingTalkReminderConfig
}

// LoadDingTalkConfig reads and validates DingTalk message-center settings.
func LoadDingTalkConfig(getenv func(string) string) (DingTalkConfig, error) {
	enabled, err := parseBoolSetting(getenv("DINGTALK_NOTIFY_ENABLED"), false)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_NOTIFY_ENABLED: %w", err)
	}
	if !enabled {
		return DingTalkConfig{Enabled: false}, nil
	}

	timeout, err := parsePositiveDurationSetting(getenv("DINGTALK_TIMEOUT"), defaultDingTalkTimeout)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_TIMEOUT: %w", err)
	}
	reminderLeadTime, err := parsePositiveDurationSetting(getenv("DINGTALK_REMINDER_LEAD_TIME"), defaultDingTalkReminderLeadTime)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_REMINDER_LEAD_TIME: %w", err)
	}
	reminderPollInterval, err := parsePositiveDurationSetting(getenv("DINGTALK_REMINDER_POLL_INTERVAL"), defaultDingTalkReminderPollInterval)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_REMINDER_POLL_INTERVAL: %w", err)
	}
	redirectEnabled, err := parseBoolSetting(getenv("DINGTALK_REDIRECT_ENABLED"), false)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_REDIRECT_ENABLED: %w", err)
	}
	redirectReceiver := strings.TrimSpace(getenv("DINGTALK_REDIRECT_RECEIVER"))
	if redirectEnabled && redirectReceiver == "" {
		return DingTalkConfig{}, fmt.Errorf("DINGTALK_REDIRECT_RECEIVER is required when DINGTALK_REDIRECT_ENABLED is true")
	}
	if !redirectEnabled {
		redirectReceiver = ""
	}

	cfg := DingTalkConfig{
		Enabled:          true,
		BaseURL:          strings.TrimSpace(getenv("DINGTALK_BASE_URL")),
		AppCode:          strings.TrimSpace(getenv("DINGTALK_APP_CODE")),
		AppKey:           strings.TrimSpace(getenv("DINGTALK_APP_KEY")),
		AppSecret:        strings.TrimSpace(getenv("DINGTALK_APP_SECRET")),
		Timeout:          timeout,
		RedirectEnabled:  redirectEnabled,
		RedirectReceiver: redirectReceiver,
		Notification: DingTalkNotificationConfig{
			Title:             dingTalkStringSetting(getenv, "DINGTALK_NOTIFICATION_TITLE", configservice.DefaultDingTalkNotificationTitle),
			Header:            dingTalkStringSetting(getenv, "DINGTALK_NOTIFICATION_HEADER", configservice.DefaultDingTalkNotificationHeader),
			QQAccount:         strings.TrimSpace(getenv("DINGTALK_NOTIFICATION_QQ_ACCOUNT")),
			TransferRoom:      strings.TrimSpace(getenv("DINGTALK_NOTIFICATION_TRANSFER_ROOM")),
			TransferAddress:   strings.TrimSpace(getenv("DINGTALK_NOTIFICATION_TRANSFER_ADDRESS")),
			ConfirmedTemplate: dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_CONFIRMED", configservice.DefaultDingTalkConfirmedTemplate),
			RejectedTemplate:  dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_REJECTED", configservice.DefaultDingTalkRejectedTemplate),
			ReferredTemplate:  dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_REFERRED", configservice.DefaultDingTalkReferredTemplate),
			NoShowTemplate:    dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_NO_SHOW", configservice.DefaultDingTalkNoShowTemplate),
			ReminderTemplate:  dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_REMINDER", configservice.DefaultDingTalkReminderTemplate),
		},
		Reminder: DingTalkReminderConfig{
			LeadTime:     reminderLeadTime,
			PollInterval: reminderPollInterval,
		},
	}
	if err := validateDingTalkConfig(cfg); err != nil {
		return DingTalkConfig{}, err
	}
	return cfg, nil
}

// LoadDingTalkConfigFromRuntime converts database-managed settings without
// reading process environment variables.
func LoadDingTalkConfigFromRuntime(settings configservice.RuntimeSettings) (DingTalkConfig, error) {
	dingTalk := settings.DingTalk
	cfg := DingTalkConfig{
		Enabled:          dingTalk.Enabled,
		BaseURL:          strings.TrimSpace(dingTalk.BaseURL),
		AppCode:          strings.TrimSpace(dingTalk.AppCode),
		AppKey:           strings.TrimSpace(dingTalk.AppKey),
		AppSecret:        strings.TrimSpace(dingTalk.AppSecret),
		Timeout:          dingTalk.Timeout,
		RedirectEnabled:  dingTalk.RedirectEnabled,
		RedirectReceiver: strings.TrimSpace(dingTalk.RedirectReceiver),
		Notification: DingTalkNotificationConfig{
			Title:             strings.TrimSpace(dingTalk.NotificationTitle),
			Header:            strings.TrimSpace(dingTalk.NotificationHeader),
			QQAccount:         strings.TrimSpace(dingTalk.QQAccount),
			TransferRoom:      strings.TrimSpace(dingTalk.TransferRoom),
			TransferAddress:   strings.TrimSpace(dingTalk.TransferAddress),
			ConfirmedTemplate: dingTalk.ConfirmedTemplate,
			RejectedTemplate:  dingTalk.RejectedTemplate,
			ReferredTemplate:  dingTalk.ReferredTemplate,
			NoShowTemplate:    dingTalk.NoShowTemplate,
			ReminderTemplate:  dingTalk.ReminderTemplate,
		},
		Reminder: DingTalkReminderConfig{
			LeadTime:     dingTalk.ReminderLeadTime,
			PollInterval: dingTalk.ReminderPollInterval,
		},
	}
	if err := validateDingTalkConfig(cfg); err != nil {
		return DingTalkConfig{}, err
	}
	return cfg, nil
}

func validateDingTalkConfig(cfg DingTalkConfig) error {
	if cfg.RedirectEnabled {
		receiver := strings.TrimSpace(cfg.RedirectReceiver)
		if receiver == "" {
			return fmt.Errorf("DINGTALK_REDIRECT_RECEIVER is required when DINGTALK_REDIRECT_ENABLED is true")
		}
		if strings.ContainsAny(receiver, ",，") {
			return fmt.Errorf("DINGTALK_REDIRECT_RECEIVER must contain exactly one receiver")
		}
	}
	if cfg.Enabled {
		if _, err := notificationclient.NewDingTalkClient(notificationclient.Config{
			BaseURL:          cfg.BaseURL,
			AppCode:          cfg.AppCode,
			AppKey:           cfg.AppKey,
			AppSecret:        cfg.AppSecret,
			Timeout:          cfg.Timeout,
			ReceiverOverride: dingTalkReceiverOverride(cfg),
		}); err != nil {
			return err
		}
	}
	if _, err := recordservice.NewDingTalkTicketNotifier(nil, time.UTC, dingTalkTicketNotifierConfig(cfg)); err != nil {
		return err
	}
	return nil
}

func dingTalkReceiverOverride(cfg DingTalkConfig) string {
	if !cfg.RedirectEnabled {
		return ""
	}
	return strings.TrimSpace(cfg.RedirectReceiver)
}

func dingTalkTicketNotifierConfig(cfg DingTalkConfig) recordservice.DingTalkTicketNotifierConfig {
	return recordservice.DingTalkTicketNotifierConfig{
		Title:             cfg.Notification.Title,
		Header:            cfg.Notification.Header,
		QQAccount:         cfg.Notification.QQAccount,
		TransferRoom:      cfg.Notification.TransferRoom,
		TransferAddress:   cfg.Notification.TransferAddress,
		ConfirmedTemplate: cfg.Notification.ConfirmedTemplate,
		RejectedTemplate:  cfg.Notification.RejectedTemplate,
		ReferredTemplate:  cfg.Notification.ReferredTemplate,
		NoShowTemplate:    cfg.Notification.NoShowTemplate,
		ReminderTemplate:  cfg.Notification.ReminderTemplate,
	}
}

func dingTalkStringSetting(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}
