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
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	go func() { defer close(done); w.deliverLoop(ctx, wake) }()
	defer func() { <-done }()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		scanCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := w.Discovery.Discover(scanCtx, time.Now())
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("reminder discovery: %v", err)
		}
		select {
		case wake <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *ReminderWorker) deliverLoop(ctx context.Context, wake <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for i := 0; i < 10 && ctx.Err() == nil; i++ {
			claimCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			job, err := w.Delivery.Claim(claimCtx, time.Now())
			stop()
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("reminder claim: %v", err)
				}
				break
			}
			if job == nil {
				break
			}
			deliveryCtx, stop := context.WithTimeout(ctx, w.Delivery.Client.Timeout+10*time.Second)
			err = w.Delivery.Deliver(deliveryCtx, job, time.Now())
			stop()
			if err != nil && ctx.Err() == nil {
				log.Printf("reminder %d delivery: %v", job.ID, err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}
