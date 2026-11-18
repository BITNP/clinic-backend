package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clinic-backend/internal/calendar"
	recordmodel "clinic-backend/internal/record/model"
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
	db := s.db.WithContext(ctx)
	counts := db.Model(&recordmodel.ClinicRecord{}).
		Select("room, COUNT(*) AS ticket_count").
		Where("appointment_time = ? AND status NOT IN ?", date, schedulingservice.TicketedCountExcludeStatuses(date, today)).Group("room")
	q := db.Table("clinic_room AS r").Select("r.id").
		Joins("JOIN clinic_service_date AS d ON d.room_id = r.id AND d.date = ?", date).
		Joins("LEFT JOIN (?) AS tickets ON tickets.room = r.id", counts).
		Where("r.campus_id = ? AND r.enabled = ?", campus.ID, true).
		Where("COALESCE(tickets.ticket_count, 0) < d.capacity")
	if f.Name != "" {
		q = q.Where("r.name LIKE ?", "%"+f.Name+"%")
	}
	if f.Type != nil {
		q = q.Where("r.type = ?", *f.Type)
	}
	if campus.SelectionPolicy == roommodel.SelectionPolicyBalanced {
		q = q.Order("COALESCE(tickets.ticket_count, 0) ASC")
	}
	var candidates []struct{ ID uint }
	if err := q.Order("r.id ASC").Limit(1).Find(&candidates).Error; err != nil {
		return nil, fmt.Errorf("select preferred room for campus %d: %w", campus.ID, err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	return &candidates[0].ID, nil
}
