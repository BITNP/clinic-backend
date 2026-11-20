package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultRecordPhotoMaxSizeBytes = int64(10 * 1024 * 1024)
)

// PhotoConfig holds the limits enforced for photos attached to record worker
// notes.
type PhotoConfig struct {
	MaxCount     int
	MaxSizeBytes int64
}

// LoadPhotoConfig combines the database-managed photo count with
// environment-managed upload settings.
func LoadPhotoConfig(getenv func(string) string, maxCount int) (PhotoConfig, error) {
	if maxCount <= 0 {
		return PhotoConfig{}, errors.New("invalid RECORD_PHOTO_MAX_COUNT: must be positive")
	}
	maxSizeBytes, err := parsePositiveInt64Setting(getenv("RECORD_PHOTO_MAX_SIZE_BYTES"), defaultRecordPhotoMaxSizeBytes)
	if err != nil {
		return PhotoConfig{}, fmt.Errorf("invalid RECORD_PHOTO_MAX_SIZE_BYTES: %w", err)
	}

	return PhotoConfig{
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
