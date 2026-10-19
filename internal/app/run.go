package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"clinic-backend/internal/announcement"
	authclient "clinic-backend/internal/auth/client"
	authhandler "clinic-backend/internal/auth/handler"
	authservice "clinic-backend/internal/auth/service"
	"clinic-backend/internal/calendar"
	"clinic-backend/internal/changes"
	"clinic-backend/internal/legacy"
	recordhandler "clinic-backend/internal/record/handler"
	recordservice "clinic-backend/internal/record/service"
	roomhandler "clinic-backend/internal/room/handler"
	roomservice "clinic-backend/internal/room/service"
	schedulinghandler "clinic-backend/internal/scheduling/handler"
	schedulingservice "clinic-backend/internal/scheduling/service"
	staffhandler "clinic-backend/internal/staff/handler"
	staffservice "clinic-backend/internal/staff/service"
	taggingclient "clinic-backend/internal/tagging/client"
	taggingservice "clinic-backend/internal/tagging/service"
)

// Run initializes the backend and serves requests until shutdown.
func Run() {
	db := openDatabase()

	// Redis is required infrastructure: fail fast if it is not configured or
	// not reachable.
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR not set")
	}
	redisPingCtx, cancelRedisPing := context.WithTimeout(context.Background(), 5*time.Second)
	redisClient, err := newRedisClient(redisPingCtx, redisConfig{
		addr:     redisAddr,
		password: os.Getenv("REDIS_PASSWORD"),
		db:       envInt("REDIS_DB", 0),
	})
	cancelRedisPing()
	if err != nil {
		log.Fatalf("failed to connect to redis: %v", err)
	}

	// Sync counters live in Redis so every backend replica shares them. Clients
	// poll Snapshot and refresh when a value differs from their last-seen one.
	syncSvc := changes.NewSyncService(changes.NewRedisStore(redisClient))
	syncPollInterval := envDuration("SYNC_POLL_INTERVAL", 10*time.Second)
	if syncPollInterval < 0 {
		log.Fatalf("invalid SYNC_POLL_INTERVAL: must not be negative")
	}
	syncH := changes.NewSyncHandler(syncSvc, syncPollInterval)

	recordTagSvc := taggingservice.NewRecordTagService(db)
	if _, err := recordTagSvc.SeedDefault(); err != nil {
		log.Fatalf("failed to seed default record tag: %v", err)
	}
	if err := recordTagSvc.SeedCatalog(); err != nil {
		log.Fatalf("failed to seed record tag catalog: %v", err)
	}
	if err := recordTagSvc.Load(); err != nil {
		log.Fatalf("failed to load record tags: %v", err)
	}
	if err := recordTagSvc.BackfillRecords(); err != nil {
		log.Fatalf("failed to backfill record tag_id: %v", err)
	}

	taggerURL := envString("TAGGER_URL", "http://127.0.0.1:4514")
	taggerToken := os.Getenv("TAGGER_API_TOKEN")
	taggerTagSet := envString("TAGGER_TAG_SET", "clinic_record")
	liquidMetalTagSet := envString("LM_TAGGER_TAG_SET", "clinic_laptop_liquid_metal")
	var taggerClient taggingservice.TaggerClient
	var recordTagger *taggingservice.RecordTagger
	var liquidMetalTagger *taggingservice.LiquidMetalTagger
	if taggerURL != "" && taggerToken != "" {
		taggerClient = taggingclient.NewTaggerHTTPClient(taggerURL, taggerToken, envDuration("TAGGER_TIMEOUT", 15*time.Second))
		recordTagger = taggingservice.NewRecordTagger(taggerClient, taggerTagSet, db, recordTagSvc)
		recordTagger.SetSyncService(syncSvc)

		lm, lmErr := taggingservice.NewLiquidMetalTagger(taggerClient, liquidMetalTagSet)
		if lmErr != nil {
			log.Fatalf("failed to build liquid metal tagger: %v", lmErr)
		}
		liquidMetalTagger = lm
		recordTagger.SetLiquidMetalTagger(lm)
	} else {
		log.Println("tagger disabled: set TAGGER_URL and TAGGER_API_TOKEN to enable")
	}

	announcementSvc := announcement.NewAnnouncementService(db)
	announcementH := announcement.NewAnnouncementHandler(announcementSvc)

	serviceDateLoc := envLocation("CLINIC_TIMEZONE", time.FixedZone("UTC+8", 8*60*60))
	serviceDateSvc := schedulingservice.NewServiceDateService(db, serviceDateLoc)
	serviceDateH := schedulinghandler.NewServiceDateHandler(serviceDateSvc)

	roomSvc := roomservice.NewRoomService(db)
	roomH := roomhandler.NewRoomHandler(roomSvc)

	ticketSvc := recordservice.NewTicketService(db, serviceDateLoc)
	ticketSvc.SetDefaultTagID(recordTagSvc.DefaultID())
	if recordTagger != nil {
		ticketSvc.SetTagger(recordTagger)
	}
	ticketH := recordhandler.NewTicketHandler(ticketSvc)
	legacyH := legacy.NewLegacyHandler(ticketSvc, serviceDateSvc, roomSvc, announcementSvc)

	staffSvc := staffservice.NewStaffService(db)
	staffH := staffhandler.NewStaffHandler(staffSvc)

	sessionTTL := envDuration("SESSION_TTL", 336*time.Hour)
	sessionSvc := authservice.NewSessionService(db, sessionTTL)

	adminRecordSvc := recordservice.NewAdminRecordService(db)
	adminRecordSvc.SetRecordTagService(recordTagSvc)
	recordActionSvc := recordservice.NewRecordActionService(
		recordservice.NewRedisRecordActionStore(redisClient),
		envDuration("RECORD_ACTION_WINDOW", 5*time.Minute),
	)
	adminRecordSvc.SetRecordActionService(recordActionSvc)
	adminRecordH := recordhandler.NewAdminRecordHandler(adminRecordSvc)

	workScheduleSvc := schedulingservice.NewWorkScheduleService(db)
	workScheduleH := schedulinghandler.NewWorkScheduleHandler(workScheduleSvc)

	userH := authhandler.NewUserHandler()

	apiKey := os.Getenv("CLINIC_API_KEY")
	if apiKey == "" {
		log.Fatal("CLINIC_API_KEY not set")
	}
	clientAuth := authhandler.ClientAuthMiddleware(apiKey, 5*time.Minute)

	keycloakRealm := os.Getenv("KEYCLOAK_REALM_URL")
	keycloakClient := os.Getenv("KEYCLOAK_CLIENT_ID")
	keycloakAuth := authhandler.NewKeycloakAuthenticator(keycloakRealm, keycloakClient, staffSvc)
	keycloakAuth.SetSyncService(syncSvc)

	// Current required login version. Raise STAFF_VERSION at deploy time to
	// force every staff member to re-login once.
	staffVersion := envInt("STAFF_VERSION", 0)
	keycloakAuth.SetStaffVersion(staffVersion)

	casServerURL := os.Getenv("CAS_SERVER_URL")
	casTimeout := envDuration("CAS_HTTP_TIMEOUT", 10*time.Second)
	casLogoutParam := os.Getenv("CAS_LOGOUT_RETURN_PARAM")
	var casClient authclient.CASClient
	if casServerURL != "" {
		casClient = authclient.NewCASClient(casServerURL, casLogoutParam, casTimeout)
	}

	appBaseURL := strings.TrimRight(envString("APP_BASE_URL", ""), "/")
	if casServerURL != "" && appBaseURL == "" {
		log.Fatal("CAS_SERVER_URL is set but APP_BASE_URL is empty")
	}

	casHandler := authhandler.NewCASAuthHandler(authhandler.CASAuthConfig{
		Client:         casClient,
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		SyncService:    syncSvc,
		BaseURL:        appBaseURL,
		DefaultNext:    envString("CAS_DEFAULT_REDIRECT", "/manage/"),
		CookieName:     envString("SESSION_COOKIE_NAME", "sessionid"),
		CSRFCookieName: envString("CSRF_COOKIE_NAME", "csrf_token"),
		CookieSecure:   envBool("SESSION_COOKIE_SECURE", false),
		CookieSameSite: envSameSite("SESSION_COOKIE_SAMESITE", http.SameSiteLaxMode),
		SessionTTL:     sessionTTL,
		StaffVersion:   staffVersion,
	})

	authCfg := authhandler.AdminAuthConfig{
		SessionService: sessionSvc,
		StaffService:   staffSvc,
		KeycloakAuth:   keycloakAuth,
		CookieName:     envString("SESSION_COOKIE_NAME", "sessionid"),
		StaffVersion:   staffVersion,
	}
	adminAuth := authhandler.NewAdminAuthMiddleware(authCfg)
	optionalAuth := authhandler.NewOptionalAdminAuthMiddleware(authCfg)

	r := (routeHandlers{
		announcementH: announcementH,
		serviceDateH:  serviceDateH,
		roomH:         roomH,
		staffH:        staffH,
		workScheduleH: workScheduleH,
		adminRecordH:  adminRecordH,
		ticketH:       ticketH,
		legacyH:       legacyH,
		casHandler:    casHandler,
		userH:         userH,
		syncH:         syncH,
		adminAuth:     adminAuth,
		optionalAuth:  optionalAuth,
		clientAuth:    clientAuth,
		syncSvc:       syncSvc,
		redisClient:   redisClient,
	}).router()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if recordTagger != nil {
		if err := recordTagger.RegisterTagSet(ctx); err != nil {
			log.Printf("record tagger: not connected at startup (tag set %q, url %s): %v", taggerTagSet, taggerURL, err)
		} else {
			log.Printf("record tagger: connected at startup (tag set %q, url %s)", taggerTagSet, taggerURL)
		}
		go recordTagger.Run(ctx)
	}

	if liquidMetalTagger != nil {
		if err := liquidMetalTagger.RegisterTagSet(ctx); err != nil {
			log.Printf("liquid metal tagger: not connected at startup (tag set %q, url %s): %v", liquidMetalTagSet, taggerURL, err)
		} else {
			log.Printf("liquid metal tagger: connected at startup (tag set %q, url %s)", liquidMetalTagSet, taggerURL)
		}
	}

	go func() {
		runCleanup := func() {
			cutoff := calendar.DateInLocation(time.Now(), serviceDateLoc)
			noShow, completed, err := adminRecordSvc.CloseExpiredRecords(ctx, cutoff)
			if err != nil {
				log.Printf("cleanup error: %v", err)
			} else if noShow > 0 || completed > 0 {
				log.Printf("cleanup: %d no_show, %d completed", noShow, completed)
				if err := syncSvc.Bump(ctx, changes.SyncGroupRecord); err != nil {
					log.Printf("cleanup: bump record sync counter failed: %v", err)
				}
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
	if err := redisClient.Close(); err != nil {
		log.Printf("redis close error: %v", err)
	}
}
