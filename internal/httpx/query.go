package httpx

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// ParseIntDefault reads a positive integer query parameter, using def otherwise.
func ParseIntDefault(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}
