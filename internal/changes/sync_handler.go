package changes

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// SyncHandler exposes the per-group change counters that clients compare with
// their own last-seen values to detect stale data.
type SyncHandler struct {
	svc            *SyncService
	interval       time.Duration
	intervalGetter func() time.Duration
}

func NewSyncHandler(svc *SyncService, interval time.Duration) *SyncHandler {
	return &SyncHandler{svc: svc, interval: interval}
}

// SetIntervalGetter supplies the current configured interval.
func (h *SyncHandler) SetIntervalGetter(getter func() time.Duration) { h.intervalGetter = getter }

// Get returns the current counter for every sync group.
func (h *SyncHandler) Get(c *gin.Context) {
	counters, err := h.svc.Snapshot(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make(map[string]int64, len(counters))
	for group, value := range counters {
		out[string(group)] = value
	}
	c.JSON(http.StatusOK, out)
}

// GetConfig reports how often clients should poll the counters. A zero
// interval means periodic polling is disabled.
func (h *SyncHandler) GetConfig(c *gin.Context) {
	interval := h.interval
	if h.intervalGetter != nil {
		interval = h.intervalGetter()
	}
	c.JSON(http.StatusOK, gin.H{"interval_ms": interval.Milliseconds()})
}
