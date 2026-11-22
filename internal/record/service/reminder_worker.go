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

// TicketReminderWorker periodically discovers and delivers guest reminders.
type TicketReminderWorker struct {
	Discovery       *TicketReminderDiscovery
	Delivery        *TicketReminderDelivery
	PollInterval    time.Duration
	DeliveryTimeout time.Duration
	Now             func() time.Time
}

// Run processes reminders until ctx is canceled.
func (w *TicketReminderWorker) Run(ctx context.Context) {
	interval := w.PollInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		w.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *TicketReminderWorker) runOnce(ctx context.Context) {
	now := time.Now
	if w.Now != nil {
		now = w.Now
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

		timeout := w.DeliveryTimeout
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
