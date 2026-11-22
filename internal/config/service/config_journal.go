package service

import (
	"reflect"
	"strings"

	configmodel "clinic-backend/internal/config/model"
)

var redactedConfigNames = map[string]bool{
	"TAGGER_LLM_API_KEY":            true,
	"MESSAGE_GATEWAY_ACCOUNT_TOKEN": true,
}

func configItemJournalDetails(
	before configmodel.ClinicConfigItem,
	after configmodel.ClinicConfigItem,
) (map[string]any, bool) {
	changes := make(map[string]any)
	add := func(name string, oldValue, newValue any) {
		if !reflect.DeepEqual(oldValue, newValue) {
			changes[name] = map[string]any{"before": oldValue, "after": newValue}
		}
	}

	if before.Value != after.Value {
		if before.WriteOnly || after.WriteOnly || redactedConfigNames[strings.ToUpper(after.Name)] {
			changes["value"] = map[string]any{"changed": true, "redacted": true}
		} else {
			add("value", before.Value, after.Value)
		}
	}
	add("value_type", string(before.ValueType), string(after.ValueType))
	add("display_name", before.DisplayName, after.DisplayName)
	add("unit", before.Unit, after.Unit)
	add("hint", before.Hint, after.Hint)
	add("is_required", before.IsRequired, after.IsRequired)
	add("role_level", string(before.RoleLevel), string(after.RoleLevel))
	add("writeonly", before.WriteOnly, after.WriteOnly)
	add("group_id", before.GroupID, after.GroupID)
	add("sort_order", before.SortOrder, after.SortOrder)
	if len(changes) == 0 {
		return nil, false
	}
	return map[string]any{
		"version": 1,
		"name":    after.Name,
		"changes": changes,
	}, true
}
