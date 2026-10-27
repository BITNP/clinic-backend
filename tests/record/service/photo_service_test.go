package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	"clinic-backend/internal/storage"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeObject struct {
	data        []byte
	contentType string
}

type fakePhotoStorage struct {
	objects   map[string]fakeObject
	deleted   []string
	putErr    error
	getErr    error
	deleteErr error
}

func newFakePhotoStorage() *fakePhotoStorage {
	return &fakePhotoStorage{objects: make(map[string]fakeObject)}
}

func (f *fakePhotoStorage) Put(_ context.Context, key string, body io.ReadSeeker, size int64, contentType string) (string, error) {
	if f.putErr != nil {
		return "", f.putErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if int64(len(data)) != size {
		return "", fmt.Errorf("fake put: body size %d does not match declared %d", len(data), size)
	}
	f.objects[key] = fakeObject{data: append([]byte(nil), data...), contentType: contentType}
	return `"fake-etag"`, nil
}

func (f *fakePhotoStorage) Get(_ context.Context, key string) (*storage.Object, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
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
	f.deleted = append(f.deleted, key)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.objects, key)
	return nil
}

func (f *fakePhotoStorage) has(key string) bool {
	_, ok := f.objects[key]
	return ok
}

var (
	testJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
	testPNG  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0x00, 0x00, 0x00, 0x0D}
	testWebP = []byte{'R', 'I', 'F', 'F', 0x20, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' '}
)

func photoUpload(data []byte) recordservice.PhotoUpload {
	return recordservice.PhotoUpload{Reader: bytes.NewReader(data), Size: int64(len(data))}
}

func setupPhotoServiceTest(t *testing.T, maxCount int, maxSize int64) (*gorm.DB, *fakePhotoStorage, *recordservice.PhotoService, recordmodel.ClinicRecord) {
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
	return db, store, svc, rec
}

func TestPhotoServiceAppendAssignsPositions(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	first, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG))
	if err != nil {
		t.Fatalf("Append(jpeg) error = %v", err)
	}
	second, err := svc.Append(ctx, rec.ID, photoUpload(testPNG))
	if err != nil {
		t.Fatalf("Append(png) error = %v", err)
	}

	if first.Position != 1 || second.Position != 2 {
		t.Fatalf("positions = %d/%d, want 1/2", first.Position, second.Position)
	}
	if first.ContentType != "image/jpeg" || second.ContentType != "image/png" {
		t.Fatalf("content types = %q/%q, want image/jpeg/image/png", first.ContentType, second.ContentType)
	}
	if first.Size != int64(len(testJPEG)) || second.Size != int64(len(testPNG)) {
		t.Fatalf("sizes = %d/%d, want %d/%d", first.Size, second.Size, len(testJPEG), len(testPNG))
	}

	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 2 {
		t.Fatalf("photos = %d, want 2", len(photos))
	}
	if photos[0].Position != 1 || photos[1].Position != 2 {
		t.Fatalf("list positions = %d/%d, want 1/2", photos[0].Position, photos[1].Position)
	}

	if !strings.HasPrefix(first.ObjectKey, fmt.Sprintf("photos/records/%d/", rec.ID)) {
		t.Errorf("object key = %q, want record prefix", first.ObjectKey)
	}
	if !strings.HasSuffix(first.ObjectKey, ".jpg") || !strings.HasSuffix(second.ObjectKey, ".png") {
		t.Errorf("object key extensions = %q/%q, want .jpg/.png", first.ObjectKey, second.ObjectKey)
	}
	if len(store.objects) != 2 {
		t.Errorf("stored objects = %d, want 2", len(store.objects))
	}
}

func TestPhotoServiceGet(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	photo, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG))
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	got, object, err := svc.Get(ctx, rec.ID, 1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer object.Body.Close()

	if got.ID != photo.ID || got.Position != 1 {
		t.Errorf("photo = id %d position %d, want id %d position 1", got.ID, got.Position, photo.ID)
	}
	body, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if !bytes.Equal(body, testJPEG) {
		t.Errorf("body = %v, want %v", body, testJPEG)
	}

	delete(store.objects, photo.ObjectKey)
	if _, _, err := svc.Get(ctx, rec.ID, 1); !errors.Is(err, recordservice.ErrPhotoNotFound) {
		t.Fatalf("Get() missing object error = %v, want ErrPhotoNotFound", err)
	}
}

func TestPhotoServiceAppendEnforcesLimit(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 1, 1024)
	ctx := context.Background()

	if _, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG)); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if _, err := svc.Append(ctx, rec.ID, photoUpload(testPNG)); !errors.Is(err, recordservice.ErrPhotoLimitReached) {
		t.Fatalf("Append(second) error = %v, want ErrPhotoLimitReached", err)
	}

	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 1 {
		t.Errorf("photos = %d, want 1", len(photos))
	}
	if len(store.objects) != 1 {
		t.Errorf("stored objects = %d, want 1", len(store.objects))
	}
}

func TestPhotoServiceAppendEnforcesMaxSize(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 3)
	ctx := context.Background()

	if _, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG)); !errors.Is(err, recordservice.ErrPhotoTooLarge) {
		t.Fatalf("Append() error = %v, want ErrPhotoTooLarge", err)
	}
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
}

func TestPhotoServiceAppendRejectsUnsupportedType(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	if _, err := svc.Append(ctx, rec.ID, photoUpload([]byte("not an image"))); !errors.Is(err, recordservice.ErrPhotoUnsupportedType) {
		t.Fatalf("Append() error = %v, want ErrPhotoUnsupportedType", err)
	}
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
}

func TestPhotoServiceAppendRequiresFile(t *testing.T) {
	_, _, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	tests := []recordservice.PhotoUpload{
		{},
		{Reader: bytes.NewReader(nil), Size: 0},
		{Reader: bytes.NewReader(testJPEG), Size: -1},
	}
	for _, upload := range tests {
		if _, err := svc.Append(ctx, rec.ID, upload); !errors.Is(err, recordservice.ErrPhotoFileRequired) {
			t.Fatalf("Append(%+v) error = %v, want ErrPhotoFileRequired", upload, err)
		}
	}
}

func TestPhotoServiceReplaceKeepsPositionAndRemovesOldObject(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	first, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG))
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	replaced, err := svc.Replace(ctx, rec.ID, 1, photoUpload(testPNG))
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if replaced.ID != first.ID {
		t.Errorf("id = %d, want %d", replaced.ID, first.ID)
	}
	if replaced.Position != 1 {
		t.Errorf("position = %d, want 1", replaced.Position)
	}
	if replaced.ContentType != "image/png" {
		t.Errorf("content type = %q, want image/png", replaced.ContentType)
	}
	if store.has(first.ObjectKey) {
		t.Errorf("old object %q still stored", first.ObjectKey)
	}
	if !store.has(replaced.ObjectKey) {
		t.Errorf("new object %q not stored", replaced.ObjectKey)
	}

	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 1 || photos[0].ID != first.ID || photos[0].ContentType != "image/png" {
		t.Fatalf("photos = %+v, want one replaced png", photos)
	}
}

func TestPhotoServiceReplaceNotFound(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	if _, err := svc.Replace(ctx, rec.ID, 1, photoUpload(testPNG)); !errors.Is(err, recordservice.ErrPhotoNotFound) {
		t.Fatalf("Replace() error = %v, want ErrPhotoNotFound", err)
	}
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0 after cleanup", len(store.objects))
	}
}

func TestPhotoServiceDeleteRenumbers(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	first, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG))
	if err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if _, err := svc.Append(ctx, rec.ID, photoUpload(testPNG)); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}
	third, err := svc.Append(ctx, rec.ID, photoUpload(testWebP))
	if err != nil {
		t.Fatalf("Append(third) error = %v", err)
	}

	if err := svc.Delete(ctx, rec.ID, 2); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 2 {
		t.Fatalf("photos = %d, want 2", len(photos))
	}
	if photos[0].ID != first.ID || photos[1].ID != third.ID {
		t.Fatalf("remaining ids = %d/%d, want %d/%d", photos[0].ID, photos[1].ID, first.ID, third.ID)
	}
	if photos[0].Position != 1 || photos[1].Position != 2 {
		t.Fatalf("positions = %d/%d, want 1/2", photos[0].Position, photos[1].Position)
	}
	if !store.has(photos[1].ObjectKey) {
		t.Errorf("third object should still be stored")
	}
	if len(store.objects) != 2 {
		t.Errorf("stored objects = %d, want 2", len(store.objects))
	}
}

func TestPhotoServiceDeleteNotFound(t *testing.T) {
	_, _, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	if err := svc.Delete(ctx, rec.ID, 1); !errors.Is(err, recordservice.ErrPhotoNotFound) {
		t.Fatalf("Delete() error = %v, want ErrPhotoNotFound", err)
	}
}

func TestPhotoServiceDeleteStorageFailureStillDeletesRow(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	if _, err := svc.Append(ctx, rec.ID, photoUpload(testJPEG)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	store.deleteErr = errors.New("storage unavailable")

	if err := svc.Delete(ctx, rec.ID, 1); err != nil {
		t.Fatalf("Delete() error = %v, want nil (storage cleanup is best-effort)", err)
	}
	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 0 {
		t.Errorf("photos = %d, want 0", len(photos))
	}
}

func TestPhotoServiceDeleteByRecord(t *testing.T) {
	_, store, svc, rec := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()

	for _, data := range [][]byte{testJPEG, testPNG} {
		if _, err := svc.Append(ctx, rec.ID, photoUpload(data)); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	if err := svc.DeleteByRecord(ctx, rec.ID); err != nil {
		t.Fatalf("DeleteByRecord() error = %v", err)
	}
	photos, err := svc.List(ctx, rec.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(photos) != 0 {
		t.Errorf("photos = %d, want 0", len(photos))
	}
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
}

func TestPhotoServiceMissingRecord(t *testing.T) {
	_, store, svc, _ := setupPhotoServiceTest(t, 9, 1024)
	ctx := context.Background()
	const missingID = 9999

	if _, err := svc.List(ctx, missingID); !errors.Is(err, recordservice.ErrRecordNotFound) {
		t.Fatalf("List() error = %v, want ErrRecordNotFound", err)
	}
	if _, _, err := svc.Get(ctx, missingID, 1); !errors.Is(err, recordservice.ErrRecordNotFound) {
		t.Fatalf("Get() error = %v, want ErrRecordNotFound", err)
	}
	if _, err := svc.Append(ctx, missingID, photoUpload(testJPEG)); !errors.Is(err, recordservice.ErrRecordNotFound) {
		t.Fatalf("Append() error = %v, want ErrRecordNotFound", err)
	}
	if _, err := svc.Replace(ctx, missingID, 1, photoUpload(testJPEG)); !errors.Is(err, recordservice.ErrRecordNotFound) {
		t.Fatalf("Replace() error = %v, want ErrRecordNotFound", err)
	}
	if err := svc.Delete(ctx, missingID, 1); !errors.Is(err, recordservice.ErrRecordNotFound) {
		t.Fatalf("Delete() error = %v, want ErrRecordNotFound", err)
	}
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
}

func TestPhotoServiceAppendCleansUpObjectOnDatabaseFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_fk=1"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Intentionally omit ClinicRecordPhoto so the insert fails after upload.
	if err := db.AutoMigrate(&recordmodel.ClinicRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rec := recordmodel.ClinicRecord{
		User: "u", Realname: "r", PhoneNum: "p",
		Status:          recordmodel.RecordStatusInProgress,
		AppointmentTime: time.Now().UTC(),
		QuestionDesc:    "x", RoomID: 1,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	store := newFakePhotoStorage()
	svc, err := recordservice.NewPhotoService(db, store, 9, 1024)
	if err != nil {
		t.Fatalf("NewPhotoService() error = %v", err)
	}

	if _, err := svc.Append(context.Background(), rec.ID, photoUpload(testJPEG)); err == nil {
		t.Fatal("expected Append() to fail without a photo table")
	}
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0 after cleanup", len(store.objects))
	}
}

func TestNewPhotoServiceValidation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := newFakePhotoStorage()

	if _, err := recordservice.NewPhotoService(nil, store, 1, 1); err == nil {
		t.Fatal("expected an error for a nil database")
	}
	if _, err := recordservice.NewPhotoService(db, nil, 1, 1); err == nil {
		t.Fatal("expected an error for nil storage")
	}
	if _, err := recordservice.NewPhotoService(db, store, 0, 1); err == nil {
		t.Fatal("expected an error for zero max count")
	}
	if _, err := recordservice.NewPhotoService(db, store, 1, 0); err == nil {
		t.Fatal("expected an error for zero max size")
	}
}
