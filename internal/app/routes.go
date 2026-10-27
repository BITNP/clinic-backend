package app

import (
	"clinic-backend/internal/announcement"
	authhandler "clinic-backend/internal/auth/handler"
	"clinic-backend/internal/changes"
	"clinic-backend/internal/legacy"
	recordhandler "clinic-backend/internal/record/handler"
	roomhandler "clinic-backend/internal/room/handler"
	schedulinghandler "clinic-backend/internal/scheduling/handler"
	staffhandler "clinic-backend/internal/staff/handler"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type routeHandlers struct {
	announcementH *announcement.AnnouncementHandler
	serviceDateH  *schedulinghandler.ServiceDateHandler
	roomH         *roomhandler.RoomHandler
	staffH        *staffhandler.StaffHandler
	workScheduleH *schedulinghandler.WorkScheduleHandler
	adminRecordH  *recordhandler.AdminRecordHandler
	ticketH       *recordhandler.TicketHandler
	photoH        *recordhandler.PhotoHandler
	legacyH       *legacy.LegacyHandler
	casHandler    *authhandler.CASAuthHandler
	userH         *authhandler.UserHandler
	syncH         *changes.SyncHandler
	adminAuth     gin.HandlerFunc
	optionalAuth  gin.HandlerFunc
	clientAuth    gin.HandlerFunc
	syncSvc       *changes.SyncService
	redisClient   *redis.Client
}

func (h routeHandlers) router() *gin.Engine {
	r := gin.Default()
	r.RedirectTrailingSlash = false

	// ── Health ────────────────────────────────────────────────────────────
	r.GET("/healthz", redisHealthHandler(h.redisClient))

	// ── CAS login/logout ─────────────────────────────────────────────────
	r.GET("/login", h.casHandler.Login)
	r.GET("/logout", h.casHandler.Logout)

	// ── Admin: Announcements ──────────────────────────────────────────────
	// Staff and admin can read; admin only can write.
	annRead := r.Group("/api/admin/announcements")
	annRead.Use(h.adminAuth, authhandler.RequireStaff)
	{
		annRead.GET("", h.announcementH.List)
		annRead.GET("/:id", h.announcementH.Get)
	}
	annWrite := r.Group("/api/admin/announcements")
	annWrite.Use(h.adminAuth, authhandler.RequireAdmin, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupAnnouncement))
	{
		annWrite.POST("", h.announcementH.Create)
		annWrite.PUT("/:id", h.announcementH.Update)
		annWrite.DELETE("/:id", h.announcementH.Delete)
	}

	// ── Admin: Service Dates ──────────────────────────────────────────────
	sdRead := r.Group("/api/admin/service-dates")
	sdRead.Use(h.adminAuth, authhandler.RequireStaff)
	{
		sdRead.GET("", h.serviceDateH.ListUpcoming)
		sdRead.GET("/all", h.serviceDateH.ListAll)
		sdRead.GET("/:id", h.serviceDateH.Get)
	}
	sdWrite := r.Group("/api/admin/service-dates")
	sdWrite.Use(h.adminAuth, authhandler.RequireAdmin, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupServiceDate))
	{
		sdWrite.POST("", h.serviceDateH.Create)
		sdWrite.PUT("/:id", h.serviceDateH.Update)
		sdWrite.DELETE("/:id", h.serviceDateH.Delete)
	}

	// ── Admin: Rooms ──────────────────────────────────────────────────────
	roomRead := r.Group("/api/admin/rooms")
	roomRead.Use(h.adminAuth, authhandler.RequireStaff)
	{
		roomRead.GET("", h.roomH.List)
		roomRead.GET("/:id", h.roomH.Get)
	}
	roomWrite := r.Group("/api/admin/rooms")
	roomWrite.Use(h.adminAuth, authhandler.RequireAdmin, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupRoom))
	{
		roomWrite.POST("", h.roomH.Create)
		roomWrite.PUT("/:id", h.roomH.Update)
		roomWrite.DELETE("/:id", h.roomH.Delete)
	}

	// ── Admin: Records (staff can read and perform site operations) ───────
	records := r.Group("/api/admin/records")
	records.Use(h.adminAuth, authhandler.RequireStaff, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupRecord))
	{
		records.GET("", h.adminRecordH.List)
		records.GET("/:id", h.adminRecordH.Get)
		records.PUT("/:id", h.adminRecordH.Update)
		records.GET("/:id/photos", h.photoH.List)
		records.GET("/:id/photos/:position", h.photoH.Get)
		records.POST("/:id/photos", h.photoH.Create)
		records.PUT("/:id/photos/:position", h.photoH.Replace)
		records.DELETE("/:id/photos/:position", h.photoH.Delete)
		records.POST("/:id/arrive", h.adminRecordH.Arrive)
		records.POST("/:id/in-progress", h.adminRecordH.InProgress)
		records.POST("/:id/complete", h.adminRecordH.Complete)
		records.POST("/:id/refer", h.adminRecordH.Refer)
		records.POST("/:id/no-show", h.adminRecordH.NoShow)
		records.POST("/:id/revert", h.adminRecordH.Revert)
	}
	// ── Admin: Records (admin only — approve / reject) ────────────────────
	recordsAdmin := r.Group("/api/admin/records")
	recordsAdmin.Use(h.adminAuth, authhandler.RequireAdmin, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupRecord))
	{
		recordsAdmin.POST("/:id/confirm", h.adminRecordH.Confirm)
		recordsAdmin.POST("/:id/reject", h.adminRecordH.Reject)
	}

	// ── Admin: Staff Management (admin only) ──────────────────────────────
	staffAdm := r.Group("/api/admin/staff")
	staffAdm.Use(h.adminAuth, authhandler.RequireAdmin, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupStaff))
	{
		staffAdm.GET("", h.staffH.List)
		staffAdm.GET("/:id", h.staffH.Get)
		staffAdm.POST("", h.staffH.Create)
		staffAdm.PUT("/:id", h.staffH.Update)
		staffAdm.DELETE("/:id", h.staffH.Delete)
	}

	// ── Admin: Work Schedules ─────────────────────────────────────────────
	wsRead := r.Group("/api/admin/work-schedules")
	wsRead.Use(h.adminAuth, authhandler.RequireStaff)
	{
		wsRead.GET("", h.workScheduleH.List)
		wsRead.GET("/:id", h.workScheduleH.Get)
	}
	wsWrite := r.Group("/api/admin/work-schedules")
	wsWrite.Use(h.adminAuth, authhandler.RequireAdmin, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupWorkSchedule))
	{
		wsWrite.GET("/all", h.workScheduleH.ListAll)
		wsWrite.GET("/service-availability", h.workScheduleH.ServiceAvailability)
		wsWrite.POST("", h.workScheduleH.Create)
		wsWrite.PUT("/:id", h.workScheduleH.Update)
		wsWrite.DELETE("/:id", h.workScheduleH.Delete)
		wsWrite.GET("/:id/staff", h.workScheduleH.ListStaff)
		wsWrite.POST("/:id/staff", h.workScheduleH.AddStaff)
		wsWrite.DELETE("/:id/staff", h.workScheduleH.RemoveStaff)
		wsWrite.GET("/:id/valid-staff", h.workScheduleH.ListValidStaff)
		wsWrite.PUT("/:id/weekdays", h.workScheduleH.UpdateWeekday)
	}

	// ── Client routes (API-key signature middleware). ─────────────────────
	client := r.Group("/api/announcements")
	client.Use(h.clientAuth)
	{
		client.GET("", h.announcementH.List)
		client.GET("/:id", h.announcementH.Get)
	}
	serviceDateClient := r.Group("/api/service-dates")
	serviceDateClient.Use(h.clientAuth)
	{
		serviceDateClient.GET("", h.serviceDateH.List)
		serviceDateClient.GET("/:id", h.serviceDateH.Get)
	}
	roomClient := r.Group("/api/rooms")
	roomClient.Use(h.clientAuth)
	{
		roomClient.GET("", h.roomH.List)
		roomClient.GET("/:id", h.roomH.Get)
	}

	// ── Ticket routes (client) ────────────────────────────────────────────
	ticket := r.Group("/api/tickets")
	ticket.Use(h.clientAuth, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupRecord))
	{
		ticket.GET("", h.ticketH.List)
		ticket.POST("", h.ticketH.Create)
		ticket.GET("/working", h.ticketH.Working)
		ticket.GET("/finished", h.ticketH.Finished)
		ticket.GET("/:id", h.ticketH.Get)
		ticket.PUT("/:id", h.ticketH.Update)
		ticket.PATCH("/:id", h.ticketH.Update)
		ticket.DELETE("/:id", h.ticketH.Delete)
	}

	// ── Legacy customer endpoints (old Django shapes) ─────────────────────
	// TOS — returns {"content": ...} matching Django's /api/announcement/toc/
	r.GET("/api/announcement/toc", h.legacyH.TOS)
	r.GET("/api/announcement/toc/", h.legacyH.TOS)

	// /api/wechat — ticket lifecycle in old Django DRF shape (int status,
	// {count,next,previous,results} pagination, /api/wechat/{id}/ url).
	wechat := r.Group("/api/wechat")
	wechat.Use(h.clientAuth, changes.SyncBumpMiddleware(h.syncSvc, changes.SyncGroupRecord))
	{
		wechat.GET("", h.legacyH.ListRecords)
		wechat.GET("/", h.legacyH.ListRecords)
		wechat.POST("", h.legacyH.CreateRecord)
		wechat.POST("/", h.legacyH.CreateRecord)
		wechat.GET("/working", h.legacyH.WorkingRecord)
		wechat.GET("/working/", h.legacyH.WorkingRecord)
		wechat.GET("/finished", h.legacyH.FinishRecords)
		wechat.GET("/finished/", h.legacyH.FinishRecords)
		wechat.GET("/finish", h.legacyH.FinishRecords)
		wechat.GET("/finish/", h.legacyH.FinishRecords)
		wechat.GET("/:id", h.legacyH.GetRecord)
		wechat.GET("/:id/", h.legacyH.GetRecord)
		wechat.PUT("/:id", h.legacyH.UpdateRecord)
		wechat.PUT("/:id/", h.legacyH.UpdateRecord)
		wechat.PATCH("/:id", h.legacyH.UpdateRecord)
		wechat.PATCH("/:id/", h.legacyH.UpdateRecord)
		wechat.DELETE("/:id", h.legacyH.DeleteRecord)
		wechat.DELETE("/:id/", h.legacyH.DeleteRecord)
	}

	// /api/campus/ — plain array [{name, address}]
	campus := r.Group("/api/campus")
	campus.Use(h.clientAuth)
	{
		campus.GET("", h.legacyH.ListCampus)
		campus.GET("/", h.legacyH.ListCampus)
	}

	// /api/date/ — plain array of service dates with room name & count
	sd := r.Group("/api/date")
	sd.Use(h.clientAuth)
	{
		sd.GET("", h.legacyH.ListDates)
		sd.GET("/", h.legacyH.ListDates)
		sd.GET("/all", h.legacyH.ListAllDates)
		sd.GET("/all/", h.legacyH.ListAllDates)
	}

	// /api/announcement/ — plain array of active announcements
	ann := r.Group("/api/announcement")
	ann.Use(h.clientAuth)
	{
		ann.GET("", h.legacyH.ListAnnouncements)
		ann.GET("/", h.legacyH.ListAnnouncements)
	}

	// ── Sync counters ─────────────────────────────────────────────────────
	// Clients poll these and refresh when a group's counter differs from the
	// value they last saw. Staff/admin sessions and API-key clients each get a
	// route for the same handler.
	syncAdmin := r.Group("/api/admin/sync")
	syncAdmin.Use(h.adminAuth, authhandler.RequireStaff)
	{
		syncAdmin.GET("", h.syncH.Get)
		syncAdmin.GET("/config", h.syncH.GetConfig)
	}
	syncClient := r.Group("/api/sync")
	syncClient.Use(h.clientAuth)
	{
		syncClient.GET("", h.syncH.Get)
		syncClient.GET("/config", h.syncH.GetConfig)
	}

	// ── User endpoints ────────────────────────────────────────────────────
	r.GET("/api/user", h.adminAuth, h.userH.Current)
	r.GET("/api/user/", h.adminAuth, h.userH.Current)
	r.GET("/api/users/me", h.optionalAuth, h.userH.Me)
	r.GET("/api/users/me/", h.optionalAuth, h.userH.Me)

	return r
}
