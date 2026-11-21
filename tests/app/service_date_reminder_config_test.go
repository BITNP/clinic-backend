package app_test

import (
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/app"
	configservice "clinic-backend/internal/config/service"
)

func validReminderSettings() configservice.RuntimeSettings {
	return configservice.RuntimeSettings{
		ServiceDateReminderEnabled:   true,
		ServiceDateReminderLeadTimes: []time.Duration{5 * time.Hour, 3 * time.Hour},
		ServiceDateReminderTemplate:  configservice.DefaultServiceDateReminderTemplate,
		MessageGatewayURL:            "http://message-gateway:8081/",
		MessageGatewayAccount:        "qq",
		MessageGatewayAccountToken:   "gateway-token",
		MessageGatewayTimeout:        5 * time.Second,
	}
}

func TestLoadServiceDateReminderConfigDefaults(t *testing.T) {
	settings := validReminderSettings()
	settings.ServiceDateReminderEnabled = false
	settings.ServiceDateReminderTemplate = ""
	settings.MessageGatewayURL = ""
	settings.MessageGatewayAccountToken = ""
	settings.MessageGatewayTimeout = 0

	cfg, err := app.LoadServiceDateReminderConfig(settings)
	if err != nil {
		t.Fatalf("LoadServiceDateReminderConfig() error = %v", err)
	}

	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	if len(cfg.LeadTimes) != 2 || cfg.LeadTimes[0] != 5*time.Hour || cfg.LeadTimes[1] != 3*time.Hour {
		t.Fatalf("LeadTimes = %v, want [5h 3h]", cfg.LeadTimes)
	}
	if cfg.Gateway.Account != "qq" {
		t.Errorf("Gateway.Account = %q, want qq", cfg.Gateway.Account)
	}
	if cfg.Gateway.Timeout != 5*time.Second {
		t.Errorf("Gateway.Timeout = %v, want 5s", cfg.Gateway.Timeout)
	}
	if cfg.Gateway.BaseURL != "" || cfg.Gateway.AccountToken != "" {
		t.Errorf("unexpected default gateway credentials: %+v", cfg.Gateway)
	}

	var rendered strings.Builder
	if err := cfg.MessageTemplate.Execute(&rendered, map[string]string{
		"Date":         "2026-11-12",
		"StartTime":    "18:30",
		"Room":         "北区医务室",
		"PendingCount": "3",
		"LeadTime":     "5 小时",
	}); err != nil {
		t.Fatalf("render default template: %v", err)
	}
	want := "[电脑诊所]2026-11-12 18:30 北区医务室 的值班还有3个待审核工单，请及时处理。"
	if rendered.String() != want {
		t.Errorf("rendered template = %q, want %q", rendered.String(), want)
	}
}

func TestLoadServiceDateReminderConfigCustom(t *testing.T) {
	settings := validReminderSettings()
	settings.ServiceDateReminderLeadTimes = []time.Duration{3 * time.Hour, 7 * time.Hour}
	settings.ServiceDateReminderTemplate = "{{.Room}} 有 {{.PendingCount}} 个待审核工单，{{.LeadTime}} 后开始"
	settings.MessageGatewayURL = " https://gateway.example/api/ "
	settings.MessageGatewayAccount = " qq-main "
	settings.MessageGatewayAccountToken = " secret "
	settings.MessageGatewayTimeout = 2 * time.Second

	cfg, err := app.LoadServiceDateReminderConfig(settings)
	if err != nil {
		t.Fatalf("LoadServiceDateReminderConfig() error = %v", err)
	}

	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if len(cfg.LeadTimes) != 2 || cfg.LeadTimes[0] != 7*time.Hour || cfg.LeadTimes[1] != 3*time.Hour {
		t.Fatalf("LeadTimes = %v, want [7h 3h]", cfg.LeadTimes)
	}
	if cfg.Gateway.BaseURL != "https://gateway.example/api" {
		t.Errorf("Gateway.BaseURL = %q", cfg.Gateway.BaseURL)
	}
	if cfg.Gateway.Account != "qq-main" {
		t.Errorf("Gateway.Account = %q, want qq-main", cfg.Gateway.Account)
	}
	if cfg.Gateway.AccountToken != "secret" {
		t.Errorf("Gateway.AccountToken = %q, want secret", cfg.Gateway.AccountToken)
	}
	if cfg.Gateway.Timeout != 2*time.Second {
		t.Errorf("Gateway.Timeout = %v, want 2s", cfg.Gateway.Timeout)
	}

	var rendered strings.Builder
	if err := cfg.MessageTemplate.Execute(&rendered, map[string]string{
		"Room":         "北区医务室",
		"PendingCount": "2",
		"LeadTime":     "7 小时",
	}); err != nil {
		t.Fatalf("render custom template: %v", err)
	}
	if rendered.String() != "北区医务室 有 2 个待审核工单，7 小时 后开始" {
		t.Errorf("rendered template = %q", rendered.String())
	}
}

func TestLoadServiceDateReminderConfigDisabledDoesNotRequireGatewayCredentials(t *testing.T) {
	settings := validReminderSettings()
	settings.ServiceDateReminderEnabled = false
	settings.MessageGatewayURL = ""
	settings.MessageGatewayAccountToken = ""

	cfg, err := app.LoadServiceDateReminderConfig(settings)
	if err != nil {
		t.Fatalf("LoadServiceDateReminderConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
}

func TestLoadServiceDateReminderConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		update func(*configservice.RuntimeSettings)
		want   string
	}{
		{
			name: "template syntax",
			update: func(settings *configservice.RuntimeSettings) {
				settings.ServiceDateReminderTemplate = "{{.Room"
			},
			want: "SERVICE_DATE_REMINDER_TEMPLATE",
		},
		{
			name: "template unknown field",
			update: func(settings *configservice.RuntimeSettings) {
				settings.ServiceDateReminderTemplate = "{{.Missing}}"
			},
			want: "unsupported value",
		},
		{
			name: "missing gateway url",
			update: func(settings *configservice.RuntimeSettings) {
				settings.MessageGatewayURL = ""
			},
			want: "MESSAGE_GATEWAY_URL is required",
		},
		{
			name: "invalid gateway url",
			update: func(settings *configservice.RuntimeSettings) {
				settings.MessageGatewayURL = "message-gateway:8081"
			},
			want: "invalid MESSAGE_GATEWAY_URL",
		},
		{
			name: "missing gateway token",
			update: func(settings *configservice.RuntimeSettings) {
				settings.MessageGatewayAccountToken = ""
			},
			want: "MESSAGE_GATEWAY_ACCOUNT_TOKEN is required",
		},
		{
			name: "invalid gateway account",
			update: func(settings *configservice.RuntimeSettings) {
				settings.MessageGatewayAccount = "bad/account"
			},
			want: "invalid MESSAGE_GATEWAY_ACCOUNT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := validReminderSettings()
			tt.update(&settings)

			_, err := app.LoadServiceDateReminderConfig(settings)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestReminderLeadTimesAllowOneOrMany(t *testing.T) {
	for _, input := range [][]time.Duration{
		{time.Hour},
		{30 * time.Minute, 5 * time.Hour, 3 * time.Hour},
	} {
		settings := validReminderSettings()
		settings.ServiceDateReminderEnabled = false
		settings.ServiceDateReminderLeadTimes = input
		cfg, err := app.LoadServiceDateReminderConfig(settings)
		if err != nil {
			t.Fatalf("%v: %v", input, err)
		}
		if len(cfg.LeadTimes) != len(input) {
			t.Fatalf("%v: %v", input, cfg.LeadTimes)
		}
	}
}
