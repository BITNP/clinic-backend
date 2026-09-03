package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

type ServiceDateHandler struct {
	svc *services.ServiceDateService
}

func NewServiceDateHandler(svc *services.ServiceDateService) *ServiceDateHandler {
	return &ServiceDateHandler{svc: svc}
}

type createServiceDateRequest struct {
	Capacity  uint      `json:"capacity" binding:"required"`
	RoomID    uint      `json:"room_id" binding:"required"`
	Date      time.Time `json:"date" binding:"required"`
	StartTime time.Time `json:"startTime" binding:"required"`
	EndTime   time.Time `json:"endTime" binding:"required"`
	Title     string    `json:"title" binding:"required,max=20"`
}

type updateServiceDateRequest struct {
	Capacity  *uint      `json:"capacity"`
	RoomID    *uint      `json:"room_id"`
	Date      *time.Time `json:"date"`
	StartTime *time.Time `json:"startTime"`
	EndTime   *time.Time `json:"endTime"`
	Title     *string    `json:"title" binding:"omitempty,max=20"`
}

func (h *ServiceDateHandler) Create(c *gin.Context) {
	var req createServiceDateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.validateServiceDateWindow(req.Date, req.StartTime, req.EndTime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	d, err := h.svc.Create(services.CreateServiceDateInput{
		Capacity:  req.Capacity,
		RoomID:    req.RoomID,
		Date:      req.Date,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Title:     req.Title,
	})
	if err != nil {
		writeServiceDateError(c, err)
		return
	}
	c.JSON(http.StatusCreated, d)
}

func (h *ServiceDateHandler) Get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	d, err := h.svc.GetByID(id)
	if err != nil {
		writeServiceDateError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *ServiceDateHandler) List(c *gin.Context) {
	h.list(c, c.Query("active") == "true", nil)
}

// ListUpcoming returns service dates from today onward by default. When an
// explicit date range (from/to) is supplied, that range takes precedence over
// the "today onward" default so past dates can be selected too.
func (h *ServiceDateHandler) ListUpcoming(c *gin.Context) {
	activeOnly := c.Query("from") == "" && c.Query("to") == ""
	h.list(c, activeOnly, h.svc.Location())
}

// ListAll returns all service dates regardless of date.
func (h *ServiceDateHandler) ListAll(c *gin.Context) {
	h.list(c, false, nil)
}

func (h *ServiceDateHandler) list(c *gin.Context, activeOnly bool, todayLoc *time.Location) {
	f := services.ListServiceDateFilter{
		ActiveOnly:  activeOnly,
		TodayLoc:    todayLoc,
		HasCapacity: c.Query("available") == "true",
	}

	if roomIDStr := c.Query("room_id"); roomIDStr != "" {
		id, err := strconv.ParseUint(roomIDStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid room_id: %s", roomIDStr)})
			return
		}
		if id > 0 {
			f.RoomID = new(uint(id))
		}
	}

	if roomIDsStr := c.Query("room_ids"); roomIDsStr != "" {
		parts := strings.Split(roomIDsStr, ",")
		ids := make([]uint, 0, len(parts))
		for _, p := range parts {
			id, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid room_id: %s", p)})
				return
			}
			ids = append(ids, uint(id))
		}
		f.RoomIDs = ids
	}

	if fromStr := c.Query("from"); fromStr != "" {
		v, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid from: %s", fromStr)})
			return
		}
		f.FromDate = v
	}
	if toStr := c.Query("to"); toStr != "" {
		v, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid to: %s", toStr)})
			return
		}
		f.ToDate = v
	}
	if !f.FromDate.IsZero() && !f.ToDate.IsZero() && f.ToDate.Before(f.FromDate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must not be before from"})
		return
	}

	f.Page, f.PageSize = parsePagination(c)

	items, total, err := h.svc.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	paginatedResponse(c, items, total, f.Page, f.PageSize)
}

func (h *ServiceDateHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req updateServiceDateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate the resulting window against provided fields.
	effDate := req.Date
	effStart := req.StartTime
	effEnd := req.EndTime
	if effDate != nil && effStart != nil && effEnd != nil {
		if err := h.validateServiceDateWindow(*effDate, *effStart, *effEnd); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	d, err := h.svc.Update(id, services.UpdateServiceDateInput{
		Capacity:  req.Capacity,
		RoomID:    req.RoomID,
		Date:      req.Date,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Title:     req.Title,
	})
	if err != nil {
		writeServiceDateError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *ServiceDateHandler) Delete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		writeServiceDateError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

var serviceDateErrorMappings = []errStatus{
	{services.ErrServiceDateNotFound, http.StatusNotFound},
	{services.ErrServiceDateRoomNotFound, http.StatusBadRequest},
	{services.ErrServiceDateInUse, http.StatusConflict},
}

// writeServiceDateError maps service errors to HTTP statuses.
func writeServiceDateError(c *gin.Context, err error) {
	writeMappedError(c, err, serviceDateErrorMappings)
}

// validateServiceDateWindow rejects past dates and inverted or same-day-spanning windows.
func (h *ServiceDateHandler) validateServiceDateWindow(date, start, end time.Time) error {
	loc := h.svc.Location()
	today := services.DateInLocation(time.Now(), loc)
	if services.DateInLocation(date, loc).Before(today) {
		return errors.New("date must not be in the past")
	}
	if !start.Before(end) {
		return errors.New("startTime must be before endTime")
	}
	day := services.DateInLocation(start, loc)
	if day.AddDate(0, 0, 1).Before(end) || !services.DateInLocation(end, loc).Equal(day) {
		return errors.New("endTime must fall on the same day as startTime")
	}
	return nil
}
