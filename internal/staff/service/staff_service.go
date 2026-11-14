package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"clinic-backend/internal/database"
	staffmodel "clinic-backend/internal/staff/model"
)

var (
	ErrStaffNotFound = errors.New("staff not found")
	ErrStaffConflict = errors.New("staff account already exists")
)

type StaffService struct {
	db *database.DB
}

func NewStaffService(db *database.DB) *StaffService {
	return &StaffService{db: db}
}

type CreateStaffInput struct {
	AccountID string
	Realname  string
	Email     string
}

type UpdateStaffInput struct {
	Realname *string
	Email    *string
	QQNumber *string
}

type StaffListItem struct {
	ID           int    `json:"id"`
	AccountID    string `json:"account_id"`
	Realname     string `json:"realname"`
	Email        string `json:"email"`
	QQNumber     string `json:"qq_number"`
	Role         string `json:"role"`
	HandledCount int    `json:"handled_count"`
	WorkYears    []int  `json:"work_years"`
}

const staffColumns = "id, account_id, realname, email, qq_number, role, version"

func scanStaff(row interface{ Scan(...any) error }) (staffmodel.ClinicStaff, error) {
	var staff staffmodel.ClinicStaff
	var realname, email sql.NullString
	err := row.Scan(&staff.ID, &staff.AccountID, &realname, &email, &staff.QQNumber, &staff.Role, &staff.Version)
	staff.Realname = realname.String
	staff.Email = email.String
	return staff, err
}

// GetOrCreateByAccountID resolves a staff member by account ID and applies
// identity fields supplied by the identity provider. Empty values are ignored
// so a missing claim never clears stored data.
func (s *StaffService) GetOrCreateByAccountID(accountID, realname, email, qqNumber string) (staffmodel.ClinicStaff, error) {
	staff, err := scanStaff(s.db.QueryRow(
		"SELECT "+staffColumns+" FROM clinic_staff WHERE account_id = $1",
		accountID,
	))
	if err == nil {
		var updates []string
		var args []any
		add := func(column string, value string) {
			args = append(args, value)
			updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
		}
		if realname != "" && staff.Realname != realname {
			add("realname", realname)
		}
		if email != "" && staff.Email != email {
			add("email", email)
		}
		if qqNumber != "" && staff.QQNumber != qqNumber {
			add("qq_number", qqNumber)
		}
		if len(updates) == 0 {
			return staff, nil
		}
		args = append(args, staff.ID)
		staff, err = scanStaff(s.db.QueryRow(
			"UPDATE clinic_staff SET "+strings.Join(updates, ", ")+" WHERE id = $"+fmt.Sprint(len(args))+" RETURNING "+staffColumns,
			args...,
		))
		if err != nil {
			return staffmodel.ClinicStaff{}, fmt.Errorf("update staff %s: %w", accountID, err)
		}
		return staff, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return staffmodel.ClinicStaff{}, fmt.Errorf("lookup staff %s: %w", accountID, err)
	}

	staff, err = scanStaff(s.db.QueryRow(
		`INSERT INTO clinic_staff (account_id, realname, email, qq_number, role, version)
		 VALUES ($1, $2, $3, $4, '', 0) RETURNING `+staffColumns,
		accountID, realname, email, qqNumber,
	))
	if err != nil {
		if database.IsUniqueViolation(err) {
			return staffmodel.ClinicStaff{}, ErrStaffConflict
		}
		return staffmodel.ClinicStaff{}, fmt.Errorf("create staff %s: %w", accountID, err)
	}
	return staff, nil
}

func (s *StaffService) GetByID(id int) (staffmodel.ClinicStaff, error) {
	staff, err := scanStaff(s.db.QueryRow(
		"SELECT "+staffColumns+" FROM clinic_staff WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return staffmodel.ClinicStaff{}, ErrStaffNotFound
		}
		return staffmodel.ClinicStaff{}, fmt.Errorf("get staff %d: %w", id, err)
	}
	return staff, nil
}

func (s *StaffService) List() ([]StaffListItem, error) {
	rows, err := s.db.Query("SELECT " + staffColumns + " FROM clinic_staff ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	defer rows.Close()

	staff := make([]staffmodel.ClinicStaff, 0)
	for rows.Next() {
		st, err := scanStaff(rows)
		if err != nil {
			return nil, fmt.Errorf("scan staff: %w", err)
		}
		staff = append(staff, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	if len(staff) == 0 {
		return nil, nil
	}

	staffIDs := make([]int, len(staff))
	for i, st := range staff {
		staffIDs[i] = st.ID
	}

	countMap, err := s.loadHandledCounts(staffIDs)
	if err != nil {
		return nil, err
	}
	yearMap, err := s.loadWorkYears(staffIDs)
	if err != nil {
		return nil, err
	}

	result := make([]StaffListItem, 0, len(staff))
	for _, st := range staff {
		result = append(result, StaffListItem{
			ID:           st.ID,
			AccountID:    st.AccountID,
			Realname:     st.Realname,
			Email:        st.Email,
			QQNumber:     st.QQNumber,
			Role:         st.Role,
			HandledCount: countMap[st.ID],
			WorkYears:    yearMap[st.ID],
		})
	}
	return result, nil
}

func (s *StaffService) loadHandledCounts(staffIDs []int) (map[int]int, error) {
	args := make([]any, len(staffIDs))
	for i, id := range staffIDs {
		args[i] = id
	}
	query := fmt.Sprintf(
		"SELECT worker, COUNT(*) FROM clinic_record_worker WHERE worker IN (%s) GROUP BY worker",
		database.Placeholders(1, len(staffIDs)),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("load handled counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[int]int, len(staffIDs))
	for rows.Next() {
		var workerID, count int
		if err := rows.Scan(&workerID, &count); err != nil {
			return nil, fmt.Errorf("scan handled count: %w", err)
		}
		counts[workerID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load handled counts: %w", err)
	}
	return counts, nil
}

func (s *StaffService) loadWorkYears(staffIDs []int) (map[int][]int, error) {
	args := make([]any, len(staffIDs))
	for i, id := range staffIDs {
		args[i] = id
	}
	query := fmt.Sprintf(
		"SELECT staff_id, work_year FROM clinic_staff_workyear WHERE staff_id IN (%s) ORDER BY work_year DESC",
		database.Placeholders(1, len(staffIDs)),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("load work years: %w", err)
	}
	defer rows.Close()

	years := make(map[int][]int, len(staffIDs))
	for rows.Next() {
		var staffID, year int
		if err := rows.Scan(&staffID, &year); err != nil {
			return nil, fmt.Errorf("scan work year: %w", err)
		}
		years[staffID] = append(years[staffID], year)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load work years: %w", err)
	}
	return years, nil
}

func (s *StaffService) IsValidForDate(staffID int, date time.Time) (bool, error) {
	var count int64
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM clinic_staff_workyear WHERE staff_id = $1 AND work_year = $2",
		staffID, date.Year(),
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check work year for staff %d: %w", staffID, err)
	}
	return count > 0, nil
}

func (s *StaffService) ListValidForYear(year int) ([]StaffListItem, error) {
	rows, err := s.db.Query("SELECT staff_id FROM clinic_staff_workyear WHERE work_year = $1", year)
	if err != nil {
		return nil, fmt.Errorf("list work year %d: %w", year, err)
	}
	defer rows.Close()

	staffIDs := make([]int, 0)
	for rows.Next() {
		var staffID int
		if err := rows.Scan(&staffID); err != nil {
			return nil, fmt.Errorf("scan work year %d: %w", year, err)
		}
		staffIDs = append(staffIDs, staffID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list work year %d: %w", year, err)
	}
	if len(staffIDs) == 0 {
		return []StaffListItem{}, nil
	}

	args := make([]any, len(staffIDs))
	for i, id := range staffIDs {
		args[i] = id
	}
	staffRows, err := s.db.Query(
		"SELECT "+staffColumns+" FROM clinic_staff WHERE id IN ("+database.Placeholders(1, len(staffIDs))+") ORDER BY id ASC",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("load staff for year %d: %w", year, err)
	}
	defer staffRows.Close()

	staff := make([]staffmodel.ClinicStaff, 0, len(staffIDs))
	for staffRows.Next() {
		st, err := scanStaff(staffRows)
		if err != nil {
			return nil, fmt.Errorf("scan staff for year %d: %w", year, err)
		}
		staff = append(staff, st)
	}
	if err := staffRows.Err(); err != nil {
		return nil, fmt.Errorf("load staff for year %d: %w", year, err)
	}

	yearMap, err := s.loadWorkYears(staffIDs)
	if err != nil {
		return nil, err
	}
	countMap, err := s.loadHandledCounts(staffIDs)
	if err != nil {
		return nil, err
	}
	result := make([]StaffListItem, 0, len(staff))
	for _, st := range staff {
		result = append(result, StaffListItem{
			ID:           st.ID,
			AccountID:    st.AccountID,
			Realname:     st.Realname,
			Email:        st.Email,
			QQNumber:     st.QQNumber,
			Role:         st.Role,
			HandledCount: countMap[st.ID],
			WorkYears:    yearMap[st.ID],
		})
	}
	return result, nil
}

// UpdateVersion sets a staff member's stored login version.
func (s *StaffService) UpdateVersion(id int, version int) error {
	res, err := s.db.Exec("UPDATE clinic_staff SET version = $1 WHERE id = $2", version, id)
	if err != nil {
		return fmt.Errorf("update staff %d version: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated staff %d version: %w", id, err)
	}
	if n == 0 {
		return ErrStaffNotFound
	}
	return nil
}

func (s *StaffService) UpdateRole(id int, role string) error {
	res, err := s.db.Exec("UPDATE clinic_staff SET role = $1 WHERE id = $2", role, id)
	if err != nil {
		return fmt.Errorf("update staff %d role: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated staff %d role: %w", id, err)
	}
	if n == 0 {
		return ErrStaffNotFound
	}
	return nil
}

// EnsureWorkYears records the given work years for a staff member, inserting
// only those that do not already exist.
func (s *StaffService) EnsureWorkYears(staffID int, years []int) error {
	unique := make(map[int]bool, len(years))
	for _, year := range years {
		unique[year] = true
	}
	if len(unique) == 0 {
		return nil
	}
	return database.WithTx(context.Background(), s.db.DB, func(tx *sql.Tx) error {
		for year := range unique {
			if _, err := tx.Exec(
				"INSERT INTO clinic_staff_workyear (staff_id, work_year) VALUES ($1, $2) ON CONFLICT DO NOTHING",
				staffID, year,
			); err != nil {
				return fmt.Errorf("ensure work year %d for staff %d: %w", year, staffID, err)
			}
		}
		return nil
	})
}

func (s *StaffService) Update(id int, in UpdateStaffInput) (staffmodel.ClinicStaff, error) {
	staff, err := scanStaff(s.db.QueryRow(
		"SELECT "+staffColumns+" FROM clinic_staff WHERE id = $1",
		id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return staffmodel.ClinicStaff{}, ErrStaffNotFound
		}
		return staffmodel.ClinicStaff{}, fmt.Errorf("get staff %d for update: %w", id, err)
	}

	var updates []string
	var args []any
	add := func(column string, value any) {
		args = append(args, value)
		updates = append(updates, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if in.Realname != nil {
		add("realname", *in.Realname)
	}
	if in.Email != nil {
		add("email", *in.Email)
	}
	if in.QQNumber != nil {
		add("qq_number", *in.QQNumber)
	}
	if len(updates) > 0 {
		args = append(args, id)
		staff, err = scanStaff(s.db.QueryRow(
			"UPDATE clinic_staff SET "+strings.Join(updates, ", ")+" WHERE id = $"+fmt.Sprint(len(args))+" RETURNING "+staffColumns,
			args...,
		))
		if err != nil {
			return staffmodel.ClinicStaff{}, fmt.Errorf("update staff %d: %w", id, err)
		}
	}
	return staff, nil
}

func (s *StaffService) Delete(id int) error {
	res, err := s.db.Exec("DELETE FROM clinic_staff WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete staff %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted staff %d: %w", id, err)
	}
	if n == 0 {
		return ErrStaffNotFound
	}
	return nil
}
