package service

import (
	"context"
	"log"
	"time"
)

// ReminderWorker discovers and delivers reminders until its context is cancelled.
type ReminderWorker struct {
	Discovery *ReminderDiscovery
	Delivery  *ReminderDelivery
}

func (w *ReminderWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
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

func (w *ReminderWorker) runOnce(ctx context.Context) {
	scanCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := w.Discovery.Discover(scanCtx, time.Now())
	cancel()
	if err != nil && ctx.Err() == nil {
		log.Printf("reminder discovery: %v", err)
	}
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		claimCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		job, err := w.Delivery.Claim(claimCtx, time.Now())
		stop()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("reminder claim: %v", err)
			}
			return
		}
		if job == nil {
			return
		}
		deliveryCtx, stop := context.WithTimeout(ctx, w.Delivery.Client.Timeout+10*time.Second)
		err = w.Delivery.Deliver(deliveryCtx, job, time.Now())
		stop()
		if err != nil && ctx.Err() == nil {
			log.Printf("reminder %d delivery: %v", job.ID, err)
		}
	}
}
