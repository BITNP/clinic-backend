package service

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode/utf8"

	configmodel "clinic-backend/internal/config/model"
	"clinic-backend/internal/database"
)

const (
	DefaultRuntimeSettingsFile              = "runtime_settings.json"
	runtimeSettingSyncPollInterval          = "SYNC_POLL_INTERVAL"
	runtimeSettingStaffVersion              = "STAFF_VERSION"
	runtimeSettingRecordActionWindow        = "RECORD_ACTION_WINDOW"
	runtimeSettingRecordPhotoMaxCount       = "RECORD_PHOTO_MAX_COUNT"
	runtimeSettingServiceReminderEnabled    = "SERVICE_DATE_REMINDER_ENABLED"
	runtimeSettingServiceReminderLeadTimes  = "SERVICE_DATE_REMINDER_LEAD_TIMES"
	runtimeSettingServiceReminderTemplate   = "SERVICE_DATE_REMINDER_TEMPLATE"
	runtimeSettingMessageGatewayURL         = "MESSAGE_GATEWAY_URL"
	runtimeSettingMessageGatewayAccount     = "MESSAGE_GATEWAY_ACCOUNT"
	runtimeSettingMessageGatewayToken       = "MESSAGE_GATEWAY_ACCOUNT_TOKEN"
	runtimeSettingMessageGatewayTimeout     = "MESSAGE_GATEWAY_TIMEOUT"
	runtimeSettingTaggerLLMAPIKey           = "TAGGER_LLM_API_KEY"
	runtimeSettingTaggerLLMBaseURL          = "TAGGER_LLM_BASE_URL"
	runtimeSettingTaggerLLMModel            = "TAGGER_LLM_MODEL"
	runtimeSettingTaggerPoolSize            = "TAGGER_AGENT_POOL_SIZE"
	runtimeSettingTaggerContextTokens       = "TAGGER_CONTEXT_TOKENS"
	runtimeSettingTaggerTimeout             = "TAGGER_TIMEOUT"
	runtimeSettingRecordPhotoMaxSizeBytes   = "RECORD_PHOTO_MAX_SIZE_BYTES"
	runtimeSettingDingTalkNotifyEnabled     = "DINGTALK_NOTIFY_ENABLED"
	runtimeSettingDingTalkBaseURL           = "DINGTALK_BASE_URL"
	runtimeSettingDingTalkAppCode           = "DINGTALK_APP_CODE"
	runtimeSettingDingTalkAppKey            = "DINGTALK_APP_KEY"
	runtimeSettingDingTalkAppSecret         = "DINGTALK_APP_SECRET"
	runtimeSettingDingTalkTimeout           = "DINGTALK_TIMEOUT"
	runtimeSettingDingTalkReminderLeadTime  = "DINGTALK_REMINDER_LEAD_TIME"
	runtimeSettingDingTalkReminderPoll      = "DINGTALK_REMINDER_POLL_INTERVAL"
	runtimeSettingDingTalkRedirectEnabled   = "DINGTALK_REDIRECT_ENABLED"
	runtimeSettingDingTalkRedirectReceiver  = "DINGTALK_REDIRECT_RECEIVER"
	runtimeSettingDingTalkTitle             = "DINGTALK_NOTIFICATION_TITLE"
	runtimeSettingDingTalkHeader            = "DINGTALK_NOTIFICATION_HEADER"
	runtimeSettingDingTalkQQAccount         = "DINGTALK_NOTIFICATION_QQ_ACCOUNT"
	runtimeSettingDingTalkConfirmedTemplate = "DINGTALK_TEMPLATE_CONFIRMED"
	runtimeSettingDingTalkRejectedTemplate  = "DINGTALK_TEMPLATE_REJECTED"
	runtimeSettingDingTalkReferredTemplate  = "DINGTALK_TEMPLATE_REFERRED"
	runtimeSettingDingTalkTransferTemplate  = "DINGTALK_TEMPLATE_TRANSFER"
	runtimeSettingDingTalkNoShowTemplate    = "DINGTALK_TEMPLATE_NO_SHOW"
	runtimeSettingDingTalkReminderTemplate  = "DINGTALK_TEMPLATE_REMINDER"
)

// DefaultServiceDateReminderTemplate is used for new installations and for
// legacy runtime settings files that predate the configurable template.
const DefaultServiceDateReminderTemplate = `[电脑诊所]{{.Date}} {{.StartTime}} {{.Room}} 的值班还有{{.PendingCount}}个待审核工单，请及时处理。`

const (
	DefaultDingTalkNotificationTitle  = "电脑诊所工单状态更新"
	DefaultDingTalkNotificationHeader = "网络开拓者协会"
	DefaultDingTalkConfirmedTemplate  = "【{{.HEDR}}】您的预约已通过，请于{{.DATE}}前往{{.ROOM}}诊室。诊室地址为{{.LOCA}}。若因故无法按时前往，请在电脑义诊平台删除预约。\n感谢您对网协工作的理解！"
	DefaultDingTalkRejectedTemplate   = "【{{.HEDR}}】您于{{.DATE}}的预约被驳回，驳回原因为：\n{{.REAS}}\n感谢您对网协工作的理解！"
	DefaultDingTalkReferredTemplate   = "【{{.HEDR}}】您于{{.DATE}}的预约建议联系售后进行维修，详细情况说明：\n{{.REAS}}\n感谢您对网协工作的理解！"
	DefaultDingTalkTransferTemplate   = "【{{.HEDR}}】您于{{.DATE}}的预约被转送至{{.TRRO}}，请在预约时间前往{{.TRRO}}。诊室地址为{{.TRLO}}。\n感谢您对网协工作的理解！"
	DefaultDingTalkNoShowTemplate     = "【{{.HEDR}}】由于您未在值班时间前往诊所，您于{{.DATE}}的预约已关闭。未到诊所会导致您后两天无法预约。若您并未未到诊所，请联系{{.QQAC}}进行申诉。\n感谢您对网协工作的理解！"
	DefaultDingTalkReminderTemplate   = "【{{.HEDR}}】您预约的位于{{.ROOM}}的值班即将开始，请在{{.DATE}}前往{{.ROOM}}诊室。诊所地址为{{.LOCA}}。"
	DefaultDingTalkTransferTitle      = "工单转诊"
)

//go:embed data/template.json
var runtimeSettingsTemplateJSON []byte

type runtimeGroupDefinition struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	SortOrder   int    `json:"sort_order"`
}

type runtimeSettingDefinition struct {
	Name        string                      `json:"name"`
	DisplayName string                      `json:"display_name"`
	Unit        string                      `json:"unit,omitempty"`
	Hint        string                      `json:"hint,omitempty"`
	ValueType   configmodel.ConfigValueType `json:"value_type"`
	GroupName   string                      `json:"group"`
	SortOrder   int                         `json:"sort_order"`
	IsRequired  bool                        `json:"is_required"`
	RoleLevel   configmodel.ConfigRoleLevel `json:"role_level"`
	WriteOnly   bool                        `json:"writeonly"`
	Default     *string                     `json:"default,omitempty"`
}

type runtimeSettingsTemplate struct {
	Groups []runtimeGroupDefinition   `json:"groups"`
	Items  []runtimeSettingDefinition `json:"items"`
}

var runtimeSettingExpectedTypes = map[string]configmodel.ConfigValueType{
	runtimeSettingSyncPollInterval:          configmodel.ConfigValueTypeString,
	runtimeSettingStaffVersion:              configmodel.ConfigValueTypeInt,
	runtimeSettingRecordActionWindow:        configmodel.ConfigValueTypeString,
	runtimeSettingRecordPhotoMaxCount:       configmodel.ConfigValueTypeInt,
	runtimeSettingServiceReminderEnabled:    configmodel.ConfigValueTypeBoolean,
	runtimeSettingServiceReminderLeadTimes:  configmodel.ConfigValueTypeString,
	runtimeSettingServiceReminderTemplate:   configmodel.ConfigValueTypeString,
	runtimeSettingMessageGatewayURL:         configmodel.ConfigValueTypeString,
	runtimeSettingMessageGatewayAccount:     configmodel.ConfigValueTypeString,
	runtimeSettingMessageGatewayToken:       configmodel.ConfigValueTypeString,
	runtimeSettingMessageGatewayTimeout:     configmodel.ConfigValueTypeString,
	runtimeSettingTaggerLLMAPIKey:           configmodel.ConfigValueTypeString,
	runtimeSettingTaggerLLMBaseURL:          configmodel.ConfigValueTypeString,
	runtimeSettingTaggerLLMModel:            configmodel.ConfigValueTypeString,
	runtimeSettingTaggerPoolSize:            configmodel.ConfigValueTypeInt,
	runtimeSettingTaggerContextTokens:       configmodel.ConfigValueTypeInt,
	runtimeSettingTaggerTimeout:             configmodel.ConfigValueTypeString,
	runtimeSettingRecordPhotoMaxSizeBytes:   configmodel.ConfigValueTypeInt,
	runtimeSettingDingTalkNotifyEnabled:     configmodel.ConfigValueTypeBoolean,
	runtimeSettingDingTalkBaseURL:           configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkAppCode:           configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkAppKey:            configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkAppSecret:         configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkTimeout:           configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkReminderLeadTime:  configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkReminderPoll:      configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkRedirectEnabled:   configmodel.ConfigValueTypeBoolean,
	runtimeSettingDingTalkRedirectReceiver:  configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkTitle:             configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkHeader:            configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkQQAccount:         configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkConfirmedTemplate: configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkRejectedTemplate:  configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkReferredTemplate:  configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkTransferTemplate:  configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkNoShowTemplate:    configmodel.ConfigValueTypeString,
	runtimeSettingDingTalkReminderTemplate:  configmodel.ConfigValueTypeString,
}

var superAdminOnlyRuntimeSettings = map[string]struct{}{
	runtimeSettingTaggerLLMAPIKey:          {},
	runtimeSettingTaggerLLMBaseURL:         {},
	runtimeSettingTaggerLLMModel:           {},
	runtimeSettingTaggerPoolSize:           {},
	runtimeSettingTaggerContextTokens:      {},
	runtimeSettingTaggerTimeout:            {},
	runtimeSettingMessageGatewayURL:        {},
	runtimeSettingMessageGatewayAccount:    {},
	runtimeSettingMessageGatewayToken:      {},
	runtimeSettingMessageGatewayTimeout:    {},
	runtimeSettingDingTalkNotifyEnabled:    {},
	runtimeSettingDingTalkBaseURL:          {},
	runtimeSettingDingTalkAppCode:          {},
	runtimeSettingDingTalkAppKey:           {},
	runtimeSettingDingTalkAppSecret:        {},
	runtimeSettingDingTalkTimeout:          {},
	runtimeSettingDingTalkReminderPoll:     {},
	runtimeSettingDingTalkRedirectEnabled:  {},
	runtimeSettingDingTalkRedirectReceiver: {},
}

var (
	runtimeTemplateOnce sync.Once
	runtimeTemplate     runtimeSettingsTemplate
	runtimeTemplateErr  error
)

func loadRuntimeTemplate() (runtimeSettingsTemplate, error) {
	runtimeTemplateOnce.Do(func() {
		if err := json.Unmarshal(runtimeSettingsTemplateJSON, &runtimeTemplate); err != nil {
			runtimeTemplateErr = fmt.Errorf("decode runtime settings template: %w", err)
			return
		}
		if err := validateRuntimeTemplate(runtimeTemplate); err != nil {
			runtimeTemplateErr = err
		}
	})
	return runtimeTemplate, runtimeTemplateErr
}

func validateRuntimeTemplate(template runtimeSettingsTemplate) error {
	if len(template.Groups) == 0 {
		return errors.New("runtime settings template has no groups")
	}

	groupNames := make(map[string]struct{}, len(template.Groups))
	for _, group := range template.Groups {
		if strings.TrimSpace(group.Name) == "" || strings.TrimSpace(group.DisplayName) == "" {
			return errors.New("runtime settings template contains an incomplete group")
		}
		if group.SortOrder < 0 {
			return fmt.Errorf("runtime settings template group %s has a negative sort_order", group.Name)
		}
		if _, exists := groupNames[group.Name]; exists {
			return fmt.Errorf("runtime settings template contains duplicate group %s", group.Name)
		}
		groupNames[group.Name] = struct{}{}
	}

	if len(template.Items) != len(runtimeSettingExpectedTypes) {
		return fmt.Errorf(
			"runtime settings template contains %d items; want %d",
			len(template.Items),
			len(runtimeSettingExpectedTypes),
		)
	}

	itemNames := make(map[string]struct{}, len(template.Items))
	for _, item := range template.Items {
		expectedType, known := runtimeSettingExpectedTypes[item.Name]
		if !known {
			return fmt.Errorf("runtime settings template contains unknown item %s", item.Name)
		}
		if _, exists := itemNames[item.Name]; exists {
			return fmt.Errorf("runtime settings template contains duplicate item %s", item.Name)
		}
		itemNames[item.Name] = struct{}{}

		if strings.TrimSpace(item.DisplayName) == "" {
			return fmt.Errorf("runtime settings template item %s has no display_name", item.Name)
		}
		if item.ValueType != expectedType {
			return fmt.Errorf(
				"runtime settings template item %s has value_type %s; want %s",
				item.Name,
				item.ValueType,
				expectedType,
			)
		}
		if item.IsRequired == runtimeSettingIsOptional(item.Name) {
			return fmt.Errorf(
				"runtime settings template item %s has is_required=%t; want %t",
				item.Name,
				item.IsRequired,
				!runtimeSettingIsOptional(item.Name),
			)
		}
		if item.WriteOnly != runtimeSettingIsWriteOnly(item.Name) {
			return fmt.Errorf(
				"runtime settings template item %s has writeonly=%t; want %t",
				item.Name,
				item.WriteOnly,
				runtimeSettingIsWriteOnly(item.Name),
			)
		}
		if item.RoleLevel != runtimeSettingRoleLevel(item.Name) {
			return fmt.Errorf(
				"runtime settings template item %s has role_level=%s; want %s",
				item.Name,
				item.RoleLevel,
				runtimeSettingRoleLevel(item.Name),
			)
		}
		if item.SortOrder < 0 {
			return fmt.Errorf("runtime settings template item %s has a negative sort_order", item.Name)
		}
		if _, exists := groupNames[item.GroupName]; !exists {
			return fmt.Errorf("runtime settings template item %s references unknown group %s", item.Name, item.GroupName)
		}
		if item.Default != nil {
			if _, err := validateRuntimeSettingValue(item.Name, *item.Default); err != nil {
				return fmt.Errorf("runtime settings template item %s has invalid default: %w", item.Name, err)
			}
		}
	}

	for name := range runtimeSettingExpectedTypes {
		if _, exists := itemNames[name]; !exists {
			return fmt.Errorf("runtime settings template is missing item %s", name)
		}
	}
	return nil
}

// RuntimeSettings contains validated settings loaded from the database.
type RuntimeSettings struct {
	SyncPollInterval             time.Duration
	StaffVersion                 int
	RecordActionWindow           time.Duration
	RecordPhotoMaxCount          int
	RecordPhotoMaxSizeBytes      int64
	ServiceDateReminderEnabled   bool
	ServiceDateReminderLeadTimes []time.Duration
	ServiceDateReminderTemplate  string
	MessageGatewayURL            string
	MessageGatewayAccount        string
	MessageGatewayAccountToken   string
	MessageGatewayTimeout        time.Duration
	TaggerLLMAPIKey              string
	TaggerLLMBaseURL             string
	TaggerLLMModel               string
	TaggerAgentPoolSize          int
	TaggerContextTokens          int
	TaggerTimeout                time.Duration
	DingTalk                     DingTalkRuntimeSettings
}

// DingTalkRuntimeSettings contains validated DingTalk message-center settings.
type DingTalkRuntimeSettings struct {
	Enabled              bool
	BaseURL              string
	AppCode              string
	AppKey               string
	AppSecret            string
	Timeout              time.Duration
	ReminderLeadTime     time.Duration
	ReminderPollInterval time.Duration
	RedirectEnabled      bool
	RedirectReceiver     string
	NotificationTitle    string
	NotificationHeader   string
	QQAccount            string
	ConfirmedTemplate    string
	RejectedTemplate     string
	ReferredTemplate     string
	TransferTemplate     string
	NoShowTemplate       string
	ReminderTemplate     string
}

// EnsureRuntimeSettings inserts missing groups and settings using the embedded
// template and external runtime_settings.json values. Existing values are
// preserved while reserved metadata is synchronized from the template.
func (s *ConfigService) EnsureRuntimeSettings() error {
	return s.ensureRuntimeSettings(nil)
}

// EnsureRuntimeSettingsWithInitialValues seeds newly created items from
// initialValues when present. It supports one-time migration from legacy
// environment settings; existing rows always remain authoritative.
func (s *ConfigService) EnsureRuntimeSettingsWithInitialValues(initialValues map[string]string) error {
	return s.ensureRuntimeSettings(initialValues)
}

func (s *ConfigService) ensureRuntimeSettings(initialValues map[string]string) error {
	template, err := loadRuntimeTemplate()
	if err != nil {
		return err
	}
	values, err := loadRuntimeSettingsValues(s.runtimeFile, template)
	if err != nil {
		return err
	}

	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		groupIDs := make(map[string]uint, len(template.Groups))
		for _, group := range template.Groups {
			if _, err := tx.Exec(
				`INSERT INTO clinic_config_group (name, display_name, sort_order)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (name) DO NOTHING`,
				group.Name, group.DisplayName, group.SortOrder,
			); err != nil {
				return fmt.Errorf("seed config group %s: %w", group.Name, err)
			}

			var groupID uint
			if err := tx.QueryRow("SELECT id FROM clinic_config_group WHERE name = $1", group.Name).Scan(&groupID); err != nil {
				return fmt.Errorf("get seeded config group %s: %w", group.Name, err)
			}
			groupIDs[group.Name] = groupID
		}

		for _, setting := range template.Items {
			exists, err := configItemExists(tx, setting.Name)
			if err != nil {
				return err
			}
			value := values[setting.Name]
			if !exists {
				if raw, ok := initialValues[setting.Name]; ok && strings.TrimSpace(raw) != "" {
					value, err = validateRuntimeSettingValue(setting.Name, raw)
					if err != nil {
						return fmt.Errorf("initialize %s from legacy environment: %w", setting.Name, err)
					}
				}
			}
			groupID := groupIDs[setting.GroupName]
			if _, err := tx.Exec(
				`INSERT INTO clinic_config_item
					 (name, value, value_type, display_name, unit, hint, is_required,
					  role_level, writeonly, group_id, sort_order)
					 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
					 ON CONFLICT (name) DO UPDATE SET
					     display_name = EXCLUDED.display_name,
					     unit = EXCLUDED.unit,
					     hint = EXCLUDED.hint,
					     value_type = EXCLUDED.value_type,
					     is_required = EXCLUDED.is_required,
					     role_level = EXCLUDED.role_level,
					     writeonly = EXCLUDED.writeonly,
					     group_id = EXCLUDED.group_id,
					     sort_order = EXCLUDED.sort_order`,
				setting.Name, value, setting.ValueType, setting.DisplayName,
				setting.Unit, setting.Hint, setting.IsRequired, setting.RoleLevel,
				setting.WriteOnly, groupID, setting.SortOrder,
			); err != nil {
				return fmt.Errorf("seed config item %s: %w", setting.Name, err)
			}
		}
		return nil
	})
}

func loadRuntimeSettingsValues(path string, template runtimeSettingsTemplate) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime settings file %s: %w", path, err)
	}

	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decode runtime settings file %s: %w", path, err)
	}
	if values == nil {
		return nil, fmt.Errorf("runtime settings file %s must contain a JSON object", path)
	}

	expected := make(map[string]struct{}, len(template.Items))
	for _, item := range template.Items {
		expected[item.Name] = struct{}{}
		raw, ok := values[item.Name]
		if !ok {
			if item.Default == nil {
				return nil, fmt.Errorf("runtime settings file %s is missing item %s", path, item.Name)
			}
			raw = *item.Default
		}
		normalized, err := validateRuntimeSettingValue(item.Name, raw)
		if err != nil {
			return nil, fmt.Errorf("runtime settings file %s item %s: %w", path, item.Name, err)
		}
		values[item.Name] = normalized
	}
	for name := range values {
		if _, ok := expected[name]; !ok {
			return nil, fmt.Errorf("runtime settings file %s contains unknown item %s", path, name)
		}
	}
	return values, nil
}

func configItemExists(tx *sql.Tx, name string) (bool, error) {
	var existingID uint
	err := tx.QueryRow("SELECT id FROM clinic_config_item WHERE name = $1", name).Scan(&existingID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check config item %s: %w", name, err)
}

// LoadRuntimeSettings reads and validates the database-managed runtime settings.
func (s *ConfigService) LoadRuntimeSettings() (RuntimeSettings, error) {
	return loadRuntimeSettings(s.db)
}

func loadRuntimeSettings(q interface{ QueryRow(string, ...any) *sql.Row }) (RuntimeSettings, error) {
	template, err := loadRuntimeTemplate()
	if err != nil {
		return RuntimeSettings{}, err
	}

	settings := RuntimeSettings{}
	for _, setting := range template.Items {
		var value string
		var valueType configmodel.ConfigValueType
		var required bool
		var roleLevel configmodel.ConfigRoleLevel
		var writeOnly bool
		err = q.QueryRow(
			"SELECT value, value_type, is_required, role_level, writeonly FROM clinic_config_item WHERE name = $1",
			setting.Name,
		).Scan(&value, &valueType, &required, &roleLevel, &writeOnly)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return RuntimeSettings{}, fmt.Errorf("runtime config %s is missing", setting.Name)
			}
			return RuntimeSettings{}, fmt.Errorf("load runtime config %s: %w", setting.Name, err)
		}

		normalized, err := validateKnownRuntimeSetting(
			setting.Name,
			valueType,
			required,
			roleLevel,
			writeOnly,
			value,
		)
		if err != nil {
			return RuntimeSettings{}, err
		}

		switch setting.Name {
		case runtimeSettingSyncPollInterval:
			settings.SyncPollInterval, err = time.ParseDuration(normalized)
		case runtimeSettingStaffVersion:
			settings.StaffVersion, err = strconv.Atoi(normalized)
		case runtimeSettingRecordActionWindow:
			settings.RecordActionWindow, err = time.ParseDuration(normalized)
		case runtimeSettingRecordPhotoMaxCount:
			settings.RecordPhotoMaxCount, err = strconv.Atoi(normalized)
		case runtimeSettingRecordPhotoMaxSizeBytes:
			settings.RecordPhotoMaxSizeBytes, err = strconv.ParseInt(normalized, 10, 64)
		case runtimeSettingServiceReminderEnabled:
			settings.ServiceDateReminderEnabled = normalized == "true"
		case runtimeSettingServiceReminderLeadTimes:
			settings.ServiceDateReminderLeadTimes, err = parseRuntimeLeadTimes(normalized)
		case runtimeSettingServiceReminderTemplate:
			settings.ServiceDateReminderTemplate = normalized
		case runtimeSettingMessageGatewayURL:
			settings.MessageGatewayURL = normalized
		case runtimeSettingMessageGatewayAccount:
			settings.MessageGatewayAccount = normalized
		case runtimeSettingMessageGatewayToken:
			settings.MessageGatewayAccountToken = normalized
		case runtimeSettingMessageGatewayTimeout:
			settings.MessageGatewayTimeout, err = time.ParseDuration(normalized)
		case runtimeSettingTaggerLLMAPIKey:
			settings.TaggerLLMAPIKey = normalized
		case runtimeSettingTaggerLLMBaseURL:
			settings.TaggerLLMBaseURL = normalized
		case runtimeSettingTaggerLLMModel:
			settings.TaggerLLMModel = normalized
		case runtimeSettingTaggerPoolSize:
			settings.TaggerAgentPoolSize, err = strconv.Atoi(normalized)
		case runtimeSettingTaggerContextTokens:
			settings.TaggerContextTokens, err = strconv.Atoi(normalized)
		case runtimeSettingTaggerTimeout:
			settings.TaggerTimeout, err = time.ParseDuration(normalized)
		case runtimeSettingDingTalkNotifyEnabled:
			settings.DingTalk.Enabled = normalized == "true"
		case runtimeSettingDingTalkBaseURL:
			settings.DingTalk.BaseURL = normalized
		case runtimeSettingDingTalkAppCode:
			settings.DingTalk.AppCode = normalized
		case runtimeSettingDingTalkAppKey:
			settings.DingTalk.AppKey = normalized
		case runtimeSettingDingTalkAppSecret:
			settings.DingTalk.AppSecret = normalized
		case runtimeSettingDingTalkTimeout:
			settings.DingTalk.Timeout, err = time.ParseDuration(normalized)
		case runtimeSettingDingTalkReminderLeadTime:
			settings.DingTalk.ReminderLeadTime, err = time.ParseDuration(normalized)
		case runtimeSettingDingTalkReminderPoll:
			settings.DingTalk.ReminderPollInterval, err = time.ParseDuration(normalized)
		case runtimeSettingDingTalkRedirectEnabled:
			settings.DingTalk.RedirectEnabled = normalized == "true"
		case runtimeSettingDingTalkRedirectReceiver:
			settings.DingTalk.RedirectReceiver = normalized
		case runtimeSettingDingTalkTitle:
			settings.DingTalk.NotificationTitle = normalized
		case runtimeSettingDingTalkHeader:
			settings.DingTalk.NotificationHeader = normalized
		case runtimeSettingDingTalkQQAccount:
			settings.DingTalk.QQAccount = normalized
		case runtimeSettingDingTalkConfirmedTemplate:
			settings.DingTalk.ConfirmedTemplate = normalized
		case runtimeSettingDingTalkRejectedTemplate:
			settings.DingTalk.RejectedTemplate = normalized
		case runtimeSettingDingTalkReferredTemplate:
			settings.DingTalk.ReferredTemplate = normalized
		case runtimeSettingDingTalkTransferTemplate:
			settings.DingTalk.TransferTemplate = normalized
		case runtimeSettingDingTalkNoShowTemplate:
			settings.DingTalk.NoShowTemplate = normalized
		case runtimeSettingDingTalkReminderTemplate:
			settings.DingTalk.ReminderTemplate = normalized
		}
		if err != nil {
			return RuntimeSettings{}, fmt.Errorf("parse runtime config %s: %w", setting.Name, err)
		}
	}
	return settings, nil
}

func validateKnownRuntimeSetting(
	name string,
	valueType configmodel.ConfigValueType,
	required bool,
	roleLevel configmodel.ConfigRoleLevel,
	writeOnly bool,
	value string,
) (string, error) {
	template, err := loadRuntimeTemplate()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrConfigInvalidInput, err)
	}

	var setting runtimeSettingDefinition
	found := false
	for _, candidate := range template.Items {
		if candidate.Name == name {
			setting = candidate
			found = true
			break
		}
	}
	if !found {
		return value, nil
	}
	if valueType != setting.ValueType {
		return "", fmt.Errorf(
			"%w: %s must use value_type %s",
			ErrConfigValueTypeInvalid,
			name,
			setting.ValueType,
		)
	}
	if required != !runtimeSettingIsOptional(name) {
		return "", fmt.Errorf(
			"%w: %s must have is_required=%t",
			ErrConfigInvalidInput,
			name,
			!runtimeSettingIsOptional(name),
		)
	}
	if roleLevel != runtimeSettingRoleLevel(name) {
		return "", fmt.Errorf(
			"%w: %s must have role_level=%s",
			ErrConfigInvalidInput,
			name,
			runtimeSettingRoleLevel(name),
		)
	}
	if writeOnly != runtimeSettingIsWriteOnly(name) {
		return "", fmt.Errorf(
			"%w: %s must have writeonly=%t",
			ErrConfigInvalidInput,
			name,
			runtimeSettingIsWriteOnly(name),
		)
	}
	return validateRuntimeSettingValue(name, value)
}

func runtimeSettingRoleLevel(name string) configmodel.ConfigRoleLevel {
	if _, ok := superAdminOnlyRuntimeSettings[name]; ok {
		return configmodel.ConfigRoleLevelSuperAdmin
	}
	return configmodel.ConfigRoleLevelAdmin
}

func validateRuntimeSettingValue(name, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if !runtimeSettingIsOptional(name) && value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrConfigValueInvalid, name)
	}
	switch name {
	case runtimeSettingSyncPollInterval:
		duration, err := parseRuntimeDuration(name, value)
		if err != nil {
			return "", err
		}
		if duration < 0 {
			return "", fmt.Errorf("%w: %s must not be negative", ErrConfigValueInvalid, name)
		}
		return formatRuntimeDuration(duration), nil
	case runtimeSettingStaffVersion:
		version, err := strconv.Atoi(value)
		if err != nil || version < 0 {
			return "", fmt.Errorf("%w: %s must be a non-negative integer", ErrConfigValueInvalid, name)
		}
		return strconv.Itoa(version), nil
	case runtimeSettingRecordActionWindow:
		duration, err := parseRuntimeDuration(name, value)
		if err != nil {
			return "", err
		}
		if duration <= 0 {
			return "", fmt.Errorf("%w: %s must be positive", ErrConfigValueInvalid, name)
		}
		return formatRuntimeDuration(duration), nil
	case runtimeSettingRecordPhotoMaxCount:
		count, err := strconv.Atoi(value)
		if err != nil || count <= 0 {
			return "", fmt.Errorf("%w: %s must be a positive integer", ErrConfigValueInvalid, name)
		}
		return strconv.Itoa(count), nil
	case runtimeSettingRecordPhotoMaxSizeBytes:
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil || size <= 0 {
			return "", fmt.Errorf("%w: %s must be a positive integer", ErrConfigValueInvalid, name)
		}
		return strconv.FormatInt(size, 10), nil
	case runtimeSettingServiceReminderEnabled:
		if value != "true" && value != "false" {
			return "", fmt.Errorf("%w: %s must be true or false", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingServiceReminderLeadTimes:
		leadTimes, err := parseRuntimeLeadTimes(value)
		if err != nil {
			return "", err
		}
		parts := make([]string, len(leadTimes))
		for i, leadTime := range leadTimes {
			parts[i] = formatRuntimeDuration(leadTime)
		}
		return strings.Join(parts, ","), nil
	case runtimeSettingServiceReminderTemplate:
		if err := validateServiceDateReminderTemplate(value); err != nil {
			return "", err
		}
		return value, nil
	case runtimeSettingMessageGatewayURL:
		if value == "" {
			return "", nil
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", fmt.Errorf("%w: %s must be an HTTP(S) URL", ErrConfigValueInvalid, name)
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("%w: %s must not contain a query or fragment", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingMessageGatewayToken:
		return value, nil
	case runtimeSettingMessageGatewayTimeout:
		duration, err := parseRuntimeDuration(name, value)
		if err != nil {
			return "", err
		}
		if duration <= 0 {
			return "", fmt.Errorf("%w: %s must be positive", ErrConfigValueInvalid, name)
		}
		return formatRuntimeDuration(duration), nil
	case runtimeSettingTaggerLLMAPIKey:
		return value, nil
	case runtimeSettingTaggerLLMBaseURL:
		if value == "" {
			return "", nil
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", fmt.Errorf("%w: %s must be an HTTP(S) URL", ErrConfigValueInvalid, name)
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("%w: %s must not contain a query or fragment", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingTaggerLLMModel:
		return value, nil
	case runtimeSettingTaggerPoolSize:
		size, err := strconv.Atoi(value)
		if err != nil || size <= 0 {
			return "", fmt.Errorf("%w: %s must be a positive integer", ErrConfigValueInvalid, name)
		}
		return strconv.Itoa(size), nil
	case runtimeSettingTaggerContextTokens:
		tokens, err := strconv.Atoi(value)
		if err != nil || tokens <= 0 {
			return "", fmt.Errorf("%w: %s must be a positive integer", ErrConfigValueInvalid, name)
		}
		return strconv.Itoa(tokens), nil
	case runtimeSettingTaggerTimeout:
		duration, err := parseRuntimeDuration(name, value)
		if err != nil {
			return "", err
		}
		if duration <= 0 {
			return "", fmt.Errorf("%w: %s must be positive", ErrConfigValueInvalid, name)
		}
		return formatRuntimeDuration(duration), nil
	case runtimeSettingDingTalkNotifyEnabled, runtimeSettingDingTalkRedirectEnabled:
		if value != "true" && value != "false" {
			return "", fmt.Errorf("%w: %s must be true or false", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingDingTalkBaseURL:
		if value == "" {
			return "", nil
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", fmt.Errorf("%w: %s must be an HTTP(S) URL", ErrConfigValueInvalid, name)
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("%w: %s must not contain a query or fragment", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingDingTalkAppCode, runtimeSettingDingTalkAppKey, runtimeSettingDingTalkAppSecret:
		return value, nil
	case runtimeSettingDingTalkTimeout, runtimeSettingDingTalkReminderLeadTime, runtimeSettingDingTalkReminderPoll:
		duration, err := parseRuntimeDuration(name, value)
		if err != nil {
			return "", err
		}
		if duration <= 0 {
			return "", fmt.Errorf("%w: %s must be positive", ErrConfigValueInvalid, name)
		}
		return formatRuntimeDuration(duration), nil
	case runtimeSettingDingTalkRedirectReceiver:
		if value == "" {
			return "", nil
		}
		if strings.ContainsAny(value, ",，") {
			return "", fmt.Errorf("%w: %s must contain exactly one receiver", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingDingTalkTitle:
		if utf8.RuneCountInString(value) > 128 {
			return "", fmt.Errorf("%w: %s must not exceed 128 characters", ErrConfigValueInvalid, name)
		}
		return value, nil
	case runtimeSettingDingTalkHeader,
		runtimeSettingDingTalkQQAccount:
		return value, nil
	case runtimeSettingDingTalkConfirmedTemplate,
		runtimeSettingDingTalkRejectedTemplate,
		runtimeSettingDingTalkReferredTemplate,
		runtimeSettingDingTalkTransferTemplate,
		runtimeSettingDingTalkNoShowTemplate,
		runtimeSettingDingTalkReminderTemplate:
		if err := validateDingTalkTemplate(name, value); err != nil {
			return "", err
		}
		return value, nil
	case runtimeSettingMessageGatewayAccount:
		if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/?#") {
			return "", fmt.Errorf("%w: %s must be a valid gateway account", ErrConfigValueInvalid, name)
		}
		return value, nil
	default:
		return "", fmt.Errorf("%w: unsupported runtime setting %s", ErrConfigInvalidInput, name)
	}
}

func runtimeSettingIsOptional(name string) bool {
	switch name {
	case runtimeSettingMessageGatewayURL,
		runtimeSettingMessageGatewayToken,
		runtimeSettingTaggerLLMAPIKey,
		runtimeSettingTaggerLLMBaseURL,
		runtimeSettingTaggerLLMModel,
		runtimeSettingDingTalkBaseURL,
		runtimeSettingDingTalkAppCode,
		runtimeSettingDingTalkAppKey,
		runtimeSettingDingTalkAppSecret,
		runtimeSettingDingTalkRedirectReceiver,
		runtimeSettingDingTalkQQAccount:
		return true
	default:
		return false
	}
}

func runtimeSettingIsWriteOnly(name string) bool {
	return name == runtimeSettingMessageGatewayToken ||
		name == runtimeSettingTaggerLLMAPIKey ||
		name == runtimeSettingDingTalkAppSecret
}

func validateDingTalkTemplate(name, value string) error {
	parsed, err := template.New(name).Option("missingkey=error").Parse(
		strings.TrimSpace(strings.ReplaceAll(value, `\n`, "\n")),
	)
	if err != nil {
		return fmt.Errorf("%w: %s must be a valid template", ErrConfigValueInvalid, name)
	}
	var rendered strings.Builder
	if err := parsed.Execute(&rendered, map[string]string{
		"HEDR": "header",
		"DATE": "date",
		"ID":   "#1",
		"ROOM": "room",
		"TRRO": "transfer room",
		"LOCA": "room address",
		"TRLO": "transfer address",
		"REAS": "reason",
		"QQAC": "QQ",
	}); err != nil {
		return fmt.Errorf("%w: %s must be a valid template", ErrConfigValueInvalid, name)
	}
	if strings.TrimSpace(rendered.String()) == "" {
		return fmt.Errorf("%w: %s must not render an empty message", ErrConfigValueInvalid, name)
	}
	return nil
}

func parseRuntimeDuration(name, raw string) (time.Duration, error) {
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a duration", ErrConfigValueInvalid, name)
	}
	return duration, nil
}

func formatRuntimeDuration(duration time.Duration) string {
	if duration == 0 {
		return "0s"
	}

	sign := ""
	if duration < 0 {
		sign = "-"
		duration = -duration
	}

	units := []struct {
		duration time.Duration
		suffix   string
	}{
		{time.Hour, "h"},
		{time.Minute, "m"},
		{time.Second, "s"},
		{time.Millisecond, "ms"},
		{time.Microsecond, "us"},
		{time.Nanosecond, "ns"},
	}

	var builder strings.Builder
	builder.WriteString(sign)
	for _, unit := range units {
		if duration < unit.duration {
			continue
		}
		value := duration / unit.duration
		builder.WriteString(strconv.FormatInt(int64(value), 10))
		builder.WriteString(unit.suffix)
		duration %= unit.duration
	}
	return builder.String()
}

func parseRuntimeLeadTimes(raw string) ([]time.Duration, error) {
	if raw == "" {
		return nil, fmt.Errorf(
			"%w: %s requires at least one lead time",
			ErrConfigValueInvalid,
			runtimeSettingServiceReminderLeadTimes,
		)
	}

	parts := strings.Split(raw, ",")
	leadTimes := make([]time.Duration, 0, len(parts))
	seen := make(map[time.Duration]struct{}, len(parts))
	for _, part := range parts {
		leadTime, err := time.ParseDuration(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %s contains an invalid duration",
				ErrConfigValueInvalid,
				runtimeSettingServiceReminderLeadTimes,
			)
		}
		if leadTime <= 0 || leadTime%time.Second != 0 {
			return nil, fmt.Errorf(
				"%w: %s values must be positive and use whole seconds",
				ErrConfigValueInvalid,
				runtimeSettingServiceReminderLeadTimes,
			)
		}
		if _, ok := seen[leadTime]; ok {
			return nil, fmt.Errorf(
				"%w: %s contains duplicate values",
				ErrConfigValueInvalid,
				runtimeSettingServiceReminderLeadTimes,
			)
		}
		seen[leadTime] = struct{}{}
		leadTimes = append(leadTimes, leadTime)
	}

	sort.Slice(leadTimes, func(i, j int) bool {
		return leadTimes[i] > leadTimes[j]
	})
	return leadTimes, nil
}

func validateServiceDateReminderTemplate(raw string) error {
	tmpl, err := template.New("service_date_reminder").
		Option("missingkey=error").
		Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s must be a valid Go template", ErrConfigValueInvalid, runtimeSettingServiceReminderTemplate)
	}

	var rendered strings.Builder
	if err := tmpl.Execute(&rendered, map[string]string{
		"Date":         "2026-11-12",
		"StartTime":    "18:30",
		"Room":         "北区医务室",
		"PendingCount": "3",
		"LeadTime":     "5h0m",
	}); err != nil {
		return fmt.Errorf(
			"%w: %s references an unsupported value",
			ErrConfigValueInvalid,
			runtimeSettingServiceReminderTemplate,
		)
	}
	if strings.TrimSpace(rendered.String()) == "" {
		return fmt.Errorf("%w: %s must render non-empty text", ErrConfigValueInvalid, runtimeSettingServiceReminderTemplate)
	}
	return nil
}
