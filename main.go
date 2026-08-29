package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"clinic-backend/handlers"
	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
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
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	if err := db.AutoMigrate(
		&models.ClinicAnnouncement{},
		&models.ClinicServiceDate{},
		&models.ClinicRoom{},
		&models.ClinicStaff{},
		&models.ClinicStaffWorkyear{},
		&models.ClinicRecord{},
		&models.ClinicRecordDevice{},
		&models.ClinicRecordWorker{},
		&models.ClinicRecordArrival{},
		&models.ClinicRecordRejection{},
		&models.ClinicRecordReferral{},
		&models.AuthSession{},
		&models.ClinicWorkSchedule{},
		&models.ClinicWorkScheduleWeekday{},
		&models.ClinicWorkScheduleStaff{},
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

	announcementSvc := services.NewAnnouncementService(db)
	announcementH := handlers.NewAnnouncementHandler(announcementSvc)

	serviceDateLoc := envLocation("CLINIC_TIMEZONE", time.FixedZone("UTC+8", 8*60*60))
	serviceDateSvc := services.NewServiceDateService(db, serviceDateLoc)
	serviceDateH := handlers.NewServiceDateHandler(serviceDateSvc)

	roomSvc := services.NewRoomService(db)
	roomH := handlers.NewRoomHandler(roomSvc)

	ticketSvc := services.NewTicketService(db, serviceDateLoc)
	ticketH := handlers.NewTicketHandler(ticketSvc)
	legacyH := handlers.NewLegacyHandler(ticketSvc, serviceDateSvc, roomSvc, announcementSvc)

	staffSvc := services.NewStaffService(db)
	staffH := handlers.NewStaffHandler(staffSvc)

	sessionTTL := envDuration("SESSION_TTL", 336*time.Hour)
	sessionSvc := services.NewSessionService(db, sessionTTL)

	adminRecordSvc := services.NewAdminRecordService(db)
	adminRecordH := handlers.NewAdminRecordHandler(adminRecordSvc)

	workScheduleSvc := services.NewWorkScheduleService(db)
	workScheduleH := handlers.NewWorkScheduleHandler(workScheduleSvc)

	userH := handlers.NewUserHandler()

	apiKey := os.Getenv("CLINIC_API_KEY")
	if apiKey == "" {
		log.Fatal("CLINIC_API_KEY not set")
	}
	clientAuth := handlers.ClientAuthMiddleware(apiKey, 5*time.Minute)

	keycloakRealm := os.Getenv("KEYCLOAK_REALM_URL")
	keycloakClient := os.Getenv("KEYCLOAK_CLIENT_ID")
	keycloakAuth := handlers.NewKeycloakAuthenticator(keycloakRealm, keycloakClient, staffSvc)

	casServerURL := os.Getenv("CAS_SERVER_URL")
	casTimeout := envDuration("CAS_HTTP_TIMEOUT", 10*time.Second)
	casLogoutParam := os.Getenv("CAS_LOGOUT_RETURN_PARAM")
	var casClient handlers.CASClient
	if casServerURL != "" {
		casClient = handlers.NewCASClient(casServerURL, casLogoutParam, casTimeout)
	}

	appBaseURL := strings.TrimRight(envString("APP_BASE_URL", ""), "/")
	if casServerURL != "" && appBaseURL == "" {
		log.Fatal("CAS_SERVER_URL is set but APP_BASE_URL is empty")
	}

	casHandler := handlers.NewCASAuthHandler(handlers.CASAuthConfig{
		Client:         casClient,
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		BaseURL:        appBaseURL,
		DefaultNext:    envString("CAS_DEFAULT_REDIRECT", "/manage/"),
		CookieName:     envString("SESSION_COOKIE_NAME", "sessionid"),
		CSRFCookieName: envString("CSRF_COOKIE_NAME", "csrf_token"),
		CookieSecure:   envBool("SESSION_COOKIE_SECURE", false),
		CookieSameSite: envSameSite("SESSION_COOKIE_SAMESITE", http.SameSiteLaxMode),
		SessionTTL:     sessionTTL,
	})

	authCfg := handlers.AdminAuthConfig{
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		KeycloakAuth:   keycloakAuth,
		CookieName:     envString("SESSION_COOKIE_NAME", "sessionid"),
	}
	adminAuth := handlers.NewAdminAuthMiddleware(authCfg)
	optionalAuth := handlers.NewOptionalAdminAuthMiddleware(authCfg)

	r := gin.Default()
	r.RedirectTrailingSlash = false

	// ── CAS login/logout ─────────────────────────────────────────────────
	r.GET("/login", casHandler.Login)
	r.GET("/logout", casHandler.Logout)

	// ── Admin: Announcements ──────────────────────────────────────────────
	// Staff and admin can read; admin only can write.
	annRead := r.Group("/api/admin/announcements")
	annRead.Use(adminAuth, handlers.RequireStaff)
	{
		annRead.GET("", announcementH.List)
		annRead.GET("/:id", announcementH.Get)
	}
	annWrite := r.Group("/api/admin/announcements")
	annWrite.Use(adminAuth, handlers.RequireAdmin)
	{
		annWrite.POST("", announcementH.Create)
		annWrite.PUT("/:id", announcementH.Update)
		annWrite.DELETE("/:id", announcementH.Delete)
	}

	// ── Admin: Service Dates ──────────────────────────────────────────────
	sdRead := r.Group("/api/admin/service-dates")
	sdRead.Use(adminAuth, handlers.RequireStaff)
	{
		sdRead.GET("", serviceDateH.AdminList)
		sdRead.GET("/all", serviceDateH.ListAll)
		sdRead.GET("/:id", serviceDateH.Get)
	}
	sdWrite := r.Group("/api/admin/service-dates")
	sdWrite.Use(adminAuth, handlers.RequireAdmin)
	{
		sdWrite.POST("", serviceDateH.Create)
		sdWrite.PUT("/:id", serviceDateH.Update)
		sdWrite.DELETE("/:id", serviceDateH.Delete)
	}

	// ── Admin: Rooms ──────────────────────────────────────────────────────
	roomRead := r.Group("/api/admin/rooms")
	roomRead.Use(adminAuth, handlers.RequireStaff)
	{
		roomRead.GET("", roomH.List)
		roomRead.GET("/:id", roomH.Get)
	}
	roomWrite := r.Group("/api/admin/rooms")
	roomWrite.Use(adminAuth, handlers.RequireAdmin)
	{
		roomWrite.POST("", roomH.Create)
		roomWrite.PUT("/:id", roomH.Update)
		roomWrite.DELETE("/:id", roomH.Delete)
	}

	// ── Admin: Records (staff can read and perform site operations) ───────
	records := r.Group("/api/admin/records")
	records.Use(adminAuth, handlers.RequireStaff)
	{
		records.GET("", adminRecordH.List)
		records.GET("/:id", adminRecordH.Get)
		records.PUT("/:id", adminRecordH.Update)
		records.POST("/:id/arrive", adminRecordH.Arrive)
		records.POST("/:id/in-progress", adminRecordH.InProgress)
		records.POST("/:id/complete", adminRecordH.Complete)
		records.POST("/:id/refer", adminRecordH.Refer)
		records.POST("/:id/no-show", adminRecordH.NoShow)
	}
	// ── Admin: Records (admin only — approve / reject) ────────────────────
	recordsAdmin := r.Group("/api/admin/records")
	recordsAdmin.Use(adminAuth, handlers.RequireAdmin)
	{
		recordsAdmin.POST("/:id/confirm", adminRecordH.Confirm)
		recordsAdmin.POST("/:id/reject", adminRecordH.Reject)
	}

	// ── Admin: Staff Management (admin only) ──────────────────────────────
	staffAdm := r.Group("/api/admin/staff")
	staffAdm.Use(adminAuth, handlers.RequireAdmin)
	{
		staffAdm.GET("", staffH.List)
		staffAdm.GET("/:id", staffH.Get)
		staffAdm.POST("", staffH.Create)
		staffAdm.PUT("/:id", staffH.Update)
		staffAdm.DELETE("/:id", staffH.Delete)
	}

	// ── Admin: Work Schedules ─────────────────────────────────────────────
	wsRead := r.Group("/api/admin/work-schedules")
	wsRead.Use(adminAuth, handlers.RequireStaff)
	{
		wsRead.GET("", workScheduleH.List)
		wsRead.GET("/:id", workScheduleH.Get)
	}
	wsWrite := r.Group("/api/admin/work-schedules")
	wsWrite.Use(adminAuth, handlers.RequireAdmin)
	{
		wsWrite.GET("/all", workScheduleH.ListAll)
		wsWrite.GET("/service-availability", workScheduleH.ServiceAvailability)
		wsWrite.POST("", workScheduleH.Create)
		wsWrite.PUT("/:id", workScheduleH.Update)
		wsWrite.DELETE("/:id", workScheduleH.Delete)
		wsWrite.GET("/:id/staff", workScheduleH.ListStaff)
		wsWrite.POST("/:id/staff", workScheduleH.AddStaff)
		wsWrite.DELETE("/:id/staff", workScheduleH.RemoveStaff)
		wsWrite.GET("/:id/valid-staff", workScheduleH.ListValidStaff)
		wsWrite.PUT("/:id/weekdays", workScheduleH.UpdateWeekday)
	}

	// ── Client routes (API-key signature middleware). ─────────────────────
	client := r.Group("/api/announcements")
	client.Use(clientAuth)
	{
		client.GET("", announcementH.List)
		client.GET("/:id", announcementH.Get)
	}
	serviceDateClient := r.Group("/api/service-dates")
	serviceDateClient.Use(clientAuth)
	{
		serviceDateClient.GET("", serviceDateH.List)
		serviceDateClient.GET("/:id", serviceDateH.Get)
	}
	roomClient := r.Group("/api/rooms")
	roomClient.Use(clientAuth)
	{
		roomClient.GET("", roomH.List)
		roomClient.GET("/:id", roomH.Get)
	}

	// ── Ticket routes (client) ────────────────────────────────────────────
	ticket := r.Group("/api/tickets")
	ticket.Use(clientAuth)
	{
		ticket.GET("", ticketH.List)
		ticket.POST("", ticketH.Create)
		ticket.GET("/working", ticketH.Working)
		ticket.GET("/finished", ticketH.Finished)
		ticket.GET("/:id", ticketH.Get)
		ticket.PUT("/:id", ticketH.Update)
		ticket.PATCH("/:id", ticketH.Update)
		ticket.DELETE("/:id", ticketH.Delete)
	}

	// ── Legacy customer endpoints (old Django shapes) ─────────────────────
	// TOS — returns {"content": ...} matching Django's /api/announcement/toc/
	r.GET("/api/announcement/toc", legacyH.TOS)
	r.GET("/api/announcement/toc/", legacyH.TOS)

	// /api/wechat — ticket lifecycle in old Django DRF shape (int status,
	// {count,next,previous,results} pagination, /api/wechat/{id}/ url).
	wechat := r.Group("/api/wechat")
	wechat.Use(clientAuth)
	{
		wechat.GET("", legacyH.ListRecords)
		wechat.GET("/", legacyH.ListRecords)
		wechat.POST("", legacyH.CreateRecord)
		wechat.POST("/", legacyH.CreateRecord)
		wechat.GET("/working", legacyH.WorkingRecord)
		wechat.GET("/working/", legacyH.WorkingRecord)
		wechat.GET("/finished", legacyH.FinishRecords)
		wechat.GET("/finished/", legacyH.FinishRecords)
		wechat.GET("/finish", legacyH.FinishRecords)
		wechat.GET("/finish/", legacyH.FinishRecords)
		wechat.GET("/:id", legacyH.GetRecord)
		wechat.GET("/:id/", legacyH.GetRecord)
		wechat.PUT("/:id", legacyH.UpdateRecord)
		wechat.PUT("/:id/", legacyH.UpdateRecord)
		wechat.PATCH("/:id", legacyH.UpdateRecord)
		wechat.PATCH("/:id/", legacyH.UpdateRecord)
		wechat.DELETE("/:id", legacyH.DeleteRecord)
		wechat.DELETE("/:id/", legacyH.DeleteRecord)
	}

	// /api/campus/ — plain array [{name, address}]
	campus := r.Group("/api/campus")
	campus.Use(clientAuth)
	{
		campus.GET("", legacyH.ListCampus)
		campus.GET("/", legacyH.ListCampus)
	}

	// /api/date/ — plain array of service dates with room name & count
	sd := r.Group("/api/date")
	sd.Use(clientAuth)
	{
		sd.GET("", legacyH.ListDates)
		sd.GET("/", legacyH.ListDates)
		sd.GET("/all", legacyH.ListAllDates)
		sd.GET("/all/", legacyH.ListAllDates)
	}

	// /api/announcement/ — plain array of active announcements
	ann := r.Group("/api/announcement")
	ann.Use(clientAuth)
	{
		ann.GET("", legacyH.ListAnnouncements)
		ann.GET("/", legacyH.ListAnnouncements)
	}

	// ── User endpoints ────────────────────────────────────────────────────
	r.GET("/api/user", adminAuth, userH.Current)
	r.GET("/api/user/", adminAuth, userH.Current)
	r.GET("/api/users/me", optionalAuth, userH.Me)
	r.GET("/api/users/me/", optionalAuth, userH.Me)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		runCleanup := func() {
			cutoff := services.DateInLocation(time.Now(), serviceDateLoc)
			noShow, completed, err := adminRecordSvc.CloseExpiredRecords(ctx, cutoff)
			if err != nil {
				log.Printf("cleanup error: %v", err)
			} else if noShow > 0 || completed > 0 {
				log.Printf("cleanup: %d no_show, %d completed", noShow, completed)
			}
		}

		runCleanup()

		now := time.Now().In(serviceDateLoc)
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, serviceDateLoc)
		timer := time.NewTimer(time.Until(next))
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				runCleanup()
				timer.Reset(24 * time.Hour)
			}
		}
	}()

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return d
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return b
}

func envSameSite(key string, fallback http.SameSite) http.SameSite {
	switch strings.ToLower(os.Getenv(key)) {
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	case "":
		return fallback
	default:
		log.Fatalf("invalid %s: must be none, lax, or strict", key)
		return fallback
	}
}

func envLocation(key string, fallback *time.Location) *time.Location {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	loc, err := time.LoadLocation(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return loc
}
