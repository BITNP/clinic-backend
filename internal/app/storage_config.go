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
)

func loadStorageService(getenv func(string) string) (*storage.Service, error) {
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

func parseDurationSetting(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}
