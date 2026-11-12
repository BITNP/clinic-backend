package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReminderDiscovery finds pending tickets and their assigned administrators.
type ReminderDiscovery struct {
	DB        *gorm.DB
	Location  *time.Location
	LeadTimes []time.Duration
}

func (d *ReminderDiscovery) Discover(ctx context.Context, now time.Time) error {
	db := d.DB.WithContext(ctx)
	for _, lead := range d.LeadTimes {
		from, until := ReminderStartWindow(now, lead)
		var dates []schedulingmodel.ClinicServiceDate
		if err := db.Where("start_time >= ? AND start_time < ?", from, until).Find(&dates).Error; err != nil {
			return fmt.Errorf("find service dates: %w", err)
		}
		for _, date := range dates {
			admins, count, err := d.Eligible(ctx, date)
			if err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			for _, admin := range admins {
				if strings.TrimSpace(admin.QQNumber) == "" {
					log.Printf("reminder: staff %d has no QQ number", admin.ID)
					continue
				}
				job := schedulingmodel.ServiceDateReminder{ServiceDateID: date.ID, ServiceStartAt: date.StartTime, LeadTimeSeconds: int64(lead / time.Second), StaffID: admin.ID, Status: "pending", NextAttemptAt: now}
				if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
					return fmt.Errorf("create reminder job: %w", err)
				}
			}
		}
	}
	return nil
}

// Eligible returns currently assigned administrators and the pending ticket count.
func (d *ReminderDiscovery) Eligible(ctx context.Context, date schedulingmodel.ClinicServiceDate) ([]staffmodel.ClinicStaff, int64, error) {
	if date.RoomID == nil {
		return nil, 0, nil
	}
	db := d.DB.WithContext(ctx)
	var count int64
	if err := db.Model(&recordmodel.ClinicRecord{}).Where("appointment_time = ? AND room = ? AND status = ?", date.Date, *date.RoomID, recordmodel.RecordStatusPending).Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count pending tickets: %w", err)
	}
	if count == 0 {
		return nil, 0, nil
	}
	var schedules []schedulingmodel.ClinicWorkSchedule
	if err := db.Where("enabled = ? AND start_date <= ? AND end_date >= ?", true, date.Date, date.Date).Preload("Weekdays", "room_id = ? AND weekday = ?", *date.RoomID, int(date.Date.Weekday())).Preload("Weekdays.Staff.Staff").Find(&schedules).Error; err != nil {
		return nil, 0, fmt.Errorf("find assigned admins: %w", err)
	}
	loc := d.Location
	if loc == nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	start := date.StartTime.In(loc)
	end := date.EndTime.In(loc)
	secs := func(t time.Time) int { return t.Hour()*3600 + t.Minute()*60 + t.Second() }
	seen := map[int]bool{}
	var admins []staffmodel.ClinicStaff
	for _, schedule := range schedules {
		for _, slot := range schedule.Weekdays {
			if secs(slot.StartTime) > secs(start) || secs(slot.EndTime) < secs(end) {
				continue
			}
			for _, assignment := range slot.Staff {
				s := assignment.Staff
				if s.Role == "admin" && !seen[s.ID] {
					seen[s.ID] = true
					admins = append(admins, s)
				}
			}
		}
	}
	return admins, count, nil
}
