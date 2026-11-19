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
