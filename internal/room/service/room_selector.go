package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
	roommodel "clinic-backend/internal/room/model"
	schedulingservice "clinic-backend/internal/scheduling/service"
)

// ErrSelectionDate indicates a missing or past booking date.
var ErrSelectionDate = errors.New("date is required and must not be in the past")

// PreferredRoom recommends an enabled room with remaining capacity on date.
// date is a calendar date represented at UTC midnight, as stored by booking.
// Selection respects name/type filters, but ignores pagination and EnabledOnly:
// disabled rooms are never eligible. A nil result means no room is available.
// This is advisory; it does not reserve capacity.
func (s *RoomService) PreferredRoom(ctx context.Context, campus roommodel.ClinicCampus, date time.Time, f ListRoomFilter) (*uint, error) {
	today := calendar.DateInLocation(time.Now(), s.loc)
	if date.IsZero() || date.Before(today) {
		return nil, ErrSelectionDate
	}
	if !campus.SelectionPolicy.Valid() {
		return nil, ErrCampusInvalidPolicy
	}
	excluded := schedulingservice.TicketedCountExcludeStatuses(date, today)
	args := []any{date}
	for _, status := range excluded {
		args = append(args, status)
	}
	query := `SELECT r.id FROM clinic_room r
		JOIN clinic_service_date d ON d.room_id = r.id AND d.date = $1
		LEFT JOIN (SELECT room, COUNT(*) AS ticket_count FROM clinic_record
		WHERE appointment_time = $1 AND status NOT IN (` + database.Placeholders(2, len(excluded)) + `) GROUP BY room) tickets ON tickets.room = r.id`
	args = append(args, campus.ID, true)
	query += fmt.Sprintf(" WHERE r.campus_id = $%d AND r.enabled = $%d AND COALESCE(tickets.ticket_count, 0) < d.capacity", len(args)-1, len(args))
	if f.Name != "" {
		args = append(args, "%"+f.Name+"%")
		query += fmt.Sprintf(" AND r.name LIKE $%d", len(args))
	}
	if f.Type != nil {
		args = append(args, *f.Type)
		query += fmt.Sprintf(" AND r.type = $%d", len(args))
	}
	query += " ORDER BY "
	if campus.SelectionPolicy == roommodel.SelectionPolicyBalanced {
		query += "COALESCE(tickets.ticket_count, 0) ASC, "
	}
	query += "r.id ASC LIMIT 1"
	var id uint
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select preferred room for campus %d: %w", campus.ID, err)
	}
	return &id, nil
}
