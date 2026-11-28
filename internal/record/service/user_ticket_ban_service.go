package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
)

// UserTicketBanScope identifies whether a ban applies globally or to one campus.
type UserTicketBanScope string

const (
	UserTicketBanScopeGlobal UserTicketBanScope = "global"
	UserTicketBanScopeCampus UserTicketBanScope = "campus"
)

const (
	userTicketBanMaxUserIDLen = 50
	userTicketBanMaxReasonLen = 500
)

var (
	ErrUserTicketBanNotFound          = errors.New("user ticket ban not found")
	ErrUserTicketBanConflict          = errors.New("user ticket ban already exists")
	ErrUserTicketBanInvalidUserID     = errors.New("user_id must be between 1 and 50 characters")
	ErrUserTicketBanInvalidScope      = errors.New("scope must be global or campus")
	ErrUserTicketBanInvalidCampusID   = errors.New("campus_id is required for campus scope and must be omitted for global scope")
	ErrUserTicketBanCampusNotFound    = errors.New("campus not found")
	ErrUserTicketBanInvalidReason     = errors.New("reason must be between 1 and 500 characters")
	ErrUserTicketBanInvalidExpirateAt = errors.New("expirate_at must be a future RFC3339 timestamp")
)

const userTicketBanColumns = "id, user_id, campus_id, reason, expirate_at"

// UserTicketBanService owns ticket-ban CRUD and active-ban checks.
type UserTicketBanService struct {
	db *database.DB
}

func NewUserTicketBanService(db *database.DB) *UserTicketBanService {
	return &UserTicketBanService{db: db}
}

type CreateUserTicketBanInput struct {
	UserID     string
	Scope      UserTicketBanScope
	CampusID   *uint
	Reason     string
	ExpirateAt time.Time
}

type UpdateUserTicketBanInput struct {
	Reason     *string
	ExpirateAt *time.Time
}

type ListUserTicketBanFilter struct {
	UserID   string
	Scope    UserTicketBanScope
	CampusID *uint
	Active   *bool
	Page     int
	PageSize int
}

func scanUserTicketBan(row interface{ Scan(...any) error }) (recordmodel.ClinicUserTicketBan, error) {
	var ban recordmodel.ClinicUserTicketBan
	var campusID sql.NullInt64
	err := row.Scan(&ban.ID, &ban.UserID, &campusID, &ban.Reason, &ban.ExpirateAt)
	if campusID.Valid {
		id := uint(campusID.Int64)
		ban.CampusID = &id
	}
	return ban, err
}

func (s *UserTicketBanService) Create(in CreateUserTicketBanInput) (recordmodel.ClinicUserTicketBan, error) {
	userID, campusID, reason, expirateAt, err := s.validateCreate(in)
	if err != nil {
		return recordmodel.ClinicUserTicketBan{}, err
	}

	var ban recordmodel.ClinicUserTicketBan
	err = database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		if campusID != nil {
			var exists bool
			if err := tx.QueryRow(
				"SELECT EXISTS(SELECT 1 FROM clinic_campus WHERE id = $1)",
				*campusID,
			).Scan(&exists); err != nil {
				return fmt.Errorf("check campus %d: %w", *campusID, err)
			}
			if !exists {
				return ErrUserTicketBanCampusNotFound
			}
		}

		var existingID uint
		err := tx.QueryRow(
			`SELECT id FROM clinic_user_ticket_ban
			 WHERE user_id = $1
			   AND (($2 IS NULL AND campus_id IS NULL) OR campus_id = $2)
			 LIMIT 1`,
			userID, nullableUint(campusID),
		).Scan(&existingID)
		if err == nil {
			return ErrUserTicketBanConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check existing user ticket ban: %w", err)
		}

		ban, err = scanUserTicketBan(tx.QueryRow(
			`INSERT INTO clinic_user_ticket_ban (user_id, campus_id, reason, expirate_at)
			 VALUES ($1, $2, $3, $4)
			 RETURNING `+userTicketBanColumns,
			userID, nullableUint(campusID), reason, expirateAt,
		))
		if err != nil {
			if database.IsUniqueViolation(err) {
				return ErrUserTicketBanConflict
			}
			return fmt.Errorf("create user ticket ban: %w", err)
		}
		return nil
	})
	if err != nil {
		return recordmodel.ClinicUserTicketBan{}, err
	}
	return ban, nil
}

func (s *UserTicketBanService) validateCreate(in CreateUserTicketBanInput) (string, *uint, string, time.Time, error) {
	userID := strings.TrimSpace(in.UserID)
	if userID == "" || utf8.RuneCountInString(userID) > userTicketBanMaxUserIDLen {
		return "", nil, "", time.Time{}, ErrUserTicketBanInvalidUserID
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > userTicketBanMaxReasonLen {
		return "", nil, "", time.Time{}, ErrUserTicketBanInvalidReason
	}

	expirateAt := in.ExpirateAt.UTC()
	if in.ExpirateAt.IsZero() || !expirateAt.After(time.Now().UTC()) {
		return "", nil, "", time.Time{}, ErrUserTicketBanInvalidExpirateAt
	}

	campusID, err := validateUserTicketBanScope(in.Scope, in.CampusID)
	if err != nil {
		return "", nil, "", time.Time{}, err
	}
	return userID, campusID, reason, expirateAt, nil
}

func validateUserTicketBanScope(scope UserTicketBanScope, campusID *uint) (*uint, error) {
	switch scope {
	case UserTicketBanScopeGlobal:
		if campusID != nil {
			return nil, ErrUserTicketBanInvalidCampusID
		}
		return nil, nil
	case UserTicketBanScopeCampus:
		if campusID == nil || *campusID == 0 {
			return nil, ErrUserTicketBanInvalidCampusID
		}
		id := *campusID
		return &id, nil
	default:
		return nil, ErrUserTicketBanInvalidScope
	}
}

func (s *UserTicketBanService) GetByID(id uint) (recordmodel.ClinicUserTicketBan, error) {
	ban, err := scanUserTicketBan(s.db.QueryRow(
		"SELECT "+userTicketBanColumns+" FROM clinic_user_ticket_ban WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return recordmodel.ClinicUserTicketBan{}, ErrUserTicketBanNotFound
		}
		return recordmodel.ClinicUserTicketBan{}, fmt.Errorf("get user ticket ban %d: %w", id, err)
	}
	return ban, nil
}

func (s *UserTicketBanService) List(f ListUserTicketBanFilter) ([]recordmodel.ClinicUserTicketBan, int64, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.UserID != "" {
		args = append(args, f.UserID)
		where = append(where, fmt.Sprintf("user_id = $%d", len(args)))
	}
	switch f.Scope {
	case "":
	case UserTicketBanScopeGlobal:
		where = append(where, "campus_id IS NULL")
	case UserTicketBanScopeCampus:
		where = append(where, "campus_id IS NOT NULL")
	default:
		return nil, 0, ErrUserTicketBanInvalidScope
	}
	if f.CampusID != nil {
		args = append(args, *f.CampusID)
		where = append(where, fmt.Sprintf("campus_id = $%d", len(args)))
	}
	if f.Active != nil {
		args = append(args, time.Now().UTC())
		operator := ">"
		if !*f.Active {
			operator = "<="
		}
		where = append(where, fmt.Sprintf("expirate_at %s $%d", operator, len(args)))
	}
	predicate := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_user_ticket_ban"+predicate, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count user ticket bans: %w", err)
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize
	args = append(args, f.PageSize, offset)
	rows, err := s.db.Query(
		fmt.Sprintf(
			"SELECT %s FROM clinic_user_ticket_ban%s ORDER BY id ASC LIMIT $%d OFFSET $%d",
			userTicketBanColumns, predicate, len(args)-1, len(args),
		),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list user ticket bans: %w", err)
	}
	defer rows.Close()

	items := make([]recordmodel.ClinicUserTicketBan, 0)
	for rows.Next() {
		ban, err := scanUserTicketBan(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan user ticket ban: %w", err)
		}
		items = append(items, ban)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list user ticket bans: %w", err)
	}
	return items, total, nil
}

func (s *UserTicketBanService) Update(id uint, in UpdateUserTicketBanInput) (recordmodel.ClinicUserTicketBan, error) {
	var updated recordmodel.ClinicUserTicketBan
	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		current, err := scanUserTicketBan(tx.QueryRow(
			"SELECT "+userTicketBanColumns+" FROM clinic_user_ticket_ban WHERE id = $1",
			id,
		))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrUserTicketBanNotFound
			}
			return fmt.Errorf("get user ticket ban %d for update: %w", id, err)
		}

		var updates []string
		var args []any
		if in.Reason != nil {
			reason := strings.TrimSpace(*in.Reason)
			if reason == "" || utf8.RuneCountInString(reason) > userTicketBanMaxReasonLen {
				return ErrUserTicketBanInvalidReason
			}
			args = append(args, reason)
			updates = append(updates, fmt.Sprintf("reason = $%d", len(args)))
		}
		if in.ExpirateAt != nil {
			expirateAt := in.ExpirateAt.UTC()
			if in.ExpirateAt.IsZero() || !expirateAt.After(time.Now().UTC()) {
				return ErrUserTicketBanInvalidExpirateAt
			}
			args = append(args, expirateAt)
			updates = append(updates, fmt.Sprintf("expirate_at = $%d", len(args)))
		}
		if len(updates) == 0 {
			updated = current
			return nil
		}

		args = append(args, id)
		updated, err = scanUserTicketBan(tx.QueryRow(
			fmt.Sprintf(
				"UPDATE clinic_user_ticket_ban SET %s WHERE id = $%d RETURNING %s",
				strings.Join(updates, ", "), len(args), userTicketBanColumns,
			),
			args...,
		))
		if err != nil {
			return fmt.Errorf("update user ticket ban %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return recordmodel.ClinicUserTicketBan{}, err
	}
	return updated, nil
}

func (s *UserTicketBanService) Delete(id uint) error {
	result, err := s.db.Exec("DELETE FROM clinic_user_ticket_ban WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete user ticket ban %d: %w", id, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted user ticket ban %d: %w", id, err)
	}
	if deleted == 0 {
		return ErrUserTicketBanNotFound
	}
	return nil
}

// IsBanned reports whether userID has an active global ban or an active ban
// for campusID at the supplied time.
func (s *UserTicketBanService) IsBanned(userID string, campusID *uint, at time.Time) (bool, error) {
	if strings.TrimSpace(userID) == "" {
		return false, nil
	}
	var banned bool
	if err := s.db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM clinic_user_ticket_ban
			WHERE user_id = $1
			  AND expirate_at > $2
			  AND (campus_id IS NULL OR campus_id = $3)
		)`,
		userID, at.UTC(), nullableUint(campusID),
	).Scan(&banned); err != nil {
		return false, fmt.Errorf("check user ticket ban: %w", err)
	}
	return banned, nil
}
