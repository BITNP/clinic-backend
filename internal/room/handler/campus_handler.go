package handler

import (
	"net/http"

	"clinic-backend/internal/httpx"
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
	Name string `json:"name" binding:"required,max=64"`
}

type updateCampusRequest struct {
	Name *string `json:"name" binding:"omitempty,max=64"`
}

func (h *CampusHandler) Create(c *gin.Context) {
	var req createCampusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	campus, err := h.campusSvc.Create(roomservice.CreateCampusInput{Name: req.Name})
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

	campus, err := h.campusSvc.Update(id, roomservice.UpdateCampusInput{Name: req.Name})
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
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if _, err := h.campusSvc.GetByID(id); err != nil {
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

	items, total, err := h.roomSvc.List(f)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	httpx.PaginatedResponse(c, items, total, f.Page, f.PageSize)
}

// writeCampusError maps campus service errors to HTTP statuses.
var campusErrorMappings = []httpx.ErrStatus{
	{Err: roomservice.ErrCampusNotFound, Status: http.StatusNotFound},
	{Err: roomservice.ErrCampusNameTaken, Status: http.StatusConflict},
	{Err: roomservice.ErrCampusInUse, Status: http.StatusConflict},
}

func writeCampusError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, campusErrorMappings)
}
