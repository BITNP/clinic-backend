package app

import (
	"strings"
	"testing"
	"time"

	configservice "clinic-backend/internal/config/service"
	taggingservice "clinic-backend/internal/tagging/service"
	"clinic-backend/internal/testutil"
)

func TestTaggerConfigFromRuntime(t *testing.T) {
	base := configservice.RuntimeSettings{
		TaggerAgentPoolSize: 4,
		TaggerContextTokens: 32000,
		TaggerTimeout:       30 * time.Second,
	}

	tests := []struct {
		name    string
		update  func(*configservice.RuntimeSettings)
		wantErr string
	}{
		{
			name: "disabled",
		},
		{
			name: "enabled",
			update: func(settings *configservice.RuntimeSettings) {
				settings.TaggerLLMAPIKey = " key "
				settings.TaggerLLMBaseURL = " https://ai.example/v1/ "
				settings.TaggerLLMModel = " model "
			},
		},
		{
			name: "missing base URL",
			update: func(settings *configservice.RuntimeSettings) {
				settings.TaggerLLMAPIKey = "key"
				settings.TaggerLLMModel = "model"
			},
			wantErr: "TAGGER_LLM_BASE_URL",
		},
		{
			name: "missing model",
			update: func(settings *configservice.RuntimeSettings) {
				settings.TaggerLLMAPIKey = "key"
				settings.TaggerLLMBaseURL = "https://ai.example/v1"
			},
			wantErr: "TAGGER_LLM_MODEL",
		},
		{
			name: "invalid base URL",
			update: func(settings *configservice.RuntimeSettings) {
				settings.TaggerLLMAPIKey = "key"
				settings.TaggerLLMBaseURL = "ai.example/v1"
				settings.TaggerLLMModel = "model"
			},
			wantErr: "TAGGER_LLM_BASE_URL",
		},
		{
			name: "invalid pool size",
			update: func(settings *configservice.RuntimeSettings) {
				settings.TaggerAgentPoolSize = 0
			},
			wantErr: "TAGGER_AGENT_POOL_SIZE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := base
			if tt.update != nil {
				tt.update(&settings)
			}
			config, err := taggerConfigFromRuntime(settings)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("taggerConfigFromRuntime() error = %v", err)
			}
			if tt.name == "enabled" {
				if !config.Enabled || config.APIKey != "key" || config.BaseURL != "https://ai.example/v1" || config.Model != "model" {
					t.Errorf("config = %+v", config)
				}
			}
		})
	}
}

func TestTaggerRuntimeManagerRebuildsGeneration(t *testing.T) {
	db := testutil.SetupTagTestDB(t)
	rawDB := testutil.RawDB(t, db)
	tagSvc := taggingservice.NewRecordTagService(rawDB)
	if _, err := tagSvc.SeedDefault(); err != nil {
		t.Fatal(err)
	}
	if err := tagSvc.SeedCatalog(); err != nil {
		t.Fatal(err)
	}
	if err := tagSvc.Load(); err != nil {
		t.Fatal(err)
	}

	recordTagger := taggingservice.NewRecordTagger(nil, rawDB, tagSvc)
	manager := newTaggerRuntimeManager(recordTagger, tagSvc)
	settings := configservice.RuntimeSettings{
		TaggerAgentPoolSize: 2,
		TaggerContextTokens: 16000,
		TaggerTimeout:       10 * time.Second,
	}
	if err := manager.Initialize(settings); err != nil {
		t.Fatalf("initialize disabled tagger: %v", err)
	}

	settings.TaggerLLMAPIKey = "key"
	settings.TaggerLLMBaseURL = "https://ai.example/v1"
	settings.TaggerLLMModel = "model"
	settings.TaggerAgentPoolSize = 4
	if err := manager.Prepare(settings); err != nil {
		t.Fatalf("prepare enabled tagger: %v", err)
	}
	manager.mu.Lock()
	if manager.pending == nil || manager.pending.workers != 4 || manager.pending.recordAgent == nil {
		t.Fatalf("pending generation = %+v", manager.pending)
	}
	manager.mu.Unlock()

	manager.Activate(settings)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !manager.active.Enabled || manager.active.PoolSize != 4 || manager.pending != nil {
		t.Fatalf("active generation = %+v, pending = %+v", manager.active, manager.pending)
	}
}
