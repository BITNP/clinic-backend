package app

import (
	"strings"
	"testing"
)

func TestLoadPhotoConfig_Defaults(t *testing.T) {
	cfg, err := loadPhotoConfig(envMap(nil))
	if err != nil {
		t.Fatalf("loadPhotoConfig() error = %v", err)
	}
	if cfg.MaxCount != 9 {
		t.Errorf("MaxCount = %d, want 9", cfg.MaxCount)
	}
	if cfg.MaxSizeBytes != 10*1024*1024 {
		t.Errorf("MaxSizeBytes = %d, want %d", cfg.MaxSizeBytes, 10*1024*1024)
	}
}

func TestLoadPhotoConfig_CustomValues(t *testing.T) {
	cfg, err := loadPhotoConfig(envMap(map[string]string{
		"RECORD_PHOTO_MAX_COUNT":      "4",
		"RECORD_PHOTO_MAX_SIZE_BYTES": "2048000",
	}))
	if err != nil {
		t.Fatalf("loadPhotoConfig() error = %v", err)
	}
	if cfg.MaxCount != 4 {
		t.Errorf("MaxCount = %d, want 4", cfg.MaxCount)
	}
	if cfg.MaxSizeBytes != 2048000 {
		t.Errorf("MaxSizeBytes = %d, want 2048000", cfg.MaxSizeBytes)
	}
}

func TestLoadPhotoConfig_TrimsWhitespace(t *testing.T) {
	cfg, err := loadPhotoConfig(envMap(map[string]string{
		"RECORD_PHOTO_MAX_COUNT":      " 2 ",
		"RECORD_PHOTO_MAX_SIZE_BYTES": " 1024 ",
	}))
	if err != nil {
		t.Fatalf("loadPhotoConfig() error = %v", err)
	}
	if cfg.MaxCount != 2 || cfg.MaxSizeBytes != 1024 {
		t.Fatalf("config = %+v, want count 2 and size 1024", cfg)
	}
}

func TestLoadPhotoConfig_InvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		raw  string
		want string
	}{
		{
			name: "count not a number",
			key:  "RECORD_PHOTO_MAX_COUNT",
			raw:  "many",
			want: "invalid RECORD_PHOTO_MAX_COUNT",
		},
		{
			name: "count zero",
			key:  "RECORD_PHOTO_MAX_COUNT",
			raw:  "0",
			want: "invalid RECORD_PHOTO_MAX_COUNT: must be positive",
		},
		{
			name: "count negative",
			key:  "RECORD_PHOTO_MAX_COUNT",
			raw:  "-1",
			want: "invalid RECORD_PHOTO_MAX_COUNT: must be positive",
		},
		{
			name: "size not a number",
			key:  "RECORD_PHOTO_MAX_SIZE_BYTES",
			raw:  "large",
			want: "invalid RECORD_PHOTO_MAX_SIZE_BYTES",
		},
		{
			name: "size zero",
			key:  "RECORD_PHOTO_MAX_SIZE_BYTES",
			raw:  "0",
			want: "invalid RECORD_PHOTO_MAX_SIZE_BYTES: must be positive",
		},
		{
			name: "size negative",
			key:  "RECORD_PHOTO_MAX_SIZE_BYTES",
			raw:  "-1024",
			want: "invalid RECORD_PHOTO_MAX_SIZE_BYTES: must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadPhotoConfig(envMap(map[string]string{tt.key: tt.raw}))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}
