package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	notificationclient "clinic-backend/internal/notification/client"
)

func validDingTalkConfig(baseURL string) notificationclient.Config {
	return notificationclient.Config{
		BaseURL:   baseURL,
		AppCode:   "clinic",
		AppKey:    "app-key",
		AppSecret: "app-secret",
		Timeout:   time.Second,
	}
}

func TestDingTalkClientSendAndStatus(t *testing.T) {
	tokenCalls := 0
	sendCalls := 0
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prod-api/auth/open/platform/getToken":
			tokenCalls++
			if r.Method != http.MethodGet {
				t.Errorf("token method = %s, want GET", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("token content type = %q, want application/json", got)
			}
			var credentials struct {
				AppKey    string `json:"appKey"`
				AppSecret string `json:"appSecret"`
			}
			if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
				t.Errorf("decode token request: %v", err)
			}
			if credentials.AppKey != "app-key" || credentials.AppSecret != "app-secret" {
				t.Errorf("token credentials = %+v", credentials)
			}
			fmt.Fprint(w, `{"code":200,"msg":null,"data":{"access_token":"token-1","expires_in":10}}`)
		case "/prod-api/msg-center/api/send/msg":
			sendCalls++
			if r.Method != http.MethodPost {
				t.Errorf("send method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("open-access-token"); got != "token-1" {
				t.Errorf("open-access-token = %q, want token-1", got)
			}
			if got := r.Header.Get("msg-center-app-code"); got != "clinic" {
				t.Errorf("msg-center-app-code = %q, want clinic", got)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("send content type = %q, want application/json", got)
			}
			var payload struct {
				Receivers   string `json:"receivers"`
				Level       string `json:"level"`
				MessageList []struct {
					Platform  string `json:"platform"`
					Title     string `json:"title"`
					Content   string `json:"content"`
					MsgType   string `json:"msgType"`
					PCURL     string `json:"pcUrl"`
					MobileURL string `json:"mobileUrl"`
					PicID     string `json:"picId"`
					AgentID   string `json:"agentId"`
				} `json:"messageList"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode send request: %v", err)
			}
			if payload.Receivers != "2026001,2026002" || payload.Level != "high" {
				t.Errorf("send payload = %+v", payload)
			}
			if len(payload.MessageList) != 1 {
				t.Errorf("message list = %+v", payload.MessageList)
				http.Error(w, "invalid message list", http.StatusBadRequest)
				return
			}
			message := payload.MessageList[0]
			if message.Platform != "dingTalk" || message.Title != "值班提醒" || message.Content != "请及时审核工单" || message.MsgType != "link" {
				t.Errorf("message = %+v", message)
			}
			if message.PCURL != "https://clinic.example/manage" || message.MobileURL != "https://clinic.example/m" || message.PicID != "pic-1" || message.AgentID != "agent-1" {
				t.Errorf("message links = %+v", message)
			}
			fmt.Fprint(w, `{"code":200,"msg":"发送成功","data":{"msgId":"message-1"}}`)
		case "/prod-api/msg-center/api/get/result":
			statusCalls++
			if r.Method != http.MethodGet {
				t.Errorf("status method = %s, want GET", r.Method)
			}
			if got := r.URL.Query().Get("msgId"); got != "message-1" {
				t.Errorf("msgId = %q, want message-1", got)
			}
			if got := r.Header.Get("open-access-token"); got != "token-1" {
				t.Errorf("status token = %q, want token-1", got)
			}
			fmt.Fprint(w, `{
				"code":200,
				"msg":"操作成功",
				"data":{
					"msgId":"message-1",
					"status":"send_mq",
					"statusMsg":"发送到MQ",
					"detail":[{
						"msgDetailId":"detail-1",
						"mqMsgId":"mq-1",
						"platform":"dingTalk",
						"status":"send_success",
						"statusMsg":"发送成功",
						"errorMsg":"",
						"sendCount":2,
						"sendSuccessCount":1,
						"sendFailedCount":0,
						"noReceiptCount":1,
						"unauthorizedCount":0
					}]
				}
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := notificationclient.NewDingTalkClient(validDingTalkConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	sent, err := client.Send(context.Background(), notificationclient.SendRequest{
		Receivers:   " 2026001，2026002 ",
		Title:       "值班提醒",
		Content:     "请及时审核工单",
		MessageType: "link",
		Level:       "HIGH",
		PCURL:       "https://clinic.example/manage",
		MobileURL:   "https://clinic.example/m",
		PicID:       "pic-1",
		AgentID:     "agent-1",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.MsgID != "message-1" {
		t.Errorf("MsgID = %q, want message-1", sent.MsgID)
	}

	status, err := client.Status(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.MsgID != "message-1" || status.Status != "send_mq" || len(status.Detail) != 1 {
		t.Fatalf("status = %+v", status)
	}
	detail := status.Detail[0]
	if detail.Platform != "dingTalk" || detail.Status != "send_success" || detail.SendSuccessCount != 1 || detail.NoReceiptCount != 1 {
		t.Errorf("detail = %+v", detail)
	}
	if tokenCalls != 1 || sendCalls != 1 || statusCalls != 1 {
		t.Errorf("calls token=%d send=%d status=%d", tokenCalls, sendCalls, statusCalls)
	}
}

func TestDingTalkClientSendAndCheckStatus(t *testing.T) {
	tests := []struct {
		name       string
		statusData string
		wantErr    string
	}{
		{
			name: "successful delivery",
			statusData: `{
				"msgId":"message-1",
				"status":"send_mq",
				"detail":[{
					"platform":"dingTalk",
					"status":"send_success",
					"sendSuccessCount":1,
					"sendFailedCount":0,
					"noReceiptCount":0,
					"unauthorizedCount":0
				}]
			}`,
		},
		{
			name: "failed delivery",
			statusData: `{
				"msgId":"message-1",
				"status":"send_mq",
				"detail":[{
					"platform":"dingTalk",
					"status":"send_failed",
					"statusMsg":"发送失败",
					"errorMsg":"receiver not found",
					"sendSuccessCount":0,
					"sendFailedCount":1,
					"unauthorizedCount":0
				}]
			}`,
			wantErr: "status \"send_failed\"",
		},
		{
			name: "unauthorized receiver",
			statusData: `{
				"msgId":"message-1",
				"status":"send_mq",
				"detail":[{
					"platform":"dingTalk",
					"status":"send_success",
					"sendSuccessCount":0,
					"sendFailedCount":0,
					"unauthorizedCount":1
				}]
			}`,
			wantErr: "unauthorized 1",
		},
		{
			name:       "missing dingtalk detail",
			statusData: `{"msgId":"message-1","status":"send_mq","detail":[]}`,
			wantErr:    "missing dingTalk detail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/prod-api/auth/open/platform/getToken":
					fmt.Fprint(w, `{"code":200,"data":{"access_token":"token-1","expires_in":10}}`)
				case "/prod-api/msg-center/api/send/msg":
					fmt.Fprint(w, `{"code":200,"data":{"msgId":"message-1"}}`)
				case "/prod-api/msg-center/api/get/result":
					fmt.Fprintf(w, `{"code":200,"data":%s}`, tt.statusData)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client, err := notificationclient.NewDingTalkClient(validDingTalkConfig(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			sent, status, err := client.SendAndCheckStatus(context.Background(), notificationclient.SendRequest{
				Receivers: "2026001",
				Title:     "提醒",
				Content:   "审核工单",
			})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("SendAndCheckStatus() error = %v", err)
				}
				if sent.MsgID != "message-1" || status.MsgID != "message-1" {
					t.Fatalf("sent = %+v, status = %+v", sent, status)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDingTalkClientOverridesReceiver(t *testing.T) {
	var gotReceivers string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prod-api/auth/open/platform/getToken":
			fmt.Fprint(w, `{"code":200,"data":{"access_token":"token-1","expires_in":10}}`)
		case "/prod-api/msg-center/api/send/msg":
			var payload struct {
				Receivers string `json:"receivers"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode send request: %v", err)
			}
			gotReceivers = payload.Receivers
			fmt.Fprint(w, `{"code":200,"data":{"msgId":"message-1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := validDingTalkConfig(server.URL)
	cfg.ReceiverOverride = " dev-user "
	client, err := notificationclient.NewDingTalkClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), notificationclient.SendRequest{
		Receivers: "real-user-1,real-user-2",
		Title:     "提醒",
		Content:   "审核工单",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotReceivers != "dev-user" {
		t.Errorf("receivers = %q, want dev-user", gotReceivers)
	}
}

func TestDingTalkClientRefreshesTokenAtEightyPercent(t *testing.T) {
	start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	now := start
	var tokenCalls atomic.Int32
	var mu sync.Mutex
	var usedTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prod-api/auth/open/platform/getToken":
			call := tokenCalls.Add(1)
			fmt.Fprintf(w, `{"code":200,"data":{"access_token":"token-%d","expires_in":10}}`, call)
		case "/prod-api/msg-center/api/send/msg":
			mu.Lock()
			usedTokens = append(usedTokens, r.Header.Get("open-access-token"))
			mu.Unlock()
			fmt.Fprint(w, `{"code":200,"data":{"msgId":"message-1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := validDingTalkConfig(server.URL)
	cfg.Now = func() time.Time { return now }
	client, err := notificationclient.NewDingTalkClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	send := func() {
		t.Helper()
		if _, err := client.Send(context.Background(), notificationclient.SendRequest{
			Receivers: "2026001",
			Title:     "提醒",
			Content:   "审核工单",
		}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}

	send()
	now = start.Add(8*time.Minute - time.Nanosecond)
	send()
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token calls before 80%% = %d, want 1", got)
	}

	now = start.Add(8 * time.Minute)
	send()
	if got := tokenCalls.Load(); got != 2 {
		t.Fatalf("token calls at 80%% = %d, want 2", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"token-1", "token-1", "token-2"}
	if fmt.Sprint(usedTokens) != fmt.Sprint(want) {
		t.Errorf("used tokens = %v, want %v", usedTokens, want)
	}
}

func TestDingTalkClientSerializesInitialTokenAcquisition(t *testing.T) {
	var tokenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prod-api/auth/open/platform/getToken":
			call := tokenCalls.Add(1)
			time.Sleep(20 * time.Millisecond)
			fmt.Fprintf(w, `{"code":200,"data":{"access_token":"token-%d","expires_in":10}}`, call)
		case "/prod-api/msg-center/api/send/msg":
			fmt.Fprint(w, `{"code":200,"data":{"msgId":"message-1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := notificationclient.NewDingTalkClient(validDingTalkConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 12
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := client.Send(context.Background(), notificationclient.SendRequest{
				Receivers: "2026001",
				Title:     "提醒",
				Content:   "审核工单",
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Send() error = %v", err)
		}
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Errorf("token calls = %d, want 1", got)
	}
}

func TestDingTalkClientReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prod-api/auth/open/platform/getToken":
			fmt.Fprint(w, `{"code":200,"data":{"access_token":"token-1","expires_in":10}}`)
		case "/prod-api/msg-center/api/send/msg":
			fmt.Fprint(w, `{"code":6001000,"msg":"授权应用标识不能为空"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := notificationclient.NewDingTalkClient(validDingTalkConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), notificationclient.SendRequest{
		Receivers: "2026001",
		Title:     "提醒",
		Content:   "审核工单",
	})
	var apiErr *notificationclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError", err)
	}
	if apiErr.Code != 6001000 || apiErr.Message != "授权应用标识不能为空" {
		t.Errorf("APIError = %+v", apiErr)
	}
	if strings.Contains(err.Error(), "app-secret") {
		t.Errorf("error exposed app secret: %v", err)
	}
}

func TestDingTalkClientValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*notificationclient.Config)
		want   string
	}{
		{"base URL", func(cfg *notificationclient.Config) { cfg.BaseURL = "" }, "base URL is required"},
		{"base URL scheme", func(cfg *notificationclient.Config) { cfg.BaseURL = "message-center:9000" }, "must use http or https"},
		{"base URL query", func(cfg *notificationclient.Config) { cfg.BaseURL = "https://example.com?token=x" }, "must not contain a query"},
		{"app code", func(cfg *notificationclient.Config) { cfg.AppCode = "" }, "app code is required"},
		{"app key", func(cfg *notificationclient.Config) { cfg.AppKey = "" }, "app key is required"},
		{"app secret", func(cfg *notificationclient.Config) { cfg.AppSecret = "" }, "app secret is required"},
		{"receiver override", func(cfg *notificationclient.Config) { cfg.ReceiverOverride = "first,second" }, "exactly one receiver is required"},
		{"timeout", func(cfg *notificationclient.Config) { cfg.Timeout = -time.Second }, "timeout must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDingTalkConfig("https://message-center.example")
			tt.mutate(&cfg)
			client, err := notificationclient.NewDingTalkClient(cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if client != nil {
				t.Fatal("client = non-nil, want nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestDingTalkClientValidatesRequestBeforeNetwork(t *testing.T) {
	client, err := notificationclient.NewDingTalkClient(validDingTalkConfig("https://message-center.example"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		request notificationclient.SendRequest
		want    string
	}{
		{
			name:    "missing receiver",
			request: notificationclient.SendRequest{Title: "提醒", Content: "审核工单"},
			want:    "receiver is required",
		},
		{
			name:    "missing title",
			request: notificationclient.SendRequest{Receivers: "2026001", Content: "审核工单"},
			want:    "title is required",
		},
		{
			name:    "missing content",
			request: notificationclient.SendRequest{Receivers: "2026001", Title: "提醒"},
			want:    "content is required",
		},
		{
			name:    "invalid message type",
			request: notificationclient.SendRequest{Receivers: "2026001", Title: "提醒", Content: "内容", MessageType: "card"},
			want:    "message type must be text or link",
		},
		{
			name:    "link mobile URL",
			request: notificationclient.SendRequest{Receivers: "2026001", Title: "提醒", Content: "内容", MessageType: "link", PicID: "pic"},
			want:    "mobile URL is required",
		},
		{
			name:    "link picture",
			request: notificationclient.SendRequest{Receivers: "2026001", Title: "提醒", Content: "内容", MessageType: "link", MobileURL: "https://example.com"},
			want:    "picture id is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Send(context.Background(), tt.request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
