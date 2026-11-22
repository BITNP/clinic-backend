package service

import (
	"context"
	"log"
	"time"
)

const (
	reminderDiscoveryTimeout = 15 * time.Second
	reminderDeliveryTimeout  = 30 * time.Second
	reminderBatchSize        = 20
)

// TicketReminderWorkerConfig is the active reminder schedule read once per
// worker cycle.
type TicketReminderWorkerConfig struct {
	Enabled         bool
	LeadTime        time.Duration
	PollInterval    time.Duration
	DeliveryTimeout time.Duration
}

// TicketReminderWorker periodically discovers and delivers guest reminders.
type TicketReminderWorker struct {
	Discovery       *TicketReminderDiscovery
	Delivery        *TicketReminderDelivery
	Config          func() TicketReminderWorkerConfig
	PollInterval    time.Duration
	DeliveryTimeout time.Duration
	Now             func() time.Time
}

// Run processes reminders until ctx is canceled.
func (w *TicketReminderWorker) Run(ctx context.Context) {
	for {
		config := w.currentConfig()
		if config.Enabled {
			w.runOnce(ctx, config)
		}
		interval := config.PollInterval
		if interval <= 0 {
			interval = time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *TicketReminderWorker) currentConfig() TicketReminderWorkerConfig {
	if w.Config != nil {
		return w.Config()
	}
	return TicketReminderWorkerConfig{
		Enabled:         true,
		PollInterval:    w.PollInterval,
		DeliveryTimeout: w.DeliveryTimeout,
	}
}

func (w *TicketReminderWorker) runOnce(ctx context.Context, config TicketReminderWorkerConfig) {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}

	if config.LeadTime > 0 {
		w.Discovery.LeadTime = config.LeadTime
	}
	discoveryCtx, cancelDiscovery := context.WithTimeout(ctx, reminderDiscoveryTimeout)
	created, err := w.Discovery.Discover(discoveryCtx, now())
	cancelDiscovery()
	if err != nil && ctx.Err() == nil {
		log.Printf("dingtalk ticket reminder discovery: %v", err)
	} else if created > 0 {
		log.Printf("dingtalk ticket reminders: queued %d", created)
	}

	for range reminderBatchSize {
		if ctx.Err() != nil {
			return
		}
		claimCtx, cancelClaim := context.WithTimeout(ctx, reminderDiscoveryTimeout)
		job, err := w.Delivery.Claim(claimCtx, now())
		cancelClaim()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("dingtalk ticket reminder claim: %v", err)
			}
			return
		}
		if job == nil {
			return
		}

		timeout := config.DeliveryTimeout
		if timeout <= 0 {
			timeout = reminderDeliveryTimeout
		}
		deliveryCtx, cancelDelivery := context.WithTimeout(ctx, timeout)
		err = w.Delivery.Deliver(deliveryCtx, job, now())
		cancelDelivery()
		if err != nil && ctx.Err() == nil {
			log.Printf("dingtalk ticket reminder %d delivery: %v", job.ID, err)
		}
	}
}
