package storage_test

import (
	"testing"

	"clinic-backend/internal/storage"
)

func TestNewClient_ValidLocalConfig(t *testing.T) {
	client, err := storage.NewClient(validConfig())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned a nil client")
	}
}

func TestNewClient_ValidDockerConfig(t *testing.T) {
	cfg := validConfig()
	cfg.InternalEndpoint = "http://rustfs:9000"

	client, err := storage.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned a nil client")
	}
}

func TestNewClient_ValidProductionConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoint = "https://s3.bitnp.net"
	cfg.Bucket = "clinic-photos"

	client, err := storage.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned a nil client")
	}
}

func TestNewClient_AcceptsRootPath(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoint = "http://localhost:9000/"

	client, err := storage.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned a nil client")
	}
}

func TestNewClient_DoesNotContactNetwork(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoint = "http://127.0.0.1:1"

	client, err := storage.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned a nil client")
	}
}
