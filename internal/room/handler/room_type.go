package handler

import (
	"net/http"

	roommodel "clinic-backend/internal/room/model"

	"github.com/gin-gonic/gin"
)

// parseRoomTypeQuery reads an optional room type query parameter. It writes a
// 400 response and returns ok=false when the value is invalid.
func parseRoomTypeQuery(c *gin.Context) (*roommodel.RoomType, bool) {
	v := c.Query("type")
	if v == "" {
		return nil, true
	}
	roomType := roommodel.RoomType(v)
	if !roomType.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid type"})
		return nil, false
	}
	return &roomType, true
}
