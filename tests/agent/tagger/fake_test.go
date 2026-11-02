package tagger_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tagger "clinic-backend/internal/agent/tagger"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type generateFunc func(context.Context, []*schema.Message) (*schema.Message, error)

// fakeChatModel records every input it receives. Tests script Generate through
// setGenerate.
type fakeChatModel struct {
	id int

	mu       sync.Mutex
	inputs   [][]*schema.Message
	generate generateFunc
}

func (m *fakeChatModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	fn := m.generate
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, input)
	}
	return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
}

func (m *fakeChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not implemented")
}

func (m *fakeChatModel) setGenerate(fn generateFunc) {
	m.mu.Lock()
	m.generate = fn
	m.mu.Unlock()
}

func (m *fakeChatModel) inputsSnapshot() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]*schema.Message, len(m.inputs))
	for i, input := range m.inputs {
		out[i] = append([]*schema.Message(nil), input...)
	}
	return out
}

func (m *fakeChatModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.inputs)
}

// fakeFactory creates one fake model per call and records every model.
type fakeFactory struct {
	mu     sync.Mutex
	calls  int
	nextID int
	models []*fakeChatModel
	err    error
}

func (f *fakeFactory) New() (model.BaseChatModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.nextID++
	m := &fakeChatModel{id: f.nextID}
	f.models = append(f.models, m)
	return m, nil
}

func (f *fakeFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeFactory) modelsSnapshot() []*fakeChatModel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeChatModel(nil), f.models...)
}

func testTagSet() tagger.TagSet {
	return tagger.TagSet{
		Name:   "clinic_test",
		Prompt: "classify test records",
		Tags: []tagger.Tag{
			{Name: "tag_a", Description: "first tag", ApplyRule: "when a applies"},
			{Name: "tag_b", ApplyRule: "when b applies"},
		},
	}
}

func testPoolConfig(size int) tagger.PoolConfig {
	return tagger.PoolConfig{
		Size:          size,
		Timeout:       5 * time.Second,
		ContextTokens: 10000,
	}
}

func newTestPool(t *testing.T, size int) (*tagger.Pool, *fakeFactory) {
	t.Helper()
	return newTestPoolWithConfig(t, testPoolConfig(size))
}

func newTestPoolWithConfig(t *testing.T, cfg tagger.PoolConfig) (*tagger.Pool, *fakeFactory) {
	t.Helper()
	f := &fakeFactory{}
	p, err := tagger.NewPool(cfg, testTagSet(), f.New)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	return p, f
}

func assistantMessage(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content}
}

func assistantWithUsage(content string, total, cached int) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				TotalTokens:        total,
				PromptTokens:       total,
				PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: cached},
			},
		},
	}
}

func assistantWithFinish(content, finishReason string) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: finishReason,
		},
	}
}
