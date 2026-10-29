package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"clinic-backend/internal/httpx"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"

	"github.com/gin-gonic/gin"
)

// photoMultipartOverhead leaves room for multipart boundaries and headers on
// top of the configured per-photo size limit.
const photoMultipartOverhead = 1 << 20

// PhotoHandler exposes a record's ordered worker-note photos.
type PhotoHandler struct {
	svc *recordservice.PhotoService
}

func NewPhotoHandler(svc *recordservice.PhotoService) *PhotoHandler {
	return &PhotoHandler{svc: svc}
}

type photoView struct {
	ID          uint   `json:"id"`
	Position    int    `json:"position"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
}

func (h *PhotoHandler) List(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	photos, err := h.svc.List(c.Request.Context(), id)
	if err != nil {
		writePhotoError(c, err)
		return
	}
	h.writePhotoList(c, photos)
}

type photoOrderRequest struct {
	ExpectedPhotoIDs []uint `json:"expected_photo_ids"`
	PhotoIDs         []uint `json:"photo_ids"`
}

func (h *PhotoHandler) Reorder(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	var request photoOrderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid photo order request"})
		return
	}

	photos, err := h.svc.Reorder(c.Request.Context(), id, request.ExpectedPhotoIDs, request.PhotoIDs)
	if err != nil {
		writePhotoError(c, err)
		return
	}
	h.writePhotoList(c, photos)
}

func (h *PhotoHandler) writePhotoList(c *gin.Context, photos []recordmodel.ClinicRecordPhoto) {
	items := make([]photoView, 0, len(photos))
	for _, photo := range photos {
		items = append(items, photoViewFromModel(photo))
	}
	c.JSON(http.StatusOK, gin.H{
		"items":          items,
		"count":          len(items),
		"max_count":      h.svc.MaxCount(),
		"max_size_bytes": h.svc.MaxSizeBytes(),
	})
}

func (h *PhotoHandler) Get(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	position, ok := parsePhotoPosition(c)
	if !ok {
		return
	}

	photo, object, err := h.svc.Get(c.Request.Context(), id, position)
	if err != nil {
		writePhotoError(c, err)
		return
	}
	defer object.Body.Close()

	contentType := photo.ContentType
	if object.Metadata.ContentType != "" {
		contentType = object.Metadata.ContentType
	}
	size := photo.Size
	if object.Metadata.Size > 0 {
		size = object.Metadata.Size
	}

	if object.Metadata.ETag != "" {
		c.Header("ETag", object.Metadata.ETag)
	}
	c.Header("Cache-Control", "private, no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, size, contentType, object.Body, nil)
}

func (h *PhotoHandler) Create(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}

	upload, cleanup, ok := h.readPhotoUpload(c)
	if !ok {
		return
	}
	defer cleanup()

	photo, err := h.svc.Append(c.Request.Context(), id, upload)
	if err != nil {
		writePhotoError(c, err)
		return
	}

	view := photoViewFromModel(photo)
	c.Header("Location", view.URL)
	c.JSON(http.StatusCreated, view)
}

func (h *PhotoHandler) Replace(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	position, ok := parsePhotoPosition(c)
	if !ok {
		return
	}

	upload, cleanup, ok := h.readPhotoUpload(c)
	if !ok {
		return
	}
	defer cleanup()

	photo, err := h.svc.Replace(c.Request.Context(), id, position, upload)
	if err != nil {
		writePhotoError(c, err)
		return
	}
	c.JSON(http.StatusOK, photoViewFromModel(photo))
}

func (h *PhotoHandler) Delete(c *gin.Context) {
	id, ok := httpx.ParseID(c)
	if !ok {
		return
	}
	position, ok := parsePhotoPosition(c)
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id, position); err != nil {
		writePhotoError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *PhotoHandler) readPhotoUpload(c *gin.Context) (recordservice.PhotoUpload, func(), bool) {
	maxBody := h.svc.MaxSizeBytes() + photoMultipartOverhead
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)

	form, err := c.MultipartForm()
	if err != nil {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return recordservice.PhotoUpload{}, func() {}, false
	}

	cleanup := func() { _ = form.RemoveAll() }
	files := form.File["photo"]
	switch {
	case len(files) == 0:
		cleanup()
		c.JSON(http.StatusBadRequest, gin.H{"error": recordservice.ErrPhotoFileRequired.Error()})
		return recordservice.PhotoUpload{}, func() {}, false
	case len(files) > 1:
		cleanup()
		c.JSON(http.StatusBadRequest, gin.H{"error": "exactly one photo is required"})
		return recordservice.PhotoUpload{}, func() {}, false
	}

	file, err := files[0].Open()
	if err != nil {
		cleanup()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return recordservice.PhotoUpload{}, func() {}, false
	}

	cleanup = func() {
		_ = file.Close()
		_ = form.RemoveAll()
	}
	return recordservice.PhotoUpload{
		Reader: file,
		Size:   files[0].Size,
	}, cleanup, true
}

func parsePhotoPosition(c *gin.Context) (int, bool) {
	position, err := strconv.Atoi(c.Param("position"))
	if err != nil || position <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid position"})
		return 0, false
	}
	return position, true
}

func photoViewFromModel(photo recordmodel.ClinicRecordPhoto) photoView {
	return photoView{
		ID:          photo.ID,
		Position:    photo.Position,
		ContentType: photo.ContentType,
		Size:        photo.Size,
		CreatedAt:   photo.CreatedAt.UTC().Format(time.RFC3339),
		URL:         fmt.Sprintf("/api/admin/records/%d/photos/%d", photo.RecordID, photo.Position),
	}
}

func writePhotoError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, recordservice.ErrRecordNotFound),
		errors.Is(err, recordservice.ErrPhotoNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, recordservice.ErrPhotoLimitReached),
		errors.Is(err, recordservice.ErrPhotoOrderConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, recordservice.ErrPhotoTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	case errors.Is(err, recordservice.ErrPhotoUnsupportedType),
		errors.Is(err, recordservice.ErrPhotoFileRequired),
		errors.Is(err, recordservice.ErrPhotoOrderInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
