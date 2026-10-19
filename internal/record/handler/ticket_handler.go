package handler

import (
	"errors"
	"net/http"
	"time"

	"clinic-backend/internal/httpx"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"

	"github.com/gin-gonic/gin"
)

type TicketHandler struct {
	svc *recordservice.TicketService
}

func NewTicketHandler(svc *recordservice.TicketService) *TicketHandler {
	return &TicketHandler{svc: svc}
}

// CreateTicketRequest is the booking request shared with legacy endpoints.
type CreateTicketRequest struct {
	Realname        string `json:"realname" binding:"required"`
	PhoneNum        string `json:"phone_num" binding:"required"`
	Campus          string `json:"campus" binding:"required"`
	AppointmentTime string `json:"appointment_time" binding:"required"`
	Description     string `json:"description" binding:"required"`
	Model           string `json:"model"`
	Password        string `json:"password"`
}

// UpdateTicketRequest describes customer-editable booking fields.
type UpdateTicketRequest struct {
	Realname        *string `json:"realname"`
	PhoneNum        *string `json:"phone_num"`
	Campus          *string `json:"campus"`
	AppointmentTime *string `json:"appointment_time"`
	Description     *string `json:"description"`
	Model           *string `json:"model"`
	Password        *string `json:"password"`
}

func (h *TicketHandler) Create(c *gin.Context) {
	username := c.GetString("user")
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	var req CreateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	appointmentTime, err := time.Parse("2006-01-02", req.AppointmentTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid appointment_time"})
		return
	}

	rec, err := h.svc.Create(recordservice.CreateTicketInput{
		User:            username,
		Realname:        req.Realname,
		PhoneNum:        req.PhoneNum,
		Campus:          req.Campus,
		AppointmentTime: appointmentTime,
		Description:     req.Description,
		Model:           req.Model,
		Password:        req.Password,
	})
	if err != nil {
		WriteTicketError(c, err)
		return
	}

	view, err := h.svc.View(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, view)
}

func (h *TicketHandler) List(c *gin.Context) {
	h.listTickets(c, h.svc.List)
}

func (h *TicketHandler) Finished(c *gin.Context) {
	h.listTickets(c, h.svc.Finished)
}

func (h *TicketHandler) listTickets(c *gin.Context, fetch func(string, recordservice.ListTicketFilter) ([]recordmodel.ClinicRecord, int64, error)) {
	username := c.GetString("user")
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	f := recordservice.ListTicketFilter{
		Page:     httpx.ParseIntDefault(c, "page", 1),
		PageSize: httpx.ParseIntDefault(c, "pageSize", 20),
	}
	items, total, err := fetch(username, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	views := make([]recordservice.TicketView, 0, len(items))
	for _, rec := range items {
		v, err := h.svc.View(rec)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		views = append(views, v)
	}
	c.JSON(http.StatusOK, gin.H{
		"items":    views,
		"total":    total,
		"page":     f.Page,
		"pageSize": f.PageSize,
	})
}

func (h *TicketHandler) Working(c *gin.Context) {
	username := c.GetString("user")
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	rec, err := h.svc.Working(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rec == nil {
		c.JSON(http.StatusOK, gin.H{"count": 0})
		return
	}
	view, err := h.svc.View(*rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count": 1,
		"data":  view,
	})
}

func (h *TicketHandler) Get(c *gin.Context) {
	username := c.GetString("user")
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	rec, err := h.svc.GetForUser(id, username)
	if err != nil {
		WriteTicketError(c, err)
		return
	}
	view, err := h.svc.View(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *TicketHandler) Update(c *gin.Context) {
	username := c.GetString("user")
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	var req UpdateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	in := recordservice.UpdateTicketInput{
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

	rec, err := h.svc.UpdateForUser(id, username, in)
	if err != nil {
		WriteTicketError(c, err)
		return
	}
	view, err := h.svc.View(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *TicketHandler) Delete(c *gin.Context) {
	username := c.GetString("user")
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	if err := h.svc.DeleteForUser(id, username); err != nil {
		WriteTicketError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// WriteTicketError preserves the customer API error format for ticket operations.
func WriteTicketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, recordservice.ErrTicketNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, recordservice.ErrTicketForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, recordservice.ErrTicketDateClosed),
		errors.Is(err, recordservice.ErrTicketNoCapacity),
		errors.Is(err, recordservice.ErrTicketOneWorking),
		errors.Is(err, recordservice.ErrTicketPastTime),
		errors.Is(err, recordservice.ErrTicketRoomMissing),
		errors.Is(err, recordservice.ErrTicketInvalidDate):
		c.JSON(http.StatusBadRequest, gin.H{"msg": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
