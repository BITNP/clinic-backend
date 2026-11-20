package app_test

import (
	"strings"
	"testing"
	"time"

	"clinic-backend/internal/app"
)

func validServiceDateReminderEnv() map[string]string {
	return map[string]string{
		"MESSAGE_GATEWAY_URL":           "http://message-gateway:8081/",
		"MESSAGE_GATEWAY_ACCOUNT_TOKEN": "gateway-token",
		"MESSAGE_GATEWAY_TIMEOUT":       "5s",
	}
}

func TestLoadServiceDateReminderConfigDefaults(t *testing.T) {
	cfg, err := app.LoadServiceDateReminderConfig(
		envMap(nil),
		false,
		[]time.Duration{5 * time.Hour, 3 * time.Hour},
		"qq",
	)
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
	values := validServiceDateReminderEnv()
	values["SERVICE_DATE_REMINDER_TEMPLATE"] = "{{.Room}} 有 {{.PendingCount}} 个待审核工单，{{.LeadTime}} 后开始"
	values["MESSAGE_GATEWAY_URL"] = " https://gateway.example/api/ "
	values["MESSAGE_GATEWAY_ACCOUNT"] = " qq-main "
	values["MESSAGE_GATEWAY_ACCOUNT_TOKEN"] = " secret "
	values["MESSAGE_GATEWAY_TIMEOUT"] = "2s"

	cfg, err := app.LoadServiceDateReminderConfig(
		envMap(values),
		true,
		[]time.Duration{3 * time.Hour, 7 * time.Hour},
		" qq-main ",
	)
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

func TestLoadServiceDateReminderConfigDisabledDoesNotRequireGatewayToken(t *testing.T) {
	cfg, err := app.LoadServiceDateReminderConfig(
		envMap(nil),
		false,
		[]time.Duration{5 * time.Hour, 3 * time.Hour},
		"qq",
	)
	if err != nil {
		t.Fatalf("LoadServiceDateReminderConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
}

func TestLoadServiceDateReminderConfig_UsesDatabaseSettings(t *testing.T) {
	values := validServiceDateReminderEnv()
	values["SERVICE_DATE_REMINDER_ENABLED"] = "false"
	values["SERVICE_DATE_REMINDER_LEAD_TIMES"] = "1h"

	cfg, err := app.LoadServiceDateReminderConfig(
		envMap(values),
		true,
		[]time.Duration{2 * time.Hour, 30 * time.Minute},
		"qq",
	)
	if err != nil {
		t.Fatalf("LoadServiceDateReminderConfig() error = %v", err)
	}
	if !cfg.Enabled {
		t.Error("Enabled = false, want database value true")
	}
	if len(cfg.LeadTimes) != 2 || cfg.LeadTimes[0] != 2*time.Hour || cfg.LeadTimes[1] != 30*time.Minute {
		t.Errorf("LeadTimes = %v, want [2h 30m]", cfg.LeadTimes)
	}
}

func TestLoadServiceDateReminderConfig_RejectsInvalidLeadTimes(t *testing.T) {
	_, err := app.LoadServiceDateReminderConfig(
		envMap(validServiceDateReminderEnv()),
		true,
		[]time.Duration{0},
		"qq",
	)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadServiceDateReminderConfigInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		want   string
		remove bool
	}{
		{
			name:  "template syntax",
			key:   "SERVICE_DATE_REMINDER_TEMPLATE",
			value: "{{.Room",
			want:  "SERVICE_DATE_REMINDER_TEMPLATE",
		},
		{
			name:  "template unknown field",
			key:   "SERVICE_DATE_REMINDER_TEMPLATE",
			value: "{{.Missing}}",
			want:  "unsupported value",
		},
		{
			name:   "missing gateway url",
			key:    "MESSAGE_GATEWAY_URL",
			remove: true,
			want:   "MESSAGE_GATEWAY_URL is required",
		},
		{
			name:  "invalid gateway url",
			key:   "MESSAGE_GATEWAY_URL",
			value: "message-gateway:8081",
			want:  "invalid MESSAGE_GATEWAY_URL",
		},
		{
			name:   "missing gateway token",
			key:    "MESSAGE_GATEWAY_ACCOUNT_TOKEN",
			remove: true,
			want:   "MESSAGE_GATEWAY_ACCOUNT_TOKEN is required",
		},
		{
			name:  "invalid gateway account",
			key:   "MESSAGE_GATEWAY_ACCOUNT",
			value: "bad/account",
			want:  "invalid MESSAGE_GATEWAY_ACCOUNT",
		},
		{
			name:  "invalid gateway timeout",
			key:   "MESSAGE_GATEWAY_TIMEOUT",
			value: "0s",
			want:  "invalid MESSAGE_GATEWAY_TIMEOUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validServiceDateReminderEnv()
			account := "qq"
			if tt.remove {
				delete(values, tt.key)
			} else {
				values[tt.key] = tt.value
			}
			if tt.key == "MESSAGE_GATEWAY_ACCOUNT" {
				account = tt.value
				delete(values, tt.key)
			}

			_, err := app.LoadServiceDateReminderConfig(
				envMap(values),
				true,
				[]time.Duration{5 * time.Hour, 3 * time.Hour},
				account,
			)
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
		cfg, err := app.LoadServiceDateReminderConfig(envMap(nil), false, input, "qq")
		if err != nil {
			t.Fatalf("%v: %v", input, err)
		}
		if len(cfg.LeadTimes) != len(input) {
			t.Fatalf("%v: %v", input, cfg.LeadTimes)
		}
	}
}
