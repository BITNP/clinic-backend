package app

import (
	"fmt"
	"strings"
	"time"

	notificationclient "clinic-backend/internal/notification/client"
)

const defaultDingTalkTimeout = 10 * time.Second

// DingTalkConfig contains the message-center settings used for DingTalk
// notifications.
type DingTalkConfig struct {
	BaseURL   string
	AppCode   string
	AppKey    string
	AppSecret string
	Timeout   time.Duration
}

// LoadDingTalkConfig reads and validates DingTalk message-center settings.
func LoadDingTalkConfig(getenv func(string) string) (DingTalkConfig, error) {
	timeout, err := parsePositiveDurationSetting(getenv("DINGTALK_TIMEOUT"), defaultDingTalkTimeout)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_TIMEOUT: %w", err)
	}

	cfg := DingTalkConfig{
		BaseURL:   strings.TrimSpace(getenv("DINGTALK_BASE_URL")),
		AppCode:   strings.TrimSpace(getenv("DINGTALK_APP_CODE")),
		AppKey:    strings.TrimSpace(getenv("DINGTALK_APP_KEY")),
		AppSecret: strings.TrimSpace(getenv("DINGTALK_APP_SECRET")),
		Timeout:   timeout,
	}
	if _, err := notificationclient.NewDingTalkClient(notificationclient.Config{
		BaseURL:   cfg.BaseURL,
		AppCode:   cfg.AppCode,
		AppKey:    cfg.AppKey,
		AppSecret: cfg.AppSecret,
		Timeout:   cfg.Timeout,
	}); err != nil {
		return DingTalkConfig{}, err
	}
	return cfg, nil
}
