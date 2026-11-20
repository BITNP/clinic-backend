package service

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	runtimeSettingTaggerLLMAPIKey          = "TAGGER_LLM_API_KEY"
)

//go:embed data/runtime_settings.json
var runtimeSettingsJSON []byte

type runtimeGroupDefinition struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	SortOrder   int    `json:"sort_order"`
}

type runtimeSettingDefinition struct {
	Name         string                      `json:"name"`
	DisplayName  string                      `json:"display_name"`
	ValueType    configmodel.ConfigValueType `json:"value_type"`
	GroupName    string                      `json:"group"`
	SortOrder    int                         `json:"sort_order"`
	IsRequired   bool                        `json:"is_required"`
	WriteOnly    bool                        `json:"writeonly"`
	DefaultValue string                      `json:"default_value"`
}

type runtimeSettingsSeed struct {
	Groups []runtimeGroupDefinition   `json:"groups"`
	Items  []runtimeSettingDefinition `json:"items"`
}

var runtimeSettingExpectedTypes = map[string]configmodel.ConfigValueType{
	runtimeSettingSyncPollInterval:         configmodel.ConfigValueTypeString,
	runtimeSettingStaffVersion:             configmodel.ConfigValueTypeInt,
	runtimeSettingRecordActionWindow:       configmodel.ConfigValueTypeString,
	runtimeSettingRecordPhotoMaxCount:      configmodel.ConfigValueTypeInt,
	runtimeSettingServiceReminderEnabled:   configmodel.ConfigValueTypeBoolean,
	runtimeSettingServiceReminderLeadTimes: configmodel.ConfigValueTypeString,
	runtimeSettingTaggerLLMAPIKey:          configmodel.ConfigValueTypeString,
}

var (
	runtimeSeedOnce sync.Once
	runtimeSeed     runtimeSettingsSeed
	runtimeSeedErr  error
)

func loadRuntimeSeed() (runtimeSettingsSeed, error) {
	runtimeSeedOnce.Do(func() {
		if err := json.Unmarshal(runtimeSettingsJSON, &runtimeSeed); err != nil {
			runtimeSeedErr = fmt.Errorf("decode runtime settings seed: %w", err)
			return
		}
		if err := validateRuntimeSeed(runtimeSeed); err != nil {
			runtimeSeedErr = err
		}
	})
	return runtimeSeed, runtimeSeedErr
}

func validateRuntimeSeed(seed runtimeSettingsSeed) error {
	if len(seed.Groups) == 0 {
		return errors.New("runtime settings seed has no groups")
	}

	groupNames := make(map[string]struct{}, len(seed.Groups))
	for _, group := range seed.Groups {
		if strings.TrimSpace(group.Name) == "" || strings.TrimSpace(group.DisplayName) == "" {
			return errors.New("runtime settings seed contains an incomplete group")
		}
		if group.SortOrder < 0 {
			return fmt.Errorf("runtime settings seed group %s has a negative sort_order", group.Name)
		}
		if _, exists := groupNames[group.Name]; exists {
			return fmt.Errorf("runtime settings seed contains duplicate group %s", group.Name)
		}
		groupNames[group.Name] = struct{}{}
	}

	if len(seed.Items) != len(runtimeSettingExpectedTypes) {
		return fmt.Errorf(
			"runtime settings seed contains %d items; want %d",
			len(seed.Items),
			len(runtimeSettingExpectedTypes),
		)
	}

	itemNames := make(map[string]struct{}, len(seed.Items))
	for _, item := range seed.Items {
		expectedType, known := runtimeSettingExpectedTypes[item.Name]
		if !known {
			return fmt.Errorf("runtime settings seed contains unknown item %s", item.Name)
		}
		if _, exists := itemNames[item.Name]; exists {
			return fmt.Errorf("runtime settings seed contains duplicate item %s", item.Name)
		}
		itemNames[item.Name] = struct{}{}

		if strings.TrimSpace(item.DisplayName) == "" {
			return fmt.Errorf("runtime settings seed item %s has no display_name", item.Name)
		}
		if item.ValueType != expectedType {
			return fmt.Errorf(
				"runtime settings seed item %s has value_type %s; want %s",
				item.Name,
				item.ValueType,
				expectedType,
			)
		}
		if item.IsRequired == runtimeSettingIsOptional(item.Name) {
			return fmt.Errorf(
				"runtime settings seed item %s has is_required=%t; want %t",
				item.Name,
				item.IsRequired,
				!runtimeSettingIsOptional(item.Name),
			)
		}
		if item.WriteOnly != runtimeSettingIsWriteOnly(item.Name) {
			return fmt.Errorf(
				"runtime settings seed item %s has writeonly=%t; want %t",
				item.Name,
				item.WriteOnly,
				runtimeSettingIsWriteOnly(item.Name),
			)
		}
		if item.SortOrder < 0 {
			return fmt.Errorf("runtime settings seed item %s has a negative sort_order", item.Name)
		}
		if _, exists := groupNames[item.GroupName]; !exists {
			return fmt.Errorf("runtime settings seed item %s references unknown group %s", item.Name, item.GroupName)
		}
		if _, err := validateRuntimeSettingValue(item.Name, item.DefaultValue); err != nil {
			return fmt.Errorf("runtime settings seed item %s has an invalid default: %w", item.Name, err)
		}
	}

	for name := range runtimeSettingExpectedTypes {
		if _, exists := itemNames[name]; !exists {
			return fmt.Errorf("runtime settings seed is missing item %s", name)
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
	ServiceDateReminderEnabled   bool
	ServiceDateReminderLeadTimes []time.Duration
	TaggerLLMAPIKey              string
}

// EnsureRuntimeSettings inserts missing groups and settings from the embedded
// seed catalog. Existing rows are never overwritten.
func (s *ConfigService) EnsureRuntimeSettings() error {
	return s.ensureRuntimeSettings("")
}

// EnsureRuntimeSettingsWithInitialTaggerAPIKey seeds a missing tagger API key
// from the legacy environment setting. Existing database values are preserved.
func (s *ConfigService) EnsureRuntimeSettingsWithInitialTaggerAPIKey(initialAPIKey string) error {
	return s.ensureRuntimeSettings(strings.TrimSpace(initialAPIKey))
}

func (s *ConfigService) ensureRuntimeSettings(initialTaggerAPIKey string) error {
	seed, err := loadRuntimeSeed()
	if err != nil {
		return err
	}

	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		groupIDs := make(map[string]uint, len(seed.Groups))
		for _, group := range seed.Groups {
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

		for _, setting := range seed.Items {
			exists, err := configItemExists(tx, setting.Name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}

			defaultValue := setting.DefaultValue
			if setting.Name == runtimeSettingTaggerLLMAPIKey && initialTaggerAPIKey != "" {
				defaultValue = initialTaggerAPIKey
			}
			value, err := validateRuntimeSettingValue(setting.Name, defaultValue)
			if err != nil {
				return fmt.Errorf("initialize %s: %w", setting.Name, err)
			}

			groupID := groupIDs[setting.GroupName]
			if _, err := tx.Exec(
				`INSERT INTO clinic_config_item
				 (name, value, value_type, display_name, is_required, writeonly, group_id, sort_order)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				 ON CONFLICT (name) DO NOTHING`,
				setting.Name, value, setting.ValueType, setting.DisplayName,
				setting.IsRequired, setting.WriteOnly, groupID, setting.SortOrder,
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
	return loadRuntimeSettings(s.db)
}

func loadRuntimeSettings(q interface{ QueryRow(string, ...any) *sql.Row }) (RuntimeSettings, error) {
	seed, err := loadRuntimeSeed()
	if err != nil {
		return RuntimeSettings{}, err
	}

	settings := RuntimeSettings{}
	for _, setting := range seed.Items {
		var value string
		var valueType configmodel.ConfigValueType
		var required bool
		var writeOnly bool
		err = q.QueryRow(
			"SELECT value, value_type, is_required, writeonly FROM clinic_config_item WHERE name = $1",
			setting.Name,
		).Scan(&value, &valueType, &required, &writeOnly)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return RuntimeSettings{}, fmt.Errorf("runtime config %s is missing", setting.Name)
			}
			return RuntimeSettings{}, fmt.Errorf("load runtime config %s: %w", setting.Name, err)
		}

		normalized, err := validateKnownRuntimeSetting(setting.Name, valueType, required, writeOnly, value)
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
		case runtimeSettingServiceReminderEnabled:
			settings.ServiceDateReminderEnabled = normalized == "true"
		case runtimeSettingServiceReminderLeadTimes:
			settings.ServiceDateReminderLeadTimes, err = parseRuntimeLeadTimes(normalized)
		case runtimeSettingTaggerLLMAPIKey:
			settings.TaggerLLMAPIKey = normalized
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
	writeOnly bool,
	value string,
) (string, error) {
	seed, err := loadRuntimeSeed()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrConfigInvalidInput, err)
	}

	var setting runtimeSettingDefinition
	found := false
	for _, candidate := range seed.Items {
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
	case runtimeSettingTaggerLLMAPIKey:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unsupported runtime setting %s", ErrConfigInvalidInput, name)
	}
}

func runtimeSettingIsOptional(name string) bool {
	return name == runtimeSettingTaggerLLMAPIKey
}

func runtimeSettingIsWriteOnly(name string) bool {
	return name == runtimeSettingTaggerLLMAPIKey
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
