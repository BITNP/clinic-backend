package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Payload stores the versioned, operation-specific journal details.
type Payload []byte

// Value validates and stores the payload as JSON text so PostgreSQL can cast it
// into a jsonb column and SQLite can retain it in the test schema.
func (p Payload) Value() (driver.Value, error) {
	if len(p) == 0 {
		return "{}", nil
	}
	if !json.Valid(p) {
		return nil, fmt.Errorf("journal payload is not valid JSON")
	}
	return string(p), nil
}

// Scan reads JSON from either PostgreSQL or SQLite.
func (p *Payload) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*p = Payload("{}")
	case []byte:
		*p = append((*p)[:0], v...)
	case string:
		*p = append((*p)[:0], v...)
	default:
		return fmt.Errorf("scan journal payload: unsupported type %T", value)
	}
	if len(*p) == 0 {
		*p = Payload("{}")
	}
	if !json.Valid(*p) {
		return fmt.Errorf("scan journal payload: invalid JSON")
	}
	return nil
}

// MarshalJSON emits the stored JSON object without wrapping it in a string.
func (p Payload) MarshalJSON() ([]byte, error) {
	if len(p) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(p) {
		return nil, fmt.Errorf("marshal journal payload: invalid JSON")
	}
	return p, nil
}

// Event is one immutable business-history record.
type Event struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	OccurredAt      time.Time `gorm:"not null;index:idx_journal_entity_time,priority:3;index:idx_journal_actor_time,priority:3;column:occurred_at" json:"occurred_at"`
	RecordedAt      time.Time `gorm:"not null;column:recorded_at" json:"recorded_at"`
	ActorType       string    `gorm:"size:32;not null;index:idx_journal_actor_time,priority:1;column:actor_type" json:"actor_type"`
	ActorID         string    `gorm:"size:128;not null;index:idx_journal_actor_time,priority:2;column:actor_id" json:"actor_id"`
	Action          string    `gorm:"size:64;not null;column:action" json:"action"`
	EntityType      string    `gorm:"size:32;not null;index:idx_journal_entity_time,priority:1;column:entity_type" json:"entity_type"`
	EntityID        string    `gorm:"size:128;not null;index:idx_journal_entity_time,priority:2;column:entity_id" json:"entity_id"`
	OperationID     string    `gorm:"size:64;not null;index;column:operation_id" json:"operation_id"`
	CausedByEventID *uint64   `gorm:"index;column:caused_by_event_id" json:"caused_by_event_id"`
	Details         Payload   `gorm:"type:jsonb;not null;column:details" json:"details"`
}

// TableName keeps the journal name stable across GORM versions.
func (Event) TableName() string {
	return "clinic_journal_event"
}
