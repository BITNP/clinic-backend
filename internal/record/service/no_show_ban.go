package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
)

const noShowBanReason = "预约后未到访诊所，已暂停预约"

// ErrNoShowBanPolicyInvalid indicates corrupt or invalid campus policy data.
var ErrNoShowBanPolicyInvalid = errors.New("invalid campus no-show ban policy")

// NoShowBanActionSnapshot describes the exact ban mutation made by a manual
// no-show action so it can be transactionally undone.
type NoShowBanActionSnapshot struct {
	BanID              uint       `json:"ban_id"`
	Created            bool       `json:"created"`
	PreviousExpirateAt *time.Time `json:"previous_expirate_at,omitempty"`
	PreviousReason     *string    `json:"previous_reason,omitempty"`
	AppliedExpirateAt  time.Time  `json:"applied_expirate_at"`
	AppliedReason      string     `json:"applied_reason"`
}

type noShowBanTarget struct {
	CampusID *uint
}

func (s *AdminRecordService) applyNoShowBanTx(
	ctx context.Context,
	tx *sql.Tx,
	record recordmodel.ClinicRecord,
	now time.Time,
) (*NoShowBanActionSnapshot, error) {
	userID := strings.TrimSpace(record.User)
	if userID == "" {
		return nil, nil
	}

	var campusID uint
	var policy roommodel.NoShowBanPolicy
	err := tx.QueryRowContext(
		ctx,
		`SELECT c.id, c.no_show_ban_enabled, c.no_show_ban_duration_hours, c.no_show_ban_level
		 FROM clinic_room r
		 JOIN clinic_campus c ON c.id = r.campus_id
		 WHERE r.id = $1`,
		record.RoomID,
	).Scan(&campusID, &policy.Enabled, &policy.DurationHours, &policy.Level)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load no-show ban policy for record %d: %w", record.ID, err)
	}
	if !policy.Enabled {
		return nil, nil
	}
	if policy.DurationHours <= 0 ||
		policy.DurationHours > roommodel.MaxNoShowBanDurationHours ||
		!policy.Level.Valid() {
		return nil, fmt.Errorf("%w: campus %d", ErrNoShowBanPolicyInvalid, campusID)
	}

	target := noShowBanTarget{}
	if policy.Level == roommodel.NoShowBanLevelCampus {
		target.CampusID = &campusID
	}
	expirateAt := now.UTC().Add(time.Duration(policy.DurationHours) * time.Hour)
	return s.applyNoShowUserBanTx(ctx, tx, userID, target, expirateAt)
}

func (s *AdminRecordService) applyNoShowUserBanTx(
	ctx context.Context,
	tx *sql.Tx,
	userID string,
	target noShowBanTarget,
	expirateAt time.Time,
) (*NoShowBanActionSnapshot, error) {
	ban, err := s.loadUserTicketBanForUpdateTx(ctx, tx, userID, target.CampusID)
	if errors.Is(err, sql.ErrNoRows) {
		inserted, insertErr := scanUserTicketBan(tx.QueryRowContext(
			ctx,
			`INSERT INTO clinic_user_ticket_ban (user_id, campus_id, reason, expirate_at)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT DO NOTHING
			 RETURNING `+userTicketBanColumns,
			userID, nullableUint(target.CampusID), noShowBanReason, expirateAt,
		))
		if insertErr == nil {
			return &NoShowBanActionSnapshot{
				BanID:             inserted.ID,
				Created:           true,
				AppliedExpirateAt: inserted.ExpirateAt,
				AppliedReason:     inserted.Reason,
			}, nil
		}
		if !errors.Is(insertErr, sql.ErrNoRows) {
			return nil, fmt.Errorf("create no-show ban for user %s: %w", userID, insertErr)
		}
		ban, err = s.loadUserTicketBanForUpdateTx(ctx, tx, userID, target.CampusID)
		if err != nil {
			return nil, fmt.Errorf("reload no-show ban for user %s: %w", userID, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("load no-show ban for user %s: %w", userID, err)
	}

	if !ban.ExpirateAt.Before(expirateAt) {
		return nil, nil
	}
	previousExpirateAt := ban.ExpirateAt
	previousReason := ban.Reason
	updated, err := scanUserTicketBan(tx.QueryRowContext(
		ctx,
		`UPDATE clinic_user_ticket_ban
		 SET expirate_at = $1, reason = $2
		 WHERE id = $3
		 RETURNING `+userTicketBanColumns,
		expirateAt, noShowBanReason, ban.ID,
	))
	if err != nil {
		return nil, fmt.Errorf("extend no-show ban %d: %w", ban.ID, err)
	}
	return &NoShowBanActionSnapshot{
		BanID:              updated.ID,
		PreviousExpirateAt: &previousExpirateAt,
		PreviousReason:     &previousReason,
		AppliedExpirateAt:  updated.ExpirateAt,
		AppliedReason:      updated.Reason,
	}, nil
}

func (s *AdminRecordService) loadUserTicketBanForUpdateTx(
	ctx context.Context,
	tx *sql.Tx,
	userID string,
	campusID *uint,
) (recordmodel.ClinicUserTicketBan, error) {
	query := "SELECT " + userTicketBanColumns + " FROM clinic_user_ticket_ban WHERE user_id = $1 AND campus_id IS NULL"
	args := []any{userID}
	if campusID != nil {
		query = "SELECT " + userTicketBanColumns + " FROM clinic_user_ticket_ban WHERE user_id = $1 AND campus_id = $2"
		args = append(args, *campusID)
	}
	if s.db.Driver == database.DriverPostgres {
		query += " FOR UPDATE"
	}
	return scanUserTicketBan(tx.QueryRowContext(ctx, query, args...))
}

func restoreNoShowBanTx(
	ctx context.Context,
	tx *sql.Tx,
	driver database.Driver,
	snapshot *NoShowBanActionSnapshot,
) error {
	if snapshot == nil {
		return nil
	}
	query := "SELECT " + userTicketBanColumns + " FROM clinic_user_ticket_ban WHERE id = $1"
	if driver == database.DriverPostgres {
		query += " FOR UPDATE"
	}
	current, err := scanUserTicketBan(tx.QueryRowContext(ctx, query, snapshot.BanID))
	if errors.Is(err, sql.ErrNoRows) {
		if snapshot.Created {
			return nil
		}
		return fmt.Errorf("restore no-show ban %d: %w", snapshot.BanID, ErrRevertSuperseded)
	}
	if err != nil {
		return fmt.Errorf("load no-show ban %d for revert: %w", snapshot.BanID, err)
	}
	if !current.ExpirateAt.Equal(snapshot.AppliedExpirateAt) ||
		current.Reason != snapshot.AppliedReason {
		return fmt.Errorf("restore no-show ban %d: %w", snapshot.BanID, ErrRevertSuperseded)
	}
	if snapshot.Created {
		if _, err := tx.ExecContext(ctx, "DELETE FROM clinic_user_ticket_ban WHERE id = $1", snapshot.BanID); err != nil {
			return fmt.Errorf("delete no-show ban %d: %w", snapshot.BanID, err)
		}
		return nil
	}
	if snapshot.PreviousExpirateAt == nil || snapshot.PreviousReason == nil {
		return fmt.Errorf("restore no-show ban %d: missing previous state", snapshot.BanID)
	}
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE clinic_user_ticket_ban SET expirate_at = $1, reason = $2 WHERE id = $3",
		*snapshot.PreviousExpirateAt,
		*snapshot.PreviousReason,
		snapshot.BanID,
	); err != nil {
		return fmt.Errorf("restore no-show ban %d: %w", snapshot.BanID, err)
	}
	return nil
}
