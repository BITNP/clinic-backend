package tagger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tagger "clinic-backend/internal/agent/tagger"

	"github.com/cloudwego/eino/schema"
)

func TestNewPool(t *testing.T) {
	for _, size := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			f := &fakeFactory{}
			p, err := tagger.NewPool(testPoolConfig(size), testTagSet(), f.New)
			if err != nil {
				t.Fatalf("new pool: %v", err)
			}
			if p.Size() != size {
				t.Errorf("size = %d, want %d", p.Size(), size)
			}
			if f.callCount() != size {
				t.Errorf("factory calls = %d, want %d", f.callCount(), size)
			}
			if got := len(f.modelsSnapshot()); got != size {
				t.Errorf("models = %d, want %d", got, size)
			}
		})
	}
}

func TestNewPoolConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  tagger.PoolConfig
	}{
		{"zero size", tagger.PoolConfig{Size: 0, Timeout: time.Second, ContextTokens: 10000}},
		{"negative size", tagger.PoolConfig{Size: -1, Timeout: time.Second, ContextTokens: 10000}},
		{"zero timeout", tagger.PoolConfig{Size: 1, Timeout: 0, ContextTokens: 10000}},
		{"negative timeout", tagger.PoolConfig{Size: 1, Timeout: -time.Second, ContextTokens: 10000}},
		{"zero context", tagger.PoolConfig{Size: 1, Timeout: time.Second, ContextTokens: 0}},
		{"negative context", tagger.PoolConfig{Size: 1, Timeout: time.Second, ContextTokens: -1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFactory{}
			if _, err := tagger.NewPool(tc.cfg, testTagSet(), f.New); err == nil {
				t.Fatal("expected config error")
			}
			if f.callCount() != 0 {
				t.Errorf("factory called %d times for invalid config", f.callCount())
			}
		})
	}

	if _, err := tagger.NewPool(testPoolConfig(1), testTagSet(), nil); err == nil {
		t.Error("expected an error for a nil factory")
	}
}

func TestNewPoolSetValidation(t *testing.T) {
	base := testTagSet()
	cases := []struct {
		name string
		set  tagger.TagSet
	}{
		{"empty name", func() tagger.TagSet { s := base; s.Name = " "; return s }()},
		{"empty prompt", func() tagger.TagSet { s := base; s.Prompt = " "; return s }()},
		{"no tags", func() tagger.TagSet { s := base; s.Tags = nil; return s }()},
		{"empty tag name", func() tagger.TagSet {
			s := base
			s.Tags = []tagger.Tag{{Name: " ", ApplyRule: "rule"}}
			return s
		}()},
		{"empty apply rule", func() tagger.TagSet {
			s := base
			s.Tags = []tagger.Tag{{Name: "tag", ApplyRule: " "}}
			return s
		}()},
		{"duplicate tag", func() tagger.TagSet {
			s := base
			s.Tags = []tagger.Tag{
				{Name: "tag", ApplyRule: "one"},
				{Name: "tag", ApplyRule: "two"},
			}
			return s
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFactory{}
			if _, err := tagger.NewPool(testPoolConfig(1), tc.set, f.New); err == nil {
				t.Fatal("expected set validation error")
			}
			if f.callCount() != 0 {
				t.Errorf("factory called %d times for invalid set", f.callCount())
			}
		})
	}
}

func TestNewPoolFactoryError(t *testing.T) {
	f := &fakeFactory{err: errors.New("factory down")}
	_, err := tagger.NewPool(testPoolConfig(2), testTagSet(), f.New)
	if err == nil || !strings.Contains(err.Error(), "factory down") {
		t.Fatalf("error = %v, want factory error", err)
	}
}

func TestPoolTagDoesNotCreateModels(t *testing.T) {
	p, f := newTestPool(t, 2)
	for _, model := range f.modelsSnapshot() {
		model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
			return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
		})
	}

	if _, err := p.Tag(context.Background(), "text"); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if f.callCount() != 2 {
		t.Errorf("factory calls after tag = %d, want 2", f.callCount())
	}
}

func TestPoolConcurrentDispatch(t *testing.T) {
	p, f := newTestPool(t, 2)

	started := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	for _, model := range f.modelsSnapshot() {
		id := model.id
		model.setGenerate(func(ctx context.Context, _ []*schema.Message) (*schema.Message, error) {
			started <- id
			select {
			case <-release:
				return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	}

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := p.Tag(context.Background(), "text")
			results <- err
		}()
	}

	ids := make(map[int]struct{}, 2)
	for i := 0; i < 2; i++ {
		select {
		case id := <-started:
			ids[id] = struct{}{}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent calls")
		}
	}
	if len(ids) != 2 {
		t.Errorf("models used = %d, want 2 distinct models", len(ids))
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Tag(waitCtx, "third"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("third tag error = %v, want deadline exceeded", err)
	}

	releaseAll()
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("concurrent tag: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent tags to finish")
		}
	}
}

func TestPoolSessionReuse(t *testing.T) {
	p, f := newTestPool(t, 1)
	model := f.modelsSnapshot()[0]
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantWithUsage(`{"name":"tag_a","reason":"reason"}`, 10, 0), nil
	})

	if _, err := p.Tag(context.Background(), "first"); err != nil {
		t.Fatalf("first tag: %v", err)
	}
	if _, err := p.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 4 {
		t.Fatalf("second call messages = %d, want 4", len(inputs[1]))
	}
	if inputs[1][0].Content != inputs[0][0].Content {
		t.Error("system prompt changed between turns")
	}
	if inputs[1][1].Content != "Text to tag:\nfirst" {
		t.Errorf("first user message = %q", inputs[1][1].Content)
	}
	if want := `{"name":"tag_a","reason":"reason"}`; inputs[1][2].Content != want {
		t.Errorf("assistant message = %q, want %q", inputs[1][2].Content, want)
	}
	if inputs[1][3].Content != "Text to tag:\nsecond" {
		t.Errorf("second user message = %q", inputs[1][3].Content)
	}
	if f.callCount() != 1 {
		t.Errorf("factory calls = %d, want 1", f.callCount())
	}
}

func TestPoolResetsAtUsageLimit(t *testing.T) {
	cfg := testPoolConfig(1)
	cfg.ContextTokens = 500
	p, f := newTestPoolWithConfig(t, cfg)
	model := f.modelsSnapshot()[0]
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantWithUsage(`{"name":"tag_a","reason":"reason"}`, 600, 20), nil
	})

	if _, err := p.Tag(context.Background(), "first"); err != nil {
		t.Fatalf("first tag: %v", err)
	}
	if _, err := p.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after reset = %d, want system + user", len(inputs[1]))
	}
	if f.callCount() != 1 {
		t.Errorf("factory calls = %d, want 1 (model reused)", f.callCount())
	}
}

func TestPoolResetsWhenUsageMissing(t *testing.T) {
	cfg := testPoolConfig(1)
	cfg.ContextTokens = 3000
	p, f := newTestPoolWithConfig(t, cfg)
	model := f.modelsSnapshot()[0]
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
	})

	if _, err := p.Tag(context.Background(), strings.Repeat("x", 5000)); err != nil {
		t.Fatalf("first tag: %v", err)
	}
	if _, err := p.Tag(context.Background(), "short"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after estimate reset = %d, want system + user", len(inputs[1]))
	}
}

func TestPoolResetsOnFinishReasonLength(t *testing.T) {
	p, f := newTestPool(t, 1)
	model := f.modelsSnapshot()[0]
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantWithFinish(`{"name":"tag_a","reason":"reason"}`, "length"), nil
	})

	if _, err := p.Tag(context.Background(), "first"); err != nil {
		t.Fatalf("first tag: %v", err)
	}
	if _, err := p.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after length reset = %d, want system + user", len(inputs[1]))
	}
}

func TestPoolResetsOnContextError(t *testing.T) {
	p, f := newTestPool(t, 1)
	model := f.modelsSnapshot()[0]
	calls := 0
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("context length exceeded")
		}
		return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
	})

	if _, err := p.Tag(context.Background(), "first"); err == nil {
		t.Fatal("expected the first tag to fail")
	}
	if _, err := p.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after context error reset = %d, want system + user", len(inputs[1]))
	}
}

func TestPoolRollsBackOnModelError(t *testing.T) {
	p, f := newTestPool(t, 1)
	model := f.modelsSnapshot()[0]
	calls := 0
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary failure")
		}
		return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
	})

	if _, err := p.Tag(context.Background(), "failed"); err == nil {
		t.Fatal("expected the first tag to fail")
	}
	if _, err := p.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after rollback = %d, want system + user", len(inputs[1]))
	}
}

func TestPoolRollsBackOnParseError(t *testing.T) {
	p, f := newTestPool(t, 1)
	model := f.modelsSnapshot()[0]
	calls := 0
	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls++
		if calls == 1 {
			return assistantMessage("not json"), nil
		}
		return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
	})

	if _, err := p.Tag(context.Background(), "failed"); err == nil {
		t.Fatal("expected the first tag to fail")
	}
	if _, err := p.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after parse rollback = %d, want system + user", len(inputs[1]))
	}
}

func TestPoolTagEmptyText(t *testing.T) {
	p, f := newTestPool(t, 1)
	if _, err := p.Tag(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for empty text")
	}
	if got := f.modelsSnapshot()[0].callCount(); got != 0 {
		t.Errorf("model calls = %d, want 0", got)
	}
}

func TestPoolNil(t *testing.T) {
	var p *tagger.Pool
	if _, err := p.Tag(context.Background(), "text"); err == nil {
		t.Error("expected an error from a nil pool")
	}
	if p.Size() != 0 {
		t.Errorf("nil pool size = %d, want 0", p.Size())
	}
}
