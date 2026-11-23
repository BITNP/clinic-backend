package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

// Track polls gateway delivery status for accepted reminders and records each
// terminal outcome as a new immutable event.
func (d *ReminderDelivery) Track(ctx context.Context, now time.Time) error {
	settings, err := d.settings()
	if err != nil {
		return err
	}
	rows, err := d.DB.QueryContext(
		ctx,
		"SELECT "+reminderColumns+" FROM clinic_service_date_reminder "+
			"WHERE status = 'accepted' AND gateway_delivery_id <> '' "+
			"ORDER BY COALESCE(last_tracked_at, accepted_at), id LIMIT 20",
	)
	if err != nil {
		return fmt.Errorf("find accepted reminders: %w", err)
	}
	jobs := make([]schedulingmodel.ServiceDateReminder, 0, 20)
	for rows.Next() {
		job, err := scanReminder(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("scan accepted reminder: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("find accepted reminders: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("find accepted reminders: %w", err)
	}

	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	var firstErr error
	for i := range jobs {
		job := &jobs[i]
		if job.GatewayAccount != "" && job.GatewayAccount != settings.Account {
			if err := d.trackOutcome(ctx, job, "unknown", now, "gateway_account_changed", ""); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		endpoint := strings.TrimRight(settings.BaseURL, "/") +
			"/api/v1/accounts/" + url.PathEscape(job.GatewayAccount) +
			"/message-status/" + url.PathEscape(job.GatewayDeliveryID)
		requestCtx := ctx
		cancel := func() {}
		if settings.Timeout > 0 {
			requestCtx, cancel = context.WithTimeout(ctx, settings.Timeout)
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+settings.Token)
		}
		if err != nil {
			cancel()
			if firstErr == nil {
				firstErr = fmt.Errorf("build delivery status request: %w", err)
			}
			continue
		}
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			if updateErr := d.markTracked(ctx, job.ID, now); updateErr != nil && firstErr == nil {
				firstErr = updateErr
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("load gateway delivery status: %w", err)
			}
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			if err := d.trackOutcome(ctx, job, "unknown", now, "gateway_status_unavailable", ""); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			if updateErr := d.markTracked(ctx, job.ID, now); updateErr != nil && firstErr == nil {
				firstErr = updateErr
			}
			continue
		}
		var result struct {
			Status string `json:"status"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil {
			if updateErr := d.markTracked(ctx, job.ID, now); updateErr != nil && firstErr == nil {
				firstErr = updateErr
			}
			continue
		}
		switch result.Status {
		case "queued", "retrying":
			if err := d.markTracked(ctx, job.ID, now); err != nil && firstErr == nil {
				firstErr = err
			}
		case "sent":
			if err := d.trackOutcome(ctx, job, "sent", now, "sent", ""); err != nil && firstErr == nil {
				firstErr = err
			}
		case "failed":
			if err := d.trackOutcome(ctx, job, "failed", now, "gateway_delivery_failed", "gateway delivery failed"); err != nil && firstErr == nil {
				firstErr = err
			}
		default:
			if err := d.trackOutcome(ctx, job, "unknown", now, "gateway_status_invalid", ""); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (d *ReminderDelivery) markTracked(ctx context.Context, reminderID uint, now time.Time) error {
	if _, err := d.DB.ExecContext(
		ctx,
		"UPDATE clinic_service_date_reminder SET last_tracked_at = $1, updated_at = $1 WHERE id = $2 AND status = 'accepted'",
		now.UTC(), reminderID,
	); err != nil {
		return fmt.Errorf("mark reminder %d tracked: %w", reminderID, err)
	}
	return nil
}

func (d *ReminderDelivery) trackOutcome(
	ctx context.Context,
	job *schedulingmodel.ServiceDateReminder,
	status string,
	now time.Time,
	reason string,
	lastError string,
) error {
	action := "notification.unknown"
	if status == "sent" {
		action = "notification.sent"
	} else if status == "failed" {
		action = "notification.failed"
	}
	var sentAt any
	if status == "sent" {
		sentAt = now.UTC()
	}
	journaled := false
	err := database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(
			ctx,
			`UPDATE clinic_service_date_reminder
			 SET status = $1, sent_at = $2, last_tracked_at = $3, last_error = $4, updated_at = $3
			 WHERE id = $5 AND status = 'accepted'`,
			status, sentAt, now.UTC(), lastError, job.ID,
		)
		if err != nil {
			return fmt.Errorf("save reminder %d delivery outcome: %w", job.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("check reminder %d delivery outcome: %w", job.ID, err)
		}
		if n != 1 {
			return nil
		}
		if d.Journal == nil {
			return nil
		}
		operationID, err := journal.NewOperationID()
		if err != nil {
			return fmt.Errorf("prepare reminder tracker journal: %w", err)
		}
		details := map[string]any{
			"version":             1,
			"reminder_id":         job.ID,
			"attempt":             job.Attempts,
			"gateway_account":     job.GatewayAccount,
			"gateway_delivery_id": job.GatewayDeliveryID,
			"reason":              reason,
		}
		if status == "sent" {
			details["confirmation"] = "napcat_accepted"
		} else {
			details["error"] = map[string]any{
				"code":    reason,
				"message": "The gateway reported that reminder delivery did not complete.",
			}
		}
		_, err = journal.InsertTx(ctx, tx, journal.EventInput{
			OccurredAt:  now,
			ActorType:   journal.ActorSystem,
			ActorID:     "delivery_tracker",
			Action:      action,
			EntityType:  "service_date",
			EntityID:    fmt.Sprint(job.ServiceDateID),
			OperationID: operationID,
			Details:     details,
		})
		journaled = err == nil
		return err
	})
	if err != nil {
		return err
	}
	if journaled {
		bumpJournalSync(ctx, d.Journal, "reminder tracking")
	}
	return nil
}
