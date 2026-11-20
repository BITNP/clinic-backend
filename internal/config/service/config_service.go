package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	configmodel "clinic-backend/internal/config/model"
	"clinic-backend/internal/database"
)

var (
	ErrConfigGroupNotFound    = errors.New("config group not found")
	ErrConfigItemNotFound     = errors.New("config item not found")
	ErrConfigGroupNameTaken   = errors.New("config group name already taken")
	ErrConfigItemNameTaken    = errors.New("config item name already taken")
	ErrConfigGroupInUse       = errors.New("config group has items and cannot be deleted")
	ErrConfigInvalidInput     = errors.New("invalid config input")
	ErrConfigValueTypeInvalid = errors.New("invalid config value_type")
	ErrConfigValueInvalid     = errors.New("invalid config value")
)

const (
	groupColumns = "id, name, display_name, sort_order"
	itemColumns  = "id, name, value, value_type, display_name, is_required, role_level, is_readable, group_id, sort_order"
)

// ConfigService contains the business logic for configuration groups and items.
type ConfigService struct {
	db              *database.DB
	mu              sync.Mutex
	runtime         atomic.Pointer[RuntimeSettings]
	validateRuntime func(RuntimeSettings) error
}

func NewConfigService(db *database.DB) *ConfigService {
	return &ConfigService{db: db}
}

// InitializeRuntime loads the active settings and installs application validation.
// Call before serving requests.
func (s *ConfigService) InitializeRuntime(validate func(RuntimeSettings) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.LoadRuntimeSettings()
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(settings); err != nil {
			return err
		}
	}
	s.validateRuntime = validate
	s.runtime.Store(&settings)
	return nil
}

// Current returns a private copy of the active runtime settings.
func (s *ConfigService) Current() RuntimeSettings {
	current := s.runtime.Load()
	if current == nil {
		return RuntimeSettings{}
	}
	result := *current
	result.ServiceDateReminderLeadTimes = append([]time.Duration(nil), current.ServiceDateReminderLeadTimes...)
	return result
}

// WithRuntimeSettings serializes a short background operation with settings
// changes, so it cannot create work using a setting after that setting is removed.
func (s *ConfigService) WithRuntimeSettings(fn func(RuntimeSettings) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(s.Current())
}

// Catalog is the complete flat configuration view used by the admin page.
type Catalog struct {
	Groups []configmodel.ClinicConfigGroup `json:"groups"`
	Items  []configmodel.ClinicConfigItem  `json:"items"`
}

type CreateGroupInput struct {
	Name        string
	DisplayName string
	SortOrder   int
}

type UpdateGroupInput struct {
	DisplayName *string
	SortOrder   *int
}

type CreateItemInput struct {
	Name        string
	Value       string
	ValueType   configmodel.ConfigValueType
	DisplayName string
	IsRequired  bool
	RoleLevel   configmodel.ConfigRoleLevel
	IsReadable  bool
	GroupID     uint
	SortOrder   int
}

type UpdateItemInput struct {
	Value       *string
	ValueType   *configmodel.ConfigValueType
	DisplayName *string
	IsRequired  *bool
	RoleLevel   *configmodel.ConfigRoleLevel
	IsReadable  *bool
	GroupID     *uint
	SortOrder   *int
}

// ListCatalog returns every group and item in display order.
func (s *ConfigService) ListCatalog() (Catalog, error) {
	catalog := Catalog{
		Groups: make([]configmodel.ClinicConfigGroup, 0),
		Items:  make([]configmodel.ClinicConfigItem, 0),
	}

	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var err error
		catalog.Groups, err = listGroups(tx)
		if err != nil {
			return err
		}
		catalog.Items, err = listItems(tx)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Catalog{}, fmt.Errorf("list config catalog: %w", err)
	}
	return catalog, nil
}

func listGroups(tx *sql.Tx) ([]configmodel.ClinicConfigGroup, error) {
	rows, err := tx.Query("SELECT " + groupColumns + " FROM clinic_config_group ORDER BY sort_order ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list config groups: %w", err)
	}
	defer rows.Close()

	groups := make([]configmodel.ClinicConfigGroup, 0)
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("scan config group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list config groups: %w", err)
	}
	return groups, nil
}

func listItems(tx *sql.Tx) ([]configmodel.ClinicConfigItem, error) {
	rows, err := tx.Query(`
		SELECT i.id, i.name, i.value, i.value_type, i.display_name,
		       i.is_required, i.role_level, i.is_readable, i.group_id, i.sort_order
		FROM clinic_config_item AS i
		JOIN clinic_config_group AS g ON g.id = i.group_id
		ORDER BY g.sort_order ASC, g.id ASC, i.sort_order ASC, i.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list config items: %w", err)
	}
	defer rows.Close()

	items := make([]configmodel.ClinicConfigItem, 0)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan config item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list config items: %w", err)
	}
	return items, nil
}

func (s *ConfigService) GetGroup(id uint) (configmodel.ClinicConfigGroup, error) {
	group, err := scanGroup(s.db.QueryRow(
		"SELECT "+groupColumns+" FROM clinic_config_group WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return configmodel.ClinicConfigGroup{}, ErrConfigGroupNotFound
		}
		return configmodel.ClinicConfigGroup{}, fmt.Errorf("get config group %d: %w", id, err)
	}
	return group, nil
}

func (s *ConfigService) CreateGroup(in CreateGroupInput) (configmodel.ClinicConfigGroup, error) {
	name, err := normalizeRequiredString("name", in.Name, 64)
	if err != nil {
		return configmodel.ClinicConfigGroup{}, err
	}
	displayName, err := normalizeRequiredString("display_name", in.DisplayName, 128)
	if err != nil {
		return configmodel.ClinicConfigGroup{}, err
	}
	if err := validateSortOrder(in.SortOrder); err != nil {
		return configmodel.ClinicConfigGroup{}, err
	}

	group, err := scanGroup(s.db.QueryRow(
		`INSERT INTO clinic_config_group (name, display_name, sort_order)
		 VALUES ($1, $2, $3) RETURNING `+groupColumns,
		name, displayName, in.SortOrder,
	))
	if err != nil {
		if database.IsUniqueViolation(err) {
			return configmodel.ClinicConfigGroup{}, ErrConfigGroupNameTaken
		}
		return configmodel.ClinicConfigGroup{}, fmt.Errorf("create config group: %w", err)
	}
	return group, nil
}

func (s *ConfigService) UpdateGroup(id uint, in UpdateGroupInput) (configmodel.ClinicConfigGroup, error) {
	group, err := s.GetGroup(id)
	if err != nil {
		return configmodel.ClinicConfigGroup{}, err
	}

	if in.DisplayName != nil {
		group.DisplayName, err = normalizeRequiredString("display_name", *in.DisplayName, 128)
		if err != nil {
			return configmodel.ClinicConfigGroup{}, err
		}
	}
	if in.SortOrder != nil {
		if err := validateSortOrder(*in.SortOrder); err != nil {
			return configmodel.ClinicConfigGroup{}, err
		}
		group.SortOrder = *in.SortOrder
	}

	updated, err := scanGroup(s.db.QueryRow(
		`UPDATE clinic_config_group
		 SET display_name = $1, sort_order = $2
		 WHERE id = $3
		 RETURNING `+groupColumns,
		group.DisplayName, group.SortOrder, id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return configmodel.ClinicConfigGroup{}, ErrConfigGroupNotFound
		}
		return configmodel.ClinicConfigGroup{}, fmt.Errorf("update config group %d: %w", id, err)
	}
	return updated, nil
}

func (s *ConfigService) DeleteGroup(id uint) error {
	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var existingID uint
		if err := tx.QueryRow("SELECT id FROM clinic_config_group WHERE id = $1", id).Scan(&existingID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConfigGroupNotFound
			}
			return fmt.Errorf("get config group %d for delete: %w", id, err)
		}

		var itemCount int64
		if err := tx.QueryRow("SELECT COUNT(*) FROM clinic_config_item WHERE group_id = $1", id).Scan(&itemCount); err != nil {
			return fmt.Errorf("count config items for group %d: %w", id, err)
		}
		if itemCount > 0 {
			return ErrConfigGroupInUse
		}

		if _, err := tx.Exec("DELETE FROM clinic_config_group WHERE id = $1", id); err != nil {
			return fmt.Errorf("delete config group %d: %w", id, err)
		}
		return nil
	})
}

func (s *ConfigService) GetItem(id uint) (configmodel.ClinicConfigItem, error) {
	item, err := scanItem(s.db.QueryRow(
		"SELECT "+itemColumns+" FROM clinic_config_item WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return configmodel.ClinicConfigItem{}, ErrConfigItemNotFound
		}
		return configmodel.ClinicConfigItem{}, fmt.Errorf("get config item %d: %w", id, err)
	}
	return item, nil
}

func (s *ConfigService) CreateItem(in CreateItemInput) (configmodel.ClinicConfigItem, error) {
	name, err := normalizeRequiredString("name", in.Name, 128)
	if err != nil {
		return configmodel.ClinicConfigItem{}, err
	}
	displayName, err := normalizeRequiredString("display_name", in.DisplayName, 128)
	if err != nil {
		return configmodel.ClinicConfigItem{}, err
	}
	if in.GroupID == 0 {
		return configmodel.ClinicConfigItem{}, fmt.Errorf("%w: group_id is required", ErrConfigInvalidInput)
	}
	if err := validateSortOrder(in.SortOrder); err != nil {
		return configmodel.ClinicConfigItem{}, err
	}
	roleLevel := in.RoleLevel
	if roleLevel == "" {
		roleLevel = configmodel.ConfigRoleLevelAdmin
	}
	if !roleLevel.Valid() {
		return configmodel.ClinicConfigItem{}, fmt.Errorf(
			"%w: unsupported role_level %q",
			ErrConfigInvalidInput,
			roleLevel,
		)
	}
	value, err := normalizeConfigItemValue(name, in.Value, in.ValueType, in.IsRequired)
	if err != nil {
		return configmodel.ClinicConfigItem{}, err
	}

	var item configmodel.ClinicConfigItem
	err = database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		if err := ensureGroup(tx, in.GroupID); err != nil {
			return err
		}
		var err error
		item, err = scanItem(tx.QueryRow(
			`INSERT INTO clinic_config_item
			 (name, value, value_type, display_name, is_required, role_level, is_readable, group_id, sort_order)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 RETURNING `+itemColumns,
			name, value, in.ValueType, displayName, in.IsRequired, roleLevel, in.IsReadable,
			in.GroupID, in.SortOrder,
		))
		if err != nil {
			if database.IsUniqueViolation(err) {
				return ErrConfigItemNameTaken
			}
			return fmt.Errorf("create config item: %w", err)
		}
		return nil
	})
	if err != nil {
		return configmodel.ClinicConfigItem{}, err
	}
	return item, nil
}

func (s *ConfigService) UpdateItem(id uint, in UpdateItemInput) (configmodel.ClinicConfigItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var candidate *RuntimeSettings
	var item configmodel.ClinicConfigItem
	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var err error
		item, err = scanItem(tx.QueryRow(
			"SELECT "+itemColumns+" FROM clinic_config_item WHERE id = $1",
			id,
		))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConfigItemNotFound
			}
			return fmt.Errorf("get config item %d for update: %w", id, err)
		}

		if in.Value != nil {
			item.Value = *in.Value
		}
		if in.ValueType != nil {
			item.ValueType = *in.ValueType
		}
		if in.DisplayName != nil {
			item.DisplayName, err = normalizeRequiredString("display_name", *in.DisplayName, 128)
			if err != nil {
				return err
			}
		}
		if in.IsRequired != nil {
			item.IsRequired = *in.IsRequired
		}
		if in.RoleLevel != nil {
			item.RoleLevel = *in.RoleLevel
		}
		if !item.RoleLevel.Valid() {
			return fmt.Errorf(
				"%w: unsupported role_level %q",
				ErrConfigInvalidInput,
				item.RoleLevel,
			)
		}
		if in.IsReadable != nil {
			item.IsReadable = *in.IsReadable
		}
		if in.GroupID != nil {
			if *in.GroupID == 0 {
				return fmt.Errorf("%w: group_id is required", ErrConfigInvalidInput)
			}
			item.GroupID = *in.GroupID
		}
		if in.SortOrder != nil {
			if err := validateSortOrder(*in.SortOrder); err != nil {
				return err
			}
			item.SortOrder = *in.SortOrder
		}

		item.Value, err = normalizeConfigItemValue(item.Name, item.Value, item.ValueType, item.IsRequired)
		if err != nil {
			return err
		}
		if err := ensureGroup(tx, item.GroupID); err != nil {
			return err
		}

		item, err = scanItem(tx.QueryRow(
			`UPDATE clinic_config_item
			 SET value = $1, value_type = $2, display_name = $3,
			     is_required = $4, role_level = $5, is_readable = $6,
			     group_id = $7, sort_order = $8
			 WHERE id = $9
			 RETURNING `+itemColumns,
			item.Value, item.ValueType, item.DisplayName, item.IsRequired,
			item.RoleLevel, item.IsReadable, item.GroupID, item.SortOrder, id,
		))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConfigItemNotFound
			}
			return fmt.Errorf("update config item %d: %w", id, err)
		}
		if s.runtime.Load() != nil {
			settings, err := loadRuntimeSettings(tx)
			if err != nil {
				return err
			}
			if s.validateRuntime != nil {
				if err := s.validateRuntime(settings); err != nil {
					return fmt.Errorf("%w: %v", ErrConfigValueInvalid, err)
				}
			}
			previous := s.runtime.Load()
			for _, oldLead := range previous.ServiceDateReminderLeadTimes {
				found := false
				for _, newLead := range settings.ServiceDateReminderLeadTimes {
					if newLead == oldLead {
						found = true
						break
					}
				}
				if !found {
					if _, err := tx.Exec(`UPDATE clinic_service_date_reminder
						SET status = 'cancelled', updated_at = $1
						WHERE status = 'pending' AND lead_time_seconds = $2`, time.Now().UTC(), int64(oldLead/time.Second)); err != nil {
						return fmt.Errorf("cancel reminders for removed lead time: %w", err)
					}
				}
			}
			candidate = &settings
		}
		return nil
	})
	if err != nil {
		return configmodel.ClinicConfigItem{}, err
	}
	if candidate != nil {
		s.runtime.Store(candidate)
	}
	return item, nil
}

func (s *ConfigService) DeleteItem(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.GetItem(id)
	if err != nil {
		return err
	}
	if _, ok := runtimeSettingExpectedTypes[item.Name]; ok {
		return fmt.Errorf("%w: built-in runtime settings cannot be deleted", ErrConfigInvalidInput)
	}
	result, err := s.db.Exec("DELETE FROM clinic_config_item WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete config item %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted config item %d: %w", id, err)
	}
	if affected == 0 {
		return ErrConfigItemNotFound
	}
	return nil
}

func scanGroup(row interface{ Scan(...any) error }) (configmodel.ClinicConfigGroup, error) {
	var group configmodel.ClinicConfigGroup
	err := row.Scan(&group.ID, &group.Name, &group.DisplayName, &group.SortOrder)
	return group, err
}

func scanItem(row interface{ Scan(...any) error }) (configmodel.ClinicConfigItem, error) {
	var item configmodel.ClinicConfigItem
	err := row.Scan(
		&item.ID,
		&item.Name,
		&item.Value,
		&item.ValueType,
		&item.DisplayName,
		&item.IsRequired,
		&item.RoleLevel,
		&item.IsReadable,
		&item.GroupID,
		&item.SortOrder,
	)
	return item, err
}

func ensureGroup(tx *sql.Tx, id uint) error {
	var existingID uint
	if err := tx.QueryRow("SELECT id FROM clinic_config_group WHERE id = $1", id).Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConfigGroupNotFound
		}
		return fmt.Errorf("get config group %d: %w", id, err)
	}
	return nil
}

func normalizeRequiredString(field, value string, maxLength int) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", fmt.Errorf("%w: %s is required", ErrConfigInvalidInput, field)
	}
	if utf8.RuneCountInString(normalized) > maxLength {
		return "", fmt.Errorf("%w: %s exceeds %d characters", ErrConfigInvalidInput, field, maxLength)
	}
	return normalized, nil
}

func validateSortOrder(sortOrder int) error {
	if sortOrder < 0 {
		return fmt.Errorf("%w: sort_order must not be negative", ErrConfigInvalidInput)
	}
	return nil
}

func normalizeValue(value string, valueType configmodel.ConfigValueType, required bool) (string, error) {
	if !valueType.Valid() {
		return "", fmt.Errorf("%w: unsupported value_type %q", ErrConfigValueTypeInvalid, valueType)
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return "", fmt.Errorf("%w: required value is empty", ErrConfigValueInvalid)
		}
		return "", nil
	}

	switch valueType {
	case configmodel.ConfigValueTypeInt:
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return "", fmt.Errorf("%w: expected an integer", ErrConfigValueInvalid)
		}
		return strconv.FormatInt(parsed, 10), nil
	case configmodel.ConfigValueTypeDouble:
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return "", fmt.Errorf("%w: expected a finite number", ErrConfigValueInvalid)
		}
		return strconv.FormatFloat(parsed, 'g', -1, 64), nil
	case configmodel.ConfigValueTypeBoolean:
		if trimmed != "true" && trimmed != "false" {
			return "", fmt.Errorf("%w: expected true or false", ErrConfigValueInvalid)
		}
		return trimmed, nil
	case configmodel.ConfigValueTypeString:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unsupported value_type %q", ErrConfigValueTypeInvalid, valueType)
	}
}

func normalizeConfigItemValue(
	name string,
	value string,
	valueType configmodel.ConfigValueType,
	required bool,
) (string, error) {
	if _, ok := runtimeSettingExpectedTypes[name]; ok {
		return validateKnownRuntimeSetting(name, valueType, required, value)
	}
	return normalizeValue(value, valueType, required)
}
