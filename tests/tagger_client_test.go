package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"clinic-backend/handlers"
	"clinic-backend/services"
)

type clientTagDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ApplyRule   string `json:"apply_rule"`
}

type clientRegisterBody struct {
	Name   string         `json:"name"`
	Prompt string         `json:"prompt"`
	Tags   []clientTagDef `json:"tags"`
}

func TestTaggerHTTPClient_RegisterTagSet(t *testing.T) {
	var got clientRegisterBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/tagger/tag-sets" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if gotToken := r.Header.Get("Authorization"); gotToken != "Bearer secret" {
			t.Errorf("expected bearer token, got %q", gotToken)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := handlers.NewTaggerHTTPClient(srv.URL, "secret", time.Second)
	err := client.RegisterTagSet(context.Background(), "clinic_record", "tag the text", []services.TaggerTag{
		{Name: "换风扇", Description: "fan", ApplyRule: "fan noise"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got.Name != "clinic_record" {
		t.Errorf("expected set name clinic_record, got %q", got.Name)
	}
	if got.Prompt != "tag the text" {
		t.Errorf("expected prompt in payload, got %q", got.Prompt)
	}
	if len(got.Tags) != 1 || got.Tags[0].Name != "换风扇" || got.Tags[0].ApplyRule != "fan noise" {
		t.Errorf("unexpected tags payload: %+v", got.Tags)
	}
}

func TestTaggerHTTPClient_Tag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/tagger/tag" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"name": "clinic_record",
			"tag":  map[string]any{"name": "换风扇", "reason": "fan noise"},
		})
	}))
	defer srv.Close()

	client := handlers.NewTaggerHTTPClient(srv.URL, "secret", time.Second)
	name, err := client.Tag(context.Background(), "clinic_record", "风扇异响")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if name != "换风扇" {
		t.Errorf("expected 换风扇, got %q", name)
	}
}

func TestTaggerHTTPClient_TagNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"name": "clinic_record", "tag": nil})
	}))
	defer srv.Close()

	client := handlers.NewTaggerHTTPClient(srv.URL, "secret", time.Second)
	name, err := client.Tag(context.Background(), "clinic_record", "hello")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if name != "" {
		t.Errorf("expected empty tag, got %q", name)
	}
}

func TestTaggerHTTPClient_TagNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := handlers.NewTaggerHTTPClient(srv.URL, "secret", time.Second)
	_, err := client.Tag(context.Background(), "clinic_record", "hello")
	if !errors.Is(err, services.ErrTagSetNotFound) {
		t.Errorf("expected ErrTagSetNotFound, got %v", err)
	}
}

func TestTaggerHTTPClient_RegisterTagSetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := handlers.NewTaggerHTTPClient(srv.URL, "secret", time.Second)
	err := client.RegisterTagSet(context.Background(), "clinic_record", "tag the text", []services.TaggerTag{
		{Name: "换风扇", ApplyRule: "fan noise"},
	})
	if err == nil {
		t.Fatal("expected error from 500 response")
	}
}
