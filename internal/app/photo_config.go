package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultRecordPhotoMaxCount     = 9
	defaultRecordPhotoMaxSizeBytes = int64(10 * 1024 * 1024)
)

// photoConfig holds the limits enforced for photos attached to record worker
// notes.
type photoConfig struct {
	MaxCount     int
	MaxSizeBytes int64
}

// loadPhotoConfig reads and validates the record photo limits. Empty values
// use the documented defaults.
func loadPhotoConfig(getenv func(string) string) (photoConfig, error) {
	maxCount, err := parsePositiveIntSetting(getenv("RECORD_PHOTO_MAX_COUNT"), defaultRecordPhotoMaxCount)
	if err != nil {
		return photoConfig{}, fmt.Errorf("invalid RECORD_PHOTO_MAX_COUNT: %w", err)
	}

	maxSizeBytes, err := parsePositiveInt64Setting(getenv("RECORD_PHOTO_MAX_SIZE_BYTES"), defaultRecordPhotoMaxSizeBytes)
	if err != nil {
		return photoConfig{}, fmt.Errorf("invalid RECORD_PHOTO_MAX_SIZE_BYTES: %w", err)
	}

	return photoConfig{
		MaxCount:     maxCount,
		MaxSizeBytes: maxSizeBytes,
	}, nil
}

func parsePositiveIntSetting(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, errors.New("must be positive")
	}
	return value, nil
}

func parsePositiveInt64Setting(raw string, fallback int64) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, errors.New("must be positive")
	}
	return value, nil
}
