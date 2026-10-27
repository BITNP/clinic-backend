package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	recordservice "clinic-backend/internal/record/service"

	"github.com/gin-gonic/gin"
)

func TestWritePhotoErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"record not found", recordservice.ErrRecordNotFound, http.StatusNotFound},
		{"photo not found", recordservice.ErrPhotoNotFound, http.StatusNotFound},
		{"limit reached", recordservice.ErrPhotoLimitReached, http.StatusConflict},
		{"too large", recordservice.ErrPhotoTooLarge, http.StatusRequestEntityTooLarge},
		{"unsupported type", recordservice.ErrPhotoUnsupportedType, http.StatusBadRequest},
		{"file required", recordservice.ErrPhotoFileRequired, http.StatusBadRequest},
		{"unexpected", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			writePhotoError(c, tc.err)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
