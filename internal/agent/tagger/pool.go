package tagger

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"clinic-backend/internal/agent"
)

// PoolConfig describes a tagger session pool.
type PoolConfig struct {
	// Size is the number of long-lived chat models and sessions.
	Size int
	// Timeout bounds both waiting for an idle slot and one model call.
	Timeout time.Duration
	// ContextTokens is the token count at which a session is reset.
	ContextTokens int
}

// Pool keeps long-lived taggers. Each slot owns one chat model and one session
// preloaded with the tag set system prompt.
type Pool struct {
	name    string
	timeout time.Duration
	idle    chan *Tagger
}

// NewPool creates Size chat models and sessions. The chat model factory is
// called exactly Size times; requests never construct models.
func NewPool(cfg PoolConfig, set TagSet, factory agent.ChatModelFactory) (*Pool, error) {
	if cfg.Size < 1 {
		return nil, fmt.Errorf("tagger: pool size must be at least 1")
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("tagger: pool timeout must be positive")
	}
	if cfg.ContextTokens <= 0 {
		return nil, fmt.Errorf("tagger: pool context tokens must be positive")
	}
	if factory == nil {
		return nil, fmt.Errorf("tagger: chat model factory is required")
	}

	normalized, systemPrompt, err := prepareTagSet(set)
	if err != nil {
		return nil, err
	}

	p := &Pool{
		name:    normalized.Name,
		timeout: cfg.Timeout,
		idle:    make(chan *Tagger, cfg.Size),
	}
	for i := 0; i < cfg.Size; i++ {
		chat, err := factory()
		if err != nil {
			return nil, fmt.Errorf("tagger: create chat model %d: %w", i, err)
		}
		t, err := newTagger(chat, normalized, systemPrompt, cfg.ContextTokens)
		if err != nil {
			return nil, fmt.Errorf("tagger: create tagger %d: %w", i, err)
		}
		p.idle <- t
	}
	return p, nil
}

// Tag dispatches text to an idle tagger slot. A session is reset before it is
// returned to the pool when it approaches the context limit.
func (p *Pool) Tag(ctx context.Context, text string) (Result, error) {
	if p == nil {
		return Result{}, errors.New("tagger: nil pool")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, errors.New("tagger: text is required")
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var t *Tagger
	select {
	case t = <-p.idle:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	defer func() {
		if t.IsExhausted() {
			log.Printf("tagger: reset %q session after %d tokens (%d cached)", p.name, t.Tokens(), t.CachedTokens())
			t.Reset()
		}
		p.idle <- t
	}()

	return t.Tag(ctx, text)
}

// Size returns the number of slots in the pool.
func (p *Pool) Size() int {
	if p == nil {
		return 0
	}
	return cap(p.idle)
}
