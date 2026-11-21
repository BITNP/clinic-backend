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
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

// ReminderDelivery claims and submits persisted reminder jobs.
type ReminderDelivery struct {
	DB        *database.DB
	Discovery *ReminderDiscovery
	Client    *http.Client
	Config    func() (ReminderDeliveryConfig, error)
	Lease     time.Duration
	Now       func() time.Time
}

func (d *ReminderDelivery) checkSettings(ctx context.Context, job *schedulingmodel.ServiceDateReminder, now time.Time) (bool, error) {
	settings, err := d.settings()
	if err != nil {
		return false, err
	}
	if !settings.Enabled {
		return false, d.finish(ctx, job, map[string]any{"status": "pending", "next_attempt_at": now})
	}
	for _, lead := range settings.LeadTimes {
		if int64(lead/time.Second) == job.LeadTimeSeconds {
			return true, nil
		}
	}
	return false, d.finish(ctx, job, map[string]any{"status": "cancelled"})
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
			job.ID = 0
			return nil
		}
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

func (d *ReminderDelivery) finish(ctx context.Context, job *schedulingmodel.ServiceDateReminder, updates map[string]any) error {
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
	if value, ok := updates["last_error"]; ok {
		add("last_error", value)
	}
	add("updated_at", time.Now().UTC())
	sets = append(sets, "claim_token = ''", "lease_until = NULL")
	args = append(args, job.ID, "sending", job.ClaimToken)
	whereStart := len(args) - 2
	res, err := d.DB.ExecContext(
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
	return nil
}

// Deliver rechecks eligibility and sends one claimed job.
func (d *ReminderDelivery) Deliver(ctx context.Context, job *schedulingmodel.ServiceDateReminder, now time.Time) error {
	cancelJob := func() error { return d.finish(ctx, job, map[string]any{"status": "cancelled"}) }
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
		return cancelJob()
	}
	if err != nil {
		return d.retry(ctx, job, now, fmt.Errorf("load service date: %w", err))
	}
	if !date.StartTime.Equal(job.ServiceStartAt) || !now.Before(date.StartTime) {
		return cancelJob()
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
	if count == 0 || qq == "" {
		return cancelJob()
	}
	userID, err := strconv.ParseInt(qq, 10, 64)
	if err != nil || userID <= 0 {
		return d.retry(ctx, job, now, fmt.Errorf("recipient QQ number is invalid"))
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
		return cancelJob()
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
		return d.retry(ctx, job, now, fmt.Errorf("submit reminder: %w", err))
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
		return d.retry(ctx, job, now, fmt.Errorf("decode gateway response: %w", err))
	}
	if resp.StatusCode != http.StatusAccepted || result.Status != "async" || result.Retcode != 1 || result.Data.DeliveryID == "" {
		return d.retry(ctx, job, now, fmt.Errorf("gateway rejected reminder: HTTP %d status %q retcode %d", resp.StatusCode, result.Status, result.Retcode))
	}
	return d.finish(ctx, job, map[string]any{"status": "sent", "sent_at": now, "last_error": ""})
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
	if job.Attempts >= 8 || !now.Add(delay).Before(job.ServiceStartAt) {
		status = "cancelled"
	}
	if err := d.finish(ctx, job, map[string]any{"status": status, "next_attempt_at": now.Add(delay), "last_error": cause.Error()}); err != nil {
		return err
	}
	return cause
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
