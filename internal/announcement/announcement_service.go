package announcement

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/calendar"
	"clinic-backend/internal/database"
)

var (
	ErrAnnouncementNotFound         = errors.New("announcement not found")
	ErrAnnouncementInvalidTag       = errors.New("invalid announcement tag")
	ErrAnnouncementTOSAlreadyExists = errors.New("a terms-of-service announcement already exists")
)

// AnnouncementService contains the business logic for announcement CRUD.
type AnnouncementService struct {
	db *sql.DB
}

func NewAnnouncementService(db *sql.DB) *AnnouncementService {
	return &AnnouncementService{db: db}
}

// CreateAnnouncementInput carries the fields a caller may set on creation.
// Server-controlled fields (ID, CreatedTime, LastEditedTime) are not included.
// A nil Enabled means the announcement starts enabled.
type CreateAnnouncementInput struct {
	Title      string
	Content    string
	Tag        AnnouncementTag
	Enabled    *bool
	Brief      string
	ExpireDate time.Time
	Priority   uint
}

// UpdateAnnouncementInput uses pointers so omitted fields stay unchanged.
type UpdateAnnouncementInput struct {
	Title      *string
	Content    *string
	Tag        *AnnouncementTag
	Enabled    *bool
	Brief      *string
	ExpireDate *time.Time
	Priority   *uint
}

// ListAnnouncementFilter controls listing behavior.
type ListAnnouncementFilter struct {
	Tag        AnnouncementTag
	ActiveOnly bool
	Page       int
	PageSize   int
}

const announcementColumns = "id, title, content, tag, enabled, created_time, last_edited_time, expire_date, priority, brief"

type announcementScanner interface {
	Scan(dest ...any) error
}

func scanAnnouncement(row announcementScanner) (ClinicAnnouncement, error) {
	var a ClinicAnnouncement
	err := row.Scan(&a.ID, &a.Title, &a.Content, &a.Tag, &a.Enabled, &a.CreatedTime, &a.LastEditedTime, &a.ExpireDate, &a.Priority, &a.Brief)
	return a, err
}

func (s *AnnouncementService) Create(in CreateAnnouncementInput) (ClinicAnnouncement, error) {
	if in.Tag == "" {
		in.Tag = AnnouncementTagNormal
	}
	if !in.Tag.Valid() {
		return ClinicAnnouncement{}, ErrAnnouncementInvalidTag
	}
	if in.Tag == AnnouncementTagTOS {
		var exists bool
		if err := s.db.QueryRow("SELECT EXISTS (SELECT 1 FROM clinic_announcement WHERE tag = $1)", AnnouncementTagTOS).Scan(&exists); err != nil {
			return ClinicAnnouncement{}, fmt.Errorf("check existing tos: %w", err)
		}
		if exists {
			return ClinicAnnouncement{}, ErrAnnouncementTOSAlreadyExists
		}
	}

	now := time.Now().UTC()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	a, err := scanAnnouncement(s.db.QueryRow(`INSERT INTO clinic_announcement
		(title, content, tag, enabled, created_time, last_edited_time, expire_date, priority, brief)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+announcementColumns,
		in.Title, in.Content, in.Tag, enabled, now, now, calendar.DateInLocation(in.ExpireDate, nil), in.Priority, in.Brief))
	if err != nil {
		if in.Tag == AnnouncementTagTOS && database.IsUniqueViolation(err) {
			return ClinicAnnouncement{}, ErrAnnouncementTOSAlreadyExists
		}
		return ClinicAnnouncement{}, fmt.Errorf("create announcement: %w", err)
	}
	return a, nil
}

func (s *AnnouncementService) GetByID(id uint) (ClinicAnnouncement, error) {
	a, err := scanAnnouncement(s.db.QueryRow("SELECT "+announcementColumns+" FROM clinic_announcement WHERE id = $1", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ClinicAnnouncement{}, ErrAnnouncementNotFound
	}
	if err != nil {
		return ClinicAnnouncement{}, fmt.Errorf("get announcement %d: %w", id, err)
	}
	return a, nil
}

func (s *AnnouncementService) GetTOS() (ClinicAnnouncement, error) {
	a, err := scanAnnouncement(s.db.QueryRow("SELECT "+announcementColumns+" FROM clinic_announcement WHERE tag = $1 ORDER BY id LIMIT 1", AnnouncementTagTOS))
	if errors.Is(err, sql.ErrNoRows) {
		return ClinicAnnouncement{}, ErrAnnouncementNotFound
	}
	if err != nil {
		return ClinicAnnouncement{}, fmt.Errorf("get tos: %w", err)
	}
	return a, nil
}

func (s *AnnouncementService) List(f ListAnnouncementFilter) ([]ClinicAnnouncement, int64, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("tag = $%d", len(args)))
	}
	if f.ActiveOnly {
		args = append(args, true, AnnouncementTagTOS, calendar.DateInLocation(time.Now(), nil))
		where = append(where, fmt.Sprintf("enabled = $%d AND (tag = $%d OR expire_date >= $%d)", len(args)-2, len(args)-1, len(args)))
	}
	predicate := strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM clinic_announcement WHERE "+predicate, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count announcements: %w", err)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	query := fmt.Sprintf("SELECT %s FROM clinic_announcement WHERE %s ORDER BY priority DESC, created_time DESC LIMIT $%d OFFSET $%d", announcementColumns, predicate, len(args)-1, len(args))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list announcements: %w", err)
	}
	defer rows.Close()
	items := make([]ClinicAnnouncement, 0)
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan announcement: %w", err)
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list announcements: %w", err)
	}
	return items, total, nil
}

func (s *AnnouncementService) Update(id uint, in UpdateAnnouncementInput) (ClinicAnnouncement, error) {
	a, err := s.GetByID(id)
	if err != nil {
		return ClinicAnnouncement{}, err
	}
	if in.Tag != nil {
		if !in.Tag.Valid() {
			return ClinicAnnouncement{}, ErrAnnouncementInvalidTag
		}
		if *in.Tag == AnnouncementTagTOS && a.Tag != AnnouncementTagTOS {
			var exists bool
			if err := s.db.QueryRow("SELECT EXISTS (SELECT 1 FROM clinic_announcement WHERE tag = $1 AND id != $2)", AnnouncementTagTOS, id).Scan(&exists); err != nil {
				return ClinicAnnouncement{}, fmt.Errorf("check existing tos: %w", err)
			}
			if exists {
				return ClinicAnnouncement{}, ErrAnnouncementTOSAlreadyExists
			}
		}
	}
	var updates []string
	var args []any
	add := func(column string, value any) {
		args = append(args, value)
		updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if in.Title != nil {
		add("title", *in.Title)
	}
	if in.Content != nil {
		add("content", *in.Content)
	}
	if in.Tag != nil {
		add("tag", *in.Tag)
	}
	if in.Enabled != nil {
		add("enabled", *in.Enabled)
	}
	if in.Brief != nil {
		add("brief", *in.Brief)
	}
	if in.ExpireDate != nil {
		add("expire_date", calendar.DateInLocation(*in.ExpireDate, nil))
	}
	if in.Priority != nil {
		add("priority", *in.Priority)
	}
	if len(updates) == 0 {
		return a, nil
	}
	add("last_edited_time", time.Now().UTC())
	args = append(args, id)
	query := fmt.Sprintf("UPDATE clinic_announcement SET %s WHERE id = $%d RETURNING %s", strings.Join(updates, ", "), len(args), announcementColumns)
	a, err = scanAnnouncement(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return ClinicAnnouncement{}, ErrAnnouncementNotFound
	}
	if err != nil {
		if in.Tag != nil && *in.Tag == AnnouncementTagTOS && database.IsUniqueViolation(err) {
			return ClinicAnnouncement{}, ErrAnnouncementTOSAlreadyExists
		}
		return ClinicAnnouncement{}, fmt.Errorf("update announcement %d: %w", id, err)
	}
	return a, nil
}

func (s *AnnouncementService) Delete(id uint) error {
	res, err := s.db.Exec("DELETE FROM clinic_announcement WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete announcement %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted announcement %d: %w", id, err)
	}
	if n == 0 {
		return ErrAnnouncementNotFound
	}
	return nil
}
