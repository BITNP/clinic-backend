package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clinic-backend/internal/agent"
)

const chatCompletionResponse = `{
	"id": "chatcmpl-test",
	"object": "chat.completion",
	"created": 1700000000,
	"model": "test-model",
	"choices": [
		{
			"index": 0,
			"message": {"role": "assistant", "content": "{\"ok\":true}"},
			"finish_reason": "stop"
		}
	],
	"usage": {"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8}
}`

func TestCheckConnectionSucceeds(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)

		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q, want bearer key", got)
		}

		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		} else if body.Model != "test-model" {
			t.Errorf("model = %q, want test-model", body.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, chatCompletionResponse); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	err := agent.CheckConnection(context.Background(), agent.ChatModelConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("check connection: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestCheckConnectionReportsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	err := agent.CheckConnection(context.Background(), agent.ChatModelConfig{
		APIKey:  "bad-key",
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected a provider error")
	}
	if !strings.Contains(err.Error(), "model provider request") {
		t.Errorf("error = %v, want model provider request context", err)
	}
}

func TestCheckConnectionReportsUnreachableProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()

	err := agent.CheckConnection(context.Background(), agent.ChatModelConfig{
		APIKey:  "test-key",
		BaseURL: baseURL,
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err == nil {
		t.Fatal("expected a connection error")
	}
}

func TestCheckConnectionRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  agent.ChatModelConfig
	}{
		{"missing api key", agent.ChatModelConfig{BaseURL: "http://localhost", Model: "test-model"}},
		{"missing base url", agent.ChatModelConfig{APIKey: "test-key", Model: "test-model"}},
		{"missing model", agent.ChatModelConfig{APIKey: "test-key", BaseURL: "http://localhost"}},
		{"negative timeout", agent.ChatModelConfig{APIKey: "test-key", BaseURL: "http://localhost", Model: "test-model", Timeout: -time.Second}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := agent.CheckConnection(context.Background(), tc.cfg); err == nil {
				t.Fatal("expected a config error")
			}
		})
	}
}
