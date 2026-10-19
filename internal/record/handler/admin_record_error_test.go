package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	recordservice "clinic-backend/internal/record/service"

	"github.com/gin-gonic/gin"
)

func TestWriteRecordErrorMapsRevertErrorsToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"window expired", recordservice.ErrRevertWindowExpired, http.StatusConflict},
		{"unavailable", recordservice.ErrRevertUnavailable, http.StatusConflict},
		{"superseded", recordservice.ErrRevertSuperseded, http.StatusConflict},
		{"not found", recordservice.ErrRecordNotFound, http.StatusNotFound},
		{"invalid transition", recordservice.ErrRecordInvalidTransition, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			writeRecordError(c, tc.err)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
