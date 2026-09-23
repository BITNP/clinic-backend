package handlers

import (
	"net/http"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

// SyncHandler exposes the per-group change counters that clients compare with
// their own last-seen values to detect stale data.
type SyncHandler struct {
	svc *services.SyncService
}

func NewSyncHandler(svc *services.SyncService) *SyncHandler {
	return &SyncHandler{svc: svc}
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
