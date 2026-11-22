package app

import (
	"fmt"
	"log"
	"os"
	"time"

	"clinic-backend/internal/announcement"
	authmodel "clinic-backend/internal/auth/model"
	configmodel "clinic-backend/internal/config/model"
	databasepkg "clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
	taggingmodel "clinic-backend/internal/tagging/model"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openDatabase() (*gorm.DB, *databasepkg.DB) {
	driver := envString("CLINIC_DB_DRIVER", "sqlite")
	dsn := envString("CLINIC_DB_DSN", "clinic.db")

	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.New(postgres.Config{
			DSN:                  dsn,
			PreferSimpleProtocol: true,
		})
	default:
		log.Fatalf("invalid CLINIC_DB_DRIVER %q: must be sqlite or postgres", driver)
	}
	// GORM remains for schema migration and bootstrap only. Module services
	// use database/sql, so keep migration logs quiet while retaining real
	// errors and slow-query warnings. Colorful is disabled to avoid ANSI
	// escapes in journald/syslog.
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             envDuration("DB_SLOW_QUERY", 500*time.Millisecond),
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  false,
			},
		),
	})
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	if err := MigrateBeforeAutoMigrate(db); err != nil {
		log.Fatalf("failed to run pre-migration: %v", err)
	}
	if err := db.AutoMigrate(
		&announcement.ClinicAnnouncement{},
		&configmodel.ClinicConfigGroup{},
		&configmodel.ClinicConfigItem{},
		&schedulingmodel.ClinicServiceDate{},
		&schedulingmodel.ServiceDateReminder{},
		&roommodel.ClinicCampus{},
		&roommodel.ClinicRoom{},
		&staffmodel.ClinicStaff{},
		&staffmodel.ClinicStaffWorkyear{},
		&recordmodel.ClinicRecord{},
		&taggingmodel.ClinicRecordTag{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordPhoto{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
		&recordmodel.ClinicRecordDingTalkReminder{},
		&authmodel.AuthSession{},
		&schedulingmodel.ClinicWorkSchedule{},
		&schedulingmodel.ClinicWorkScheduleWeekday{},
		&schedulingmodel.ClinicWorkScheduleStaff{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	if err := MigrateAfterAutoMigrate(db); err != nil {
		log.Fatalf("failed to run post-migration: %v", err)
	}
	if err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_tos
		ON clinic_announcement(tag)
		WHERE tag = 'tos'
	`).Error; err != nil {
		log.Fatalf("failed to create tos unique index: %v", err)
	}
	boolLiteral := "1"
	if driver == "postgres" {
		boolLiteral = "true"
	}
	if err := db.Exec(fmt.Sprintf(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_enabled_schedule
		ON clinic_work_schedule(enabled)
		WHERE enabled = %s
	`, boolLiteral)).Error; err != nil {
		log.Fatalf("failed to create enabled-schedule unique index: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get SQL database: %v", err)
	}
	rawDriver := databasepkg.DriverSQLite
	if driver == "postgres" {
		rawDriver = databasepkg.DriverPostgres
	}
	return db, databasepkg.Wrap(sqlDB, rawDriver)
}
