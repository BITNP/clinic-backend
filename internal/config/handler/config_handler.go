package handler

import (
	"net/http"

	configmodel "clinic-backend/internal/config/model"
	configservice "clinic-backend/internal/config/service"
	"clinic-backend/internal/httpx"

	"github.com/gin-gonic/gin"
)

type ConfigHandler struct {
	svc *configservice.ConfigService
}

func NewConfigHandler(svc *configservice.ConfigService) *ConfigHandler {
	return &ConfigHandler{svc: svc}
}

type createGroupRequest struct {
	Name        string `json:"name" binding:"required,max=64"`
	DisplayName string `json:"display_name" binding:"required,max=128"`
	SortOrder   int    `json:"sort_order" binding:"min=0"`
}

type updateGroupRequest struct {
	DisplayName *string `json:"display_name" binding:"omitempty,max=128"`
	SortOrder   *int    `json:"sort_order" binding:"omitempty,min=0"`
}

type createItemRequest struct {
	Name        string                      `json:"name" binding:"required,max=128"`
	Value       string                      `json:"value"`
	ValueType   configmodel.ConfigValueType `json:"value_type" binding:"required,oneof=int double boolean string"`
	DisplayName string                      `json:"display_name" binding:"required,max=128"`
	IsRequired  bool                        `json:"is_required"`
	GroupID     uint                        `json:"group_id" binding:"required"`
	SortOrder   int                         `json:"sort_order" binding:"min=0"`
}

type updateItemRequest struct {
	Value       *string                      `json:"value"`
	ValueType   *configmodel.ConfigValueType `json:"value_type" binding:"omitempty,oneof=int double boolean string"`
	DisplayName *string                      `json:"display_name" binding:"omitempty,max=128"`
	IsRequired  *bool                        `json:"is_required"`
	GroupID     *uint                        `json:"group_id" binding:"omitempty,gt=0"`
	SortOrder   *int                         `json:"sort_order" binding:"omitempty,min=0"`
}

func (h *ConfigHandler) ListCatalog(c *gin.Context) {
	catalog, err := h.svc.ListCatalog()
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, catalog)
}

func (h *ConfigHandler) GetGroup(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	group, err := h.svc.GetGroup(id)
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, group)
}

func (h *ConfigHandler) CreateGroup(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	group, err := h.svc.CreateGroup(configservice.CreateGroupInput{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusCreated, group)
}

func (h *ConfigHandler) UpdateGroup(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	var req updateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	group, err := h.svc.UpdateGroup(id, configservice.UpdateGroupInput{
		DisplayName: req.DisplayName,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, group)
}

func (h *ConfigHandler) DeleteGroup(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteGroup(id); err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *ConfigHandler) GetItem(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	item, err := h.svc.GetItem(id)
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *ConfigHandler) CreateItem(c *gin.Context) {
	var req createItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	item, err := h.svc.CreateItem(configservice.CreateItemInput{
		Name:        req.Name,
		Value:       req.Value,
		ValueType:   req.ValueType,
		DisplayName: req.DisplayName,
		IsRequired:  req.IsRequired,
		GroupID:     req.GroupID,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *ConfigHandler) UpdateItem(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	var req updateItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	item, err := h.svc.UpdateItem(id, configservice.UpdateItemInput{
		Value:       req.Value,
		ValueType:   req.ValueType,
		DisplayName: req.DisplayName,
		IsRequired:  req.IsRequired,
		GroupID:     req.GroupID,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *ConfigHandler) DeleteItem(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteItem(id); err != nil {
		writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

var configErrorMappings = []httpx.ErrStatus{
	{Err: configservice.ErrConfigInvalidInput, Status: http.StatusBadRequest},
	{Err: configservice.ErrConfigValueTypeInvalid, Status: http.StatusBadRequest},
	{Err: configservice.ErrConfigValueInvalid, Status: http.StatusBadRequest},
	{Err: configservice.ErrConfigGroupNotFound, Status: http.StatusNotFound},
	{Err: configservice.ErrConfigItemNotFound, Status: http.StatusNotFound},
	{Err: configservice.ErrConfigGroupNameTaken, Status: http.StatusConflict},
	{Err: configservice.ErrConfigItemNameTaken, Status: http.StatusConflict},
	{Err: configservice.ErrConfigGroupInUse, Status: http.StatusConflict},
}

func writeConfigError(c *gin.Context, err error) {
	httpx.WriteMappedError(c, err, configErrorMappings)
}
