package services

import (
	"errors"
	"fmt"
	"time"

	"clinic-backend/models"

	"gorm.io/gorm"
)

var (
	ErrStaffNotFound = errors.New("staff not found")
	ErrStaffConflict = errors.New("staff account already exists")
)

type StaffService struct {
	db *gorm.DB
}

func NewStaffService(db *gorm.DB) *StaffService {
	return &StaffService{db: db}
}

type CreateStaffInput struct {
	AccountID string
	Realname  string
	PhoneNum  string
}

type UpdateStaffInput struct {
	Realname *string
	PhoneNum *string
}

type StaffListItem struct {
	ID           int    `json:"id"`
	AccountID    string `json:"account_id"`
	Realname     string `json:"realname"`
	PhoneNum     string `json:"phone_num"`
	Role         string `json:"role"`
	HandledCount int    `json:"handled_count"`
	WorkYears    []int  `json:"work_years"`
}

func (s *StaffService) GetOrCreateByAccountID(accountID, realname string) (models.ClinicStaff, error) {
	var staff models.ClinicStaff
	err := s.db.Where("account_id = ?", accountID).First(&staff).Error
	if err == nil {
		if realname != "" && staff.Realname != realname {
			s.db.Model(&staff).Update("realname", realname)
		}
		return staff, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.ClinicStaff{}, fmt.Errorf("lookup staff %s: %w", accountID, err)
	}

	staff = models.ClinicStaff{
		AccountID: accountID,
		Realname:  realname,
	}
	if err := s.db.Create(&staff).Error; err != nil {
		return models.ClinicStaff{}, fmt.Errorf("create staff %s: %w", accountID, err)
	}
	return staff, nil
}

func (s *StaffService) GetByID(id int) (models.ClinicStaff, error) {
	var staff models.ClinicStaff
	if err := s.db.First(&staff, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicStaff{}, ErrStaffNotFound
		}
		return models.ClinicStaff{}, fmt.Errorf("get staff %d: %w", id, err)
	}
	return staff, nil
}

func (s *StaffService) List() ([]StaffListItem, error) {
	var staff []models.ClinicStaff
	if err := s.db.Order("id ASC").Find(&staff).Error; err != nil {
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
			PhoneNum:     st.PhoneNum,
			Role:         st.Role,
			HandledCount: countMap[st.ID],
			WorkYears:    yearMap[st.ID],
		})
	}
	return result, nil
}

func (s *StaffService) loadHandledCounts(staffIDs []int) (map[int]int, error) {
	type countRow struct {
		WorkerID int `gorm:"column:worker"`
		Cnt      int
	}
	var rows []countRow
	if err := s.db.
		Model(&models.ClinicRecordWorker{}).
		Select("worker, COUNT(*) as cnt").
		Where("worker IN ?", staffIDs).
		Group("worker").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load handled counts: %w", err)
	}
	m := make(map[int]int, len(rows))
	for _, r := range rows {
		m[r.WorkerID] = r.Cnt
	}
	return m, nil
}

func (s *StaffService) loadWorkYears(staffIDs []int) (map[int][]int, error) {
	var years []models.ClinicStaffWorkyear
	if err := s.db.
		Where("staff_id IN ?", staffIDs).
		Order("work_year DESC").
		Find(&years).Error; err != nil {
		return nil, fmt.Errorf("load work years: %w", err)
	}
	m := make(map[int][]int, len(years))
	for _, y := range years {
		m[y.StaffID] = append(m[y.StaffID], y.WorkYear)
	}
	return m, nil
}

func (s *StaffService) IsValidForDate(staffID int, date time.Time) (bool, error) {
	year := date.Year()
	var count int64
	if err := s.db.Model(&models.ClinicStaffWorkyear{}).
		Where("staff_id = ? AND work_year = ?", staffID, year).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check work year for staff %d: %w", staffID, err)
	}
	return count > 0, nil
}

func (s *StaffService) ListValidForYear(year int) ([]StaffListItem, error) {
	var rows []models.ClinicStaffWorkyear
	if err := s.db.Where("work_year = ?", year).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list work year %d: %w", year, err)
	}
	if len(rows) == 0 {
		return []StaffListItem{}, nil
	}
	staffIDs := make([]int, len(rows))
	for i, r := range rows {
		staffIDs[i] = r.StaffID
	}
	var staff []models.ClinicStaff
	if err := s.db.Where("id IN ?", staffIDs).Order("id ASC").Find(&staff).Error; err != nil {
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
			PhoneNum:     st.PhoneNum,
			Role:         st.Role,
			HandledCount: countMap[st.ID],
			WorkYears:    yearMap[st.ID],
		})
	}
	return result, nil
}

func (s *StaffService) UpdateRole(id int, role string) error {
	result := s.db.Model(&models.ClinicStaff{}).Where("id = ?", id).Update("role", role)
	if err := result.Error; err != nil {
		return fmt.Errorf("update staff %d role: %w", id, err)
	}
	if result.RowsAffected == 0 {
		return ErrStaffNotFound
	}
	return nil
}

func (s *StaffService) Update(id int, in UpdateStaffInput) (models.ClinicStaff, error) {
	var staff models.ClinicStaff
	if err := s.db.First(&staff, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ClinicStaff{}, ErrStaffNotFound
		}
		return models.ClinicStaff{}, fmt.Errorf("get staff %d for update: %w", id, err)
	}

	updates := map[string]any{}
	if in.Realname != nil {
		updates["realname"] = *in.Realname
	}
	if in.PhoneNum != nil {
		updates["phone_num"] = *in.PhoneNum
	}
	if len(updates) > 0 {
		if err := s.db.Model(&staff).Updates(updates).Error; err != nil {
			return models.ClinicStaff{}, fmt.Errorf("update staff %d: %w", id, err)
		}
	}

	if err := s.db.First(&staff, id).Error; err != nil {
		return models.ClinicStaff{}, fmt.Errorf("reload staff %d: %w", id, err)
	}
	return staff, nil
}

func (s *StaffService) Delete(id int) error {
	res := s.db.Delete(&models.ClinicStaff{}, id)
	if err := res.Error; err != nil {
		return fmt.Errorf("delete staff %d: %w", id, err)
	}
	if res.RowsAffected == 0 {
		return ErrStaffNotFound
	}
	return nil
}
