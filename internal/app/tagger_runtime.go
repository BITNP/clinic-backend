package app

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"clinic-backend/internal/agent"
	"clinic-backend/internal/agent/tagger"
	configservice "clinic-backend/internal/config/service"
	taggingservice "clinic-backend/internal/tagging/service"
)

type taggerRuntimeConfig struct {
	Enabled       bool
	APIKey        string
	BaseURL       string
	Model         string
	PoolSize      int
	ContextTokens int
	Timeout       time.Duration
}

type preparedTaggerGeneration struct {
	config      taggerRuntimeConfig
	recordAgent taggingservice.TagAgent
	liquidMetal *taggingservice.LiquidMetalTagger
	workers     int
}

type taggerRuntimeManager struct {
	mu          sync.Mutex
	record      *taggingservice.RecordTagger
	tagSvc      *taggingservice.RecordTagService
	active      taggerRuntimeConfig
	pending     *preparedTaggerGeneration
	initialized bool
}

func newTaggerRuntimeManager(record *taggingservice.RecordTagger, tagSvc *taggingservice.RecordTagService) *taggerRuntimeManager {
	return &taggerRuntimeManager{record: record, tagSvc: tagSvc}
}

func (m *taggerRuntimeManager) Initialize(settings configservice.RuntimeSettings) error {
	config, err := taggerConfigFromRuntime(settings)
	if err != nil {
		return err
	}
	generation, err := m.build(config)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyLocked(generation)
	return nil
}

// Prepare validates and builds a candidate generation without changing the
// active pools. ConfigService calls it before committing a runtime update.
func (m *taggerRuntimeManager) Prepare(settings configservice.RuntimeSettings) error {
	config, err := taggerConfigFromRuntime(settings)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initialized && config == m.active {
		m.pending = nil
		return nil
	}

	generation, err := m.build(config)
	if err != nil {
		return err
	}
	m.pending = generation
	return nil
}

// Activate applies the generation prepared for settings after the database
// commit succeeds.
func (m *taggerRuntimeManager) Activate(settings configservice.RuntimeSettings) {
	config, err := taggerConfigFromRuntime(settings)
	if err != nil {
		log.Printf("tagger runtime: skipped activation of invalid settings: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil || m.pending.config != config {
		return
	}
	m.applyLocked(m.pending)
}

func (m *taggerRuntimeManager) applyLocked(generation *preparedTaggerGeneration) {
	m.record.SetTaggers(generation.recordAgent, generation.liquidMetal, generation.workers)
	m.active = generation.config
	m.pending = nil
	m.initialized = true
	if generation.config.Enabled {
		log.Printf(
			"tagger agent: enabled (model %s, %d sessions per tag set, context limit %d tokens)",
			generation.config.Model,
			generation.config.PoolSize,
			generation.config.ContextTokens,
		)
	} else {
		log.Println("tagger disabled: TAGGER_LLM_API_KEY config item is empty")
	}
}

func (m *taggerRuntimeManager) build(config taggerRuntimeConfig) (*preparedTaggerGeneration, error) {
	generation := &preparedTaggerGeneration{config: config, workers: 1}
	if !config.Enabled {
		return generation, nil
	}

	poolConfig := tagger.PoolConfig{
		Size:          config.PoolSize,
		Timeout:       config.Timeout,
		ContextTokens: config.ContextTokens,
	}
	modelConfig := agent.ChatModelConfig{
		APIKey:  config.APIKey,
		BaseURL: config.BaseURL,
		Model:   config.Model,
		Timeout: config.Timeout,
	}
	factory := agent.NewChatModelFactory(context.Background(), modelConfig)

	recordPool, err := tagger.NewPool(
		poolConfig,
		taggingservice.RecordTagSet(m.tagSvc),
		factory,
	)
	if err != nil {
		return nil, fmt.Errorf("build record tagger pool: %w", err)
	}

	liquidMetalSet, err := taggingservice.LiquidMetalTagSet()
	if err != nil {
		return nil, fmt.Errorf("build liquid metal tag set: %w", err)
	}
	liquidMetalPool, err := tagger.NewPool(poolConfig, liquidMetalSet, factory)
	if err != nil {
		return nil, fmt.Errorf("build liquid metal tagger pool: %w", err)
	}

	generation.recordAgent = recordPool
	generation.liquidMetal = taggingservice.NewLiquidMetalTagger(liquidMetalPool, liquidMetalSet)
	generation.workers = recordPool.Size()
	return generation, nil
}

func taggerConfigFromRuntime(settings configservice.RuntimeSettings) (taggerRuntimeConfig, error) {
	config := taggerRuntimeConfig{
		APIKey:        strings.TrimSpace(settings.TaggerLLMAPIKey),
		BaseURL:       strings.TrimRight(strings.TrimSpace(settings.TaggerLLMBaseURL), "/"),
		Model:         strings.TrimSpace(settings.TaggerLLMModel),
		PoolSize:      settings.TaggerAgentPoolSize,
		ContextTokens: settings.TaggerContextTokens,
		Timeout:       settings.TaggerTimeout,
	}
	config.Enabled = config.APIKey != ""
	if config.PoolSize < 1 {
		return taggerRuntimeConfig{}, fmt.Errorf("TAGGER_AGENT_POOL_SIZE must be at least 1")
	}
	if config.ContextTokens < 1 {
		return taggerRuntimeConfig{}, fmt.Errorf("TAGGER_CONTEXT_TOKENS must be positive")
	}
	if config.Timeout <= 0 {
		return taggerRuntimeConfig{}, fmt.Errorf("TAGGER_TIMEOUT must be positive")
	}
	if config.BaseURL != "" {
		parsed, err := url.Parse(config.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return taggerRuntimeConfig{}, fmt.Errorf("TAGGER_LLM_BASE_URL must be an HTTP(S) URL")
		}
	}
	if !config.Enabled {
		return config, nil
	}
	if config.BaseURL == "" {
		return taggerRuntimeConfig{}, fmt.Errorf("TAGGER_LLM_BASE_URL is required when TAGGER_LLM_API_KEY is set")
	}
	if config.Model == "" {
		return taggerRuntimeConfig{}, fmt.Errorf("TAGGER_LLM_MODEL is required when TAGGER_LLM_API_KEY is set")
	}
	return config, nil
}
