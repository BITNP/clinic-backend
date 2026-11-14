package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
)

// ErrRoomNotFound is returned when no room matches the given id.
var ErrRoomNotFound = errors.New("room not found")

// ErrRoomNameTaken is returned on create/update when the room name already exists.
var ErrRoomNameTaken = errors.New("room name already taken")

// ErrRoomInUse is returned when a room cannot be deleted because
// repair records or service dates still reference it.
var ErrRoomInUse = errors.New("room has existing service dates or records and cannot be deleted")

// ErrRoomHasUnfinishedRecords is returned when a room cannot be disabled
// because it still has tickets in an unfinished (working) state.
var ErrRoomHasUnfinishedRecords = errors.New("room has unfinished tickets and cannot be disabled")

// ErrRoomCampusRequired is returned when a room is created without a campus.
var ErrRoomCampusRequired = errors.New("campus_id is required")

// ErrRoomCampusNotFound is returned when the referenced campus does not exist.
var ErrRoomCampusNotFound = errors.New("campus does not exist")

// ErrRoomInvalidType is returned when the room type is not one of the supported values.
var ErrRoomInvalidType = errors.New("invalid room type")

// RoomService contains the business logic for clinic room CRUD.
type RoomService struct {
	db *database.DB
}

func NewRoomService(db *database.DB) *RoomService {
	return &RoomService{db: db}
}

const roomColumns = "id, name, address, enabled, campus_id, type"

func scanRoom(row interface{ Scan(...any) error }) (roommodel.ClinicRoom, error) {
	var r roommodel.ClinicRoom
	var campusID sql.NullInt64
	err := row.Scan(&r.ID, &r.Name, &r.Address, &r.Enabled, &campusID, &r.Type)
	if campusID.Valid {
		id := uint(campusID.Int64)
		r.CampusID = &id
	}
	return r, err
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// CreateRoomInput carries the fields a caller may set on creation.
// Server-controlled fields (ID) are not included.
type CreateRoomInput struct {
	Name     string
	Address  string
	Enabled  *bool // nil defaults to true
	CampusID uint
	Type     roommodel.RoomType // empty defaults to normal
}

// UpdateRoomInput uses pointers so omitted fields stay unchanged.
type UpdateRoomInput struct {
	Name     *string
	Address  *string
	Enabled  *bool
	CampusID *uint
	Type     *roommodel.RoomType
}

// ListRoomFilter controls listing behavior.
type ListRoomFilter struct {
	Name        string // substring match (ILIKE/LIKE)
	EnabledOnly bool   // true: only enabled rooms (used by client routes)
	CampusID    *uint
	Type        *roommodel.RoomType
	Page        int
	PageSize    int
}

func (s *RoomService) Create(in CreateRoomInput) (roommodel.ClinicRoom, error) {
	if in.CampusID == 0 {
		return roommodel.ClinicRoom{}, ErrRoomCampusRequired
	}
	campus, err := lookupCampus(s.db.DB, in.CampusID)
	if err != nil {
		return roommodel.ClinicRoom{}, err
	}

	roomType := in.Type
	if roomType == "" {
		roomType = roommodel.RoomTypeNormal
	}
	if !roomType.Valid() {
		return roommodel.ClinicRoom{}, ErrRoomInvalidType
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	campusID := in.CampusID
	r, err := scanRoom(s.db.QueryRow(
		`INSERT INTO clinic_room (name, address, enabled, campus_id, type)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+roomColumns,
		in.Name, in.Address, enabled, campusID, roomType,
	))
	if err != nil {
		if database.IsUniqueViolation(err) {
			return roommodel.ClinicRoom{}, ErrRoomNameTaken
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("create room: %w", err)
	}
	r.Campus = &campus
	return r, nil
}

func (s *RoomService) GetByID(id uint) (roommodel.ClinicRoom, error) {
	r, err := scanRoom(s.db.QueryRow(
		"SELECT "+roomColumns+" FROM clinic_room WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roommodel.ClinicRoom{}, ErrRoomNotFound
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("get room %d: %w", id, err)
	}
	if r.CampusID != nil {
		campus, err := lookupCampus(s.db.DB, *r.CampusID)
		if err != nil {
			return roommodel.ClinicRoom{}, err
		}
		r.Campus = &campus
	}
	return r, nil
}

func (s *RoomService) GetByName(name string) (roommodel.ClinicRoom, error) {
	r, err := scanRoom(s.db.QueryRow(
		"SELECT "+roomColumns+" FROM clinic_room WHERE name = $1 ORDER BY id LIMIT 1",
		name,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roommodel.ClinicRoom{}, ErrRoomNotFound
		}
		return roommodel.ClinicRoom{}, fmt.Errorf("get room by name %q: %w", name, err)
	}
	if r.CampusID != nil {
		campus, err := lookupCampus(s.db.DB, *r.CampusID)
		if err != nil {
			return roommodel.ClinicRoom{}, err
		}
		r.Campus = &campus
	}
	return r, nil
}

// lookupCampus resolves a room's campus and maps missing rows to room errors.
func lookupCampus(db rowQuerier, id uint) (roommodel.ClinicCampus, error) {
	if id == 0 {
		return roommodel.ClinicCampus{}, ErrRoomCampusRequired
	}
	c, err := scanCampus(db.QueryRow(
		"SELECT "+campusColumns+" FROM clinic_campus WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return roommodel.ClinicCampus{}, ErrRoomCampusNotFound
		}
		return roommodel.ClinicCampus{}, fmt.Errorf("get campus %d: %w", id, err)
	}
	return c, nil
}

func (s *RoomService) List(f ListRoomFilter) ([]roommodel.ClinicRoom, int64, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.Name != "" {
		args = append(args, "%"+f.Name+"%")
		where = append(where, fmt.Sprintf("name LIKE $%d", len(args)))
	}
	if f.EnabledOnly {
		args = append(args, true)
		where = append(where, fmt.Sprintf("enabled = $%d", len(args)))
	}
	if f.CampusID != nil {
		args = append(args, *f.CampusID)
		where = append(where, fmt.Sprintf("campus_id = $%d", len(args)))
	}
	if f.Type != nil {
		args = append(args, *f.Type)
		where = append(where, fmt.Sprintf("type = $%d", len(args)))
	}
	predicate := " WHERE " + joinAnd(where)

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_room"+predicate, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count rooms: %w", err)
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
		"SELECT %s FROM clinic_room%s ORDER BY id ASC LIMIT $%d OFFSET $%d",
		roomColumns, predicate, len(args)-1, len(args),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list rooms: %w", err)
	}
	defer rows.Close()
	items := make([]roommodel.ClinicRoom, 0)
	for rows.Next() {
		r, err := scanRoom(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan room: %w", err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list rooms: %w", err)
	}
	if err := loadRoomCampuses(s.db, items); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *RoomService) Update(id uint, in UpdateRoomInput) (roommodel.ClinicRoom, error) {
	var r roommodel.ClinicRoom
	err := database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		var err error
		r, err = scanRoom(tx.QueryRow(
			"SELECT "+roomColumns+" FROM clinic_room WHERE id = $1",
			id,
		))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrRoomNotFound
			}
			return fmt.Errorf("get room %d for update: %w", id, err)
		}

		// Disabling a room is refused while it still has unfinished tickets.
		if in.Enabled != nil && !*in.Enabled {
			unfinished, err := hasUnfinishedRecords(tx, id)
			if err != nil {
				return err
			}
			if unfinished {
				return ErrRoomHasUnfinishedRecords
			}
		}

		var updates []string
		var args []any
		add := func(column string, value any) {
			args = append(args, value)
			updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
		}
		if in.Name != nil {
			add("name", *in.Name)
		}
		if in.Address != nil {
			add("address", *in.Address)
		}
		if in.Enabled != nil {
			add("enabled", *in.Enabled)
		}
		if in.CampusID != nil {
			if _, err := lookupCampus(tx, *in.CampusID); err != nil {
				return err
			}
			add("campus_id", *in.CampusID)
		}
		if in.Type != nil {
			if !in.Type.Valid() {
				return ErrRoomInvalidType
			}
			add("type", *in.Type)
		}

		if len(updates) > 0 {
			args = append(args, id)
			query := fmt.Sprintf(
				"UPDATE clinic_room SET %s WHERE id = $%d",
				strings.Join(updates, ", "), len(args),
			)
			if _, err := tx.Exec(query, args...); err != nil {
				if database.IsUniqueViolation(err) {
					return ErrRoomNameTaken
				}
				return fmt.Errorf("update room %d: %w", id, err)
			}
			r, err = scanRoom(tx.QueryRow(
				"SELECT "+roomColumns+" FROM clinic_room WHERE id = $1",
				id,
			))
			if err != nil {
				return fmt.Errorf("reload room %d: %w", id, err)
			}
		}

		if r.CampusID != nil {
			campus, err := lookupCampus(tx, *r.CampusID)
			if err != nil {
				return fmt.Errorf("reload room %d: %w", id, err)
			}
			r.Campus = &campus
		}
		return nil
	})
	if err != nil {
		return roommodel.ClinicRoom{}, err
	}
	return r, nil
}

func (s *RoomService) Delete(id uint) error {
	if _, err := scanRoom(s.db.QueryRow(
		"SELECT "+roomColumns+" FROM clinic_room WHERE id = $1",
		id,
	)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRoomNotFound
		}
		return fmt.Errorf("get room %d for delete: %w", id, err)
	}

	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		// A room with unfinished tickets cannot be removed.
		unfinished, err := hasUnfinishedRecords(tx, id)
		if err != nil {
			return err
		}
		if unfinished {
			return ErrRoomHasUnfinishedRecords
		}

		inUse, err := isRoomInUse(tx, id)
		if err != nil {
			return err
		}
		if inUse {
			return ErrRoomInUse
		}
		if _, err := tx.Exec("DELETE FROM clinic_room WHERE id = $1", id); err != nil {
			return fmt.Errorf("delete room %d: %w", id, err)
		}
		return nil
	})
}

// hasUnfinishedRecords reports whether any ticket referencing the room is still
// in an unfinished (working) state. Shared by the room disable/remove guards.
func hasUnfinishedRecords(tx *sql.Tx, roomID uint) (bool, error) {
	statuses := recordmodel.WorkingStatuses()
	args := make([]any, 0, len(statuses)+1)
	args = append(args, roomID)
	for _, status := range statuses {
		args = append(args, status)
	}
	var n int64
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM clinic_record WHERE room = $1 AND status IN (%s)",
		database.Placeholders(2, len(statuses)),
	)
	if err := tx.QueryRow(query, args...).Scan(&n); err != nil {
		return false, fmt.Errorf("count unfinished records for room %d: %w", roomID, err)
	}
	return n > 0, nil
}

// isRoomInUse reports whether any service date or repair record references the room.
func isRoomInUse(tx *sql.Tx, roomID uint) (bool, error) {
	var dates int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM clinic_service_date WHERE room_id = $1", roomID).Scan(&dates); err != nil {
		return false, fmt.Errorf("count service dates for room %d: %w", roomID, err)
	}
	if dates > 0 {
		return true, nil
	}

	var records int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM clinic_record WHERE room = $1", roomID).Scan(&records); err != nil {
		return false, fmt.Errorf("count records for room %d: %w", roomID, err)
	}
	return records > 0, nil
}

func loadRoomCampuses(db rowsQuerier, rooms []roommodel.ClinicRoom) error {
	ids := make([]uint, 0, len(rooms))
	seen := make(map[uint]struct{}, len(rooms))
	for _, room := range rooms {
		if room.CampusID == nil {
			continue
		}
		id := *room.CampusID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := "SELECT " + campusColumns + " FROM clinic_campus WHERE id IN (" + database.Placeholders(1, len(ids)) + ")"
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("load room campuses: %w", err)
	}
	defer rows.Close()
	campuses := make(map[uint]roommodel.ClinicCampus, len(ids))
	for rows.Next() {
		campus, err := scanCampus(rows)
		if err != nil {
			return fmt.Errorf("scan room campus: %w", err)
		}
		campuses[campus.ID] = campus
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load room campuses: %w", err)
	}
	for i := range rooms {
		if rooms[i].CampusID == nil {
			continue
		}
		campus, ok := campuses[*rooms[i].CampusID]
		if !ok {
			continue
		}
		rooms[i].Campus = &campus
	}
	return nil
}
