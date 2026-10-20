package announcement

import (
	"errors"
	"net/http"
	"time"

	"clinic-backend/internal/httpx"

	"github.com/gin-gonic/gin"
)

type AnnouncementHandler struct {
	svc *AnnouncementService
}

func NewAnnouncementHandler(svc *AnnouncementService) *AnnouncementHandler {
	return &AnnouncementHandler{svc: svc}
}

type createAnnouncementRequest struct {
	Title      string `json:"title" binding:"required,max=20"`
	Content    string `json:"content" binding:"required"`
	Tag        string `json:"tag" binding:"max=16"`
	Enabled    *bool  `json:"enabled"`
	Brief      string `json:"brief" binding:"required,max=64"`
	ExpireDate string `json:"expireDate" binding:"required"`
	Priority   uint   `json:"priority"`
}

type updateAnnouncementRequest struct {
	Title      *string `json:"title" binding:"omitempty,max=20"`
	Content    *string `json:"content"`
	Tag        *string `json:"tag" binding:"omitempty,max=16"`
	Enabled    *bool   `json:"enabled"`
	Brief      *string `json:"brief" binding:"omitempty,max=64"`
	ExpireDate *string `json:"expireDate"`
	Priority   *uint   `json:"priority"`
}

func (h *AnnouncementHandler) Create(c *gin.Context) {
	var req createAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	expireDate, err := httpx.ParseDateString(req.ExpireDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateExpireDate(expireDate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	a, err := h.svc.Create(CreateAnnouncementInput{
		Title:      req.Title,
		Content:    req.Content,
		Tag:        AnnouncementTag(req.Tag),
		Enabled:    req.Enabled,
		Brief:      req.Brief,
		ExpireDate: expireDate,
		Priority:   req.Priority,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, a)
}

func (h *AnnouncementHandler) Get(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	a, err := h.svc.GetByID(id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

func (h *AnnouncementHandler) List(c *gin.Context) {
	f := ListAnnouncementFilter{
		Tag:        AnnouncementTag(c.Query("tag")),
		ActiveOnly: c.Query("active") == "true",
	}
	f.Page, f.PageSize = httpx.ParsePagination(c)

	items, total, err := h.svc.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

func (h *AnnouncementHandler) Update(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req updateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var expireDate *time.Time
	if req.ExpireDate != nil {
		parsed, err := httpx.ParseDateString(*req.ExpireDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := validateExpireDate(parsed); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		expireDate = &parsed
	}

	var tag *AnnouncementTag
	if req.Tag != nil {
		t := AnnouncementTag(*req.Tag)
		tag = &t
	}

	a, err := h.svc.Update(id, UpdateAnnouncementInput{
		Title:      req.Title,
		Content:    req.Content,
		Tag:        tag,
		Enabled:    req.Enabled,
		Brief:      req.Brief,
		ExpireDate: expireDate,
		Priority:   req.Priority,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

func (h *AnnouncementHandler) Delete(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *AnnouncementHandler) TOS(c *gin.Context) {
	a, err := h.svc.GetTOS()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

var announcementErrorMappings = []httpx.ErrStatus{
	{Err: ErrAnnouncementNotFound, Status: http.StatusNotFound},
	{Err: ErrAnnouncementInvalidTag, Status: http.StatusBadRequest},
	{Err: ErrAnnouncementTOSAlreadyExists, Status: http.StatusConflict},
}

func writeServiceError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, announcementErrorMappings)
}

// validateExpireDate rejects expiry dates in the past.
func validateExpireDate(t time.Time) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if t.Truncate(24 * time.Hour).Before(today) {
		return errors.New("expireDate must not be in the past")
	}
	return nil
}
