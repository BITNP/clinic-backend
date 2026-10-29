package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	recordmodel "clinic-backend/internal/record/model"
	"clinic-backend/internal/storage"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPhotoNotFound        = errors.New("photo not found")
	ErrPhotoLimitReached    = errors.New("photo limit reached")
	ErrPhotoTooLarge        = errors.New("photo too large")
	ErrPhotoUnsupportedType = errors.New("unsupported image type")
	ErrPhotoFileRequired    = errors.New("photo file is required")
	ErrPhotoOrderInvalid    = errors.New("invalid photo order")
	ErrPhotoOrderConflict   = errors.New("photo order changed")
)

// photoStorage is the subset of storage.Service used by PhotoService.
type photoStorage interface {
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) (string, error)
	Get(ctx context.Context, key string) (*storage.Object, error)
	Delete(ctx context.Context, key string) error
}

// PhotoUpload describes one incoming photo file.
type PhotoUpload struct {
	Reader io.ReadSeeker
	Size   int64
}

// PhotoService owns ordered photos attached to a record.
type PhotoService struct {
	db           *gorm.DB
	store        photoStorage
	maxCount     int
	maxSizeBytes int64
}

// NewPhotoService creates a photo service. The database and storage are
// required, and both limits must be positive.
func NewPhotoService(db *gorm.DB, store photoStorage, maxCount int, maxSizeBytes int64) (*PhotoService, error) {
	switch {
	case db == nil:
		return nil, errors.New("photo service: database is required")
	case store == nil:
		return nil, errors.New("photo service: storage is required")
	case maxCount <= 0:
		return nil, errors.New("photo service: max count must be positive")
	case maxSizeBytes <= 0:
		return nil, errors.New("photo service: max size must be positive")
	}

	return &PhotoService{
		db:           db,
		store:        store,
		maxCount:     maxCount,
		maxSizeBytes: maxSizeBytes,
	}, nil
}

// MaxCount is the maximum number of photos a record may have.
func (s *PhotoService) MaxCount() int {
	return s.maxCount
}

// MaxSizeBytes is the maximum size of one photo.
func (s *PhotoService) MaxSizeBytes() int64 {
	return s.maxSizeBytes
}

// List returns a record's photos ordered by position.
func (s *PhotoService) List(ctx context.Context, recordID uint) ([]recordmodel.ClinicRecordPhoto, error) {
	if err := s.ensureRecord(ctx, recordID); err != nil {
		return nil, err
	}

	var photos []recordmodel.ClinicRecordPhoto
	if err := s.db.WithContext(ctx).
		Where("record_id = ?", recordID).
		Order("position ASC").
		Find(&photos).Error; err != nil {
		return nil, fmt.Errorf("list photos for record %d: %w", recordID, err)
	}
	return photos, nil
}

// Get returns one photo and its storage object.
func (s *PhotoService) Get(ctx context.Context, recordID uint, position int) (recordmodel.ClinicRecordPhoto, *storage.Object, error) {
	if err := s.ensureRecord(ctx, recordID); err != nil {
		return recordmodel.ClinicRecordPhoto{}, nil, err
	}

	photo, err := s.findPhoto(ctx, recordID, position)
	if err != nil {
		return recordmodel.ClinicRecordPhoto{}, nil, err
	}

	object, err := s.store.Get(ctx, photo.ObjectKey)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			return recordmodel.ClinicRecordPhoto{}, nil, ErrPhotoNotFound
		}
		return recordmodel.ClinicRecordPhoto{}, nil, fmt.Errorf("get photo object %q: %w", photo.ObjectKey, err)
	}
	return photo, object, nil
}

// Append validates and uploads a photo, then inserts it at the end of the
// record's ordered photo list.
func (s *PhotoService) Append(ctx context.Context, recordID uint, upload PhotoUpload) (recordmodel.ClinicRecordPhoto, error) {
	if err := s.ensureRecord(ctx, recordID); err != nil {
		return recordmodel.ClinicRecordPhoto{}, err
	}

	contentType, err := s.validateUpload(upload)
	if err != nil {
		return recordmodel.ClinicRecordPhoto{}, err
	}

	key, err := newPhotoObjectKey(recordID, contentType)
	if err != nil {
		return recordmodel.ClinicRecordPhoto{}, err
	}
	if _, err := s.store.Put(ctx, key, upload.Reader, upload.Size, contentType); err != nil {
		return recordmodel.ClinicRecordPhoto{}, fmt.Errorf("store photo: %w", err)
	}

	var photo recordmodel.ClinicRecordPhoto
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rec recordmodel.ClinicRecord
		if err := lockPhotoRecordTx(tx, recordID, &rec); err != nil {
			return err
		}

		var count int64
		if err := tx.Model(&recordmodel.ClinicRecordPhoto{}).
			Where("record_id = ?", recordID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("count photos for record %d: %w", recordID, err)
		}
		if count >= int64(s.maxCount) {
			return ErrPhotoLimitReached
		}

		photo = recordmodel.ClinicRecordPhoto{
			RecordID:    recordID,
			ObjectKey:   key,
			ContentType: contentType,
			Size:        upload.Size,
			Position:    int(count) + 1,
		}
		if err := tx.Create(&photo).Error; err != nil {
			return fmt.Errorf("create photo for record %d: %w", recordID, err)
		}
		return nil
	})
	if err != nil {
		s.deleteObjectQuietly(ctx, key)
		return recordmodel.ClinicRecordPhoto{}, err
	}
	return photo, nil
}

// Replace validates and uploads a new photo, replacing the photo at position
// while keeping its position unchanged.
func (s *PhotoService) Replace(ctx context.Context, recordID uint, position int, upload PhotoUpload) (recordmodel.ClinicRecordPhoto, error) {
	if err := s.ensureRecord(ctx, recordID); err != nil {
		return recordmodel.ClinicRecordPhoto{}, err
	}

	contentType, err := s.validateUpload(upload)
	if err != nil {
		return recordmodel.ClinicRecordPhoto{}, err
	}

	key, err := newPhotoObjectKey(recordID, contentType)
	if err != nil {
		return recordmodel.ClinicRecordPhoto{}, err
	}
	if _, err := s.store.Put(ctx, key, upload.Reader, upload.Size, contentType); err != nil {
		return recordmodel.ClinicRecordPhoto{}, fmt.Errorf("store photo: %w", err)
	}

	var photo recordmodel.ClinicRecordPhoto
	var oldKey string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rec recordmodel.ClinicRecord
		if err := lockPhotoRecordTx(tx, recordID, &rec); err != nil {
			return err
		}

		if err := tx.Where("record_id = ? AND position = ?", recordID, position).First(&photo).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPhotoNotFound
			}
			return fmt.Errorf("get photo at position %d: %w", position, err)
		}
		oldKey = photo.ObjectKey

		if err := tx.Model(&photo).Updates(map[string]any{
			"object_key":   key,
			"content_type": contentType,
			"size":         upload.Size,
		}).Error; err != nil {
			return fmt.Errorf("replace photo at position %d: %w", position, err)
		}
		return nil
	})
	if err != nil {
		s.deleteObjectQuietly(ctx, key)
		return recordmodel.ClinicRecordPhoto{}, err
	}

	if oldKey != key {
		s.deleteObjectQuietly(ctx, oldKey)
	}
	photo.ObjectKey = key
	photo.ContentType = contentType
	photo.Size = upload.Size
	return photo, nil
}

// Reorder rewrites the ordered photo list atomically. Every photo currently
// attached to the record must appear exactly once in both expectedPhotoIDs and
// photoIDs. expectedPhotoIDs is the client's snapshot of the current order; a
// mismatch means another request changed the order and returns
// ErrPhotoOrderConflict. Reordering never changes photo ids or image content.
func (s *PhotoService) Reorder(ctx context.Context, recordID uint, expectedPhotoIDs, photoIDs []uint) ([]recordmodel.ClinicRecordPhoto, error) {
	var photos []recordmodel.ClinicRecordPhoto
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rec recordmodel.ClinicRecord
		if err := lockPhotoRecordTx(tx, recordID, &rec); err != nil {
			return err
		}

		if err := tx.Where("record_id = ?", recordID).
			Order("position ASC").
			Find(&photos).Error; err != nil {
			return fmt.Errorf("list photos for record %d: %w", recordID, err)
		}

		byID := make(map[uint]struct{}, len(photos))
		for _, photo := range photos {
			byID[photo.ID] = struct{}{}
		}
		if !validPhotoOrder(expectedPhotoIDs, byID) || !validPhotoOrder(photoIDs, byID) {
			return ErrPhotoOrderInvalid
		}
		for index, photo := range photos {
			if expectedPhotoIDs[index] != photo.ID {
				return ErrPhotoOrderConflict
			}
		}

		unchanged := true
		for index, photoID := range photoIDs {
			if photos[index].ID != photoID {
				unchanged = false
				break
			}
		}
		if unchanged {
			return nil
		}

		// Move every row out of the 1..n range first. The current schema does
		// not enforce uniqueness on position, but this keeps the update safe if
		// that constraint is added later.
		offset := len(photos)
		for _, photo := range photos {
			if err := tx.Model(&recordmodel.ClinicRecordPhoto{}).
				Where("record_id = ? AND id = ?", recordID, photo.ID).
				UpdateColumn("position", photo.Position+offset).Error; err != nil {
				return fmt.Errorf("stage photo %d position: %w", photo.ID, err)
			}
		}
		for index, photoID := range photoIDs {
			if err := tx.Model(&recordmodel.ClinicRecordPhoto{}).
				Where("record_id = ? AND id = ?", recordID, photoID).
				UpdateColumn("position", index+1).Error; err != nil {
				return fmt.Errorf("set photo %d position: %w", photoID, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.List(ctx, recordID)
}

func validPhotoOrder(ids []uint, byID map[uint]struct{}) bool {
	if len(ids) != len(byID) {
		return false
	}
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

// Delete removes the photo at position and shifts every later photo down by
// one so positions stay contiguous.
func (s *PhotoService) Delete(ctx context.Context, recordID uint, position int) error {
	var key string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rec recordmodel.ClinicRecord
		if err := lockPhotoRecordTx(tx, recordID, &rec); err != nil {
			return err
		}

		var photo recordmodel.ClinicRecordPhoto
		if err := tx.Where("record_id = ? AND position = ?", recordID, position).First(&photo).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPhotoNotFound
			}
			return fmt.Errorf("get photo at position %d: %w", position, err)
		}
		key = photo.ObjectKey

		if err := tx.Delete(&photo).Error; err != nil {
			return fmt.Errorf("delete photo at position %d: %w", position, err)
		}
		if err := tx.Model(&recordmodel.ClinicRecordPhoto{}).
			Where("record_id = ? AND position > ?", recordID, position).
			UpdateColumn("position", gorm.Expr("position - 1")).Error; err != nil {
			return fmt.Errorf("shift photos after position %d: %w", position, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.deleteObjectQuietly(ctx, key)
	return nil
}

// DeleteByRecord removes every photo row and object belonging to a record.
// It is intended to run after the record itself has been deleted.
func (s *PhotoService) DeleteByRecord(ctx context.Context, recordID uint) error {
	var photos []recordmodel.ClinicRecordPhoto
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("record_id = ?", recordID).Find(&photos).Error; err != nil {
			return fmt.Errorf("list photos for record %d: %w", recordID, err)
		}
		if len(photos) == 0 {
			return nil
		}
		if err := tx.Where("record_id = ?", recordID).Delete(&recordmodel.ClinicRecordPhoto{}).Error; err != nil {
			return fmt.Errorf("delete photos for record %d: %w", recordID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, photo := range photos {
		s.deleteObjectQuietly(ctx, photo.ObjectKey)
	}
	return nil
}

func (s *PhotoService) ensureRecord(ctx context.Context, recordID uint) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&recordmodel.ClinicRecord{}).Where("id = ?", recordID).Count(&count).Error; err != nil {
		return fmt.Errorf("check record %d: %w", recordID, err)
	}
	if count == 0 {
		return ErrRecordNotFound
	}
	return nil
}

func (s *PhotoService) findPhoto(ctx context.Context, recordID uint, position int) (recordmodel.ClinicRecordPhoto, error) {
	var photo recordmodel.ClinicRecordPhoto
	if err := s.db.WithContext(ctx).
		Where("record_id = ? AND position = ?", recordID, position).
		First(&photo).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return recordmodel.ClinicRecordPhoto{}, ErrPhotoNotFound
		}
		return recordmodel.ClinicRecordPhoto{}, fmt.Errorf("get photo at position %d: %w", position, err)
	}
	return photo, nil
}

func (s *PhotoService) validateUpload(upload PhotoUpload) (string, error) {
	if upload.Reader == nil || upload.Size <= 0 {
		return "", ErrPhotoFileRequired
	}
	if upload.Size > s.maxSizeBytes {
		return "", ErrPhotoTooLarge
	}

	contentType, err := detectPhotoContentType(upload.Reader)
	if err != nil {
		return "", err
	}
	if _, ok := photoExtensions[contentType]; !ok {
		return "", ErrPhotoUnsupportedType
	}
	return contentType, nil
}

func (s *PhotoService) deleteObjectQuietly(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if err := s.store.Delete(ctx, key); err != nil {
		log.Printf("photo service: delete object %q: %v", key, err)
	}
}

func lockPhotoRecordTx(tx *gorm.DB, recordID uint, rec *recordmodel.ClinicRecord) error {
	q := tx
	// SQLite serializes writers itself and does not support SELECT ... FOR
	// UPDATE. Postgres needs the row lock to serialize position changes.
	if tx.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	if err := q.First(rec, recordID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRecordNotFound
		}
		return fmt.Errorf("lock record %d: %w", recordID, err)
	}
	return nil
}

var photoExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

func detectPhotoContentType(reader io.ReadSeeker) (string, error) {
	var header [512]byte
	n, err := io.ReadFull(reader, header[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read photo header: %w", err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind photo: %w", err)
	}
	if n == 0 {
		return "", ErrPhotoFileRequired
	}
	return http.DetectContentType(header[:n]), nil
}

func newPhotoObjectKey(recordID uint, contentType string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate photo object key: %w", err)
	}
	return fmt.Sprintf("photos/records/%d/%s%s", recordID, hex.EncodeToString(raw[:]), photoExtensions[contentType]), nil
}
