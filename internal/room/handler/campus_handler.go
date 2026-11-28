package handler

import (
	"net/http"
	"time"

	"clinic-backend/internal/httpx"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"

	"github.com/gin-gonic/gin"
)

type CampusHandler struct {
	campusSvc *roomservice.CampusService
	roomSvc   *roomservice.RoomService
}

func NewCampusHandler(campusSvc *roomservice.CampusService, roomSvc *roomservice.RoomService) *CampusHandler {
	return &CampusHandler{campusSvc: campusSvc, roomSvc: roomSvc}
}

type createCampusRequest struct {
	SelectionPolicy roommodel.SelectionPolicy `json:"selection_policy" binding:"omitempty,oneof=balanced fulfill"`
	Name            string                    `json:"name" binding:"required,max=64"`
	NoShowBanPolicy *noShowBanPolicyRequest   `json:"no_show_ban_policy"`
}

type updateCampusRequest struct {
	SelectionPolicy *roommodel.SelectionPolicy `json:"selection_policy" binding:"omitempty,oneof=balanced fulfill"`
	Name            *string                    `json:"name" binding:"omitempty,max=64"`
	NoShowBanPolicy *noShowBanPolicyRequest    `json:"no_show_ban_policy"`
}

type noShowBanPolicyRequest struct {
	Enabled       *bool                     `json:"enabled"`
	DurationHours *int64                    `json:"duration_hours"`
	Level         *roommodel.NoShowBanLevel `json:"level" binding:"omitempty,oneof=campus global"`
}

func (h *CampusHandler) Create(c *gin.Context) {
	var req createCampusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	campus, err := h.campusSvc.Create(roomservice.CreateCampusInput{
		Name:            req.Name,
		SelectionPolicy: req.SelectionPolicy,
		NoShowBanPolicy: toNoShowBanPolicyPatch(req.NoShowBanPolicy),
	})
	if err != nil {
		writeCampusError(c, err)
		return
	}
	c.JSON(http.StatusCreated, campus)
}

func (h *CampusHandler) Get(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	campus, err := h.campusSvc.GetByID(id)
	if err != nil {
		writeCampusError(c, err)
		return
	}
	c.JSON(http.StatusOK, campus)
}

func (h *CampusHandler) List(c *gin.Context) {
	f := roomservice.ListCampusFilter{Name: c.Query("name")}
	f.Page, f.PageSize = httpx.ParsePagination(c)

	items, total, err := h.campusSvc.List(f)
	if err != nil {
		writeCampusError(c, err)
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

func (h *CampusHandler) Update(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req updateCampusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	campus, err := h.campusSvc.Update(id, roomservice.UpdateCampusInput{
		Name:            req.Name,
		SelectionPolicy: req.SelectionPolicy,
		NoShowBanPolicy: toNoShowBanPolicyPatch(req.NoShowBanPolicy),
	})
	if err != nil {
		writeCampusError(c, err)
		return
	}
	c.JSON(http.StatusOK, campus)
}

func (h *CampusHandler) Delete(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.campusSvc.Delete(id); err != nil {
		writeCampusError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *CampusHandler) ListRooms(c *gin.Context) {
	h.listRooms(c, false)
}

// ListClientRooms adds a date-specific room recommendation to the client list.
func (h *CampusHandler) ListClientRooms(c *gin.Context) {
	h.listRooms(c, true)
}

func (h *CampusHandler) listRooms(c *gin.Context, recommend bool) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	campus, err := h.campusSvc.GetByID(id)
	if err != nil {
		writeCampusError(c, err)
		return
	}

	f := roomservice.ListRoomFilter{
		Name:        c.Query("name"),
		EnabledOnly: c.Query("enabled") == "true",
		CampusID:    &id,
	}
	roomType, ok := parseRoomTypeQuery(c)
	if !ok {
		return
	}
	f.Type = roomType
	f.Page, f.PageSize = httpx.ParsePagination(c)

	var preferred *uint
	if recommend {
		date, err := time.Parse("2006-01-02", c.Query("date"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "date is required and must be YYYY-MM-DD"})
			return
		}
		preferred, err = h.roomSvc.PreferredRoom(c.Request.Context(), campus, date, f)
		if err != nil {
			writeRoomError(c, err)
			return
		}
	}
	items, total, err := h.roomSvc.List(f)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	if recommend {
		c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": f.Page, "pageSize": f.PageSize, "preferred_room_id": preferred})
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

// writeCampusError maps campus service errors to HTTP statuses.
var campusErrorMappings = []httpx.ErrStatus{
	{Err: roomservice.ErrCampusInvalidPolicy, Status: http.StatusBadRequest},
	{Err: roomservice.ErrCampusInvalidNoShowBanDuration, Status: http.StatusBadRequest},
	{Err: roomservice.ErrCampusInvalidNoShowBanLevel, Status: http.StatusBadRequest},
	{Err: roomservice.ErrCampusNotFound, Status: http.StatusNotFound},
	{Err: roomservice.ErrCampusNameTaken, Status: http.StatusConflict},
	{Err: roomservice.ErrCampusInUse, Status: http.StatusConflict},
}

func writeCampusError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, campusErrorMappings)
}

func toNoShowBanPolicyPatch(req *noShowBanPolicyRequest) *roomservice.NoShowBanPolicyPatch {
	if req == nil {
		return nil
	}
	return &roomservice.NoShowBanPolicyPatch{
		Enabled:       req.Enabled,
		DurationHours: req.DurationHours,
		Level:         req.Level,
	}
}
