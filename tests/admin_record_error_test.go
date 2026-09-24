package tests

import (
	"clinic-backend/handlers"
	"net/http"
	"net/http/httptest"
	"testing"

	"clinic-backend/services"

	"github.com/gin-gonic/gin"
)

func TestWriteRecordErrorMapsRevertErrorsToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"window expired", services.ErrRevertWindowExpired, http.StatusConflict},
		{"unavailable", services.ErrRevertUnavailable, http.StatusConflict},
		{"superseded", services.ErrRevertSuperseded, http.StatusConflict},
		{"not found", services.ErrRecordNotFound, http.StatusNotFound},
		{"invalid transition", services.ErrRecordInvalidTransition, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			handlers.writeRecordError(c, tc.err)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
