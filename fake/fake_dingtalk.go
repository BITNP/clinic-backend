//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultAddr = "127.0.0.1:9998"

	tokenPath  = "/prod-api/auth/open/platform/getToken"
	sendPath   = "/prod-api/msg-center/api/send/msg"
	statusPath = "/prod-api/msg-center/api/get/result"

	fakeAccessToken    = "fake-dingtalk-access-token"
	fakeTokenExpiresIn = 120
	maxRequestBody     = 1 << 20
)

type apiEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg,omitempty"`
	Data any    `json:"data,omitempty"`
}

type sentMessage struct {
	id        string
	receivers []string
}

type sendPayload struct {
	Receivers   string            `json:"receivers"`
	MessageList []json.RawMessage `json:"messageList"`
}

type fakeServer struct {
	logger *log.Logger

	mu            sync.Mutex
	nextMessageID int
	messages      map[string]sentMessage
}

func main() {
	addr := strings.TrimSpace(os.Getenv("FAKE_DINGTALK_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}

	logger := log.New(os.Stdout, "fake-dingtalk: ", log.LstdFlags|log.Lmicroseconds)
	server := &fakeServer{
		logger:        logger,
		nextMessageID: 1,
		messages:      make(map[string]sentMessage),
	}

	mux := http.NewServeMux()
	mux.Handle(tokenPath, server.logged(server.handleToken))
	mux.Handle(sendPath, server.logged(server.handleSend))
	mux.Handle(statusPath, server.logged(server.handleStatus))
	mux.Handle("/", server.logged(server.handleNotFound))

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Printf("listening on http://%s", addr)
	logger.Printf("token=%s send=%s status=%s", tokenPath, sendPath, statusPath)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "fake dingtalk: %v\n", err)
		os.Exit(1)
	}
}

func (s *fakeServer) logged(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
		if err != nil {
			s.writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			s.logRequest(r, nil)
			s.logger.Printf("request body rejected: %v", err)
			return
		}

		s.logRequest(r, body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r)
	}
}

func (s *fakeServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}

	var credentials struct {
		AppKey    string `json:"appKey"`
		AppSecret string `json:"appSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid token request")
		return
	}
	if strings.TrimSpace(credentials.AppKey) == "" || strings.TrimSpace(credentials.AppSecret) == "" {
		s.writeError(w, http.StatusUnauthorized, "appKey and appSecret are required")
		return
	}

	s.writeJSON(w, http.StatusOK, apiEnvelope{
		Code: http.StatusOK,
		Data: map[string]any{
			"access_token": fakeAccessToken,
			"expires_in":   fakeTokenExpiresIn,
		},
	})
}

func (s *fakeServer) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.authorized(r) {
		s.writeError(w, http.StatusUnauthorized, "missing DingTalk access token or app code")
		return
	}

	var payload sendPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid send request")
		return
	}
	receivers := splitReceivers(payload.Receivers)
	if len(receivers) == 0 || len(payload.MessageList) == 0 {
		s.writeError(w, http.StatusBadRequest, "receivers and messageList are required")
		return
	}

	message := s.storeMessage(sentMessage{receivers: receivers})
	s.writeJSON(w, http.StatusOK, apiEnvelope{
		Code: http.StatusOK,
		Msg:  "发送成功",
		Data: map[string]string{"msgId": message.id},
	})
}

func (s *fakeServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	if !s.authorized(r) {
		s.writeError(w, http.StatusUnauthorized, "missing DingTalk access token or app code")
		return
	}

	messageID := strings.TrimSpace(r.URL.Query().Get("msgId"))
	if messageID == "" {
		s.writeError(w, http.StatusBadRequest, "msgId is required")
		return
	}

	message, ok := s.message(messageID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "message not found")
		return
	}

	s.writeJSON(w, http.StatusOK, apiEnvelope{
		Code: http.StatusOK,
		Msg:  "操作成功",
		Data: map[string]any{
			"msgId":     message.id,
			"status":    "send_mq",
			"statusMsg": "发送到MQ",
			"detail": []map[string]any{{
				"msgDetailId":       "fake-detail-" + message.id,
				"mqMsgId":           "fake-mq-" + message.id,
				"platform":          "dingTalk",
				"status":            "send_success",
				"statusMsg":         "发送成功",
				"errorMsg":          "",
				"sendCount":         len(message.receivers),
				"sendSuccessCount":  len(message.receivers),
				"sendFailedCount":   0,
				"noReceiptCount":    0,
				"unauthorizedCount": 0,
			}},
		},
	})
}

func (s *fakeServer) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, http.StatusNotFound, "endpoint not found")
}

func (s *fakeServer) authorized(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("open-access-token")) != "" &&
		strings.TrimSpace(r.Header.Get("msg-center-app-code")) != ""
}

func (s *fakeServer) storeMessage(message sentMessage) sentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	message.id = fmt.Sprintf("fake-msg-%d", s.nextMessageID)
	s.nextMessageID++
	s.messages[message.id] = message
	return message
}

func (s *fakeServer) message(messageID string) (sentMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	return message, ok
}

func (s *fakeServer) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *fakeServer) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, apiEnvelope{
		Code: status,
		Msg:  message,
	})
}

func (s *fakeServer) writeJSON(w http.ResponseWriter, status int, payload apiEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.logger.Printf("write response: %v", err)
	}
}

func (s *fakeServer) logRequest(r *http.Request, body []byte) {
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	query := formatQuery(r.URL.Query())
	if query == "" {
		query = "<none>"
	}
	s.logger.Printf(
		"received %s %s\nquery: %s\nheaders:\n%s\nbody:\n%s",
		r.Method,
		path,
		query,
		formatHeaders(r.Header),
		formatBody(body),
	)
}

func formatQuery(values url.Values) string {
	redacted := make(url.Values, len(values))
	for key, entries := range values {
		if isSensitiveField(key) {
			redacted[key] = []string{"<redacted>"}
			continue
		}
		redacted[key] = entries
	}
	return redacted.Encode()
}

func formatHeaders(header http.Header) string {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var lines []string
	for _, key := range keys {
		values := header.Values(key)
		if isSensitiveField(key) {
			values = []string{"<redacted>"}
		}
		for _, value := range values {
			lines = append(lines, fmt.Sprintf("  %s: %s", key, value))
		}
	}
	if len(lines) == 0 {
		return "  <none>"
	}
	return strings.Join(lines, "\n")
}

func formatBody(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return "<empty>"
	}

	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return fmt.Sprintf("<non-JSON body omitted: %d bytes>", len(body))
	}
	redactJSON(value)

	var formatted bytes.Buffer
	encoder := json.NewEncoder(&formatted)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprintf("<unprintable JSON body omitted: %d bytes>", len(body))
	}
	return strings.TrimSpace(formatted.String())
}

func redactJSON(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveField(key) {
				typed[key] = "<redacted>"
				continue
			}
			redactJSON(child)
		}
	case []any:
		for _, child := range typed {
			redactJSON(child)
		}
	}
}

func isSensitiveField(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.NewReplacer("-", "", "_", "", " ", "").Replace(normalized)
	switch normalized {
	case "appsecret", "accesstoken", "authorization", "cookie", "setcookie", "openaccesstoken", "apikey":
		return true
	default:
		return false
	}
}

func splitReceivers(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，'
	})
	receivers := make([]string, 0, len(parts))
	for _, part := range parts {
		if receiver := strings.TrimSpace(part); receiver != "" {
			receivers = append(receivers, receiver)
		}
	}
	return receivers
}
