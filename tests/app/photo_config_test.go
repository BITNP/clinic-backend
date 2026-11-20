package app_test

import (
	"testing"

	"clinic-backend/internal/app"
)

func TestLoadPhotoConfig(t *testing.T) {
	cfg, err := app.LoadPhotoConfig(4, 2048000)
	if err != nil {
		t.Fatalf("LoadPhotoConfig() error = %v", err)
	}
	if cfg.MaxCount != 4 || cfg.MaxSizeBytes != 2048000 {
		t.Fatalf("config = %+v, want count 4 and size 2048000", cfg)
	}
}

func TestLoadPhotoConfigRejectsNonPositiveLimits(t *testing.T) {
	if _, err := app.LoadPhotoConfig(0, 1024); err == nil {
		t.Fatal("expected non-positive count error")
	}
	if _, err := app.LoadPhotoConfig(1, 0); err == nil {
		t.Fatal("expected non-positive size error")
	}
}
