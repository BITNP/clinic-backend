package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// maxIdleConnsPerHost keeps one warm connection per slot model.
const maxIdleConnsPerHost = 2

// ChatModelConfig describes one OpenAI-compatible Eino chat model.
type ChatModelConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
}

func (c ChatModelConfig) validate() error {
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("agent: model API key is required")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("agent: model base URL is required")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("agent: model name is required")
	}
	if c.Timeout < 0 {
		return fmt.Errorf("agent: model timeout must not be negative")
	}
	return nil
}

// NewChatModel builds one Eino OpenAI-compatible chat model with a dedicated
// HTTP client. Each pool slot owns one model so requests never share a
// conversation or a connection pool.
func NewChatModel(ctx context.Context, cfg ChatModelConfig) (model.BaseChatModel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	transport.MaxIdleConns = maxIdleConnsPerHost
	transport.MaxIdleConnsPerHost = maxIdleConnsPerHost

	client := &http.Client{Transport: transport}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}

	chat, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:     strings.TrimSpace(cfg.APIKey),
		BaseURL:    strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		Model:      strings.TrimSpace(cfg.Model),
		HTTPClient: client,
		ResponseFormat: &einoopenai.ChatCompletionResponseFormat{
			Type: einoopenai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("agent: create chat model: %w", err)
	}
	return chat, nil
}

// ChatModelFactory creates one chat model. The pool calls it once per slot.
type ChatModelFactory func() (model.BaseChatModel, error)

// NewChatModelFactory returns a factory that builds models from cfg.
func NewChatModelFactory(ctx context.Context, cfg ChatModelConfig) ChatModelFactory {
	return func() (model.BaseChatModel, error) {
		return NewChatModel(ctx, cfg)
	}
}
