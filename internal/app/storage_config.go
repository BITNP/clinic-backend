package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/storage"
)

const (
	defaultPutURLTTL = 5 * time.Minute
	defaultGetURLTTL = 10 * time.Minute

	defaultMultimeterS3Bucket = "clinic-executable"
)

// LoadStorageService reads storage settings and builds the configured service.
func LoadStorageService(getenv func(string) string) (*storage.Service, error) {
	putURLTTL, err := parseDurationSetting(getenv("S3_PUT_URL_TTL"), defaultPutURLTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid S3_PUT_URL_TTL: %w", err)
	}
	getURLTTL, err := parseDurationSetting(getenv("S3_GET_URL_TTL"), defaultGetURLTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid S3_GET_URL_TTL: %w", err)
	}
	if putURLTTL <= 0 {
		return nil, errors.New("S3_PUT_URL_TTL must be positive")
	}
	if getURLTTL <= 0 {
		return nil, errors.New("S3_GET_URL_TTL must be positive")
	}

	client, err := storage.NewClient(storage.Config{
		Endpoint:         getenv("S3_ENDPOINT"),
		InternalEndpoint: getenv("S3_INTERNAL_ENDPOINT"),
		Bucket:           getenv("S3_BUCKET"),
		Region:           getenv("S3_REGION"),
		AccessKeyID:      getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey:  getenv("S3_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("invalid storage configuration: %w", err)
	}

	service, err := storage.NewService(client, putURLTTL, getURLTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid storage expiry configuration: %w", err)
	}
	return service, nil
}

// LoadMultimeterStorageService builds the storage service used to create
// multimeter executable download links.
func LoadMultimeterStorageService(getenv func(string) string) (*storage.Service, error) {
	getURLTTL, err := parseDurationSetting(getenv("S3_GET_URL_TTL"), defaultGetURLTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid S3_GET_URL_TTL: %w", err)
	}
	if getURLTTL <= 0 {
		return nil, errors.New("S3_GET_URL_TTL must be positive")
	}

	bucket := strings.TrimSpace(getenv("MULTIMETER_S3_BUCKET"))
	if bucket == "" {
		bucket = defaultMultimeterS3Bucket
	}

	client, err := storage.NewClient(storage.Config{
		Endpoint:         getenv("S3_ENDPOINT"),
		InternalEndpoint: getenv("S3_INTERNAL_ENDPOINT"),
		Bucket:           bucket,
		Region:           getenv("S3_REGION"),
		AccessKeyID:      settingWithFallback(getenv, "MULTIMETER_S3_ACCESS_KEY_ID", "S3_ACCESS_KEY_ID"),
		SecretAccessKey:  settingWithFallback(getenv, "MULTIMETER_S3_SECRET_ACCESS_KEY", "S3_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("invalid multimeter storage configuration: %w", err)
	}

	service, err := storage.NewService(client, getURLTTL, getURLTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid multimeter storage expiry configuration: %w", err)
	}
	return service, nil
}

func settingWithFallback(getenv func(string) string, primary, fallback string) string {
	if value := getenv(primary); strings.TrimSpace(value) != "" {
		return value
	}
	return getenv(fallback)
}

func parseDurationSetting(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}
