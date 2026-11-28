package service_test

import (
	"errors"
	"testing"
	"time"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	roommodel "clinic-backend/internal/room/model"
	"clinic-backend/internal/testutil"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupUserTicketBanTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&roommodel.ClinicCampus{}, &recordmodel.ClinicUserTicketBan{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestUserTicketBanService_CreateAndRead(t *testing.T) {
	db := setupUserTicketBanTestDB(t)
	campus := roommodel.ClinicCampus{Name: "Main"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	svc := recordservice.NewUserTicketBanService(testutil.RawDB(t, db))
	expirateAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)

	created, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID:     " 1120221234 ",
		Scope:      recordservice.UserTicketBanScopeCampus,
		CampusID:   &campus.ID,
		Reason:     " repeated abuse ",
		ExpirateAt: expirateAt,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.UserID != "1120221234" || created.Reason != "repeated abuse" {
		t.Fatalf("normalized ban = %+v", created)
	}
	if created.CampusID == nil || *created.CampusID != campus.ID {
		t.Fatalf("campus id = %v, want %d", created.CampusID, campus.ID)
	}
	if !created.ExpirateAt.Equal(expirateAt) {
		t.Fatalf("expirate_at = %v, want %v", created.ExpirateAt, expirateAt)
	}

	got, err := svc.GetByID(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID ||
		got.UserID != created.UserID ||
		got.CampusID == nil ||
		*created.CampusID != *got.CampusID ||
		got.Reason != created.Reason ||
		!got.ExpirateAt.Equal(created.ExpirateAt) {
		t.Fatalf("get = %+v, want %+v", got, created)
	}
}

func TestUserTicketBanService_CreateValidation(t *testing.T) {
	db := setupUserTicketBanTestDB(t)
	campus := roommodel.ClinicCampus{Name: "Main"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	svc := recordservice.NewUserTicketBanService(testutil.RawDB(t, db))
	future := time.Now().Add(time.Hour)

	tests := []struct {
		name string
		in   recordservice.CreateUserTicketBanInput
		want error
	}{
		{
			name: "missing user",
			in: recordservice.CreateUserTicketBanInput{
				Scope: recordservice.UserTicketBanScopeGlobal, Reason: "reason", ExpirateAt: future,
			},
			want: recordservice.ErrUserTicketBanInvalidUserID,
		},
		{
			name: "invalid scope",
			in: recordservice.CreateUserTicketBanInput{
				UserID: "u", Scope: "invalid", Reason: "reason", ExpirateAt: future,
			},
			want: recordservice.ErrUserTicketBanInvalidScope,
		},
		{
			name: "global with campus",
			in: recordservice.CreateUserTicketBanInput{
				UserID: "u", Scope: recordservice.UserTicketBanScopeGlobal, CampusID: &campus.ID, Reason: "reason", ExpirateAt: future,
			},
			want: recordservice.ErrUserTicketBanInvalidCampusID,
		},
		{
			name: "campus without campus",
			in: recordservice.CreateUserTicketBanInput{
				UserID: "u", Scope: recordservice.UserTicketBanScopeCampus, Reason: "reason", ExpirateAt: future,
			},
			want: recordservice.ErrUserTicketBanInvalidCampusID,
		},
		{
			name: "missing reason",
			in: recordservice.CreateUserTicketBanInput{
				UserID: "u", Scope: recordservice.UserTicketBanScopeGlobal, Reason: " ", ExpirateAt: future,
			},
			want: recordservice.ErrUserTicketBanInvalidReason,
		},
		{
			name: "past expiration",
			in: recordservice.CreateUserTicketBanInput{
				UserID: "u", Scope: recordservice.UserTicketBanScopeGlobal, Reason: "reason", ExpirateAt: time.Now().Add(-time.Minute),
			},
			want: recordservice.ErrUserTicketBanInvalidExpirateAt,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}

	_, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "u", Scope: recordservice.UserTicketBanScopeCampus, CampusID: func() *uint {
			id := uint(999)
			return &id
		}(), Reason: "reason", ExpirateAt: future,
	})
	if !errors.Is(err, recordservice.ErrUserTicketBanCampusNotFound) {
		t.Fatalf("unknown campus: expected %v, got %v", recordservice.ErrUserTicketBanCampusNotFound, err)
	}
}

func TestUserTicketBanService_DuplicateScopeConflict(t *testing.T) {
	db := setupUserTicketBanTestDB(t)
	svc := recordservice.NewUserTicketBanService(testutil.RawDB(t, db))
	input := recordservice.CreateUserTicketBanInput{
		UserID: "u", Scope: recordservice.UserTicketBanScopeGlobal,
		Reason: "reason", ExpirateAt: time.Now().Add(time.Hour),
	}
	if _, err := svc.Create(input); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(input); !errors.Is(err, recordservice.ErrUserTicketBanConflict) {
		t.Fatalf("duplicate: expected %v, got %v", recordservice.ErrUserTicketBanConflict, err)
	}

	input.Scope = recordservice.UserTicketBanScopeCampus
	input.CampusID = func() *uint {
		id := uint(1)
		return &id
	}()
	if err := db.Create(&roommodel.ClinicCampus{ID: 1, Name: "Main"}).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	if _, err := svc.Create(input); err != nil {
		t.Fatalf("global and campus bans should coexist: %v", err)
	}
}

func TestUserTicketBanService_ListUpdateDelete(t *testing.T) {
	db := setupUserTicketBanTestDB(t)
	campus := roommodel.ClinicCampus{Name: "Main"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	svc := recordservice.NewUserTicketBanService(testutil.RawDB(t, db))
	first, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "alice", Scope: recordservice.UserTicketBanScopeGlobal,
		Reason: "first", ExpirateAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create global: %v", err)
	}
	_, err = svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "bob", Scope: recordservice.UserTicketBanScopeCampus, CampusID: &campus.ID,
		Reason: "second", ExpirateAt: time.Now().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create campus: %v", err)
	}

	active := true
	items, total, err := svc.List(recordservice.ListUserTicketBanFilter{Scope: recordservice.UserTicketBanScopeGlobal, Active: &active})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("unexpected list: total=%d items=%+v", total, items)
	}

	reason := "updated reason"
	newExpiry := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	updated, err := svc.Update(first.ID, recordservice.UpdateUserTicketBanInput{Reason: &reason, ExpirateAt: &newExpiry})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Reason != reason || !updated.ExpirateAt.Equal(newExpiry) {
		t.Fatalf("updated = %+v", updated)
	}
	if err := svc.Delete(updated.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetByID(updated.ID); !errors.Is(err, recordservice.ErrUserTicketBanNotFound) {
		t.Fatalf("get deleted: expected %v, got %v", recordservice.ErrUserTicketBanNotFound, err)
	}
}

func TestUserTicketBanService_IsBanned(t *testing.T) {
	db := setupUserTicketBanTestDB(t)
	firstCampus := roommodel.ClinicCampus{Name: "First"}
	secondCampus := roommodel.ClinicCampus{Name: "Second"}
	if err := db.Create(&firstCampus).Error; err != nil {
		t.Fatalf("seed first campus: %v", err)
	}
	if err := db.Create(&secondCampus).Error; err != nil {
		t.Fatalf("seed second campus: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour)
	past := time.Now().UTC().Add(-time.Hour)
	rows := []recordmodel.ClinicUserTicketBan{
		{UserID: "global", Reason: "global", ExpirateAt: future},
		{UserID: "campus", CampusID: &firstCampus.ID, Reason: "campus", ExpirateAt: future},
		{UserID: "expired", Reason: "expired", ExpirateAt: past},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed bans: %v", err)
	}
	svc := recordservice.NewUserTicketBanService(testutil.RawDB(t, db))

	tests := []struct {
		name     string
		userID   string
		campusID *uint
		want     bool
	}{
		{name: "global any campus", userID: "global", campusID: &secondCampus.ID, want: true},
		{name: "matching campus", userID: "campus", campusID: &firstCampus.ID, want: true},
		{name: "other campus", userID: "campus", campusID: &secondCampus.ID, want: false},
		{name: "expired", userID: "expired", campusID: &firstCampus.ID, want: false},
		{name: "unknown user", userID: "other", campusID: &firstCampus.ID, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.IsBanned(tc.userID, tc.campusID, time.Now())
			if err != nil {
				t.Fatalf("is banned: %v", err)
			}
			if got != tc.want {
				t.Fatalf("is banned = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUserTicketBanService_ActiveBanReasonAndPrecedence(t *testing.T) {
	db := setupUserTicketBanTestDB(t)
	campus := roommodel.ClinicCampus{Name: "Main"}
	if err := db.Create(&campus).Error; err != nil {
		t.Fatalf("seed campus: %v", err)
	}
	svc := recordservice.NewUserTicketBanService(testutil.RawDB(t, db))
	future := time.Now().UTC().Add(time.Hour)
	if _, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "both", Scope: recordservice.UserTicketBanScopeCampus, CampusID: &campus.ID,
		Reason: "campus reason", ExpirateAt: future,
	}); err != nil {
		t.Fatalf("create campus ban: %v", err)
	}
	if _, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "both", Scope: recordservice.UserTicketBanScopeGlobal,
		Reason: "global reason", ExpirateAt: future,
	}); err != nil {
		t.Fatalf("create global ban: %v", err)
	}
	if _, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "campus-only", Scope: recordservice.UserTicketBanScopeCampus, CampusID: &campus.ID,
		Reason: "campus-only reason", ExpirateAt: future,
	}); err != nil {
		t.Fatalf("create campus-only ban: %v", err)
	}

	tests := []struct {
		name       string
		userID     string
		wantReason string
	}{
		{name: "global takes precedence", userID: "both", wantReason: "global reason"},
		{name: "campus reason", userID: "campus-only", wantReason: "campus-only reason"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ban, err := svc.ActiveBan(tc.userID, &campus.ID, time.Now())
			if err != nil {
				t.Fatalf("active ban: %v", err)
			}
			if ban == nil || ban.Reason != tc.wantReason {
				t.Fatalf("active ban = %+v, want reason %q", ban, tc.wantReason)
			}
		})
	}
}

func TestUserTicketBanServicePostgres(t *testing.T) {
	db, gormDB := setupRecordPostgres(t)
	firstCampus := roommodel.ClinicCampus{Name: "First"}
	secondCampus := roommodel.ClinicCampus{Name: "Second"}
	if err := gormDB.Create(&firstCampus).Error; err != nil {
		t.Fatalf("seed first campus: %v", err)
	}
	if err := gormDB.Create(&secondCampus).Error; err != nil {
		t.Fatalf("seed second campus: %v", err)
	}

	svc := recordservice.NewUserTicketBanService(database.Wrap(db, database.DriverPostgres))
	future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	global, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "global", Scope: recordservice.UserTicketBanScopeGlobal,
		Reason: "global reason", ExpirateAt: future,
	})
	if err != nil {
		t.Fatalf("create global ban: %v", err)
	}
	if _, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "global", Scope: recordservice.UserTicketBanScopeGlobal,
		Reason: "duplicate", ExpirateAt: future,
	}); !errors.Is(err, recordservice.ErrUserTicketBanConflict) {
		t.Fatalf("duplicate global: expected %v, got %v", recordservice.ErrUserTicketBanConflict, err)
	}
	campus, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "global", Scope: recordservice.UserTicketBanScopeCampus, CampusID: &firstCampus.ID,
		Reason: "campus reason", ExpirateAt: future,
	})
	if err != nil {
		t.Fatalf("global and campus bans should coexist: %v", err)
	}
	if global.CampusID != nil || campus.CampusID == nil || *campus.CampusID != firstCampus.ID {
		t.Fatalf("created scopes: global=%+v campus=%+v", global, campus)
	}
	if _, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "campus", Scope: recordservice.UserTicketBanScopeCampus, CampusID: &firstCampus.ID,
		Reason: "campus-only reason", ExpirateAt: future,
	}); err != nil {
		t.Fatalf("create campus-only ban: %v", err)
	}

	tests := []struct {
		name     string
		userID   string
		campusID *uint
		want     bool
	}{
		{name: "global on first campus", userID: "global", campusID: &firstCampus.ID, want: true},
		{name: "global on second campus", userID: "global", campusID: &secondCampus.ID, want: true},
		{name: "campus on matching campus", userID: "campus", campusID: &firstCampus.ID, want: true},
		{name: "campus on other campus", userID: "campus", campusID: &secondCampus.ID, want: false},
		{name: "other user", userID: "other", campusID: &firstCampus.ID, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.IsBanned(tc.userID, tc.campusID, time.Now())
			if err != nil {
				t.Fatalf("check ban: %v", err)
			}
			if got != tc.want {
				t.Fatalf("banned = %v, want %v", got, tc.want)
			}
		})
	}

	expired, err := svc.Create(recordservice.CreateUserTicketBanInput{
		UserID: "expired", Scope: recordservice.UserTicketBanScopeGlobal,
		Reason: "expired reason", ExpirateAt: future,
	})
	if err != nil {
		t.Fatalf("create expiring ban: %v", err)
	}
	if err := gormDB.Model(&recordmodel.ClinicUserTicketBan{}).
		Where("id = ?", expired.ID).
		Update("expirate_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire ban: %v", err)
	}
	banned, err := svc.IsBanned("expired", &firstCampus.ID, time.Now())
	if err != nil {
		t.Fatalf("check expired ban: %v", err)
	}
	if banned {
		t.Fatal("expired ban should not apply")
	}
}
