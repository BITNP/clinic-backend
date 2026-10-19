package app

import (
	"fmt"
	"log"
	"os"
	"time"

	"clinic-backend/internal/announcement"
	authmodel "clinic-backend/internal/auth/model"
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

func openDatabase() *gorm.DB {
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
	// The application uses gorm.ErrRecordNotFound as a normal control-flow
	// signal when probing optional side tables, so silence those logs while
	// keeping real errors and slow-query warnings. Colorful is disabled to
	// avoid ANSI escapes in journald/syslog.
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
	// clinic_staff.phone_num was replaced by email. AutoMigrate only adds
	// missing columns, so rename an existing phone_num column explicitly.
	if db.Migrator().HasColumn(&staffmodel.ClinicStaff{}, "phone_num") {
		if err := db.Migrator().RenameColumn(&staffmodel.ClinicStaff{}, "phone_num", "email"); err != nil {
			log.Fatalf("failed to rename clinic_staff.phone_num to email: %v", err)
		}
	}
	if err := db.AutoMigrate(
		&announcement.ClinicAnnouncement{},
		&schedulingmodel.ClinicServiceDate{},
		&roommodel.ClinicRoom{},
		&staffmodel.ClinicStaff{},
		&staffmodel.ClinicStaffWorkyear{},
		&recordmodel.ClinicRecord{},
		&taggingmodel.ClinicRecordTag{},
		&recordmodel.ClinicRecordDevice{},
		&recordmodel.ClinicRecordWorker{},
		&recordmodel.ClinicRecordArrival{},
		&recordmodel.ClinicRecordRejection{},
		&recordmodel.ClinicRecordReferral{},
		&authmodel.AuthSession{},
		&schedulingmodel.ClinicWorkSchedule{},
		&schedulingmodel.ClinicWorkScheduleWeekday{},
		&schedulingmodel.ClinicWorkScheduleStaff{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
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

	return db
}
