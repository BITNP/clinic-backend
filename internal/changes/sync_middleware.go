package changes

import (
	"context"
	"log"
	"net/http"
	"time"

	"clinic-backend/internal/httpx"

	"github.com/gin-gonic/gin"
)

// syncBumpTimeout bounds the counter write so a slow Redis cannot keep the
// request goroutine alive after the response was already written.
const SyncBumpTimeout = 2 * time.Second

// SyncBumpMiddleware increments the counter for group after a successful
// mutating request. It is best-effort: a counter failure is logged and never
// changes the response, because the underlying data write already succeeded.
func SyncBumpMiddleware(svc *SyncService, group SyncGroup) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if !httpx.IsMutatingMethod(c.Request.Method) {
			return
		}
		status := c.Writer.Status()
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return
		}

		// Bump outside the request context: the client may have disconnected
		// after receiving the response, but the change still happened.
		ctx, cancel := context.WithTimeout(context.Background(), SyncBumpTimeout)
		defer cancel()
		if err := svc.Bump(ctx, group); err != nil {
			log.Printf("sync: bump %s failed: %v", group, err)
		}
	}
}
