package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PhotoConfig holds the limits enforced for photos attached to record worker
// notes.
type PhotoConfig struct {
	MaxCount     int
	MaxSizeBytes int64
}

// LoadPhotoConfig validates the database-managed photo limits.
func LoadPhotoConfig(maxCount int, maxSizeBytes int64) (PhotoConfig, error) {
	if maxCount <= 0 {
		return PhotoConfig{}, errors.New("invalid RECORD_PHOTO_MAX_COUNT: must be positive")
	}
	if maxSizeBytes <= 0 {
		return PhotoConfig{}, fmt.Errorf("invalid RECORD_PHOTO_MAX_SIZE_BYTES: must be positive")
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
