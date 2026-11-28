package handler

import (
	"net/http"
	"strconv"
	"time"

	"clinic-backend/internal/httpx"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"

	"github.com/gin-gonic/gin"
)

type UserTicketBanHandler struct {
	svc *recordservice.UserTicketBanService
}

func NewUserTicketBanHandler(svc *recordservice.UserTicketBanService) *UserTicketBanHandler {
	return &UserTicketBanHandler{svc: svc}
}

type createUserTicketBanRequest struct {
	UserID     string                           `json:"user_id" binding:"required"`
	Scope      recordservice.UserTicketBanScope `json:"scope" binding:"required"`
	CampusID   *uint                            `json:"campus_id"`
	Reason     string                           `json:"reason" binding:"required"`
	ExpirateAt string                           `json:"expirate_at" binding:"required"`
}

type updateUserTicketBanRequest struct {
	Reason     *string `json:"reason"`
	ExpirateAt *string `json:"expirate_at"`
}

type userTicketBanView struct {
	ID         uint                             `json:"id"`
	UserID     string                           `json:"user_id"`
	Scope      recordservice.UserTicketBanScope `json:"scope"`
	CampusID   *uint                            `json:"campus_id"`
	Reason     string                           `json:"reason"`
	ExpirateAt string                           `json:"expirate_at"`
	Active     bool                             `json:"active"`
}

func newUserTicketBanView(ban recordmodel.ClinicUserTicketBan, now time.Time) userTicketBanView {
	scope := recordservice.UserTicketBanScopeGlobal
	if ban.CampusID != nil {
		scope = recordservice.UserTicketBanScopeCampus
	}
	return userTicketBanView{
		ID:         ban.ID,
		UserID:     ban.UserID,
		Scope:      scope,
		CampusID:   ban.CampusID,
		Reason:     ban.Reason,
		ExpirateAt: ban.ExpirateAt.UTC().Format(time.RFC3339Nano),
		Active:     ban.ExpirateAt.After(now.UTC()),
	}
}

func (h *UserTicketBanHandler) Create(c *gin.Context) {
	var req createUserTicketBanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	expirateAt, err := time.Parse(time.RFC3339Nano, req.ExpirateAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expirate_at must be RFC3339"})
		return
	}

	ban, err := h.svc.Create(recordservice.CreateUserTicketBanInput{
		UserID:     req.UserID,
		Scope:      req.Scope,
		CampusID:   req.CampusID,
		Reason:     req.Reason,
		ExpirateAt: expirateAt,
	})
	if err != nil {
		writeUserTicketBanError(c, err)
		return
	}
	c.JSON(http.StatusCreated, newUserTicketBanView(ban, time.Now()))
}

func (h *UserTicketBanHandler) Get(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	ban, err := h.svc.GetByID(id)
	if err != nil {
		writeUserTicketBanError(c, err)
		return
	}
	c.JSON(http.StatusOK, newUserTicketBanView(ban, time.Now()))
}

func (h *UserTicketBanHandler) List(c *gin.Context) {
	f := recordservice.ListUserTicketBanFilter{
		UserID: c.Query("user_id"),
	}
	if raw := c.Query("scope"); raw != "" {
		f.Scope = recordservice.UserTicketBanScope(raw)
		if f.Scope != recordservice.UserTicketBanScopeGlobal && f.Scope != recordservice.UserTicketBanScopeCampus {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be global or campus"})
			return
		}
	}
	if raw := c.Query("campus_id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid campus_id"})
			return
		}
		campusID := uint(id)
		f.CampusID = &campusID
	}
	if raw := c.Query("active"); raw != "" {
		switch raw {
		case "true":
			active := true
			f.Active = &active
		case "false":
			active := false
			f.Active = &active
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "active must be true or false"})
			return
		}
	}
	f.Page, f.PageSize = httpx.ParsePagination(c)

	items, total, err := h.svc.List(f)
	if err != nil {
		writeUserTicketBanError(c, err)
		return
	}
	now := time.Now()
	views := make([]userTicketBanView, 0, len(items))
	for _, ban := range items {
		views = append(views, newUserTicketBanView(ban, now))
	}
	httpx.PaginatedResponse(c, views, total, f.Page, f.PageSize)
}

func (h *UserTicketBanHandler) Update(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req updateUserTicketBanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	in := recordservice.UpdateUserTicketBanInput{Reason: req.Reason}
	if req.ExpirateAt != nil {
		expirateAt, err := time.Parse(time.RFC3339Nano, *req.ExpirateAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expirate_at must be RFC3339"})
			return
		}
		in.ExpirateAt = &expirateAt
	}

	ban, err := h.svc.Update(id, in)
	if err != nil {
		writeUserTicketBanError(c, err)
		return
	}
	c.JSON(http.StatusOK, newUserTicketBanView(ban, time.Now()))
}

func (h *UserTicketBanHandler) Delete(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		writeUserTicketBanError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

var userTicketBanErrorMappings = []httpx.ErrStatus{
	{Err: recordservice.ErrUserTicketBanInvalidUserID, Status: http.StatusBadRequest},
	{Err: recordservice.ErrUserTicketBanInvalidScope, Status: http.StatusBadRequest},
	{Err: recordservice.ErrUserTicketBanInvalidCampusID, Status: http.StatusBadRequest},
	{Err: recordservice.ErrUserTicketBanCampusNotFound, Status: http.StatusBadRequest},
	{Err: recordservice.ErrUserTicketBanInvalidReason, Status: http.StatusBadRequest},
	{Err: recordservice.ErrUserTicketBanInvalidExpirateAt, Status: http.StatusBadRequest},
	{Err: recordservice.ErrUserTicketBanNotFound, Status: http.StatusNotFound},
	{Err: recordservice.ErrUserTicketBanConflict, Status: http.StatusConflict},
}

func writeUserTicketBanError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, userTicketBanErrorMappings)
}
