package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// FinalizeFunc converts a model response into the assistant message stored in
// the session. Returning an error rolls the turn back. A nil finalizer stores
// the response content unchanged.
type FinalizeFunc func(*schema.Message) (string, error)

// Session is one long-lived chat model plus the conversation it has built so
// far. A session is used by one caller at a time and retains its context across
// rounds until it approaches the token limit.
type Session struct {
	chat         model.BaseChatModel
	systemPrompt string
	messages     []*schema.Message
	tokens       int
	maxTokens    int
	exhausted    bool
	cachedTokens int
}

// NewSession creates a session whose first message is the given system prompt.
func NewSession(chat model.BaseChatModel, systemPrompt string, maxTokens int) (*Session, error) {
	if chat == nil {
		return nil, errors.New("agent: chat model is required")
	}
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		return nil, errors.New("agent: system prompt is required")
	}
	if maxTokens <= 0 {
		return nil, errors.New("agent: token limit must be positive")
	}

	s := &Session{
		chat:         chat,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
	}
	s.Reset()
	return s, nil
}

// Reset discards all accumulated context and reloads the system prompt.
func (s *Session) Reset() {
	s.messages = []*schema.Message{schema.SystemMessage(s.systemPrompt)}
	s.tokens = 0
	s.exhausted = false
	s.cachedTokens = 0
}

// IsExhausted reports whether the session should be reset before reuse.
func (s *Session) IsExhausted() bool {
	return s.exhausted || s.tokens >= s.maxTokens
}

// Tokens returns the last known context size.
func (s *Session) Tokens() int {
	return s.tokens
}

// CachedTokens returns the provider-reported cached prompt tokens from the last
// call.
func (s *Session) CachedTokens() int {
	return s.cachedTokens
}

// Chat appends user, calls the model, passes the response to finalize, and
// appends the resulting assistant content. A failed turn is rolled back.
func (s *Session) Chat(ctx context.Context, user string, finalize FinalizeFunc, opts ...model.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		return "", errors.New("agent: user message is required")
	}

	base := len(s.messages)
	s.messages = append(s.messages, schema.UserMessage(user))

	input := make([]*schema.Message, len(s.messages))
	copy(input, s.messages)

	resp, err := s.chat.Generate(ctx, input, opts...)
	if err != nil {
		s.messages = s.messages[:base]
		if isContextLimitError(err) {
			s.exhausted = true
		}
		return "", fmt.Errorf("agent: generate: %w", err)
	}
	if resp == nil {
		s.messages = s.messages[:base]
		return "", errors.New("agent: model returned no message")
	}
	if resp.ResponseMeta != nil && resp.ResponseMeta.FinishReason == "length" {
		s.exhausted = true
	}

	assistant := resp.Content
	if finalize != nil {
		assistant, err = finalize(resp)
		if err != nil {
			s.messages = s.messages[:base]
			return "", fmt.Errorf("agent: finalize response: %w", err)
		}
	}
	s.messages = append(s.messages, schema.AssistantMessage(assistant, nil))

	s.tokens, s.cachedTokens = responseUsage(resp, s.messages)
	if s.tokens >= s.maxTokens {
		s.exhausted = true
	}
	return assistant, nil
}

// responseUsage returns the provider-reported context size and cached tokens,
// falling back to a conservative estimate when usage is unavailable.
func responseUsage(resp *schema.Message, messages []*schema.Message) (tokens, cached int) {
	if resp != nil && resp.ResponseMeta != nil && resp.ResponseMeta.Usage != nil {
		usage := resp.ResponseMeta.Usage
		tokens = usage.TotalTokens
		if tokens == 0 {
			tokens = usage.PromptTokens + usage.CompletionTokens
		}
		if tokens > 0 {
			return tokens, usage.PromptTokenDetails.CachedTokens
		}
	}
	return estimateTokens(messages), 0
}

// estimateTokens overestimates rather than undercounts so a session is reset
// before the provider can truncate the system prompt.
func estimateTokens(messages []*schema.Message) int {
	total := 0
	for _, message := range messages {
		if message == nil {
			continue
		}
		total += utf8.RuneCountInString(message.Content) + 8
	}
	return total
}

// isContextLimitError reports whether err looks like a provider context-window
// failure. Such a session must be reset before it is reused.
func isContextLimitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"context length",
		"context window",
		"maximum context",
		"max context",
		"too many tokens",
		"token limit",
		"maximum number of tokens",
		"prompt is too long",
		"input is too long",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
