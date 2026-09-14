package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clinic-backend/services"
)

const taggerMaxResponseBytes = 1 << 20

// taggerTagPayload is the wire representation of one tag definition.
type taggerTagPayload struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ApplyRule   string `json:"apply_rule"`
}

type registerTagSetPayload struct {
	Name   string             `json:"name"`
	Prompt string             `json:"prompt"`
	Tags   []taggerTagPayload `json:"tags"`
}

type tagPayload struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type tagResponse struct {
	Name string `json:"name"`
	Tag  *struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"tag"`
}

// taggerHTTPClient is a small standard-library client for the tagger API.
type taggerHTTPClient struct {
	baseURL  string
	apiToken string
	client   *http.Client
}

// NewTaggerHTTPClient creates a tagger client targeting the given base URL.
// timeout bounds each request; apiToken is sent as a bearer token.
func NewTaggerHTTPClient(baseURL, apiToken string, timeout time.Duration) services.TaggerClient {
	return &taggerHTTPClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
		client:   &http.Client{Timeout: timeout},
	}
}

// RegisterTagSet registers (or replaces) a named tag set.
func (c *taggerHTTPClient) RegisterTagSet(ctx context.Context, name, prompt string, tags []services.TaggerTag) error {
	body := registerTagSetPayload{Name: name, Prompt: prompt, Tags: make([]taggerTagPayload, 0, len(tags))}
	for _, t := range tags {
		body.Tags = append(body.Tags, taggerTagPayload{
			Name:        t.Name,
			Description: t.Description,
			ApplyRule:   t.ApplyRule,
		})
	}
	_, err := c.postJSON(ctx, "/api/v1/agents/tagger/tag-sets", body, nil)
	return err
}

// Tag returns the best-matching tag name for text, or an empty string when
// nothing applies. A 404 is reported as services.ErrTagSetNotFound so the
// caller can re-register the set.
func (c *taggerHTTPClient) Tag(ctx context.Context, name, text string) (string, error) {
	var resp tagResponse
	status, err := c.postJSON(ctx, "/api/v1/agents/tagger/tag", tagPayload{Name: name, Text: text}, &resp)
	if status == http.StatusNotFound {
		return "", services.ErrTagSetNotFound
	}
	if err != nil {
		return "", err
	}
	if resp.Tag == nil {
		return "", nil
	}
	return resp.Tag.Name, nil
}

// postJSON sends a JSON POST with bearer auth and returns the HTTP status. The
// response body is decoded into out when out is non-nil.
func (c *taggerHTTPClient) postJSON(ctx context.Context, path string, payload, out any) (int, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return 0, fmt.Errorf("tagger: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &buf)
	if err != nil {
		return 0, fmt.Errorf("tagger: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tagger: request %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, taggerMaxResponseBytes))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("tagger: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("tagger: %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("tagger: decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}
