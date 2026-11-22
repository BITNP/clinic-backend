package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	configservice "clinic-backend/internal/config/service"
	notificationclient "clinic-backend/internal/notification/client"
	recordservice "clinic-backend/internal/record/service"
)

type dingTalkRuntimeState struct {
	config   DingTalkConfig
	notifier *recordservice.DingTalkTicketNotifier
}

type dingTalkSenderFactory func(notificationclient.Config) (recordservice.DingTalkStatusSender, error)

type dingTalkRuntimeManager struct {
	location      *time.Location
	senderFactory dingTalkSenderFactory

	mu      sync.Mutex
	active  atomic.Pointer[dingTalkRuntimeState]
	pending *dingTalkRuntimeState
}

func newDingTalkRuntimeManager(location *time.Location) *dingTalkRuntimeManager {
	if location == nil {
		location = time.Local
	}
	return &dingTalkRuntimeManager{
		location: location,
		senderFactory: func(cfg notificationclient.Config) (recordservice.DingTalkStatusSender, error) {
			return notificationclient.NewDingTalkClient(cfg)
		},
	}
}

func (m *dingTalkRuntimeManager) Initialize(settings configservice.RuntimeSettings) error {
	cfg, err := LoadDingTalkConfigFromRuntime(settings)
	if err != nil {
		return err
	}
	state, err := m.build(cfg)
	if err != nil {
		return err
	}
	m.active.Store(state)
	logDingTalkRuntime(cfg)
	return nil
}

// Prepare validates and builds a candidate notifier before ConfigService
// commits an updated runtime configuration.
func (m *dingTalkRuntimeManager) Prepare(settings configservice.RuntimeSettings) error {
	cfg, err := LoadDingTalkConfigFromRuntime(settings)
	if err != nil {
		return err
	}
	state, err := m.build(cfg)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.active.Load(); current != nil && current.config == cfg {
		m.pending = nil
		return nil
	}
	m.pending = state
	return nil
}

// Activate publishes the candidate prepared for the successfully committed
// settings.
func (m *dingTalkRuntimeManager) Activate(settings configservice.RuntimeSettings) {
	cfg, err := LoadDingTalkConfigFromRuntime(settings)
	if err != nil {
		log.Printf("dingtalk runtime: skipped activation of invalid settings: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil || m.pending.config != cfg {
		return
	}
	m.active.Store(m.pending)
	logDingTalkRuntime(cfg)
	m.pending = nil
}

func (m *dingTalkRuntimeManager) Config() DingTalkConfig {
	state := m.active.Load()
	if state == nil {
		return DingTalkConfig{}
	}
	return state.config
}

func (m *dingTalkRuntimeManager) Notify(ctx context.Context, notification recordservice.TicketStatusNotification) error {
	state := m.active.Load()
	if state == nil || !state.config.Enabled {
		return nil
	}
	return state.notifier.Notify(ctx, notification)
}

func (m *dingTalkRuntimeManager) NotifyReminder(ctx context.Context, notification recordservice.TicketStatusNotification) error {
	state := m.active.Load()
	if state == nil || !state.config.Enabled {
		return fmt.Errorf("dingtalk reminder delivery is disabled")
	}
	return state.notifier.NotifyReminder(ctx, notification)
}

func (m *dingTalkRuntimeManager) build(cfg DingTalkConfig) (*dingTalkRuntimeState, error) {
	var sender recordservice.DingTalkStatusSender
	if cfg.Enabled {
		var err error
		sender, err = m.senderFactory(notificationclient.Config{
			BaseURL:          cfg.BaseURL,
			AppCode:          cfg.AppCode,
			AppKey:           cfg.AppKey,
			AppSecret:        cfg.AppSecret,
			Timeout:          cfg.Timeout,
			ReceiverOverride: dingTalkReceiverOverride(cfg),
		})
		if err != nil {
			return nil, err
		}
	}
	notifier, err := recordservice.NewDingTalkTicketNotifier(
		sender,
		m.location,
		dingTalkTicketNotifierConfig(cfg),
	)
	if err != nil {
		return nil, err
	}
	return &dingTalkRuntimeState{config: cfg, notifier: notifier}, nil
}

func logDingTalkRuntime(cfg DingTalkConfig) {
	if !cfg.Enabled {
		log.Println("dingtalk ticket notifications disabled")
		return
	}
	if cfg.RedirectEnabled {
		log.Println("dingtalk notifications enabled with receiver redirection")
		return
	}
	log.Println("dingtalk ticket notifications enabled")
}
