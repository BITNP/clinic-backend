package app

import (
	"errors"
	"fmt"

	"clinic-backend/internal/database"
	roommodel "clinic-backend/internal/room/model"
	staffmodel "clinic-backend/internal/staff/model"

	"gorm.io/gorm"
)

// defaultCampusName is the temporary campus used to group rooms that existed
// before campuses were introduced.
const defaultCampusName = "默认校区"

// MigrateBeforeAutoMigrate applies column renames that AutoMigrate cannot do
// safely after it has created the replacement column.
func MigrateBeforeAutoMigrate(db *gorm.DB) error {
	// clinic_staff.phone_num was replaced by email. AutoMigrate only adds
	// missing columns, so rename an existing phone_num column explicitly.
	if !db.Migrator().HasColumn(&staffmodel.ClinicStaff{}, "phone_num") {
		return nil
	}
	if err := db.Migrator().RenameColumn(&staffmodel.ClinicStaff{}, "phone_num", "email"); err != nil {
		return fmt.Errorf("rename clinic_staff.phone_num to email: %w", err)
	}
	return nil
}

// MigrateAfterAutoMigrate backfills data introduced by schema changes. It is
// safe to run on every startup.
func MigrateAfterAutoMigrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := backfillRoomTypes(tx); err != nil {
			return err
		}
		return assignRoomsToDefaultCampus(tx)
	})
}

// backfillRoomTypes gives rooms created before the type column existed the
// normal type.
func backfillRoomTypes(tx *gorm.DB) error {
	if err := tx.Model(&roommodel.ClinicRoom{}).
		Where("type IS NULL OR type = ?", "").
		Update("type", roommodel.RoomTypeNormal).Error; err != nil {
		return fmt.Errorf("backfill room type: %w", err)
	}
	return nil
}

// assignRoomsToDefaultCampus links every room without a campus to a temporary
// default campus. Rooms created by older binaries after the migration has run
// are picked up on the next startup.
func assignRoomsToDefaultCampus(tx *gorm.DB) error {
	var unassigned int64
	if err := tx.Model(&roommodel.ClinicRoom{}).
		Where("campus_id IS NULL").
		Count(&unassigned).Error; err != nil {
		return fmt.Errorf("count rooms without campus: %w", err)
	}
	if unassigned == 0 {
		return nil
	}

	campus, err := findOrCreateDefaultCampus(tx)
	if err != nil {
		return err
	}

	if err := tx.Model(&roommodel.ClinicRoom{}).
		Where("campus_id IS NULL").
		Update("campus_id", campus.ID).Error; err != nil {
		return fmt.Errorf("assign rooms to default campus: %w", err)
	}
	return nil
}

func findOrCreateDefaultCampus(tx *gorm.DB) (roommodel.ClinicCampus, error) {
	var campus roommodel.ClinicCampus
	err := tx.Where("name = ?", defaultCampusName).First(&campus).Error
	if err == nil {
		return campus, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return roommodel.ClinicCampus{}, fmt.Errorf("find default campus: %w", err)
	}

	campus = roommodel.ClinicCampus{Name: defaultCampusName}
	if err := tx.Create(&campus).Error; err != nil {
		// Another process may have created the campus first.
		if database.IsUniqueViolation(err) {
			if lookupErr := tx.Where("name = ?", defaultCampusName).First(&campus).Error; lookupErr == nil {
				return campus, nil
			}
		}
		return roommodel.ClinicCampus{}, fmt.Errorf("create default campus: %w", err)
	}
	return campus, nil
}
