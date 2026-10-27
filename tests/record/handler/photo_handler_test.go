package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	recordhandler "clinic-backend/internal/record/handler"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	"clinic-backend/internal/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakePhotoObject struct {
	data        []byte
	contentType string
}

type fakePhotoStorage struct {
	objects map[string]fakePhotoObject
}

func newFakePhotoStorage() *fakePhotoStorage {
	return &fakePhotoStorage{objects: make(map[string]fakePhotoObject)}
}

func (f *fakePhotoStorage) Put(_ context.Context, key string, body io.ReadSeeker, size int64, contentType string) (string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if int64(len(data)) != size {
		return "", fmt.Errorf("fake put: body size %d does not match declared %d", len(data), size)
	}
	f.objects[key] = fakePhotoObject{data: append([]byte(nil), data...), contentType: contentType}
	return `"fake-etag"`, nil
}

func (f *fakePhotoStorage) Get(_ context.Context, key string) (*storage.Object, error) {
	object, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("fake get: %w", storage.ErrObjectNotFound)
	}
	return &storage.Object{
		Body: io.NopCloser(bytes.NewReader(object.data)),
		Metadata: storage.ObjectMetadata{
			Size:        int64(len(object.data)),
			ContentType: object.contentType,
			ETag:        `"fake-etag"`,
		},
	}, nil
}

func (f *fakePhotoStorage) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

var (
	handlerTestJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
	handlerTestPNG  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0x00, 0x00, 0x00, 0x0D}
)

func handlerPhotoUpload(data []byte) recordservice.PhotoUpload {
	return recordservice.PhotoUpload{Reader: bytes.NewReader(data), Size: int64(len(data))}
}

func setupPhotoHandlerTest(t *testing.T, maxCount int, maxSize int64) (*recordhandler.PhotoHandler, *recordservice.PhotoService, recordmodel.ClinicRecord, *fakePhotoStorage) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&recordmodel.ClinicRecord{}, &recordmodel.ClinicRecordPhoto{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rec := recordmodel.ClinicRecord{
		User:            "20230001",
		Realname:        "Test User",
		PhoneNum:        "555-0100",
		Status:          recordmodel.RecordStatusInProgress,
		AppointmentTime: time.Now().UTC(),
		QuestionDesc:    "broken screen",
		RoomID:          1,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	store := newFakePhotoStorage()
	svc, err := recordservice.NewPhotoService(db, store, maxCount, maxSize)
	if err != nil {
		t.Fatalf("NewPhotoService() error = %v", err)
	}
	return recordhandler.NewPhotoHandler(svc), svc, rec, store
}

func newPhotoContext(method, target string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, body)
	return c, w
}

func recordParams(id uint, position string) gin.Params {
	params := gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(id), 10)}}
	if position != "" {
		params = append(params, gin.Param{Key: "position", Value: position})
	}
	return params
}

type photoPart struct {
	field    string
	filename string
	data     []byte
}

func multipartPhotoRequest(t *testing.T, method, target string, parts ...photoPart) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		file, err := writer.CreateFormFile(part.field, part.filename)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := file.Write(part.data); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, target, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

type photoResponse struct {
	ID          uint   `json:"id"`
	Position    int    `json:"position"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
}

type photoListResponse struct {
	Items        []photoResponse `json:"items"`
	Count        int             `json:"count"`
	MaxCount     int             `json:"max_count"`
	MaxSizeBytes int64           `json:"max_size_bytes"`
}

func TestPhotoHandlerList(t *testing.T) {
	handler, svc, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	for _, data := range [][]byte{handlerTestJPEG, handlerTestPNG} {
		if _, err := svc.Append(context.Background(), rec.ID, handlerPhotoUpload(data)); err != nil {
			t.Fatalf("seed photo: %v", err)
		}
	}

	c, w := newPhotoContext(http.MethodGet, fmt.Sprintf("/api/admin/records/%d/photos", rec.ID), nil)
	c.Params = recordParams(rec.ID, "")
	handler.List(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp photoListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 || len(resp.Items) != 2 {
		t.Fatalf("items = %d count = %d, want 2", len(resp.Items), resp.Count)
	}
	if resp.MaxCount != 9 || resp.MaxSizeBytes != 1024 {
		t.Errorf("limits = %d/%d, want 9/1024", resp.MaxCount, resp.MaxSizeBytes)
	}
	if resp.Items[0].Position != 1 || resp.Items[1].Position != 2 {
		t.Errorf("positions = %d/%d, want 1/2", resp.Items[0].Position, resp.Items[1].Position)
	}
	if want := fmt.Sprintf("/api/admin/records/%d/photos/1", rec.ID); resp.Items[0].URL != want {
		t.Errorf("url = %q, want %q", resp.Items[0].URL, want)
	}
}

func TestPhotoHandlerCreate(t *testing.T) {
	handler, svc, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target, photoPart{field: "photo", filename: "repair.jpg", data: handlerTestJPEG})

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != fmt.Sprintf("/api/admin/records/%d/photos/1", rec.ID) {
		t.Errorf("Location = %q", got)
	}

	var resp photoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Position != 1 || resp.ContentType != "image/jpeg" || resp.Size != int64(len(handlerTestJPEG)) {
		t.Errorf("photo = %+v", resp)
	}

	photos, err := svc.List(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 1 {
		t.Fatalf("photos = %d, want 1", len(photos))
	}
}

func TestPhotoHandlerReplaceAndGet(t *testing.T) {
	handler, svc, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	ctx := context.Background()

	if _, err := svc.Append(ctx, rec.ID, handlerPhotoUpload(handlerTestJPEG)); err != nil {
		t.Fatalf("seed photo: %v", err)
	}

	target := fmt.Sprintf("/api/admin/records/%d/photos/1", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPut, target, photoPart{field: "photo", filename: "updated.png", data: handlerTestPNG})
	c, w := newPhotoContext(http.MethodPut, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "1")
	handler.Replace(c)

	if w.Code != http.StatusOK {
		t.Fatalf("replace status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var replaced photoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &replaced); err != nil {
		t.Fatalf("decode replace response: %v", err)
	}
	if replaced.Position != 1 || replaced.ContentType != "image/png" {
		t.Errorf("replaced photo = %+v", replaced)
	}

	c, w = newPhotoContext(http.MethodGet, target, nil)
	c.Params = recordParams(rec.ID, "1")
	handler.Get(c)

	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := w.Header().Get("ETag"); got != `"fake-etag"` {
		t.Errorf("ETag = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if !bytes.Equal(w.Body.Bytes(), handlerTestPNG) {
		t.Errorf("body = %v, want %v", w.Body.Bytes(), handlerTestPNG)
	}
}

func TestPhotoHandlerDelete(t *testing.T) {
	handler, svc, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	ctx := context.Background()

	for _, data := range [][]byte{handlerTestJPEG, handlerTestPNG} {
		if _, err := svc.Append(ctx, rec.ID, handlerPhotoUpload(data)); err != nil {
			t.Fatalf("seed photo: %v", err)
		}
	}

	target := fmt.Sprintf("/api/admin/records/%d/photos/1", rec.ID)
	c, w := newPhotoContext(http.MethodDelete, target, nil)
	c.Params = recordParams(rec.ID, "1")
	handler.Delete(c)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}

	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 1 || photos[0].Position != 1 || photos[0].ContentType != "image/png" {
		t.Fatalf("photos = %+v, want one png at position 1", photos)
	}
}

func TestPhotoHandlerInvalidPosition(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 1024)

	for _, position := range []string{"0", "-1", "abc"} {
		t.Run(position, func(t *testing.T) {
			target := fmt.Sprintf("/api/admin/records/%d/photos/%s", rec.ID, position)
			c, w := newPhotoContext(http.MethodGet, target, nil)
			c.Params = recordParams(rec.ID, position)
			handler.Get(c)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPhotoHandlerMissingPhoto(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	target := fmt.Sprintf("/api/admin/records/%d/photos/2", rec.ID)

	c, w := newPhotoContext(http.MethodGet, target, nil)
	c.Params = recordParams(rec.ID, "2")
	handler.Get(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestPhotoHandlerMissingRecord(t *testing.T) {
	handler, _, _, _ := setupPhotoHandlerTest(t, 9, 1024)
	const missingID = 9999
	target := fmt.Sprintf("/api/admin/records/%d/photos", missingID)
	req := multipartPhotoRequest(t, http.MethodPost, target, photoPart{field: "photo", filename: "repair.jpg", data: handlerTestJPEG})

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(missingID, "")
	handler.Create(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestPhotoHandlerLimitReached(t *testing.T) {
	handler, svc, rec, _ := setupPhotoHandlerTest(t, 1, 1024)
	if _, err := svc.Append(context.Background(), rec.ID, handlerPhotoUpload(handlerTestJPEG)); err != nil {
		t.Fatalf("seed photo: %v", err)
	}

	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target, photoPart{field: "photo", filename: "second.png", data: handlerTestPNG})

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestPhotoHandlerFileTooLarge(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 3)
	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target, photoPart{field: "photo", filename: "repair.jpg", data: handlerTestJPEG})

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestPhotoHandlerRequestBodyTooLarge(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 3)
	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target, photoPart{
		field:    "photo",
		filename: "huge.jpg",
		data:     bytes.Repeat([]byte{0xFF}, 2<<20),
	})

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestPhotoHandlerUnsupportedType(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target, photoPart{field: "photo", filename: "notes.txt", data: []byte("not an image")})

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestPhotoHandlerMissingFile(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target)

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), recordservice.ErrPhotoFileRequired.Error()) {
		t.Errorf("body = %s, want file-required error", w.Body.String())
	}
}

func TestPhotoHandlerMultipleFiles(t *testing.T) {
	handler, _, rec, _ := setupPhotoHandlerTest(t, 9, 1024)
	target := fmt.Sprintf("/api/admin/records/%d/photos", rec.ID)
	req := multipartPhotoRequest(t, http.MethodPost, target,
		photoPart{field: "photo", filename: "one.jpg", data: handlerTestJPEG},
		photoPart{field: "photo", filename: "two.png", data: handlerTestPNG},
	)

	c, w := newPhotoContext(http.MethodPost, target, req.Body)
	c.Request.Header = req.Header
	c.Params = recordParams(rec.ID, "")
	handler.Create(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}
