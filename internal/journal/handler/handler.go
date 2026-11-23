package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"clinic-backend/internal/journal"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *journal.Service
}

func NewHandler(svc *journal.Service) *Handler {
	return &Handler{svc: svc}
}

type listResponse struct {
	Items      any     `json:"items"`
	NextCursor *uint64 `json:"next_cursor"`
}

type entityKindsResponse struct {
	Items []journal.EntityKind `json:"items"`
}

// ListEntityKinds returns the entity types and actions available to the
// journal filters.
func (h *Handler) ListEntityKinds(c *gin.Context) {
	items, err := h.svc.ListEntityKinds(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, entityKindsResponse{Items: items})
}

// List returns filtered, newest-first journal events.
func (h *Handler) List(c *gin.Context) {
	filter := journal.Filter{
		EntityType: c.Query("entity_type"),
		EntityID:   c.Query("entity_id"),
		ActorType:  c.Query("actor_type"),
		ActorID:    c.Query("actor_id"),
		Action:     c.Query("action"),
	}
	if filter.ActorType != "" &&
		filter.ActorType != journal.ActorStaff &&
		filter.ActorType != journal.ActorCustomer &&
		filter.ActorType != journal.ActorSystem &&
		filter.ActorType != journal.ActorExternal {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid actor_type"})
		return
	}

	limit, err := queryInt(c, "limit", 100)
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
		return
	}
	filter.Limit = limit

	if value := strings.TrimSpace(c.Query("cursor")); value != "" {
		cursor, err := strconv.ParseUint(value, 10, 64)
		if err != nil || cursor == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
			return
		}
		filter.Cursor = &cursor
	}
	if filter.From, err = queryTime(c, "from"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must be RFC3339"})
		return
	}
	if filter.To, err = queryTime(c, "to"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must be RFC3339"})
		return
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must not be after to"})
		return
	}

	page, err := h.svc.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, listResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func queryInt(c *gin.Context, name string, fallback int) (int, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}

func queryTime(c *gin.Context, name string) (*time.Time, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
