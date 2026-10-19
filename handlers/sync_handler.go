package handlers

import (
	"net/http"
	"time"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

// SyncHandler exposes the per-group change counters that clients compare with
// their own last-seen values to detect stale data.
type SyncHandler struct {
	svc      *services.SyncService
	interval time.Duration
}

func NewSyncHandler(svc *services.SyncService, interval time.Duration) *SyncHandler {
	return &SyncHandler{svc: svc, interval: interval}
}

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
	c.JSON(http.StatusOK, gin.H{"interval_ms": h.interval.Milliseconds()})
}
