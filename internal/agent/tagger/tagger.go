package tagger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"clinic-backend/internal/agent"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const tagInputPrefix = "Text to tag:\n"

// Tagger selects the best-matching tag for a text using one stateful agent
// session.
type Tagger struct {
	session *agent.Session
	tags    []Tag
}

// NewTagger builds a tagger whose session is preloaded with the tag set's
// system prompt. maxTokens is the context size at which the session should be
// reset before reuse.
func NewTagger(chat model.BaseChatModel, set TagSet, maxTokens int) (*Tagger, error) {
	normalized, systemPrompt, err := prepareTagSet(set)
	if err != nil {
		return nil, err
	}
	return newTagger(chat, normalized, systemPrompt, maxTokens)
}

// newTagger builds a tagger from an already prepared tag set and system prompt.
// It lets a pool render the prompt once and reuse it for every slot.
func newTagger(chat model.BaseChatModel, set TagSet, systemPrompt string, maxTokens int) (*Tagger, error) {
	session, err := agent.NewSession(chat, systemPrompt, maxTokens)
	if err != nil {
		return nil, err
	}
	return &Tagger{session: session, tags: append([]Tag(nil), set.Tags...)}, nil
}

// Tag appends text to the session and returns the best-matching tag. A Result
// with an empty Name means no tag applies.
func (t *Tagger) Tag(ctx context.Context, text string) (Result, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, errors.New("tagger: text is required")
	}

	var result Result
	_, err := t.session.Chat(ctx, tagInputPrefix+text, func(resp *schema.Message) (string, error) {
		parsed, err := parseResult(resp.Content)
		if err != nil {
			return "", fmt.Errorf("tagger: parse model output: %w", err)
		}
		parsed, err = normalizeResult(parsed, t.tags)
		if err != nil {
			return "", fmt.Errorf("tagger: normalize model output: %w", err)
		}
		canonical, err := json.Marshal(parsed)
		if err != nil {
			return "", fmt.Errorf("tagger: marshal result: %w", err)
		}
		result = parsed
		return string(canonical), nil
	}, model.WithTemperature(0))
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Reset discards the accumulated context and reloads the system prompt.
func (t *Tagger) Reset() {
	t.session.Reset()
}

// IsExhausted reports whether the session should be reset before reuse.
func (t *Tagger) IsExhausted() bool {
	return t.session.IsExhausted()
}

// Tokens returns the last known context size.
func (t *Tagger) Tokens() int {
	return t.session.Tokens()
}

// CachedTokens returns the provider-reported cached prompt tokens from the
// last call.
func (t *Tagger) CachedTokens() int {
	return t.session.CachedTokens()
}
