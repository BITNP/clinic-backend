package handler

import (
	"net/http"

	staffmodel "clinic-backend/internal/staff/model"

	"github.com/gin-gonic/gin"
)

type UserResponse struct {
	ID        int    `json:"id"`
	AccountID string `json:"account_id"`
	Realname  string `json:"realname"`
	Email     string `json:"email"`
	Role      string `json:"role"`
}

type UserHandler struct{}

func NewUserHandler() *UserHandler {
	return &UserHandler{}
}

func (h *UserHandler) Current(c *gin.Context) {
	staff, ok := ContextStaff(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	role, _ := ContextRole(c)
	c.JSON(http.StatusOK, toUserResponse(staff, role))
}

func (h *UserHandler) Me(c *gin.Context) {
	staff, ok := ContextStaff(c)
	if !ok {
		c.JSON(http.StatusOK, UserResponse{})
		return
	}
	role, _ := ContextRole(c)
	c.JSON(http.StatusOK, toUserResponse(staff, role))
}

func toUserResponse(staff staffmodel.ClinicStaff, role StaffRole) UserResponse {
	return UserResponse{
		ID:        staff.ID,
		AccountID: staff.AccountID,
		Realname:  staff.Realname,
		Email:     staff.Email,
		Role:      string(role),
	}
}
