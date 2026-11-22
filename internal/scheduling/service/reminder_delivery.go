package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"clinic-backend/internal/database"
	"clinic-backend/internal/journal"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	staffmodel "clinic-backend/internal/staff/model"
)

// ReminderDelivery claims and submits persisted reminder jobs.
type ReminderDelivery struct {
	DB        *database.DB
	Discovery *ReminderDiscovery
	Client    *http.Client
	Config    func() (ReminderDeliveryConfig, error)
	Lease     time.Duration
	Now       func() time.Time
	Journal   *journal.Service
}

func (d *ReminderDelivery) checkSettings(ctx context.Context, job *schedulingmodel.ServiceDateReminder, now time.Time) (bool, error) {
	settings, err := d.settings()
	if err != nil {
		return false, err
	}
	if !settings.Enabled {
		return false, d.finish(ctx, job,
			map[string]any{"status": "pending", "next_attempt_at": now},
			"notification.retry_scheduled",
			map[string]any{"reason": "reminders_disabled"},
		)
	}
	for _, lead := range settings.LeadTimes {
		if int64(lead/time.Second) == job.LeadTimeSeconds {
			return true, nil
		}
	}
	return false, d.finish(ctx, job,
		map[string]any{"status": "cancelled"},
		"notification.cancelled",
		map[string]any{"reason": "lead_time_removed"},
	)
}

func (d *ReminderDelivery) Claim(ctx context.Context, now time.Time) (*schedulingmodel.ServiceDateReminder, error) {
	settings, err := d.settings()
	if err != nil {
		return nil, err
	}
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return nil, fmt.Errorf("create claim token: %w", err)
	}
	token := hex.EncodeToString(randomBytes[:])
	var job schedulingmodel.ServiceDateReminder
	err = database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
		query := "SELECT " + reminderColumns +
			" FROM clinic_service_date_reminder" +
			" WHERE (status = $1 AND next_attempt_at <= $2) OR (status = $3 AND lease_until <= $4)" +
			" ORDER BY next_attempt_at, id LIMIT 1"
		if d.DB.Driver == database.DriverPostgres {
			query += " FOR UPDATE SKIP LOCKED"
		}
		var err error
		job, err = scanReminder(tx.QueryRow(query, "pending", now, "sending", now))
		if err != nil {
			return err
		}
		if job.Attempts >= 8 {
			if _, err := tx.Exec(
				`UPDATE clinic_service_date_reminder
				 SET status = 'cancelled', lease_until = NULL, claim_token = '', last_error = 'retry limit reached', updated_at = $1
				 WHERE id = $2`,
				time.Now().UTC(), job.ID,
			); err != nil {
				return err
			}
			if d.Journal != nil {
				operationID, err := journal.NewOperationID()
				if err != nil {
					return fmt.Errorf("prepare reminder failure journal: %w", err)
				}
				if _, err := journal.InsertTx(ctx, tx, journal.EventInput{
					OccurredAt:  now,
					ActorType:   journal.ActorSystem,
					ActorID:     "reminder_worker",
					Action:      "notification.failed",
					EntityType:  "service_date",
					EntityID:    fmt.Sprint(job.ServiceDateID),
					OperationID: operationID,
					Details: map[string]any{
						"version":     1,
						"reminder_id": job.ID,
						"attempt":     job.Attempts,
						"reason":      "retry_limit_reached",
					},
				}); err != nil {
					return err
				}
			}
			job.ID = 0
			return nil
		}
		previousStatus := job.Status
		leaseDuration := d.Lease
		if leaseDuration <= 0 {
			leaseDuration = settings.Timeout + 45*time.Second
			if leaseDuration <= 0 {
				leaseDuration = time.Minute
			}
		}
		lease := now.Add(leaseDuration)
		if _, err := tx.Exec(
			`UPDATE clinic_service_date_reminder
			 SET status = 'sending', lease_until = $1, claim_token = $2, attempts = attempts + 1, updated_at = $3
			 WHERE id = $4`,
			lease, token, time.Now().UTC(), job.ID,
		); err != nil {
			return err
		}
		if d.Journal != nil {
			operationID, err := journal.NewOperationID()
			if err != nil {
				return fmt.Errorf("prepare reminder attempt journal: %w", err)
			}
			reason := "scheduled"
			if previousStatus == "sending" {
				reason = "lease_expired"
			}
			if _, err := journal.InsertTx(ctx, tx, journal.EventInput{
				OccurredAt:  now,
				ActorType:   journal.ActorSystem,
				ActorID:     "reminder_worker",
				Action:      "notification.attempt_started",
				EntityType:  "service_date",
				EntityID:    fmt.Sprint(job.ServiceDateID),
				OperationID: operationID,
				Details: map[string]any{
					"version":     1,
					"reminder_id": job.ID,
					"attempt":     job.Attempts + 1,
					"reason":      reason,
				},
			}); err != nil {
				return err
			}
		}
		job.Status = "sending"
		job.LeaseUntil = &lease
		job.ClaimToken = token
		job.Attempts++
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim reminder: %w", err)
	}
	if job.ID == 0 {
		return nil, nil
	}
	return &job, nil
}

func (d *ReminderDelivery) finish(
	ctx context.Context,
	job *schedulingmodel.ServiceDateReminder,
	updates map[string]any,
	eventAction string,
	eventDetails map[string]any,
) error {
	status, ok := updates["status"].(string)
	if !ok || status == "" {
		return fmt.Errorf("finish reminder %d: status is required", job.ID)
	}
	sets := []string{"status = $1"}
	args := []any{status}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if value, ok := updates["next_attempt_at"]; ok {
		add("next_attempt_at", value)
	}
	if value, ok := updates["sent_at"]; ok {
		add("sent_at", value)
	}
	if value, ok := updates["accepted_at"]; ok {
		add("accepted_at", value)
	}
	if value, ok := updates["gateway_delivery_id"]; ok {
		add("gateway_delivery_id", value)
	}
	if value, ok := updates["gateway_account"]; ok {
		add("gateway_account", value)
	}
	if value, ok := updates["last_tracked_at"]; ok {
		add("last_tracked_at", value)
	}
	if value, ok := updates["last_error"]; ok {
		add("last_error", value)
	}
	add("updated_at", time.Now().UTC())
	sets = append(sets, "claim_token = ''", "lease_until = NULL")
	args = append(args, job.ID, "sending", job.ClaimToken)
	whereStart := len(args) - 2
	return database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(
			ctx,
			"UPDATE clinic_service_date_reminder SET "+strings.Join(sets, ", ")+
				fmt.Sprintf(" WHERE id = $%d AND status = $%d AND claim_token = $%d", whereStart, whereStart+1, whereStart+2),
			args...,
		)
		if err != nil {
			return fmt.Errorf("update reminder %d: %w", job.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("check updated reminder %d: %w", job.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("reminder %d claim expired", job.ID)
		}
		if d.Journal == nil || eventAction == "" {
			return nil
		}
		operationID, err := journal.NewOperationID()
		if err != nil {
			return fmt.Errorf("prepare reminder journal operation: %w", err)
		}
		details := make(map[string]any, len(eventDetails)+4)
		details["version"] = 1
		details["reminder_id"] = job.ID
		details["attempt"] = job.Attempts
		for key, value := range eventDetails {
			details[key] = value
		}
		_, err = journal.InsertTx(ctx, tx, journal.EventInput{
			OccurredAt:  time.Now().UTC(),
			ActorType:   journal.ActorSystem,
			ActorID:     "reminder_worker",
			Action:      eventAction,
			EntityType:  "service_date",
			EntityID:    fmt.Sprint(job.ServiceDateID),
			OperationID: operationID,
			Details:     details,
		})
		return err
	})
}

// Deliver rechecks eligibility and sends one claimed job.
func (d *ReminderDelivery) Deliver(ctx context.Context, job *schedulingmodel.ServiceDateReminder, now time.Time) error {
	cancelJob := func(reason string) error {
		return d.finish(
			ctx,
			job,
			map[string]any{"status": "cancelled"},
			"notification.cancelled",
			map[string]any{"reason": reason},
		)
	}
	settings, err := d.settings()
	if err != nil {
		return d.retry(ctx, job, now, err)
	}
	if allowed, err := d.checkSettings(ctx, job, now); !allowed || err != nil {
		return err
	}
	date, err := scanServiceDate(d.DB.QueryRowContext(
		ctx,
		"SELECT "+serviceDateColumns+" FROM clinic_service_date WHERE id = $1",
		job.ServiceDateID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return cancelJob("service_date_deleted")
	}
	if err != nil {
		return d.retry(ctx, job, now, fmt.Errorf("load service date: %w", err))
	}
	if !date.StartTime.Equal(job.ServiceStartAt) || !now.Before(date.StartTime) {
		if !date.StartTime.Equal(job.ServiceStartAt) {
			return cancelJob("service_start_changed")
		}
		return cancelJob("service_already_started")
	}
	admins, count, err := d.Discovery.Eligible(ctx, date)
	if err != nil {
		return d.retry(ctx, job, now, err)
	}
	var qq string
	for _, admin := range admins {
		if admin.ID == job.StaffID {
			qq = strings.TrimSpace(admin.QQNumber)
			break
		}
	}
	if count == 0 {
		return cancelJob("no_pending_records")
	}
	if qq == "" {
		if _, ok := findAdmin(admins, job.StaffID); !ok {
			return cancelJob("recipient_no_longer_eligible")
		}
		return cancelJob("recipient_address_missing")
	}
	userID, err := strconv.ParseInt(qq, 10, 64)
	if err != nil || userID <= 0 {
		return cancelJob("recipient_address_invalid")
	}
	room, err := scanRoom(d.DB.QueryRowContext(
		ctx,
		"SELECT "+roomColumns+" FROM clinic_room WHERE id = $1",
		*date.RoomID,
	))
	if err != nil {
		return d.retry(ctx, job, now, fmt.Errorf("load room: %w", err))
	}
	loc := d.Discovery.Location
	if loc == nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	start := date.StartTime.In(loc)
	clock := d.Now
	if clock == nil {
		clock = time.Now
	}
	deliveryTime := clock()
	if !deliveryTime.Before(date.StartTime) {
		return cancelJob("service_already_started")
	}
	remaining := date.StartTime.Sub(deliveryTime).Truncate(time.Minute)
	if settings.Template == nil {
		return d.retry(ctx, job, now, fmt.Errorf("render reminder: template is not configured"))
	}
	var message bytes.Buffer
	err = settings.Template.Execute(&message, map[string]string{"Date": date.Date.Format("2006-01-02"), "StartTime": start.Format("15:04"), "Room": room.Name, "PendingCount": strconv.FormatInt(count, 10), "LeadTime": remaining.String()})
	if err != nil {
		return d.retry(ctx, job, now, fmt.Errorf("render reminder: %w", err))
	}
	body, err := json.Marshal(map[string]any{"user_id": userID, "message": message.String()})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(settings.BaseURL, "/") + "/api/v1/accounts/" + url.PathEscape(settings.Account) + "/send_private_msg_async"
	requestCtx := ctx
	cancelRequest := func() {}
	if settings.Timeout > 0 {
		requestCtx, cancelRequest = context.WithTimeout(ctx, settings.Timeout)
	}
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return d.retry(ctx, job, now, err)
	}
	req.Header.Set("Authorization", "Bearer "+settings.Token)
	req.Header.Set("Content-Type", "application/json")
	if allowed, err := d.checkSettings(ctx, job, now); !allowed || err != nil {
		return err
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return d.unknown(ctx, job, "gateway_transport_error")
	}
	defer resp.Body.Close()
	var result struct {
		Status  string `json:"status"`
		Retcode int    `json:"retcode"`
		Data    struct {
			DeliveryID string `json:"delivery_id"`
		} `json:"data"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return d.unknown(ctx, job, "gateway_response_invalid")
	}
	if resp.StatusCode != http.StatusAccepted || result.Status != "async" || result.Retcode != 1 || result.Data.DeliveryID == "" {
		if resp.StatusCode >= http.StatusInternalServerError {
			return d.unknown(ctx, job, "gateway_server_error")
		}
		return d.retry(ctx, job, now, fmt.Errorf("gateway rejected reminder: HTTP %d status %q retcode %d", resp.StatusCode, result.Status, result.Retcode))
	}
	return d.finish(
		ctx,
		job,
		map[string]any{
			"status":              "accepted",
			"accepted_at":         now,
			"gateway_delivery_id": result.Data.DeliveryID,
			"gateway_account":     settings.Account,
			"last_tracked_at":     now,
			"last_error":          "",
		},
		"notification.accepted",
		map[string]any{
			"channel":             "qq",
			"recipient":           map[string]any{"type": "staff", "id": job.StaffID},
			"gateway_account":     settings.Account,
			"gateway_delivery_id": result.Data.DeliveryID,
			"http_status":         resp.StatusCode,
			"message": map[string]any{
				"template_key": "SERVICE_DATE_REMINDER_TEMPLATE",
				"parameters": map[string]any{
					"Date":         date.Date.Format("2006-01-02"),
					"StartTime":    start.Format("15:04"),
					"Room":         room.Name,
					"PendingCount": strconv.FormatInt(count, 10),
					"LeadTime":     remaining.String(),
				},
			},
		},
	)
}

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
	return database.WithTx(ctx, d.DB.DB, func(tx *sql.Tx) error {
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
		return err
	})
}

func (d *ReminderDelivery) retry(ctx context.Context, job *schedulingmodel.ServiceDateReminder, now time.Time, cause error) error {
	delay := time.Minute
	for n := 1; n < job.Attempts && delay < 15*time.Minute; n++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	status := "pending"
	eventAction := "notification.retry_scheduled"
	reason := "gateway_submission_failed"
	terminal := false
	if job.Attempts >= 8 {
		status = "cancelled"
		eventAction = "notification.failed"
		reason = "retry_limit_reached"
		terminal = true
	} else if !now.Add(delay).Before(job.ServiceStartAt) {
		status = "cancelled"
		eventAction = "notification.failed"
		reason = "retry_deadline_reached"
		terminal = true
	}
	eventDetails := map[string]any{"reason": reason}
	if !terminal {
		eventDetails["next_attempt_at"] = now.Add(delay).UTC().Format(time.RFC3339Nano)
	}
	code, message := notificationError(cause)
	eventDetails["error"] = map[string]any{"code": code, "message": message}
	if err := d.finish(
		ctx,
		job,
		map[string]any{"status": status, "next_attempt_at": now.Add(delay), "last_error": cause.Error()},
		eventAction,
		eventDetails,
	); err != nil {
		return err
	}
	return cause
}

func (d *ReminderDelivery) unknown(ctx context.Context, job *schedulingmodel.ServiceDateReminder, reason string) error {
	return d.finish(
		ctx,
		job,
		map[string]any{"status": "unknown", "last_error": "gateway delivery outcome unknown"},
		"notification.unknown",
		map[string]any{"reason": reason, "may_have_been_accepted": true},
	)
}

func findAdmin(admins []staffmodel.ClinicStaff, staffID int) (staffmodel.ClinicStaff, bool) {
	for _, admin := range admins {
		if admin.ID == staffID {
			return admin, true
		}
	}
	return staffmodel.ClinicStaff{}, false
}

func notificationError(cause error) (string, string) {
	message := cause.Error()
	switch {
	case strings.Contains(message, "HTTP 429"):
		return "gateway_queue_full", "The gateway rejected the reminder because its queue is full."
	case strings.Contains(message, "HTTP 4"):
		return "gateway_rejected", "The gateway rejected the reminder submission."
	case strings.Contains(message, "template"):
		return "template_error", "The reminder template could not be rendered."
	case strings.Contains(message, "room"):
		return "room_unavailable", "The service room could not be loaded."
	case strings.Contains(message, "service date"):
		return "service_date_unavailable", "The service date could not be loaded."
	default:
		return "delivery_error", "The reminder could not be prepared or submitted."
	}
}

// DeliveryTimeout returns the configured HTTP timeout for one delivery. The
// worker uses it to bound the whole record-and-send operation.
func (d *ReminderDelivery) DeliveryTimeout() time.Duration {
	settings, err := d.settings()
	if err == nil && settings.Timeout > 0 {
		return settings.Timeout
	}
	return 5 * time.Second
}

// ReminderDeliveryConfig is the active database-backed snapshot used for one
// reminder operation.
type ReminderDeliveryConfig struct {
	Enabled   bool
	LeadTimes []time.Duration
	BaseURL   string
	Account   string
	Token     string
	Timeout   time.Duration
	Template  *template.Template
}

func (d *ReminderDelivery) settings() (ReminderDeliveryConfig, error) {
	if d.Config == nil {
		return ReminderDeliveryConfig{Enabled: true}, nil
	}
	settings, err := d.Config()
	if err != nil {
		return ReminderDeliveryConfig{}, fmt.Errorf("load reminder delivery settings: %w", err)
	}
	settings.LeadTimes = append([]time.Duration(nil), settings.LeadTimes...)
	return settings, nil
}
