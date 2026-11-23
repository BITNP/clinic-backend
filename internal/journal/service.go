package journal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/database"
	journalmodel "clinic-backend/internal/journal/model"
)

const (
	ActorStaff    = "staff"
	ActorCustomer = "customer"
	ActorSystem   = "system"
	ActorExternal = "integration"
)

var ErrInvalidEvent = errors.New("invalid journal event")

// EventInput describes one event before its server timestamps and ID are
// assigned.
type EventInput struct {
	OccurredAt      time.Time
	ActorType       string
	ActorID         string
	Action          string
	EntityType      string
	EntityID        string
	OperationID     string
	CausedByEventID *uint64
	Details         map[string]any
}

// Service owns journal reads. Inserts use InsertTx so business mutations and
// their history commit or roll back together.
type Service struct {
	db *database.DB
}

func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// NewOperationID returns an opaque identifier shared by related events.
func NewOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate journal operation id: %w", err)
	}
	return "op_" + hex.EncodeToString(value[:]), nil
}

// InsertTx appends one immutable event in the caller's business transaction.
func InsertTx(ctx context.Context, tx *sql.Tx, in EventInput) (uint64, error) {
	in.ActorType = strings.TrimSpace(in.ActorType)
	in.ActorID = strings.TrimSpace(in.ActorID)
	in.Action = strings.TrimSpace(in.Action)
	in.EntityType = strings.TrimSpace(in.EntityType)
	in.EntityID = strings.TrimSpace(in.EntityID)
	in.OperationID = strings.TrimSpace(in.OperationID)
	if in.ActorType == "" || in.ActorID == "" || in.Action == "" ||
		in.EntityType == "" || in.EntityID == "" || in.OperationID == "" {
		return 0, ErrInvalidEvent
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	if in.Details == nil {
		in.Details = map[string]any{"version": 1}
	}
	details, err := json.Marshal(in.Details)
	if err != nil {
		return 0, fmt.Errorf("marshal journal details: %w", err)
	}

	var id uint64
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO clinic_journal_event
		 (occurred_at, recorded_at, actor_type, actor_id, action, entity_type,
		  entity_id, operation_id, caused_by_event_id, details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		in.OccurredAt.UTC(),
		time.Now().UTC(),
		in.ActorType,
		in.ActorID,
		in.Action,
		in.EntityType,
		in.EntityID,
		in.OperationID,
		in.CausedByEventID,
		journalmodel.Payload(details),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert journal event: %w", err)
	}
	return id, nil
}

// Filter narrows journal reads. Cursor is exclusive and pages newest first.
type Filter struct {
	EntityType string
	EntityID   string
	ActorType  string
	ActorID    string
	Action     string
	From       *time.Time
	To         *time.Time
	Cursor     *uint64
	Limit      int
}

type Page struct {
	Items      []journalmodel.Event
	NextCursor *uint64
}

// EntityKind lists the actions recorded for one entity type.
type EntityKind struct {
	EntityType string   `json:"entity_type"`
	Actions    []string `json:"actions"`
}

const MaxPageSize = 100

// ListEntityKinds returns the entity and action vocabulary currently present
// in the journal. The result is grouped by entity type and sorted for stable
// filter options.
func (s *Service) ListEntityKinds(ctx context.Context) ([]EntityKind, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT entity_type, action
		 FROM clinic_journal_event
		 GROUP BY entity_type, action
		 ORDER BY entity_type ASC, action ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list journal entity kinds: %w", err)
	}
	defer rows.Close()

	kinds := make([]EntityKind, 0)
	for rows.Next() {
		var entityType, action string
		if err := rows.Scan(&entityType, &action); err != nil {
			return nil, fmt.Errorf("scan journal entity kind: %w", err)
		}
		if len(kinds) == 0 || kinds[len(kinds)-1].EntityType != entityType {
			kinds = append(kinds, EntityKind{
				EntityType: entityType,
				Actions:    make([]string, 0, 1),
			})
		}
		last := &kinds[len(kinds)-1]
		last.Actions = append(last.Actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list journal entity kinds: %w", err)
	}
	return kinds, nil
}

// List returns a stable newest-first page of journal events.
func (s *Service) List(ctx context.Context, filter Filter) (Page, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > MaxPageSize {
		filter.Limit = MaxPageSize
	}

	where := []string{"1 = 1"}
	args := make([]any, 0, 9)
	add := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, value)
			where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
		}
	}
	add("entity_type", filter.EntityType)
	add("entity_id", filter.EntityID)
	add("actor_type", filter.ActorType)
	add("actor_id", filter.ActorID)
	add("action", filter.Action)
	if filter.From != nil {
		args = append(args, filter.From.UTC())
		where = append(where, fmt.Sprintf("occurred_at >= $%d", len(args)))
	}
	if filter.To != nil {
		args = append(args, filter.To.UTC())
		where = append(where, fmt.Sprintf("occurred_at <= $%d", len(args)))
	}
	if filter.Cursor != nil {
		args = append(args, *filter.Cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	args = append(args, filter.Limit+1)

	rows, err := s.db.QueryContext(
		ctx,
		"SELECT id, occurred_at, recorded_at, actor_type, actor_id, action, "+
			"entity_type, entity_id, operation_id, caused_by_event_id, details "+
			"FROM clinic_journal_event WHERE "+strings.Join(where, " AND ")+
			fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args)),
		args...,
	)
	if err != nil {
		return Page{}, fmt.Errorf("list journal events: %w", err)
	}
	defer rows.Close()

	items := make([]journalmodel.Event, 0, filter.Limit+1)
	for rows.Next() {
		var event journalmodel.Event
		if err := rows.Scan(
			&event.ID,
			&event.OccurredAt,
			&event.RecordedAt,
			&event.ActorType,
			&event.ActorID,
			&event.Action,
			&event.EntityType,
			&event.EntityID,
			&event.OperationID,
			&event.CausedByEventID,
			&event.Details,
		); err != nil {
			return Page{}, fmt.Errorf("scan journal event: %w", err)
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list journal events: %w", err)
	}

	var next *uint64
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		value := items[len(items)-1].ID
		next = &value
	}
	return Page{Items: items, NextCursor: next}, nil
}
