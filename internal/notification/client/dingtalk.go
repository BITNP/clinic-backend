// Package client integrates with outbound notification providers.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	tokenPath  = "/prod-api/auth/open/platform/getToken"
	sendPath   = "/prod-api/msg-center/api/send/msg"
	statusPath = "/prod-api/msg-center/api/get/result"

	dingTalkPlatform = "dingTalk"
	maxResponseBytes = 1 << 20
)

// Config describes the message-center endpoint and credentials used for
// DingTalk notifications.
type Config struct {
	BaseURL          string
	AppCode          string
	AppKey           string
	AppSecret        string
	Timeout          time.Duration
	ReceiverOverride string
	HTTPClient       *http.Client
	Now              func() time.Time
}

// SendRequest describes a DingTalk message submitted through the message
// center.
type SendRequest struct {
	Receivers   string
	Title       string
	Content     string
	MessageType string
	Level       string
	PCURL       string
	MobileURL   string
	PicID       string
	AgentID     string
}

// SendResult identifies a message accepted by the message center.
type SendResult struct {
	MsgID string `json:"msgId"`
}

// MessageStatus is the overall delivery state returned by the message center.
type MessageStatus struct {
	MsgID     string          `json:"msgId"`
	Status    string          `json:"status"`
	StatusMsg string          `json:"statusMsg"`
	Detail    []MessageDetail `json:"detail"`
}

// MessageDetail is the delivery state for one message-center platform.
type MessageDetail struct {
	MsgDetailID       string `json:"msgDetailId"`
	MQMsgID           string `json:"mqMsgId"`
	Platform          string `json:"platform"`
	Status            string `json:"status"`
	StatusMsg         string `json:"statusMsg"`
	ErrorMsg          string `json:"errorMsg"`
	SendCount         int    `json:"sendCount"`
	SendSuccessCount  int    `json:"sendSuccessCount"`
	SendFailedCount   int    `json:"sendFailedCount"`
	NoReceiptCount    int    `json:"noReceiptCount"`
	UnauthorizedCount int    `json:"unauthorizedCount"`
}

// APIError describes a rejected or malformed message-center response.
type APIError struct {
	StatusCode int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	parts := []string{"dingtalk message center"}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	if e.Code != 0 {
		parts = append(parts, fmt.Sprintf("code %d", e.Code))
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

// DingTalkClient sends messages and checks their state through the shared
// message center. It caches one access token and refreshes it after 80% of the
// advertised lifetime has elapsed.
type DingTalkClient struct {
	baseURL    string
	appCode    string
	appKey     string
	appSecret  string
	receiver   string
	httpClient *http.Client
	now        func() time.Time

	tokenMu   sync.Mutex
	token     string
	refreshAt time.Time
}

// NewDingTalkClient validates cfg and constructs a client without contacting
// the message center.
func NewDingTalkClient(cfg Config) (*DingTalkClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	appCode := strings.TrimSpace(cfg.AppCode)
	appKey := strings.TrimSpace(cfg.AppKey)
	appSecret := strings.TrimSpace(cfg.AppSecret)

	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}
	if appCode == "" {
		return nil, fmt.Errorf("dingtalk client: app code is required")
	}
	if appKey == "" {
		return nil, fmt.Errorf("dingtalk client: app key is required")
	}
	if appSecret == "" {
		return nil, fmt.Errorf("dingtalk client: app secret is required")
	}
	receiverOverride := strings.TrimSpace(cfg.ReceiverOverride)
	if receiverOverride != "" {
		normalized, err := normalizeReceiver(receiverOverride)
		if err != nil {
			return nil, fmt.Errorf("dingtalk client: invalid receiver override: %w", err)
		}
		receiverOverride = normalized
	}

	httpClient := cfg.HTTPClient
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("dingtalk client: timeout must not be negative")
	}
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &DingTalkClient{
		baseURL:    baseURL,
		appCode:    appCode,
		appKey:     appKey,
		appSecret:  appSecret,
		receiver:   receiverOverride,
		httpClient: httpClient,
		now:        now,
	}, nil
}

// Send submits one DingTalk text or link message and returns its message id.
func (c *DingTalkClient) Send(ctx context.Context, input SendRequest) (SendResult, error) {
	payload, err := c.sendPayload(input)
	if err != nil {
		return SendResult{}, err
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return SendResult{}, err
	}

	var result SendResult
	if err := c.do(ctx, http.MethodPost, sendPath, nil, token, payload, &result); err != nil {
		return SendResult{}, fmt.Errorf("send dingtalk message: %w", err)
	}
	if result.MsgID == "" {
		return SendResult{}, fmt.Errorf("send dingtalk message: response missing msgId")
	}
	return result, nil
}

// SendAndCheckStatus submits one DingTalk message, then verifies the message
// center's per-platform delivery result. A queued send response alone is not
// treated as successful delivery.
func (c *DingTalkClient) SendAndCheckStatus(ctx context.Context, input SendRequest) (SendResult, MessageStatus, error) {
	sent, err := c.Send(ctx, input)
	if err != nil {
		return SendResult{}, MessageStatus{}, err
	}

	status, err := c.Status(ctx, sent.MsgID)
	if err != nil {
		return sent, MessageStatus{}, fmt.Errorf("check dingtalk message %s delivery: %w", sent.MsgID, err)
	}
	if err := checkDingTalkDelivery(sent.MsgID, status); err != nil {
		return sent, status, err
	}
	return sent, status, nil
}

// Status returns the current state of a previously submitted message.
func (c *DingTalkClient) Status(ctx context.Context, msgID string) (MessageStatus, error) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return MessageStatus{}, fmt.Errorf("check dingtalk message status: msgId is required")
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return MessageStatus{}, err
	}

	query := url.Values{"msgId": []string{msgID}}
	var result MessageStatus
	if err := c.do(ctx, http.MethodGet, statusPath, query, token, nil, &result); err != nil {
		return MessageStatus{}, fmt.Errorf("check dingtalk message status: %w", err)
	}
	if result.MsgID == "" {
		return MessageStatus{}, fmt.Errorf("check dingtalk message status: response missing msgId")
	}
	return result, nil
}

func checkDingTalkDelivery(msgID string, status MessageStatus) error {
	if status.MsgID != msgID {
		return fmt.Errorf("check dingtalk message %s delivery: response msgId is %q", msgID, status.MsgID)
	}
	for _, detail := range status.Detail {
		if !strings.EqualFold(strings.TrimSpace(detail.Platform), dingTalkPlatform) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(detail.Status), "send_success") &&
			detail.SendSuccessCount > 0 &&
			detail.SendFailedCount == 0 &&
			detail.UnauthorizedCount == 0 {
			return nil
		}
		return fmt.Errorf(
			"check dingtalk message %s delivery: status %q, status message %q, error message %q, success %d, failed %d, unauthorized %d",
			msgID,
			detail.Status,
			detail.StatusMsg,
			detail.ErrorMsg,
			detail.SendSuccessCount,
			detail.SendFailedCount,
			detail.UnauthorizedCount,
		)
	}
	return fmt.Errorf("check dingtalk message %s delivery: response missing dingTalk detail", msgID)
}

func (c *DingTalkClient) sendPayload(input SendRequest) (any, error) {
	rawReceivers := input.Receivers
	if c.receiver != "" {
		rawReceivers = c.receiver
	}
	receivers, err := normalizeReceivers(rawReceivers)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("send dingtalk message: title is required")
	}
	if utf8.RuneCountInString(title) > 128 {
		return nil, fmt.Errorf("send dingtalk message: title must not exceed 128 characters")
	}
	if strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("send dingtalk message: content is required")
	}

	messageType := strings.ToLower(strings.TrimSpace(input.MessageType))
	if messageType == "" {
		messageType = "text"
	}
	if messageType != "text" && messageType != "link" {
		return nil, fmt.Errorf("send dingtalk message: message type must be text or link")
	}
	if messageType == "link" {
		if strings.TrimSpace(input.MobileURL) == "" {
			return nil, fmt.Errorf("send dingtalk message: mobile URL is required for link messages")
		}
		if strings.TrimSpace(input.PicID) == "" {
			return nil, fmt.Errorf("send dingtalk message: picture id is required for link messages")
		}
	}

	level := strings.ToLower(strings.TrimSpace(input.Level))
	switch level {
	case "", "low", "normal", "high", "critical":
	default:
		return nil, fmt.Errorf("send dingtalk message: level must be low, normal, high, or critical")
	}

	message := map[string]any{
		"platform": dingTalkPlatform,
		"title":    title,
		"content":  input.Content,
		"msgType":  messageType,
	}
	addOptional(message, "pcUrl", input.PCURL)
	addOptional(message, "mobileUrl", input.MobileURL)
	addOptional(message, "picId", input.PicID)
	addOptional(message, "agentId", input.AgentID)

	payload := map[string]any{
		"receivers":   receivers,
		"messageList": []any{message},
	}
	if level != "" {
		payload["level"] = level
	}
	return payload, nil
}

func (c *DingTalkClient) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && c.now().Before(c.refreshAt) {
		return c.token, nil
	}

	payload := struct {
		AppKey    string `json:"appKey"`
		AppSecret string `json:"appSecret"`
	}{
		AppKey:    c.appKey,
		AppSecret: c.appSecret,
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.do(ctx, http.MethodGet, tokenPath, nil, "", payload, &result); err != nil {
		return "", fmt.Errorf("get dingtalk access token: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("get dingtalk access token: response missing access_token")
	}
	refreshAfter, err := tokenRefreshAfter(result.ExpiresIn)
	if err != nil {
		return "", fmt.Errorf("get dingtalk access token: %w", err)
	}

	c.token = result.AccessToken
	c.refreshAt = c.now().Add(refreshAfter)
	return c.token, nil
}

func (c *DingTalkClient) do(ctx context.Context, method, path string, query url.Values, token string, body, out any) error {
	var encodedBody []byte
	var err error
	if body != nil {
		encodedBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}

	endpoint := c.baseURL + path
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encodedBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("open-access-token", token)
		req.Header.Set("msg-center-app-code", c.appCode)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}

	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || envelope.Code != http.StatusOK {
		return &APIError{StatusCode: resp.StatusCode, Code: envelope.Code, Message: strings.TrimSpace(envelope.Msg)}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("decode response: missing data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode response data: %w", err)
	}
	return nil
}

func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("dingtalk client: base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("dingtalk client: invalid base URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("dingtalk client: base URL must use http or https")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("dingtalk client: base URL must include a hostname")
	}
	if parsed.User != nil {
		return fmt.Errorf("dingtalk client: base URL must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("dingtalk client: base URL must not contain a query")
	}
	if parsed.Fragment != "" || strings.Contains(baseURL, "#") {
		return fmt.Errorf("dingtalk client: base URL must not contain a fragment")
	}
	return nil
}

func tokenRefreshAfter(expiresInMinutes int) (time.Duration, error) {
	if expiresInMinutes <= 0 {
		return 0, fmt.Errorf("expires_in must be positive")
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if int64(expiresInMinutes) > int64(maxDuration/time.Minute) {
		return 0, fmt.Errorf("expires_in is too large")
	}
	lifetime := time.Duration(expiresInMinutes) * time.Minute
	return lifetime - lifetime/5, nil
}

func normalizeReceivers(raw string) (string, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，'
	})
	receivers := make([]string, 0, len(parts))
	for _, part := range parts {
		if receiver := strings.TrimSpace(part); receiver != "" {
			receivers = append(receivers, receiver)
		}
	}
	if len(receivers) == 0 {
		return "", fmt.Errorf("send dingtalk message: at least one receiver is required")
	}
	return strings.Join(receivers, ","), nil
}

func normalizeReceiver(raw string) (string, error) {
	receivers, err := normalizeReceivers(raw)
	if err != nil {
		return "", err
	}
	if strings.Contains(receivers, ",") {
		return "", fmt.Errorf("send dingtalk message: exactly one receiver is required")
	}
	return receivers, nil
}

func addOptional(values map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values[key] = value
	}
}
