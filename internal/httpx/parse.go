package httpx

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ParseID reads a positive ID path parameter or writes a bad-request response.
func ParseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return uint(id), true
}

// ParseDateString parses a date value sent by the admin frontend.
// It accepts either "YYYY-MM-DD" or an RFC3339 timestamp for backwards compatibility.
func ParseDateString(s string) (time.Time, error) {
	layouts := []string{"2006-01-02", time.RFC3339, time.RFC3339Nano}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("expireDate must be in YYYY-MM-DD or RFC3339 format")
}
