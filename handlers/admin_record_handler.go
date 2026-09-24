package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

type AdminRecordHandler struct {
	svc *services.AdminRecordService
}

func NewAdminRecordHandler(svc *services.AdminRecordService) *AdminRecordHandler {
	return &AdminRecordHandler{svc: svc}
}

type rejectRecordRequest struct {
	Reason string `json:"reason" binding:"required"`
}

type referRecordRequest struct {
	Reason string `json:"reason"`
}

func (h *AdminRecordHandler) List(c *gin.Context) {
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	f := services.ListAdminRecordFilter{
		Status:   c.Query("status"),
		Page:     parseIntDefault(c, "page", 1),
		PageSize: parseIntDefault(c, "pageSize", 20),
	}
	if v := c.Query("room_id"); v != "" {
		id, err := parseUintQuery(v)
		if err == nil {
			f.RoomID = &id
		}
	}
	if v := c.Query("from_date"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err == nil {
			f.FromDate = &t
		}
	}
	if v := c.Query("to_date"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err == nil {
			f.ToDate = &t
		}
	}

	items, total, err := h.svc.List(c.Request.Context(), staff.ID, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":    items,
		"total":    total,
		"page":     f.Page,
		"pageSize": f.PageSize,
	})
}

func (h *AdminRecordHandler) Get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	v, err := h.svc.GetByID(c.Request.Context(), staff.ID, id)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

type updateRecordRequest struct {
	WorkerDesc *string `json:"worker_desc"`
}

func (h *AdminRecordHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req updateRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	in := services.UpdateAdminRecordInput{
		WorkerDesc: req.WorkerDesc,
	}

	v, err := h.svc.Update(c.Request.Context(), staff.ID, id, in)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) Confirm(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "missing staff context"})
		return
	}

	v, err := h.svc.MarkConfirmed(c.Request.Context(), id, uint(staff.ID))
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) Arrive(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	v, err := h.svc.MarkArrived(c.Request.Context(), staff.ID, id)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) InProgress(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "missing staff context"})
		return
	}
	v, err := h.svc.MarkInProgress(c.Request.Context(), staff.ID, id)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) Complete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	v, err := h.svc.MarkCompleted(c.Request.Context(), staff.ID, id)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) Reject(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "missing staff context"})
		return
	}

	var req rejectRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	v, err := h.svc.MarkRejected(c.Request.Context(), id, req.Reason, uint(staff.ID))
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) Refer(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "missing staff context"})
		return
	}

	var req referRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	v, err := h.svc.MarkReferred(c.Request.Context(), id, req.Reason, uint(staff.ID))
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *AdminRecordHandler) NoShow(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	v, err := h.svc.MarkNoShow(c.Request.Context(), staff.ID, id)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// Revert undoes the acting staff member's latest action on a record.
func (h *AdminRecordHandler) Revert(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	staff, ok := contextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	v, err := h.svc.Revert(c.Request.Context(), staff.ID, id)
	if err != nil {
		writeRecordError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func writeRecordError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrRecordInvalidTransition):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrRevertWindowExpired),
		errors.Is(err, services.ErrRevertUnavailable),
		errors.Is(err, services.ErrRevertSuperseded):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func parseUintQuery(s string) (uint, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}
