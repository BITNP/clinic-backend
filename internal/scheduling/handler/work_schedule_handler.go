package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	authhandler "clinic-backend/internal/auth/handler"
	"clinic-backend/internal/httpx"
	schedulingservice "clinic-backend/internal/scheduling/service"

	"github.com/gin-gonic/gin"
)

type WorkScheduleHandler struct {
	svc *schedulingservice.WorkScheduleService
}

func NewWorkScheduleHandler(svc *schedulingservice.WorkScheduleService) *WorkScheduleHandler {
	return &WorkScheduleHandler{svc: svc}
}

type weekdayRequest struct {
	Weekday   int    `json:"weekday" binding:"required"`
	StartTime string `json:"start_time" binding:"required"`
	EndTime   string `json:"end_time" binding:"required"`
	RoomID    uint   `json:"room_id" binding:"required"`
	StaffIDs  []int  `json:"staff_ids"`
}

type createWorkScheduleRequest struct {
	Name      string           `json:"name" binding:"required,max=128"`
	StartDate string           `json:"start_date" binding:"required"`
	EndDate   string           `json:"end_date" binding:"required"`
	Enabled   *bool            `json:"enabled"`
	Weekdays  []weekdayRequest `json:"weekdays"`
}

type updateWorkScheduleRequest struct {
	Name      *string          `json:"name" binding:"omitempty,max=128"`
	StartDate *string          `json:"start_date"`
	EndDate   *string          `json:"end_date"`
	Enabled   *bool            `json:"enabled"`
	Weekdays  []weekdayRequest `json:"weekdays"`
}

type staffAssignmentRequest struct {
	WeekdayID uint `json:"weekday_id"`
	StaffID   int  `json:"staff_id" binding:"required"`
	RoomID    uint `json:"room_id"`
	Weekday   int  `json:"weekday"`
}

type updateWeekdayTimeRequest struct {
	RoomID    uint   `json:"room_id" binding:"required"`
	Weekday   int    `json:"weekday" binding:"required"`
	StartTime string `json:"start_time" binding:"required"`
	EndTime   string `json:"end_time" binding:"required"`
}

func (h *WorkScheduleHandler) Create(c *gin.Context) {
	var req createWorkScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	startDate, err := httpx.ParseDateString(req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("start_date: %v", err)})
		return
	}
	endDate, err := httpx.ParseDateString(req.EndDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("end_date: %v", err)})
		return
	}
	enabled := false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	weekdays := make([]schedulingservice.WeekdayInput, len(req.Weekdays))
	for i, wd := range req.Weekdays {
		if wd.StaffIDs == nil {
			wd.StaffIDs = []int{}
		}
		weekdays[i] = schedulingservice.WeekdayInput{
			Weekday:   wd.Weekday,
			StartTime: wd.StartTime,
			EndTime:   wd.EndTime,
			RoomID:    wd.RoomID,
			StaffIDs:  wd.StaffIDs,
		}
	}

	sch, err := h.svc.Create(schedulingservice.CreateWorkScheduleInput{
		Name:      req.Name,
		StartDate: startDate,
		EndDate:   endDate,
		Enabled:   enabled,
		Weekdays:  weekdays,
	})
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, sch)
}

func (h *WorkScheduleHandler) Get(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	sch, err := h.svc.GetByID(id)
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	// Staff-only requests must only see enabled schedules.
	role, roleOk := authhandler.ContextRole(c)
	if roleOk && role == authhandler.RoleStaff && !sch.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": schedulingservice.ErrWorkScheduleNotFound.Error()})
		return
	}
	c.JSON(http.StatusOK, sch)
}

func (h *WorkScheduleHandler) List(c *gin.Context) {
	f := schedulingservice.ListWorkScheduleFilter{
		Enabled: boolPtr(true),
	}
	f.Page, f.PageSize = httpx.ParsePagination(c)
	items, total, err := h.svc.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

func (h *WorkScheduleHandler) ListAll(c *gin.Context) {
	f := schedulingservice.ListWorkScheduleFilter{}
	if v := c.Query("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			f.Enabled = &b
		}
	}
	f.Page, f.PageSize = httpx.ParsePagination(c)
	items, total, err := h.svc.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

func (h *WorkScheduleHandler) ServiceAvailability(c *gin.Context) {
	from, err := time.Parse(time.RFC3339, c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing from"})
		return
	}
	to, err := time.Parse(time.RFC3339, c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing to"})
		return
	}
	if to.Before(from) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must not be before from"})
		return
	}

	roomIDsStr := c.Query("room_ids")
	if roomIDsStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing room_ids"})
		return
	}

	parts := strings.Split(roomIDsStr, ",")
	roomIDs := make([]uint, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid room_id: %s", p)})
			return
		}
		roomIDs = append(roomIDs, uint(id))
	}

	items, err := h.svc.ServiceAvailability(from, to, roomIDs)
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *WorkScheduleHandler) Update(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req updateWorkScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var startDate, endDate *time.Time
	if req.StartDate != nil {
		parsed, err := httpx.ParseDateString(*req.StartDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("start_date: %v", err)})
			return
		}
		startDate = &parsed
	}
	if req.EndDate != nil {
		parsed, err := httpx.ParseDateString(*req.EndDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("end_date: %v", err)})
			return
		}
		endDate = &parsed
	}

	var weekdays []schedulingservice.WeekdayInput
	if req.Weekdays != nil {
		weekdays = make([]schedulingservice.WeekdayInput, len(req.Weekdays))
		for i, wd := range req.Weekdays {
			if wd.StaffIDs == nil {
				wd.StaffIDs = []int{}
			}
			weekdays[i] = schedulingservice.WeekdayInput{
				Weekday:   wd.Weekday,
				StartTime: wd.StartTime,
				EndTime:   wd.EndTime,
				RoomID:    wd.RoomID,
				StaffIDs:  wd.StaffIDs,
			}
		}
	}

	sch, err := h.svc.Update(id, schedulingservice.UpdateWorkScheduleInput{
		Name:      req.Name,
		StartDate: startDate,
		EndDate:   endDate,
		Enabled:   req.Enabled,
		Weekdays:  weekdays,
	})
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusOK, sch)
}

func (h *WorkScheduleHandler) Delete(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *WorkScheduleHandler) AddStaff(c *gin.Context) {
	scheduleID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req staffAssignmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.WeekdayID == 0 && req.RoomID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must provide weekday_id or (room_id + weekday)"})
		return
	}
	assign, err := h.svc.AddStaff(scheduleID, schedulingservice.StaffAssignmentInput{
		WeekdayID: req.WeekdayID,
		StaffID:   req.StaffID,
		RoomID:    req.RoomID,
		Weekday:   req.Weekday,
	})
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, assign)
}

func (h *WorkScheduleHandler) RemoveStaff(c *gin.Context) {
	scheduleID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req staffAssignmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.RemoveStaff(scheduleID, schedulingservice.StaffAssignmentInput{
		WeekdayID: req.WeekdayID,
		StaffID:   req.StaffID,
	}); err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *WorkScheduleHandler) ListValidStaff(c *gin.Context) {
	scheduleID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	staff, err := h.svc.ListValidStaff(scheduleID)
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": staff})
}

func (h *WorkScheduleHandler) ListStaff(c *gin.Context) {
	scheduleID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	staff, err := h.svc.ListStaff(scheduleID)
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": staff})
}

func (h *WorkScheduleHandler) UpdateWeekday(c *gin.Context) {
	scheduleID, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req updateWeekdayTimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	wd, err := h.svc.UpdateWeekday(scheduleID, schedulingservice.UpdateWeekdayInput{
		RoomID:    req.RoomID,
		Weekday:   req.Weekday,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	})
	if err != nil {
		writeWorkScheduleError(c, err)
		return
	}
	c.JSON(http.StatusOK, wd)
}

var workScheduleErrorMappings = []httpx.ErrStatus{
	{Err: schedulingservice.ErrWorkScheduleNotFound, Status: http.StatusNotFound},
	{Err: schedulingservice.ErrWorkScheduleNameTaken, Status: http.StatusConflict},
	{Err: schedulingservice.ErrWorkScheduleAlreadyEnabled, Status: http.StatusConflict},
	{Err: schedulingservice.ErrWorkScheduleInvalidDateRange, Status: http.StatusBadRequest},
	{Err: schedulingservice.ErrWorkScheduleInvalidWeekday, Status: http.StatusBadRequest},
	{Err: schedulingservice.ErrWorkScheduleInvalidTimeWindow, Status: http.StatusBadRequest},
	{Err: schedulingservice.ErrWorkScheduleRoomNotFound, Status: http.StatusNotFound},
	{Err: schedulingservice.ErrWorkScheduleStaffNotFound, Status: http.StatusNotFound},
	{Err: schedulingservice.ErrWorkScheduleStaffNotInWorkYear, Status: http.StatusBadRequest},
	{Err: schedulingservice.ErrWorkScheduleWeekdayNotFound, Status: http.StatusNotFound},
}

func writeWorkScheduleError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, workScheduleErrorMappings)
}

func boolPtr(v bool) *bool { return &v }
