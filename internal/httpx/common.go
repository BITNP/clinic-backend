package httpx

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ErrStatus maps an error (matched via errors.Is) to an HTTP status code.
type ErrStatus struct {
	Err    error
	Status int
}

// WriteMappedError writes a JSON error response by matching err against the
// provided mappings. The first match wins. If nothing matches, 500 is returned.
func WriteMappedError(c *gin.Context, err error, mappings []ErrStatus) {
	for _, m := range mappings {
		if errors.Is(err, m.Err) {
			c.JSON(m.Status, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// ParsePagination extracts page and pageSize from query parameters.
// Defaults are page=1, pageSize=20.
func ParsePagination(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize = 20
	if v, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && v > 0 {
		page = v
	}
	if v, err := strconv.Atoi(c.DefaultQuery("pageSize", "20")); err == nil && v > 0 {
		pageSize = v
	}
	return page, pageSize
}

// PaginatedResponse writes a JSON response with items, total, page and pageSize.
func PaginatedResponse(c *gin.Context, items any, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, gin.H{
		"items":    items,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}
