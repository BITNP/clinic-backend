package app_test

import (
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/app"
)

func validDingTalkEnv() map[string]string {
	return map[string]string{
		"DINGTALK_NOTIFY_ENABLED": "true",
		"DINGTALK_BASE_URL":       "https://message-center.example/",
		"DINGTALK_APP_CODE":       "clinic",
		"DINGTALK_APP_KEY":        "app-key",
		"DINGTALK_APP_SECRET":     "app-secret",
	}
}

func TestLoadDingTalkConfig(t *testing.T) {
	values := validDingTalkEnv()
	values["DINGTALK_BASE_URL"] = " https://message-center.example/ "
	values["DINGTALK_APP_CODE"] = " clinic "
	values["DINGTALK_APP_KEY"] = " app-key "
	values["DINGTALK_APP_SECRET"] = " app-secret "
	values["DINGTALK_TIMEOUT"] = "3s"

	cfg, err := app.LoadDingTalkConfig(envMap(values))
	if err != nil {
		t.Fatalf("LoadDingTalkConfig() error = %v", err)
	}
	if !cfg.Enabled || cfg.BaseURL != "https://message-center.example/" || cfg.AppCode != "clinic" || cfg.AppKey != "app-key" || cfg.AppSecret != "app-secret" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v, want 3s", cfg.Timeout)
	}
	if cfg.Notification.Title != "电脑诊所工单状态更新" || cfg.Notification.Header != "网络开拓者协会" {
		t.Errorf("notification defaults = %+v", cfg.Notification)
	}
	if cfg.Reminder.LeadTime != 30*time.Minute || cfg.Reminder.PollInterval != time.Minute {
		t.Errorf("reminder defaults = %+v", cfg.Reminder)
	}
	if !strings.Contains(cfg.Notification.ConfirmedTemplate, "您的预约已通过") ||
		!strings.Contains(cfg.Notification.ReferredTemplate, "建议联系售后进行维修") ||
		!strings.Contains(cfg.Notification.ReferredTemplate, "{{.REAS}}") ||
		!strings.Contains(cfg.Notification.NoShowTemplate, "{{.QQAC}}") {
		t.Errorf("notification templates = %+v", cfg.Notification)
	}
}

func TestLoadDingTalkConfigNotificationOverrides(t *testing.T) {
	values := validDingTalkEnv()
	values["DINGTALK_NOTIFICATION_TITLE"] = "自定义标题"
	values["DINGTALK_NOTIFICATION_HEADER"] = "自定义协会"
	values["DINGTALK_NOTIFICATION_QQ_ACCOUNT"] = "123456"
	values["DINGTALK_NOTIFICATION_TRANSFER_ROOM"] = "转诊室"
	values["DINGTALK_NOTIFICATION_TRANSFER_ADDRESS"] = "转诊地址"
	values["DINGTALK_TEMPLATE_CONFIRMED"] = "确认 {{.ROOM}}"
	values["DINGTALK_TEMPLATE_REJECTED"] = "驳回 {{.REAS}}"
	values["DINGTALK_TEMPLATE_REFERRED"] = "转诊 {{.TRRO}}"
	values["DINGTALK_TEMPLATE_NO_SHOW"] = "未到 {{.QQAC}}"
	values["DINGTALK_TEMPLATE_REMINDER"] = "提醒 {{.DATE}}"
	values["DINGTALK_REMINDER_LEAD_TIME"] = "45m"
	values["DINGTALK_REMINDER_POLL_INTERVAL"] = "30s"

	cfg, err := app.LoadDingTalkConfig(envMap(values))
	if err != nil {
		t.Fatalf("LoadDingTalkConfig() error = %v", err)
	}
	got := cfg.Notification
	if got.Title != "自定义标题" || got.Header != "自定义协会" || got.QQAccount != "123456" ||
		got.TransferRoom != "转诊室" || got.TransferAddress != "转诊地址" ||
		got.ConfirmedTemplate != "确认 {{.ROOM}}" || got.RejectedTemplate != "驳回 {{.REAS}}" ||
		got.ReferredTemplate != "转诊 {{.TRRO}}" || got.NoShowTemplate != "未到 {{.QQAC}}" ||
		got.ReminderTemplate != "提醒 {{.DATE}}" {
		t.Errorf("notification config = %+v", got)
	}
	if cfg.Reminder.LeadTime != 45*time.Minute || cfg.Reminder.PollInterval != 30*time.Second {
		t.Errorf("reminder config = %+v", cfg.Reminder)
	}
}

func TestLoadDingTalkConfigDisabledByDefault(t *testing.T) {
	cfg, err := app.LoadDingTalkConfig(envMap(nil))
	if err != nil {
		t.Fatalf("LoadDingTalkConfig() error = %v", err)
	}
	if cfg.Enabled || cfg.BaseURL != "" || cfg.AppCode != "" || cfg.AppKey != "" || cfg.AppSecret != "" || cfg.Timeout != 0 {
		t.Errorf("config = %+v, want disabled zero delivery config", cfg)
	}
}

func TestLoadDingTalkConfigDefaults(t *testing.T) {
	cfg, err := app.LoadDingTalkConfig(envMap(validDingTalkEnv()))
	if err != nil {
		t.Fatalf("LoadDingTalkConfig() error = %v", err)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", cfg.Timeout)
	}
}

func TestLoadDingTalkConfigDisabledSkipsDeliverySettings(t *testing.T) {
	cfg, err := app.LoadDingTalkConfig(envMap(map[string]string{
		"DINGTALK_NOTIFY_ENABLED": "false",
		"DINGTALK_TIMEOUT":        "invalid",
	}))
	if err != nil {
		t.Fatalf("LoadDingTalkConfig() error = %v", err)
	}
	if cfg.Enabled || cfg.BaseURL != "" || cfg.AppCode != "" || cfg.AppKey != "" || cfg.AppSecret != "" || cfg.Timeout != 0 {
		t.Errorf("config = %+v, want disabled zero delivery config", cfg)
	}
}

func TestLoadDingTalkConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"enabled", "DINGTALK_NOTIFY_ENABLED", "sometimes", "invalid DINGTALK_NOTIFY_ENABLED"},
		{"base URL", "DINGTALK_BASE_URL", "", "base URL is required"},
		{"app code", "DINGTALK_APP_CODE", "", "app code is required"},
		{"app key", "DINGTALK_APP_KEY", "", "app key is required"},
		{"app secret", "DINGTALK_APP_SECRET", "", "app secret is required"},
		{"timeout", "DINGTALK_TIMEOUT", "0s", "invalid DINGTALK_TIMEOUT"},
		{"reminder lead time", "DINGTALK_REMINDER_LEAD_TIME", "0s", "invalid DINGTALK_REMINDER_LEAD_TIME"},
		{"reminder poll interval", "DINGTALK_REMINDER_POLL_INTERVAL", "-1s", "invalid DINGTALK_REMINDER_POLL_INTERVAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validDingTalkEnv()
			values[tt.key] = tt.value
			_, err := app.LoadDingTalkConfig(envMap(values))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
