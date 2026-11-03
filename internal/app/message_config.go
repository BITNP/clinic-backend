package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"clinic-backend/internal/message"
	"clinic-backend/internal/message/napcat"
)

const (
	defaultMessageQueueSize    = 100
	defaultMessageSendTimeout  = 30 * time.Second
	defaultMessageMaxAttempts  = 3
	defaultMessageRetryBackoff = 2 * time.Second
	defaultMessageGapFixed     = 3 * time.Second
	defaultMessageGapLength    = time.Second
	defaultMessageGapPerRune   = 50 * time.Millisecond
	defaultMessageGapJitter    = time.Second
	defaultMessageGapMin       = time.Second

	defaultNapcatTimeout          = 10 * time.Second
	defaultNapcatDirectoryRefresh = 30 * time.Minute
	defaultNapcatDirectoryRetry   = 5 * time.Second
	defaultNapcatDirectoryTimeout = 60 * time.Second
)

// MessageRuntimeConfig is the validated messaging configuration used by the
// application wiring.
type MessageRuntimeConfig struct {
	Enabled   bool
	SenderKey string
	NapCat    napcat.Config
	Manager   message.Config
	Gap       message.GapPolicy
}

// LoadMessageConfig reads and validates the messaging environment. Messaging is
// disabled when NAPCAT_BASE_URL is empty; the remaining settings are still
// parsed so configuration mistakes fail at startup.
func LoadMessageConfig(getenv func(string) string) (MessageRuntimeConfig, error) {
	queueSize, err := parsePositiveIntSetting(getenv("MESSAGE_QUEUE_SIZE"), defaultMessageQueueSize)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_QUEUE_SIZE: %w", err)
	}

	sendTimeout, err := parsePositiveDurationSetting(getenv("MESSAGE_SEND_TIMEOUT"), defaultMessageSendTimeout)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_SEND_TIMEOUT: %w", err)
	}

	maxAttempts, err := parsePositiveIntSetting(getenv("MESSAGE_MAX_ATTEMPTS"), defaultMessageMaxAttempts)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_MAX_ATTEMPTS: %w", err)
	}

	retryBackoff, err := parseNonNegativeDurationSetting(getenv("MESSAGE_RETRY_BACKOFF"), defaultMessageRetryBackoff)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_RETRY_BACKOFF: %w", err)
	}

	minGap, err := parseNonNegativeDurationSetting(getenv("MESSAGE_GAP_MIN"), defaultMessageGapMin)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_GAP_MIN: %w", err)
	}

	gapMode := message.GapMode(strings.ToLower(strings.TrimSpace(getenv("MESSAGE_GAP_MODE"))))
	if gapMode == "" {
		gapMode = message.GapModeFixed
	}

	fixedGap, err := parseNonNegativeDurationSetting(getenv("MESSAGE_GAP_FIXED"), defaultMessageGapFixed)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_GAP_FIXED: %w", err)
	}

	lengthBase, err := parseNonNegativeDurationSetting(getenv("MESSAGE_GAP_LENGTH_BASE"), defaultMessageGapLength)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_GAP_LENGTH_BASE: %w", err)
	}

	perRune, err := parseNonNegativeDurationSetting(getenv("MESSAGE_GAP_PER_RUNE"), defaultMessageGapPerRune)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_GAP_PER_RUNE: %w", err)
	}

	jitter, err := parseNonNegativeDurationSetting(getenv("MESSAGE_GAP_JITTER"), defaultMessageGapJitter)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_GAP_JITTER: %w", err)
	}

	gapBase := fixedGap
	if gapMode == message.GapModeLength {
		gapBase = lengthBase
	}
	gapPolicy, err := message.NewGapPolicy(gapMode, gapBase, perRune, jitter)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid MESSAGE_GAP_MODE: %w", err)
	}

	timeout, err := parsePositiveDurationSetting(getenv("NAPCAT_TIMEOUT"), defaultNapcatTimeout)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid NAPCAT_TIMEOUT: %w", err)
	}

	directoryRefresh, err := parsePositiveDurationSetting(getenv("NAPCAT_DIRECTORY_REFRESH"), defaultNapcatDirectoryRefresh)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid NAPCAT_DIRECTORY_REFRESH: %w", err)
	}

	directoryRetry, err := parsePositiveDurationSetting(getenv("NAPCAT_DIRECTORY_RETRY"), defaultNapcatDirectoryRetry)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid NAPCAT_DIRECTORY_RETRY: %w", err)
	}

	directoryTimeout, err := parsePositiveDurationSetting(getenv("NAPCAT_DIRECTORY_TIMEOUT"), defaultNapcatDirectoryTimeout)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid NAPCAT_DIRECTORY_TIMEOUT: %w", err)
	}

	groupTempSession, err := parseBoolSetting(getenv("NAPCAT_GROUP_TEMP_SESSION"), true)
	if err != nil {
		return MessageRuntimeConfig{}, fmt.Errorf("invalid NAPCAT_GROUP_TEMP_SESSION: %w", err)
	}

	senderKey := strings.TrimSpace(getenv("NAPCAT_SENDER_KEY"))
	if senderKey == "" {
		senderKey = "qq"
	}

	baseURL := strings.TrimSpace(getenv("NAPCAT_BASE_URL"))
	return MessageRuntimeConfig{
		Enabled:   baseURL != "",
		SenderKey: senderKey,
		NapCat: napcat.Config{
			BaseURL:          baseURL,
			AccessToken:      strings.TrimSpace(getenv("NAPCAT_ACCESS_TOKEN")),
			Timeout:          timeout,
			DirectoryRefresh: directoryRefresh,
			DirectoryRetry:   directoryRetry,
			DirectoryTimeout: directoryTimeout,
			GroupTempSession: groupTempSession,
		},
		Manager: message.Config{
			QueueSize:    queueSize,
			SendTimeout:  sendTimeout,
			MaxAttempts:  maxAttempts,
			RetryBackoff: retryBackoff,
			MinGap:       minGap,
		},
		Gap: gapPolicy,
	}, nil
}

func parsePositiveDurationSetting(raw string, fallback time.Duration) (time.Duration, error) {
	value, err := parseDurationSetting(raw, fallback)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, errors.New("must be positive")
	}
	return value, nil
}

func parseNonNegativeDurationSetting(raw string, fallback time.Duration) (time.Duration, error) {
	value, err := parseDurationSetting(raw, fallback)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, errors.New("must not be negative")
	}
	return value, nil
}

func parseBoolSetting(raw string, fallback bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseBool(raw)
}
