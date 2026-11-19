package app

import (
	"fmt"
	"strings"
	"time"

	notificationclient "clinic-backend/internal/notification/client"
)

const (
	defaultDingTalkTimeout = 10 * time.Second

	defaultDingTalkNotificationTitle  = "电脑诊所工单状态更新"
	defaultDingTalkNotificationHeader = "网络开拓者协会"
	defaultDingTalkConfirmedTemplate  = "【{{.HEDR}}】您的预约已通过，请于{{.DATE}}前往{{.ROOM}}诊室。诊室地址为{{.LOCA}}。若因故无法按时前往，请在电脑义诊平台删除预约。\n感谢您对网协工作的理解！"
	defaultDingTalkRejectedTemplate   = "【{{.HEDR}}】您于{{.DATE}}的预约被驳回，驳回原因为：\n{{.REAS}}\n感谢您对网协工作的理解！"
	defaultDingTalkReferredTemplate   = "【{{.HEDR}}】您于{{.DATE}}的预约被转送至{{.TRRO}}诊室，请在预约时间前往{{.TRRO}}诊室。诊室地址为{{.TRLO}}。\n感谢您对网协工作的理解！"
	defaultDingTalkNoShowTemplate     = "【{{.HEDR}}】由于您未在值班时间前往诊所，您于{{.DATE}}的预约已关闭。未到诊所会导致您后两天无法预约。若您并未未到诊所，请联系{{.QQAC}}进行申诉。\n感谢您对网协工作的理解！"
	defaultDingTalkReminderTemplate   = "【{{.HEDR}}】您预约的位于{{.ROOM}}的值班即将开始，请在{{.DATE}}前往{{.ROOM}}诊室。诊所地址为{{.LOCA}}。"
)

// DingTalkNotificationConfig contains the configurable guest notification
// templates and their organization-specific values.
type DingTalkNotificationConfig struct {
	Title             string
	Header            string
	QQAccount         string
	TransferRoom      string
	TransferAddress   string
	ConfirmedTemplate string
	RejectedTemplate  string
	ReferredTemplate  string
	NoShowTemplate    string
	ReminderTemplate  string
}

// DingTalkConfig contains the message-center settings used for DingTalk
// notifications.
type DingTalkConfig struct {
	Enabled      bool
	BaseURL      string
	AppCode      string
	AppKey       string
	AppSecret    string
	Timeout      time.Duration
	Notification DingTalkNotificationConfig
}

// LoadDingTalkConfig reads and validates DingTalk message-center settings.
func LoadDingTalkConfig(getenv func(string) string) (DingTalkConfig, error) {
	enabled, err := parseBoolSetting(getenv("DINGTALK_NOTIFY_ENABLED"), false)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_NOTIFY_ENABLED: %w", err)
	}
	if !enabled {
		return DingTalkConfig{Enabled: false}, nil
	}

	timeout, err := parsePositiveDurationSetting(getenv("DINGTALK_TIMEOUT"), defaultDingTalkTimeout)
	if err != nil {
		return DingTalkConfig{}, fmt.Errorf("invalid DINGTALK_TIMEOUT: %w", err)
	}

	cfg := DingTalkConfig{
		Enabled:   true,
		BaseURL:   strings.TrimSpace(getenv("DINGTALK_BASE_URL")),
		AppCode:   strings.TrimSpace(getenv("DINGTALK_APP_CODE")),
		AppKey:    strings.TrimSpace(getenv("DINGTALK_APP_KEY")),
		AppSecret: strings.TrimSpace(getenv("DINGTALK_APP_SECRET")),
		Timeout:   timeout,
		Notification: DingTalkNotificationConfig{
			Title:             dingTalkStringSetting(getenv, "DINGTALK_NOTIFICATION_TITLE", defaultDingTalkNotificationTitle),
			Header:            dingTalkStringSetting(getenv, "DINGTALK_NOTIFICATION_HEADER", defaultDingTalkNotificationHeader),
			QQAccount:         strings.TrimSpace(getenv("DINGTALK_NOTIFICATION_QQ_ACCOUNT")),
			TransferRoom:      strings.TrimSpace(getenv("DINGTALK_NOTIFICATION_TRANSFER_ROOM")),
			TransferAddress:   strings.TrimSpace(getenv("DINGTALK_NOTIFICATION_TRANSFER_ADDRESS")),
			ConfirmedTemplate: dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_CONFIRMED", defaultDingTalkConfirmedTemplate),
			RejectedTemplate:  dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_REJECTED", defaultDingTalkRejectedTemplate),
			ReferredTemplate:  dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_REFERRED", defaultDingTalkReferredTemplate),
			NoShowTemplate:    dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_NO_SHOW", defaultDingTalkNoShowTemplate),
			ReminderTemplate:  dingTalkStringSetting(getenv, "DINGTALK_TEMPLATE_REMINDER", defaultDingTalkReminderTemplate),
		},
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

func dingTalkStringSetting(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}
