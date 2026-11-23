package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type fakeRedisPinger struct {
	err error
}

func (f fakeRedisPinger) Ping(context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", f.err)
}

func TestRedisHealthHandlerResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		pingErr    error
		wantCode   int
		wantStatus string
	}{
		{
			name:       "redis available",
			wantCode:   http.StatusOK,
			wantStatus: "ok",
		},
		{
			name:       "redis unavailable",
			pingErr:    errors.New("redis unavailable"),
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/healthz", redisHealthHandler(fakeRedisPinger{err: tt.pingErr}))

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			router.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantCode {
				t.Fatalf("status code = %d, want %d", recorder.Code, tt.wantCode)
			}

			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["status"] != tt.wantStatus {
				t.Errorf("status = %q, want %q", body["status"], tt.wantStatus)
			}
			if _, ok := body["redis"]; ok {
				t.Error("response unexpectedly contains redis status")
			}
			if len(body) != 1 {
				t.Errorf("response fields = %v, want only status", body)
			}
		})
	}
}
