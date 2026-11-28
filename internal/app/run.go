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
	confighandler "clinic-backend/internal/config/handler"
	configservice "clinic-backend/internal/config/service"
	"clinic-backend/internal/journal"
	journalhandler "clinic-backend/internal/journal/handler"
	"clinic-backend/internal/legacy"
	"clinic-backend/internal/message"
	"clinic-backend/internal/message/napcat"
	"clinic-backend/internal/multimeter"
	recordhandler "clinic-backend/internal/record/handler"
	recordservice "clinic-backend/internal/record/service"
	roomhandler "clinic-backend/internal/room/handler"
	roomservice "clinic-backend/internal/room/service"
	schedulinghandler "clinic-backend/internal/scheduling/handler"
	schedulingservice "clinic-backend/internal/scheduling/service"
	staffhandler "clinic-backend/internal/staff/handler"
	staffservice "clinic-backend/internal/staff/service"
	taggingservice "clinic-backend/internal/tagging/service"

	"gorm.io/gorm"
)

const (
	storageCheckTimeout = 10 * time.Second
)

// Run initializes the backend and serves requests until shutdown.
func Run() {
	storageSvc, err := LoadStorageService(os.Getenv)
	if err != nil {
		log.Fatalf("storage configuration: %v", err)
	}
	multimeterStorageSvc, err := LoadMultimeterStorageService(os.Getenv)
	if err != nil {
		log.Fatalf("multimeter storage configuration: %v", err)
	}

	serviceDateLoc := envLocation("CLINIC_TIMEZONE", time.FixedZone("UTC+8", 8*60*60))

	msgCfg, err := LoadMessageConfig(os.Getenv)
	if err != nil {
		log.Fatalf("message configuration: %v", err)
	}
	msgMgr, err := message.NewManager(msgCfg.Manager)
	if err != nil {
		log.Fatalf("message manager: %v", err)
	}

	var napcatClient *napcat.Client
	if msgCfg.Enabled {
		napcatClient, err = napcat.NewClient(msgCfg.NapCat)
		if err != nil {
			log.Fatalf("napcat client: %v", err)
		}
		if err := msgMgr.Register(msgCfg.SenderKey, napcatClient, msgCfg.Gap); err != nil {
			log.Fatalf("register napcat sender: %v", err)
		}
	}

	db, rawDB := openDatabase()
	journalSvc := journal.NewService(rawDB)
	journalH := journalhandler.NewHandler(journalSvc)
	configSvc := configservice.NewConfigService(rawDB)
	if err := configSvc.EnsureRuntimeSettingsWithInitialValues(legacyRuntimeSettingValues(os.Getenv)); err != nil {
		closeDatabase(db)
		log.Fatalf("seed runtime configuration: %v", err)
	}
	err = configSvc.InitializeRuntime(func(settings configservice.RuntimeSettings) error {
		_, err := LoadServiceDateReminderConfig(settings)
		return err
	})
	if err != nil {
		closeDatabase(db)
		log.Fatalf("load runtime configuration: %v", err)
	}
	configSvc.SetJournalService(journalSvc)
	runtimeSettings := configSvc.Current()
	dingtalkRuntime := newDingTalkRuntimeManager(serviceDateLoc)
	if err := dingtalkRuntime.Initialize(runtimeSettings); err != nil {
		closeDatabase(db)
		log.Fatalf("initialize dingtalk runtime: %v", err)
	}

	photoCfg, err := LoadPhotoConfig(runtimeSettings.RecordPhotoMaxCount, runtimeSettings.RecordPhotoMaxSizeBytes)
	if err != nil {
		closeDatabase(db)
		log.Fatalf("photo configuration: %v", err)
	}

	photoSvc, err := recordservice.NewPhotoService(rawDB, storageSvc, photoCfg.MaxCount, photoCfg.MaxSizeBytes)
	if err != nil {
		closeDatabase(db)
		log.Fatalf("photo service: %v", err)
	}
	photoSvc.SetMaxCountGetter(func() int { return configSvc.Current().RecordPhotoMaxCount })
	photoSvc.SetMaxSizeGetter(func() int64 { return configSvc.Current().RecordPhotoMaxSizeBytes })
	photoH := recordhandler.NewPhotoHandler(photoSvc)

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
		closeDatabase(db)
		log.Fatalf("failed to connect to redis: %v", err)
	}

	// Storage is required infrastructure: verify the configured bucket before
	// starting background workers or accepting HTTP requests.
	storageCheckCtx, cancelStorageCheck := context.WithTimeout(context.Background(), storageCheckTimeout)
	storageCheckErr := storageSvc.Check(storageCheckCtx)
	cancelStorageCheck()
	if storageCheckErr != nil {
		if closeErr := redisClient.Close(); closeErr != nil {
			log.Printf("redis close error: %v", closeErr)
		}
		closeDatabase(db)
		log.Fatalf("storage bucket check failed: %v", storageCheckErr)
	}
	log.Println("storage bucket check succeeded")

	// Sync counters live in Redis so every backend replica shares them. Clients
	// poll Snapshot and refresh when a value differs from their last-seen one.
	syncSvc := changes.NewSyncService(changes.NewRedisStore(redisClient))
	journalSvc.SetSyncService(syncSvc)
	syncH := changes.NewSyncHandler(syncSvc, runtimeSettings.SyncPollInterval)
	syncH.SetIntervalGetter(func() time.Duration { return configSvc.Current().SyncPollInterval })

	recordTagSvc := taggingservice.NewRecordTagService(rawDB)
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

	recordTagger := taggingservice.NewRecordTagger(nil, rawDB, recordTagSvc)
	recordTagger.SetSyncService(syncSvc)
	taggerRuntime := newTaggerRuntimeManager(recordTagger, recordTagSvc)
	if err := taggerRuntime.Initialize(runtimeSettings); err != nil {
		log.Fatalf("initialize tagger runtime: %v", err)
	}
	configSvc.SetRuntimeValidator(func(settings configservice.RuntimeSettings) error {
		if _, err := LoadServiceDateReminderConfig(settings); err != nil {
			return err
		}
		if err := taggerRuntime.Prepare(settings); err != nil {
			return err
		}
		return dingtalkRuntime.Prepare(settings)
	})
	configSvc.SetRuntimeApplyHook(func(settings configservice.RuntimeSettings) {
		taggerRuntime.Activate(settings)
		dingtalkRuntime.Activate(settings)
	})

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("get SQL database: %v", err)
	}
	announcementSvc := announcement.NewAnnouncementService(sqlDB)
	announcementH := announcement.NewAnnouncementHandler(announcementSvc)
	multimeterH := multimeter.NewHandler(multimeterStorageSvc)

	serviceDateSvc := schedulingservice.NewServiceDateService(rawDB, serviceDateLoc)
	serviceDateH := schedulinghandler.NewServiceDateHandler(serviceDateSvc)

	roomSvc := roomservice.NewRoomService(rawDB, serviceDateLoc)
	roomH := roomhandler.NewRoomHandler(roomSvc)
	campusSvc := roomservice.NewCampusService(rawDB)
	campusSvc.SetJournalService(journalSvc)
	campusH := roomhandler.NewCampusHandler(campusSvc, roomSvc)
	userTicketBanSvc := recordservice.NewUserTicketBanService(rawDB)
	userTicketBanH := recordhandler.NewUserTicketBanHandler(userTicketBanSvc)

	ticketSvc := recordservice.NewTicketService(rawDB, serviceDateLoc)
	ticketSvc.SetDefaultTagID(recordTagSvc.DefaultID())
	ticketSvc.SetPhotoService(photoSvc)
	ticketSvc.SetTagger(recordTagger)
	ticketH := recordhandler.NewTicketHandler(ticketSvc)
	legacyH := legacy.NewLegacyHandler(ticketSvc, serviceDateSvc, roomSvc, announcementSvc)

	staffSvc := staffservice.NewStaffService(rawDB)
	staffH := staffhandler.NewStaffHandler(staffSvc)
	configH := confighandler.NewConfigHandler(configSvc)

	sessionTTL := envDuration("SESSION_TTL", 336*time.Hour)
	sessionSvc := authservice.NewSessionService(sqlDB, sessionTTL)

	adminRecordSvc := recordservice.NewAdminRecordService(rawDB)
	adminRecordSvc.SetRecordTagService(recordTagSvc)
	recordActionSvc := recordservice.NewRecordActionService(
		recordservice.NewRedisRecordActionStore(redisClient),
		runtimeSettings.RecordActionWindow,
	)
	recordActionSvc.SetWindowGetter(func() time.Duration { return configSvc.Current().RecordActionWindow })
	adminRecordSvc.SetRecordActionService(recordActionSvc)
	adminRecordSvc.SetTicketStatusNotifier(dingtalkRuntime)
	adminRecordSvc.SetJournalService(journalSvc)
	adminRecordH := recordhandler.NewAdminRecordHandler(adminRecordSvc)

	workScheduleSvc := schedulingservice.NewWorkScheduleService(rawDB)
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

	// Current required login version, loaded from the database config item.
	staffVersion := func() int { return configSvc.Current().StaffVersion }
	keycloakAuth.SetStaffVersionGetter(staffVersion)

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
		Client:             casClient,
		SessionService:     sessionSvc,
		StaffService:       staffSvc,
		SyncService:        syncSvc,
		BaseURL:            appBaseURL,
		DefaultNext:        envString("CAS_DEFAULT_REDIRECT", "/manage/"),
		CookieName:         envString("SESSION_COOKIE_NAME", "sessionid"),
		CSRFCookieName:     envString("CSRF_COOKIE_NAME", "csrf_token"),
		CookieSecure:       envBool("SESSION_COOKIE_SECURE", false),
		CookieSameSite:     envSameSite("SESSION_COOKIE_SAMESITE", http.SameSiteLaxMode),
		SessionTTL:         sessionTTL,
		StaffVersionGetter: staffVersion,
	})

	authCfg := authhandler.AdminAuthConfig{
		SessionService:     sessionSvc,
		StaffService:       staffSvc,
		KeycloakAuth:       keycloakAuth,
		CookieName:         envString("SESSION_COOKIE_NAME", "sessionid"),
		StaffVersionGetter: staffVersion,
	}
	adminAuth := authhandler.NewAdminAuthMiddleware(authCfg)
	optionalAuth := authhandler.NewOptionalAdminAuthMiddleware(authCfg)

	r := NewRouter(RouterConfig{
		AnnouncementH:  announcementH,
		ServiceDateH:   serviceDateH,
		CampusH:        campusH,
		UserTicketBanH: userTicketBanH,
		RoomH:          roomH,
		ConfigH:        configH,
		JournalH:       journalH,
		StaffH:         staffH,
		WorkScheduleH:  workScheduleH,
		AdminRecordH:   adminRecordH,
		TicketH:        ticketH,
		LegacyH:        legacyH,
		CASHandler:     casHandler,
		UserH:          userH,
		SyncH:          syncH,
		AdminAuth:      adminAuth,
		OptionalAuth:   optionalAuth,
		ClientAuth:     clientAuth,
		SyncSvc:        syncSvc,
		RedisClient:    redisClient,
		PhotoH:         photoH,
		MultimeterH:    multimeterH,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var reminderDone <-chan struct{}
	{
		discovery := &schedulingservice.ReminderDiscovery{
			DB:        rawDB,
			Location:  serviceDateLoc,
			LeadTimes: runtimeSettings.ServiceDateReminderLeadTimes,
			Journal:   journalSvc,
		}
		delivery := &schedulingservice.ReminderDelivery{
			DB:        rawDB,
			Discovery: discovery,
			Client:    &http.Client{},
			Journal:   journalSvc,
			Config: func() (schedulingservice.ReminderDeliveryConfig, error) {
				cfg, err := LoadServiceDateReminderConfig(configSvc.Current())
				if err != nil {
					return schedulingservice.ReminderDeliveryConfig{}, err
				}
				return schedulingservice.ReminderDeliveryConfig{
					Enabled:            cfg.Enabled,
					LeadTimes:          cfg.LeadTimes,
					CompletionDelay:    cfg.CompletionDelay,
					BaseURL:            cfg.Gateway.BaseURL,
					Account:            cfg.Gateway.Account,
					Token:              cfg.Gateway.AccountToken,
					Timeout:            cfg.Gateway.Timeout,
					Template:           cfg.MessageTemplate,
					CompletionTemplate: cfg.CompletionMessageTemplate,
				}, nil
			},
		}
		leadTimes := func() []time.Duration { return configSvc.Current().ServiceDateReminderLeadTimes }
		completionDelay := func() time.Duration {
			return configSvc.Current().ServiceDateCompletionReminderDelay
		}
		enabled := func() bool { return configSvc.Current().ServiceDateReminderEnabled }
		discovery.LeadTimesGetter = leadTimes
		discovery.CompletionDelayGetter = completionDelay
		discovery.Guard = func(run func() error) error {
			return configSvc.WithRuntimeSettings(func(settings configservice.RuntimeSettings) error {
				if !settings.ServiceDateReminderEnabled {
					return nil
				}
				return run()
			})
		}
		worker := &schedulingservice.ReminderWorker{Discovery: discovery, Delivery: delivery, Enabled: enabled}
		done := make(chan struct{})
		reminderDone = done
		go func() { defer close(done); worker.Run(ctx) }()
	}

	var dingtalkReminderDone <-chan struct{}
	{
		cfg := dingtalkRuntime.Config()
		discovery := &recordservice.TicketReminderDiscovery{
			DB:       rawDB,
			LeadTime: cfg.Reminder.LeadTime,
			Journal:  journalSvc,
		}
		delivery := &recordservice.TicketReminderDelivery{
			DB:       rawDB,
			Notifier: dingtalkRuntime,
			Location: serviceDateLoc,
			Journal:  journalSvc,
		}
		worker := &recordservice.TicketReminderWorker{
			Discovery:       discovery,
			Delivery:        delivery,
			PollInterval:    cfg.Reminder.PollInterval,
			DeliveryTimeout: 3*cfg.Timeout + 5*time.Second,
			Config: func() recordservice.TicketReminderWorkerConfig {
				current := dingtalkRuntime.Config()
				return recordservice.TicketReminderWorkerConfig{
					Enabled:         current.Enabled,
					LeadTime:        current.Reminder.LeadTime,
					PollInterval:    current.Reminder.PollInterval,
					DeliveryTimeout: 3*current.Timeout + 5*time.Second,
				}
			},
		}
		done := make(chan struct{})
		dingtalkReminderDone = done
		go func() { defer close(done); worker.Run(ctx) }()
	}

	if napcatClient != nil {
		go napcatClient.Run(ctx)
		log.Printf("legacy in-process message manager enabled at %s (sender %s); prefer message-gateway", msgCfg.NapCat.BaseURL, msgCfg.SenderKey)
	} else {
		log.Println("legacy in-process message manager disabled; outbound messaging is handled by message-gateway")
	}

	msgDone := make(chan struct{})
	go func() {
		defer close(msgDone)
		msgMgr.Run(ctx)
	}()

	go recordTagger.Run(ctx)

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
	select {
	case <-msgDone:
	case <-time.After(5 * time.Second):
		log.Println("message manager shutdown timed out")
	}
	if reminderDone != nil {
		select {
		case <-reminderDone:
		case <-time.After(5 * time.Second):
			log.Println("reminder worker shutdown timed out")
		}
	}
	if dingtalkReminderDone != nil {
		select {
		case <-dingtalkReminderDone:
		case <-time.After(5 * time.Second):
			log.Println("dingtalk ticket reminder worker shutdown timed out")
		}
	}
	if err := redisClient.Close(); err != nil {
		log.Printf("redis close error: %v", err)
	}
}

func closeDatabase(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		log.Printf("database close error: %v", err)
		return
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("database close error: %v", err)
	}
}
