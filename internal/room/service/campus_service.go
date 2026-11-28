package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"clinic-backend/internal/database"
	roommodel "clinic-backend/internal/room/model"
)

// ErrCampusInvalidPolicy indicates an unsupported selection policy.
var ErrCampusInvalidPolicy = errors.New("invalid selection_policy")

// ErrCampusInvalidNoShowBanDuration indicates a non-positive or overflowing no-show ban duration.
var ErrCampusInvalidNoShowBanDuration = errors.New("invalid no_show_ban_policy.duration_hours")

// ErrCampusInvalidNoShowBanLevel indicates an unsupported no-show ban level.
var ErrCampusInvalidNoShowBanLevel = errors.New("invalid no_show_ban_policy.level")

// ErrCampusNotFound is returned when no campus matches the given id.
var ErrCampusNotFound = errors.New("campus not found")

// ErrCampusNameTaken is returned on create/update when the campus name already exists.
var ErrCampusNameTaken = errors.New("campus name already taken")

// ErrCampusInUse is returned when a campus cannot be deleted because rooms still reference it.
var ErrCampusInUse = errors.New("campus has rooms and cannot be deleted")

// CampusService contains the business logic for clinic campus CRUD.
type CampusService struct {
	db *database.DB
}

func NewCampusService(db *database.DB) *CampusService {
	return &CampusService{db: db}
}

const campusColumns = "id, name, selection_policy, no_show_ban_enabled, no_show_ban_duration_hours, no_show_ban_level"

func joinAnd(parts []string) string {
	return strings.Join(parts, " AND ")
}

func scanCampus(row interface{ Scan(...any) error }) (roommodel.ClinicCampus, error) {
	var c roommodel.ClinicCampus
	err := row.Scan(
		&c.ID,
		&c.Name,
		&c.SelectionPolicy,
		&c.NoShowBanPolicy.Enabled,
		&c.NoShowBanPolicy.DurationHours,
		&c.NoShowBanPolicy.Level,
	)
	return c, err
}

type NoShowBanPolicyPatch struct {
	Enabled       *bool
	DurationHours *int64
	Level         *roommodel.NoShowBanLevel
}

// CreateCampusInput carries the fields a caller may set on creation.
type CreateCampusInput struct {
	SelectionPolicy roommodel.SelectionPolicy
	Name            string
	NoShowBanPolicy *NoShowBanPolicyPatch
}

// UpdateCampusInput uses pointers so omitted fields stay unchanged.
type UpdateCampusInput struct {
	SelectionPolicy *roommodel.SelectionPolicy
	Name            *string
	NoShowBanPolicy *NoShowBanPolicyPatch
}

// ListCampusFilter controls listing behavior.
type ListCampusFilter struct {
	Name     string // substring match
	Page     int
	PageSize int
}

func (s *CampusService) Create(in CreateCampusInput) (roommodel.ClinicCampus, error) {
	policy := in.SelectionPolicy
	if policy == "" {
		policy = roommodel.SelectionPolicyBalanced
	}
	if !policy.Valid() {
		return roommodel.ClinicCampus{}, ErrCampusInvalidPolicy
	}
	noShowPolicy := defaultNoShowBanPolicy(in.NoShowBanPolicy)
	if err := validateNoShowBanPolicy(noShowPolicy); err != nil {
		return roommodel.ClinicCampus{}, err
	}
	c, err := scanCampus(s.db.QueryRow(
		`INSERT INTO clinic_campus
		 (name, selection_policy, no_show_ban_enabled, no_show_ban_duration_hours, no_show_ban_level)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+campusColumns,
		in.Name, policy, noShowPolicy.Enabled, noShowPolicy.DurationHours, noShowPolicy.Level,
	))
	if err != nil {
		if database.IsUniqueViolation(err) {
			return roommodel.ClinicCampus{}, ErrCampusNameTaken
		}
		return roommodel.ClinicCampus{}, fmt.Errorf("create campus: %w", err)
	}
	return c, nil
}

func (s *CampusService) GetByID(id uint) (roommodel.ClinicCampus, error) {
	c, err := scanCampus(s.db.QueryRow(
		"SELECT "+campusColumns+" FROM clinic_campus WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roommodel.ClinicCampus{}, ErrCampusNotFound
		}
		return roommodel.ClinicCampus{}, fmt.Errorf("get campus %d: %w", id, err)
	}
	return c, nil
}

func (s *CampusService) List(f ListCampusFilter) ([]roommodel.ClinicCampus, int64, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.Name != "" {
		args = append(args, "%"+f.Name+"%")
		where = append(where, fmt.Sprintf("name LIKE $%d", len(args)))
	}
	predicate := " WHERE " + joinAnd(where)

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_campus"+predicate, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count campuses: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	args = append(args, f.PageSize, offset)
	query := fmt.Sprintf(
		"SELECT %s FROM clinic_campus%s ORDER BY id ASC LIMIT $%d OFFSET $%d",
		campusColumns, predicate, len(args)-1, len(args),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list campuses: %w", err)
	}
	defer rows.Close()
	items := make([]roommodel.ClinicCampus, 0)
	for rows.Next() {
		c, err := scanCampus(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan campus: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list campuses: %w", err)
	}
	return items, total, nil
}

func (s *CampusService) Update(id uint, in UpdateCampusInput) (roommodel.ClinicCampus, error) {
	var c roommodel.ClinicCampus
	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var err error
		c, err = scanCampus(tx.QueryRow(
			"SELECT "+campusColumns+" FROM clinic_campus WHERE id = $1",
			id,
		))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrCampusNotFound
			}
			return fmt.Errorf("get campus %d for update: %w", id, err)
		}

		var updates []string
		var args []any
		if in.SelectionPolicy != nil {
			if !in.SelectionPolicy.Valid() {
				return ErrCampusInvalidPolicy
			}
			args = append(args, *in.SelectionPolicy)
			updates = append(updates, fmt.Sprintf("selection_policy = $%d", len(args)))
		}
		if in.Name != nil {
			args = append(args, *in.Name)
			updates = append(updates, fmt.Sprintf("name = $%d", len(args)))
		}
		if in.NoShowBanPolicy != nil {
			effective := c.NoShowBanPolicy
			applyNoShowBanPolicyPatch(&effective, in.NoShowBanPolicy)
			if err := validateNoShowBanPolicy(effective); err != nil {
				return err
			}
			if in.NoShowBanPolicy.Enabled != nil {
				args = append(args, *in.NoShowBanPolicy.Enabled)
				updates = append(updates, fmt.Sprintf("no_show_ban_enabled = $%d", len(args)))
			}
			if in.NoShowBanPolicy.DurationHours != nil {
				args = append(args, *in.NoShowBanPolicy.DurationHours)
				updates = append(updates, fmt.Sprintf("no_show_ban_duration_hours = $%d", len(args)))
			}
			if in.NoShowBanPolicy.Level != nil {
				args = append(args, *in.NoShowBanPolicy.Level)
				updates = append(updates, fmt.Sprintf("no_show_ban_level = $%d", len(args)))
			}
		}
		if len(updates) > 0 {
			args = append(args, id)
			c, err = scanCampus(tx.QueryRow(
				fmt.Sprintf("UPDATE clinic_campus SET %s WHERE id = $%d RETURNING %s", strings.Join(updates, ", "), len(args), campusColumns),
				args...,
			))
			if err != nil {
				if database.IsUniqueViolation(err) {
					return ErrCampusNameTaken
				}
				return fmt.Errorf("update campus %d: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		return roommodel.ClinicCampus{}, err
	}
	return c, nil
}

func defaultNoShowBanPolicy(patch *NoShowBanPolicyPatch) roommodel.NoShowBanPolicy {
	policy := roommodel.DefaultNoShowBanPolicy()
	applyNoShowBanPolicyPatch(&policy, patch)
	return policy
}

func applyNoShowBanPolicyPatch(policy *roommodel.NoShowBanPolicy, patch *NoShowBanPolicyPatch) {
	if patch == nil {
		return
	}
	if patch.Enabled != nil {
		policy.Enabled = *patch.Enabled
	}
	if patch.DurationHours != nil {
		policy.DurationHours = *patch.DurationHours
	}
	if patch.Level != nil {
		policy.Level = *patch.Level
	}
}

func validateNoShowBanPolicy(policy roommodel.NoShowBanPolicy) error {
	if policy.DurationHours <= 0 || policy.DurationHours > roommodel.MaxNoShowBanDurationHours {
		return ErrCampusInvalidNoShowBanDuration
	}
	if !policy.Level.Valid() {
		return ErrCampusInvalidNoShowBanLevel
	}
	return nil
}

func (s *CampusService) Delete(id uint) error {
	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var existingID uint
		if err := tx.QueryRow("SELECT id FROM clinic_campus WHERE id = $1", id).Scan(&existingID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrCampusNotFound
			}
			return fmt.Errorf("get campus %d for delete: %w", id, err)
		}

		var rooms int64
		if err := tx.QueryRow("SELECT COUNT(*) FROM clinic_room WHERE campus_id = $1", id).Scan(&rooms); err != nil {
			return fmt.Errorf("count rooms for campus %d: %w", id, err)
		}
		if rooms > 0 {
			return ErrCampusInUse
		}
		if _, err := tx.Exec("DELETE FROM clinic_campus WHERE id = $1", id); err != nil {
			return fmt.Errorf("delete campus %d: %w", id, err)
		}
		return nil
	})
}
