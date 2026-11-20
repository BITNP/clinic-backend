package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// RuntimeSettingsValues returns a complete external runtime settings fixture.
func RuntimeSettingsValues() map[string]string {
	return map[string]string{
		"SYNC_POLL_INTERVAL":               "10s",
		"STAFF_VERSION":                    "0",
		"TAGGER_LLM_API_KEY":               "",
		"RECORD_ACTION_WINDOW":             "5m",
		"RECORD_PHOTO_MAX_COUNT":           "9",
		"SERVICE_DATE_REMINDER_ENABLED":    "false",
		"SERVICE_DATE_REMINDER_LEAD_TIMES": "5h,3h",
	}
}

// WriteRuntimeSettingsFile writes values to a temporary external settings file.
func WriteRuntimeSettingsFile(t *testing.T, values map[string]string) string {
	t.Helper()

	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		t.Fatalf("marshal runtime settings: %v", err)
	}
	path := filepath.Join(t.TempDir(), "runtime_settings.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write runtime settings: %v", err)
	}
	return path
}
