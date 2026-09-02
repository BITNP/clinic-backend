package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

type StaffHandler struct {
	svc *services.StaffService
}

func NewStaffHandler(svc *services.StaffService) *StaffHandler {
	return &StaffHandler{svc: svc}
}

type createStaffRequest struct {
	AccountID string `json:"account_id" binding:"required"`
	Realname  string `json:"realname"`
	Email     string `json:"email"`
}

type updateStaffRequest struct {
	Realname *string `json:"realname"`
	Email    *string `json:"email"`
}

func (h *StaffHandler) List(c *gin.Context) {
	staff, err := h.svc.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": staff})
}

func (h *StaffHandler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	s, err := h.svc.GetByID(id)
	if err != nil {
		if errors.Is(err, services.ErrStaffNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}

func (h *StaffHandler) Create(c *gin.Context) {
	var req createStaffRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s, err := h.svc.GetOrCreateByAccountID(req.AccountID, req.Realname, req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.Email != "" {
		s, err = h.svc.Update(s.ID, services.UpdateStaffInput{Email: &req.Email})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusCreated, s)
}

func (h *StaffHandler) Update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req updateStaffRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s, err := h.svc.Update(id, services.UpdateStaffInput{
		Realname: req.Realname,
		Email:    req.Email,
	})
	if err != nil {
		if errors.Is(err, services.ErrStaffNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}

func (h *StaffHandler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.svc.Delete(id); err != nil {
		if errors.Is(err, services.ErrStaffNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}
