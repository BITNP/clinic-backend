package handler

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ClientAuthMiddleware verifies the API-key signature sent by the DingTalk
// proxy. The proxy sets:
//
//	X-API-KEY: md5(SHARED_SECRET + username + DateHeader)
//	Date:      RFC1123 timestamp (same value used in the md5 input)
//	?username= job_number of the logged-in student
//
// Requests whose Date header is more than maxSkew away from server time, or
// whose signature does not match, are rejected with 401. On success the
// verified username is stored in the gin context under the "user" key.
func ClientAuthMiddleware(secret string, maxSkew time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		dateHeader := c.GetHeader("Date")
		username := c.Query("username")
		clientKey := c.GetHeader("X-API-KEY")

		if dateHeader == "" || username == "" || clientKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing auth credentials"})
			return
		}

		t, err := time.Parse(time.RFC1123, dateHeader)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid date header"})
			return
		}

		if absDuration(time.Since(t)) > maxSkew {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "stale date header"})
			return
		}

		sum := md5.Sum([]byte(secret + username + dateHeader))
		expected := hex.EncodeToString(sum[:])

		if subtle.ConstantTimeCompare([]byte(expected), []byte(clientKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		c.Set("user", username)
		c.Next()
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
