package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"clinic-backend/models"
	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

// legacyRecord mirrors Django's RecordSerializerWechat shape with int status.
type legacyRecord struct {
	URL               string `json:"url"`
	User              string `json:"user"`
	Status            int    `json:"status"`
	Realname          string `json:"realname"`
	PhoneNum          string `json:"phone_num"`
	Campus            string `json:"campus"`
	AppointmentTime   string `json:"appointment_time"`
	Description       string `json:"description"`
	WorkerDescription string `json:"worker_description"`
	Model             string `json:"model"`
	RejectReason      string `json:"reject_reason"`
	Password          string `json:"password"`
}

// legacyTimezone is the timezone the old Django API ran in (Asia/Shanghai,
// UTC+8). The legacy /api/date/ endpoints always shift service times into
// this zone, regardless of the CLINIC_TIMEZONE configured for the rest of
// the new backend.
var legacyTimezone = time.FixedZone("UTC+8", 8*60*60)

// statusMap translates the new backend's string RecordStatus to the old
// Django int codes (1=预约待确认, 2=预约已确认, 3=预约已驳回, 4=登记待受理,
// 5=正在处理, 6=已解决问题, 7=建议返厂, 9=未到诊所).
var statusMap = map[string]int{
	"pending":     1,
	"confirmed":   2,
	"rejected":    3,
	"arrived":     4,
	"in_progress": 5,
	"completed":   6,
	"referred":    7,
	"no_show":     9,
}

func legacyStatus(s models.RecordStatus) int {
	v, ok := statusMap[string(s)]
	if !ok {
		return 1 // default to pending for unknown statuses
	}
	return v
}

// LegacyHandler serves old Django-style responses for the customer-facing
// light-app. It delegates to the existing services and reshapes every response
// to match the DRF + RecordSerializerWechat wire format from clinic_django.
type LegacyHandler struct {
	ticketSvc       *services.TicketService
	serviceDateSvc  *services.ServiceDateService
	roomSvc         *services.RoomService
	announcementSvc *services.AnnouncementService
}

func NewLegacyHandler(
	ticketSvc *services.TicketService,
	serviceDateSvc *services.ServiceDateService,
	roomSvc *services.RoomService,
	announcementSvc *services.AnnouncementService,
) *LegacyHandler {
	return &LegacyHandler{
		ticketSvc:       ticketSvc,
		serviceDateSvc:  serviceDateSvc,
		roomSvc:         roomSvc,
		announcementSvc: announcementSvc,
	}
}

// ── Record helpers ──────────────────────────────────────────────────────

func (h *LegacyHandler) username(c *gin.Context) (string, bool) {
	user := c.GetString("user")
	if user == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return "", false
	}
	return user, true
}

// legacyRecordFrom reshapes a record for the legacy /api/wechat endpoints.
// Unlike TicketView (which formats in UTC), the legacy wire format always
// shifts times into the clinic timezone (UTC+8 for Asia/Shanghai), matching
// what the old Django API returned with TIME_ZONE=Asia/Shanghai.
func (h *LegacyHandler) legacyRecordFrom(rec models.ClinicRecord) (legacyRecord, error) {
	v, err := h.ticketSvc.View(rec)
	if err != nil {
		return legacyRecord{}, err
	}
	return legacyRecord{
		URL:               fmt.Sprintf("/api/wechat/%d/", rec.ID),
		User:              v.User,
		Status:            legacyStatus(rec.Status),
		Realname:          v.Realname,
		PhoneNum:          v.PhoneNum,
		Campus:            v.Campus,
		AppointmentTime:   rec.AppointmentTime.In(h.serviceDateSvc.Location()).Format("2006-01-02"),
		Description:       v.Description,
		WorkerDescription: v.WorkerDescription,
		Model:             v.Model,
		RejectReason:      v.RejectReason,
		Password:          v.Password,
	}, nil
}

// drfPage wraps a list of items into Django's DRF pagination envelope.
// PageSize is fixed at 10 (DRF default used by clinic_django).
func drfPage(items any, total int64, page int) gin.H {
	pageSize := int64(10)
	var next any
	if int64(page)*pageSize < total {
		s := "?page=" + strconv.Itoa(page+1)
		next = s
	}
	var prev any
	if page > 1 {
		s := "?page=" + strconv.Itoa(page-1)
		prev = s
	}
	return gin.H{
		"count":    total,
		"next":     next,
		"previous": prev,
		"results":  items,
	}
}

// ── Ticket / Wechat endpoints ──────────────────────────────────────────

func (h *LegacyHandler) ListRecords(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	page := parseIntDefault(c, "page", 1)
	f := services.ListTicketFilter{Page: page, PageSize: 10}
	records, total, err := h.ticketSvc.List(user, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]legacyRecord, 0, len(records))
	for _, rec := range records {
		lr, err := h.legacyRecordFrom(rec)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		items = append(items, lr)
	}
	c.JSON(http.StatusOK, drfPage(items, total, page))
}

func (h *LegacyHandler) CreateRecord(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	var req createTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	appointmentTime, err := time.Parse("2006-01-02", req.AppointmentTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid appointment_time"})
		return
	}
	rec, err := h.ticketSvc.Create(services.CreateTicketInput{
		User:            user,
		Realname:        req.Realname,
		PhoneNum:        req.PhoneNum,
		Campus:          req.Campus,
		AppointmentTime: appointmentTime,
		Description:     req.Description,
		Model:           req.Model,
		Password:        req.Password,
	})
	if err != nil {
		writeTicketError(c, err)
		return
	}
	lr, err := h.legacyRecordFrom(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, lr)
}

func (h *LegacyHandler) WorkingRecord(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	rec, err := h.ticketSvc.Working(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rec == nil {
		c.JSON(http.StatusOK, gin.H{"count": 0})
		return
	}
	lr, err := h.legacyRecordFrom(*rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count": 1,
		"data":  lr,
	})
}

func (h *LegacyHandler) FinishRecords(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	page := parseIntDefault(c, "page", 1)
	f := services.ListTicketFilter{Page: page, PageSize: 10}
	records, total, err := h.ticketSvc.Finished(user, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]legacyRecord, 0, len(records))
	for _, rec := range records {
		lr, err := h.legacyRecordFrom(rec)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		items = append(items, lr)
	}
	c.JSON(http.StatusOK, drfPage(items, total, page))
}

func (h *LegacyHandler) GetRecord(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	rec, err := h.ticketSvc.GetForUser(id, user)
	if err != nil {
		writeTicketError(c, err)
		return
	}
	lr, err := h.legacyRecordFrom(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, lr)
}

func (h *LegacyHandler) UpdateRecord(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req updateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	in := services.UpdateTicketInput{
		Realname:    req.Realname,
		PhoneNum:    req.PhoneNum,
		Campus:      req.Campus,
		Description: req.Description,
		Model:       req.Model,
		Password:    req.Password,
	}
	if req.AppointmentTime != nil {
		t, err := time.Parse("2006-01-02", *req.AppointmentTime)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid appointment_time"})
			return
		}
		in.AppointmentTime = &t
	}
	rec, err := h.ticketSvc.UpdateForUser(id, user, in)
	if err != nil {
		writeTicketError(c, err)
		return
	}
	lr, err := h.legacyRecordFrom(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, lr)
}

func (h *LegacyHandler) DeleteRecord(c *gin.Context) {
	user, ok := h.username(c)
	if !ok {
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.ticketSvc.DeleteForUser(id, user); err != nil {
		writeTicketError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// ── Campus (old: /api/campus/) ──────────────────────────────────────────

func (h *LegacyHandler) ListCampus(c *gin.Context) {
	f := services.ListRoomFilter{EnabledOnly: true, Page: 1, PageSize: 1000}
	rooms, _, err := h.roomSvc.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type campusItem struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	items := make([]campusItem, 0, len(rooms))
	for _, r := range rooms {
		items = append(items, campusItem{Name: r.Name, Address: r.Address})
	}
	c.JSON(http.StatusOK, items)
}

// dateItem is the legacy /api/date/ response shape.
type dateItem struct {
	ID        uint   `json:"id"`
	Title     string `json:"title"`
	Date      string `json:"date"`
	Capacity  uint   `json:"capacity"`
	Campus    string `json:"campus"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	Count     int64  `json:"count"`
	Finish    int    `json:"finish"`
	Working   int    `json:"working"`
}

// listDateItems returns service dates formatted for the legacy endpoint.
// When activeOnly is true, only dates from today onward are returned.
func (h *LegacyHandler) listDateItems(activeOnly bool) ([]dateItem, error) {
	f := services.ListServiceDateFilter{
		ActiveOnly: activeOnly,
		TodayLoc:   legacyTimezone,
		Page:       1,
		PageSize:   1000,
	}
	dates, _, err := h.serviceDateSvc.List(f)
	if err != nil {
		return nil, err
	}

	// Build a room-id → room-name map for joining.
	roomF := services.ListRoomFilter{Page: 1, PageSize: 1000}
	rooms, _, _ := h.roomSvc.List(roomF)
	roomNames := make(map[uint]string, len(rooms))
	for _, r := range rooms {
		roomNames[r.ID] = r.Name
	}

	loc := legacyTimezone
	items := make([]dateItem, 0, len(dates))
	for _, d := range dates {
		campus := ""
		if d.RoomID != nil {
			campus = roomNames[*d.RoomID]
		}
		items = append(items, dateItem{
			ID:        d.ID,
			Title:     d.Title,
			Date:      d.Date.In(loc).Format("2006-01-02"),
			Capacity:  d.Capacity,
			Campus:    campus,
			StartTime: d.StartTime.In(loc).Format("15:04:05"),
			EndTime:   d.EndTime.In(loc).Format("15:04:05"),
			Count:     d.Count,
			Finish:    0, // not exposed by new backend; kept for shape compat
			Working:   0,
		})
	}
	return items, nil
}

// ── Service Dates (old: /api/date/) ─────────────────────────────────────

// ListDates returns service dates from today onward.
func (h *LegacyHandler) ListDates(c *gin.Context) {
	items, err := h.listDateItems(true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

// ListAllDates returns all service dates regardless of date.
func (h *LegacyHandler) ListAllDates(c *gin.Context) {
	items, err := h.listDateItems(false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

// ── Announcements (old: /api/announcement/) ─────────────────────────────

func (h *LegacyHandler) ListAnnouncements(c *gin.Context) {
	f := services.ListAnnouncementFilter{ActiveOnly: true, Page: 1, PageSize: 1000}
	announcements, _, err := h.announcementSvc.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type announcementItem struct {
		ID             uint   `json:"id"`
		Title          string `json:"title"`
		Content        string `json:"content"`
		Brief          string `json:"brief"`
		Tag            string `json:"tag"`
		CreatedTime    string `json:"createdTime"`
		LastEditedTime string `json:"lastEditedTime"`
		ExpireDate     string `json:"expireDate"`
		Priority       uint   `json:"priority"`
	}
	loc := h.serviceDateSvc.Location()
	items := make([]announcementItem, 0, len(announcements))
	for _, a := range announcements {
		items = append(items, announcementItem{
			ID:             a.ID,
			Title:          a.Title,
			Content:        a.Content,
			Brief:          a.Brief,
			Tag:            string(a.Tag),
			CreatedTime:    a.CreatedTime.In(loc).Format(time.RFC3339),
			LastEditedTime: a.LastEditedTime.In(loc).Format(time.RFC3339),
			ExpireDate:     a.ExpireDate.In(loc).Format("2006-01-02"),
			Priority:       a.Priority,
		})
	}
	c.JSON(http.StatusOK, items)
}

// ── TOS (old: /api/announcement/toc/) ───────────────────────────────────

func (h *LegacyHandler) TOS(c *gin.Context) {
	a, err := h.announcementSvc.GetTOS()
	if err != nil {
		if errors.Is(err, services.ErrAnnouncementNotFound) {
			c.JSON(http.StatusOK, gin.H{"content": "暂无公告"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"content": a.Content})
}
