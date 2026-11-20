package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	configmodel "clinic-backend/internal/config/model"
	"clinic-backend/internal/database"
)

const (
	runtimeSettingSyncPollInterval         = "SYNC_POLL_INTERVAL"
	runtimeSettingStaffVersion             = "STAFF_VERSION"
	runtimeSettingRecordActionWindow       = "RECORD_ACTION_WINDOW"
	runtimeSettingRecordPhotoMaxCount      = "RECORD_PHOTO_MAX_COUNT"
	runtimeSettingServiceReminderEnabled   = "SERVICE_DATE_REMINDER_ENABLED"
	runtimeSettingServiceReminderLeadTimes = "SERVICE_DATE_REMINDER_LEAD_TIMES"
)

type runtimeGroupDefinition struct {
	Name        string
	DisplayName string
	SortOrder   int
}

type runtimeSettingDefinition struct {
	Name         string
	DisplayName  string
	ValueType    configmodel.ConfigValueType
	GroupName    string
	SortOrder    int
	DefaultValue string
}

var runtimeGroupDefinitions = []runtimeGroupDefinition{
	{Name: "system", DisplayName: "系统配置", SortOrder: 0},
	{Name: "record", DisplayName: "工单配置", SortOrder: 1},
	{Name: "reminder", DisplayName: "提醒与通知", SortOrder: 2},
}

var runtimeSettingDefinitions = []runtimeSettingDefinition{
	{
		Name:         runtimeSettingSyncPollInterval,
		DisplayName:  "客户端同步轮询间隔",
		ValueType:    configmodel.ConfigValueTypeString,
		GroupName:    "system",
		SortOrder:    0,
		DefaultValue: "10s",
	},
	{
		Name:         runtimeSettingStaffVersion,
		DisplayName:  "员工登录会话版本",
		ValueType:    configmodel.ConfigValueTypeInt,
		GroupName:    "system",
		SortOrder:    1,
		DefaultValue: "0",
	},
	{
		Name:         runtimeSettingRecordActionWindow,
		DisplayName:  "操作撤销时限",
		ValueType:    configmodel.ConfigValueTypeString,
		GroupName:    "record",
		SortOrder:    0,
		DefaultValue: "5m",
	},
	{
		Name:         runtimeSettingRecordPhotoMaxCount,
		DisplayName:  "单条工单照片上限",
		ValueType:    configmodel.ConfigValueTypeInt,
		GroupName:    "record",
		SortOrder:    1,
		DefaultValue: "9",
	},
	{
		Name:         runtimeSettingServiceReminderEnabled,
		DisplayName:  "启用服务日期提醒",
		ValueType:    configmodel.ConfigValueTypeBoolean,
		GroupName:    "reminder",
		SortOrder:    0,
		DefaultValue: "false",
	},
	{
		Name:         runtimeSettingServiceReminderLeadTimes,
		DisplayName:  "提前提醒时间列表",
		ValueType:    configmodel.ConfigValueTypeString,
		GroupName:    "reminder",
		SortOrder:    1,
		DefaultValue: "5h,3h",
	},
}

var runtimeSettingDefinitionByName = func() map[string]runtimeSettingDefinition {
	settings := make(map[string]runtimeSettingDefinition, len(runtimeSettingDefinitions))
	for _, definition := range runtimeSettingDefinitions {
		settings[definition.Name] = definition
	}
	return settings
}()

// RuntimeSettings contains validated settings loaded from the database.
type RuntimeSettings struct {
	SyncPollInterval             time.Duration
	StaffVersion                 int
	RecordActionWindow           time.Duration
	RecordPhotoMaxCount          int
	ServiceDateReminderEnabled   bool
	ServiceDateReminderLeadTimes []time.Duration
}

// EnsureRuntimeSettings inserts missing groups and settings. Existing rows are
// never overwritten; environment values are only used to initialize a new row.
func (s *ConfigService) EnsureRuntimeSettings(getenv func(string) string) error {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		groupIDs := make(map[string]uint, len(runtimeGroupDefinitions))
		for _, group := range runtimeGroupDefinitions {
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

		for _, setting := range runtimeSettingDefinitions {
			exists, err := configItemExists(tx, setting.Name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}

			rawValue := strings.TrimSpace(getenv(setting.Name))
			if rawValue == "" {
				rawValue = setting.DefaultValue
			}
			value, err := validateRuntimeSettingValue(setting.Name, rawValue)
			if err != nil {
				return fmt.Errorf("initialize %s: %w", setting.Name, err)
			}

			groupID := groupIDs[setting.GroupName]
			if _, err := tx.Exec(
				`INSERT INTO clinic_config_item
				 (name, value, value_type, display_name, is_required, group_id, sort_order)
				 VALUES ($1, $2, $3, $4, true, $5, $6)
				 ON CONFLICT (name) DO NOTHING`,
				setting.Name, value, setting.ValueType, setting.DisplayName, groupID, setting.SortOrder,
			); err != nil {
				return fmt.Errorf("seed config item %s: %w", setting.Name, err)
			}
		}
		return nil
	})
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
	values := make(map[string]string, len(runtimeSettingDefinitions))
	for _, setting := range runtimeSettingDefinitions {
		var value string
		var valueType configmodel.ConfigValueType
		var required bool
		err := s.db.QueryRow(
			"SELECT value, value_type, is_required FROM clinic_config_item WHERE name = $1",
			setting.Name,
		).Scan(&value, &valueType, &required)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return RuntimeSettings{}, fmt.Errorf("runtime config %s is missing", setting.Name)
			}
			return RuntimeSettings{}, fmt.Errorf("load runtime config %s: %w", setting.Name, err)
		}

		normalized, err := validateKnownRuntimeSetting(setting.Name, valueType, required, value)
		if err != nil {
			return RuntimeSettings{}, err
		}
		values[setting.Name] = normalized
	}

	settings := RuntimeSettings{}
	for name, value := range values {
		switch name {
		case runtimeSettingSyncPollInterval:
			settings.SyncPollInterval, _ = time.ParseDuration(value)
		case runtimeSettingStaffVersion:
			settings.StaffVersion, _ = strconv.Atoi(value)
		case runtimeSettingRecordActionWindow:
			settings.RecordActionWindow, _ = time.ParseDuration(value)
		case runtimeSettingRecordPhotoMaxCount:
			settings.RecordPhotoMaxCount, _ = strconv.Atoi(value)
		case runtimeSettingServiceReminderEnabled:
			settings.ServiceDateReminderEnabled = value == "true"
		case runtimeSettingServiceReminderLeadTimes:
			settings.ServiceDateReminderLeadTimes, _ = parseRuntimeLeadTimes(value)
		}
	}
	return settings, nil
}

func validateKnownRuntimeSetting(
	name string,
	valueType configmodel.ConfigValueType,
	required bool,
	value string,
) (string, error) {
	setting, ok := runtimeSettingDefinitionByName[name]
	if !ok {
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
	if !required {
		return "", fmt.Errorf("%w: %s must be required", ErrConfigInvalidInput, name)
	}
	return validateRuntimeSettingValue(name, value)
}

func validateRuntimeSettingValue(name, raw string) (string, error) {
	value := strings.TrimSpace(raw)
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
	default:
		return "", fmt.Errorf("%w: unsupported runtime setting %s", ErrConfigInvalidInput, name)
	}
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
