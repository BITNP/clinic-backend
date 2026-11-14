package handler

import (
	"net/http"
	"strconv"

	"clinic-backend/internal/httpx"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"

	"github.com/gin-gonic/gin"
)

type RoomHandler struct {
	svc *roomservice.RoomService
}

func NewRoomHandler(svc *roomservice.RoomService) *RoomHandler {
	return &RoomHandler{svc: svc}
}

type createRoomRequest struct {
	Name     string `json:"name" binding:"required,max=64"`
	Address  string `json:"address" binding:"required,max=256"`
	Enabled  *bool  `json:"enabled"`
	CampusID uint   `json:"campus_id" binding:"required"`
	Type     string `json:"type" binding:"omitempty,oneof=normal dynamic auto_select"`
}

type updateRoomRequest struct {
	Name     *string `json:"name" binding:"omitempty,max=64"`
	Address  *string `json:"address" binding:"omitempty,max=256"`
	Enabled  *bool   `json:"enabled"`
	CampusID *uint   `json:"campus_id"`
	Type     *string `json:"type" binding:"omitempty,oneof=normal dynamic auto_select"`
}

func (h *RoomHandler) Create(c *gin.Context) {
	var req createRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	r, err := h.svc.Create(roomservice.CreateRoomInput{
		Name:     req.Name,
		Address:  req.Address,
		Enabled:  req.Enabled,
		CampusID: req.CampusID,
		Type:     roommodel.RoomType(req.Type),
	})
	if err != nil {
		writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusCreated, r)
}

func (h *RoomHandler) Get(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	r, err := h.svc.GetByID(id)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *RoomHandler) List(c *gin.Context) {
	f := roomservice.ListRoomFilter{
		Name:        c.Query("name"),
		EnabledOnly: c.Query("enabled") == "true",
	}
	if v := c.Query("campus_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid campus_id"})
			return
		}
		campusID := uint(id)
		f.CampusID = &campusID
	}
	roomType, ok := parseRoomTypeQuery(c)
	if !ok {
		return
	}
	f.Type = roomType
	f.Page, f.PageSize = httpx.ParsePagination(c)

	items, total, err := h.svc.List(f)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

func (h *RoomHandler) Update(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	var req updateRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	in := roomservice.UpdateRoomInput{
		Name:     req.Name,
		Address:  req.Address,
		Enabled:  req.Enabled,
		CampusID: req.CampusID,
	}
	if req.Type != nil {
		roomType := roommodel.RoomType(*req.Type)
		in.Type = &roomType
	}
	r, err := h.svc.Update(id, in)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *RoomHandler) Delete(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// writeRoomError maps service errors to HTTP statuses.
var roomErrorMappings = []httpx.ErrStatus{
	{Err: roomservice.ErrRoomNotFound, Status: http.StatusNotFound},
	{Err: roomservice.ErrRoomNameTaken, Status: http.StatusConflict},
	{Err: roomservice.ErrRoomInUse, Status: http.StatusConflict},
	{Err: roomservice.ErrRoomHasUnfinishedRecords, Status: http.StatusConflict},
	{Err: roomservice.ErrRoomCampusRequired, Status: http.StatusBadRequest},
	{Err: roomservice.ErrRoomCampusNotFound, Status: http.StatusBadRequest},
	{Err: roomservice.ErrRoomInvalidType, Status: http.StatusBadRequest},
}

func writeRoomError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, roomErrorMappings)
}
