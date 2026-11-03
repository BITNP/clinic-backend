package app_test

import (
	"testing"
	"time"

	"clinic-backend/internal/app"
	"clinic-backend/internal/message"
)

func envMap(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func validMessageEnv() map[string]string {
	return map[string]string{
		"NAPCAT_BASE_URL": "http://napcat:3001",
	}
}

func TestLoadMessageConfigDefaults(t *testing.T) {
	cfg, err := app.LoadMessageConfig(envMap(nil))
	if err != nil {
		t.Fatalf("LoadMessageConfig() error = %v", err)
	}

	if cfg.Enabled {
		t.Fatal("Enabled = true, want false without NAPCAT_BASE_URL")
	}
	if cfg.SenderKey != "qq" {
		t.Errorf("SenderKey = %q, want qq", cfg.SenderKey)
	}
	if cfg.Manager.QueueSize != 100 {
		t.Errorf("QueueSize = %d, want 100", cfg.Manager.QueueSize)
	}
	if cfg.Manager.SendTimeout != 30*time.Second {
		t.Errorf("SendTimeout = %v, want 30s", cfg.Manager.SendTimeout)
	}
	if cfg.Manager.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", cfg.Manager.MaxAttempts)
	}
	if cfg.Manager.RetryBackoff != 2*time.Second {
		t.Errorf("RetryBackoff = %v, want 2s", cfg.Manager.RetryBackoff)
	}
	if cfg.Manager.MinGap != time.Second {
		t.Errorf("MinGap = %v, want 1s", cfg.Manager.MinGap)
	}
	if cfg.NapCat.Timeout != 10*time.Second {
		t.Errorf("NapCat.Timeout = %v, want 10s", cfg.NapCat.Timeout)
	}
	if cfg.NapCat.DirectoryRefresh != 30*time.Minute {
		t.Errorf("DirectoryRefresh = %v, want 30m", cfg.NapCat.DirectoryRefresh)
	}
	if cfg.NapCat.DirectoryRetry != 5*time.Second {
		t.Errorf("DirectoryRetry = %v, want 5s", cfg.NapCat.DirectoryRetry)
	}
	if cfg.NapCat.DirectoryTimeout != 60*time.Second {
		t.Errorf("DirectoryTimeout = %v, want 60s", cfg.NapCat.DirectoryTimeout)
	}
	if !cfg.NapCat.GroupTempSession {
		t.Error("GroupTempSession = false, want true")
	}

	gap := cfg.Gap.Gap(message.Message{Text: "hello"})
	if gap < 3*time.Second || gap >= 4*time.Second {
		t.Errorf("default gap = %v, want in [3s, 4s)", gap)
	}
}

func TestLoadMessageConfigCustom(t *testing.T) {
	cfg, err := app.LoadMessageConfig(envMap(map[string]string{
		"NAPCAT_BASE_URL":           "http://napcat:3001",
		"NAPCAT_ACCESS_TOKEN":       " secret ",
		"NAPCAT_TIMEOUT":            "2s",
		"NAPCAT_SENDER_KEY":         " qq-main ",
		"NAPCAT_DIRECTORY_REFRESH":  "10m",
		"NAPCAT_DIRECTORY_RETRY":    "1s",
		"NAPCAT_DIRECTORY_TIMEOUT":  "20s",
		"NAPCAT_GROUP_TEMP_SESSION": "false",
		"MESSAGE_QUEUE_SIZE":        "5",
		"MESSAGE_SEND_TIMEOUT":      "4s",
		"MESSAGE_MAX_ATTEMPTS":      "2",
		"MESSAGE_RETRY_BACKOFF":     "1s",
		"MESSAGE_GAP_MODE":          "fixed",
		"MESSAGE_GAP_FIXED":         "2s",
		"MESSAGE_GAP_JITTER":        "0s",
		"MESSAGE_GAP_MIN":           "500ms",
	}))
	if err != nil {
		t.Fatalf("LoadMessageConfig() error = %v", err)
	}

	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.SenderKey != "qq-main" {
		t.Errorf("SenderKey = %q, want qq-main", cfg.SenderKey)
	}
	if cfg.NapCat.BaseURL != "http://napcat:3001" {
		t.Errorf("BaseURL = %q", cfg.NapCat.BaseURL)
	}
	if cfg.NapCat.AccessToken != "secret" {
		t.Errorf("AccessToken = %q, want secret", cfg.NapCat.AccessToken)
	}
	if cfg.NapCat.Timeout != 2*time.Second {
		t.Errorf("NapCat.Timeout = %v, want 2s", cfg.NapCat.Timeout)
	}
	if cfg.NapCat.DirectoryRefresh != 10*time.Minute {
		t.Errorf("DirectoryRefresh = %v, want 10m", cfg.NapCat.DirectoryRefresh)
	}
	if cfg.NapCat.DirectoryRetry != time.Second {
		t.Errorf("DirectoryRetry = %v, want 1s", cfg.NapCat.DirectoryRetry)
	}
	if cfg.NapCat.DirectoryTimeout != 20*time.Second {
		t.Errorf("DirectoryTimeout = %v, want 20s", cfg.NapCat.DirectoryTimeout)
	}
	if cfg.NapCat.GroupTempSession {
		t.Error("GroupTempSession = true, want false")
	}
	if cfg.Manager.QueueSize != 5 {
		t.Errorf("QueueSize = %d, want 5", cfg.Manager.QueueSize)
	}
	if cfg.Manager.SendTimeout != 4*time.Second {
		t.Errorf("SendTimeout = %v, want 4s", cfg.Manager.SendTimeout)
	}
	if cfg.Manager.MaxAttempts != 2 {
		t.Errorf("MaxAttempts = %d, want 2", cfg.Manager.MaxAttempts)
	}
	if cfg.Manager.RetryBackoff != time.Second {
		t.Errorf("RetryBackoff = %v, want 1s", cfg.Manager.RetryBackoff)
	}
	if cfg.Manager.MinGap != 500*time.Millisecond {
		t.Errorf("MinGap = %v, want 500ms", cfg.Manager.MinGap)
	}

	if gap := cfg.Gap.Gap(message.Message{Text: "hello"}); gap != 2*time.Second {
		t.Errorf("gap = %v, want 2s", gap)
	}
}

func TestLoadMessageConfigLengthGap(t *testing.T) {
	cfg, err := app.LoadMessageConfig(envMap(map[string]string{
		"NAPCAT_BASE_URL":         "http://napcat:3001",
		"MESSAGE_GAP_MODE":        "length",
		"MESSAGE_GAP_LENGTH_BASE": "1s",
		"MESSAGE_GAP_PER_RUNE":    "100ms",
		"MESSAGE_GAP_JITTER":      "0s",
	}))
	if err != nil {
		t.Fatalf("LoadMessageConfig() error = %v", err)
	}

	if gap := cfg.Gap.Gap(message.Message{Text: "你好"}); gap != 1200*time.Millisecond {
		t.Errorf("gap = %v, want 1200ms", gap)
	}
}

func TestLoadMessageConfigDisabled(t *testing.T) {
	cfg, err := app.LoadMessageConfig(envMap(map[string]string{
		"NAPCAT_BASE_URL":    "   ",
		"MESSAGE_GAP_MODE":   "length",
		"MESSAGE_GAP_JITTER": "0s",
	}))
	if err != nil {
		t.Fatalf("LoadMessageConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
}

func TestLoadMessageConfigInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"queue size", "MESSAGE_QUEUE_SIZE", "0"},
		{"send timeout", "MESSAGE_SEND_TIMEOUT", "0s"},
		{"max attempts", "MESSAGE_MAX_ATTEMPTS", "0"},
		{"retry backoff", "MESSAGE_RETRY_BACKOFF", "-1s"},
		{"gap mode", "MESSAGE_GAP_MODE", "random"},
		{"gap fixed", "MESSAGE_GAP_FIXED", "-1s"},
		{"gap length base", "MESSAGE_GAP_LENGTH_BASE", "-1s"},
		{"gap per rune", "MESSAGE_GAP_PER_RUNE", "-1s"},
		{"gap jitter", "MESSAGE_GAP_JITTER", "-1s"},
		{"gap min", "MESSAGE_GAP_MIN", "-1s"},
		{"napcat timeout", "NAPCAT_TIMEOUT", "0s"},
		{"directory refresh", "NAPCAT_DIRECTORY_REFRESH", "0s"},
		{"directory retry", "NAPCAT_DIRECTORY_RETRY", "0s"},
		{"directory timeout", "NAPCAT_DIRECTORY_TIMEOUT", "0s"},
		{"group temp session", "NAPCAT_GROUP_TEMP_SESSION", "sometimes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validMessageEnv()
			values[tt.key] = tt.value

			if _, err := app.LoadMessageConfig(envMap(values)); err == nil {
				t.Fatalf("LoadMessageConfig() error = nil for %s=%q", tt.key, tt.value)
			}
		})
	}
}
