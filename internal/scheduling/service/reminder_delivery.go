package service

import (
	"bytes"
	"context"
	"crypto/rand"
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

	roommodel "clinic-backend/internal/room/model"
	schedulingmodel "clinic-backend/internal/scheduling/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReminderDelivery claims and submits persisted reminder jobs.
type ReminderDelivery struct {
	DB                      *gorm.DB
	Discovery               *ReminderDiscovery
	Client                  *http.Client
	BaseURL, Account, Token string
	Template                *template.Template
	Lease                   time.Duration
}

func (d *ReminderDelivery) Claim(ctx context.Context, now time.Time) (*schedulingmodel.ServiceDateReminder, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("create claim token: %w", err)
	}
	token := hex.EncodeToString(b[:])
	var job schedulingmodel.ServiceDateReminder
	err := d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("(status = ? AND next_attempt_at <= ?) OR (status = ? AND lease_until <= ?)", "pending", now, "sending", now).Order("next_attempt_at, id").First(&job).Error
		if err != nil {
			return err
		}
		lease := now.Add(d.Lease)
		return tx.Model(&job).Updates(map[string]any{"status": "sending", "lease_until": lease, "claim_token": token, "attempts": gorm.Expr("attempts + 1")}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim reminder: %w", err)
	}
	job.ClaimToken = token
	job.Attempts++
	return &job, nil
}

func (d *ReminderDelivery) finish(ctx context.Context, job *schedulingmodel.ServiceDateReminder, updates map[string]any) error {
	updates["claim_token"] = ""
	updates["lease_until"] = nil
	r := d.DB.WithContext(ctx).Model(&schedulingmodel.ServiceDateReminder{}).Where("id = ? AND status = ? AND claim_token = ?", job.ID, "sending", job.ClaimToken).Updates(updates)
	if r.Error != nil {
		return fmt.Errorf("update reminder %d: %w", job.ID, r.Error)
	}
	if r.RowsAffected != 1 {
		return fmt.Errorf("reminder %d claim expired", job.ID)
	}
	return nil
}

// Deliver rechecks eligibility and sends one claimed job.
func (d *ReminderDelivery) Deliver(ctx context.Context, job *schedulingmodel.ServiceDateReminder, now time.Time) error {
	cancelJob := func() error { return d.finish(ctx, job, map[string]any{"status": "cancelled"}) }
	var date schedulingmodel.ClinicServiceDate
	err := d.DB.WithContext(ctx).First(&date, job.ServiceDateID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cancelJob()
	}
	if err != nil {
		return fmt.Errorf("load service date: %w", err)
	}
	if !date.StartTime.Equal(job.ServiceStartAt) || !now.Before(date.StartTime) {
		return cancelJob()
	}
	admins, count, err := d.Discovery.Eligible(ctx, date)
	if err != nil {
		return err
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
	var room roommodel.ClinicRoom
	if err = d.DB.WithContext(ctx).First(&room, *date.RoomID).Error; err != nil {
		return fmt.Errorf("load room: %w", err)
	}
	loc := d.Discovery.Location
	if loc == nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	start := date.StartTime.In(loc)
	var message bytes.Buffer
	err = d.Template.Execute(&message, map[string]string{"Date": date.Date.Format("2006-01-02"), "StartTime": start.Format("15:04"), "Room": room.Name, "PendingCount": strconv.FormatInt(count, 10), "LeadTime": (time.Duration(job.LeadTimeSeconds) * time.Second).String()})
	if err != nil {
		return d.retry(ctx, job, now, fmt.Errorf("render reminder: %w", err))
	}
	body, err := json.Marshal(map[string]any{"user_id": userID, "message": message.String()})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(d.BaseURL, "/") + "/api/v1/accounts/" + url.PathEscape(d.Account) + "/send_private_msg_async"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return d.retry(ctx, job, now, err)
	}
	req.Header.Set("Authorization", "Bearer "+d.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.Client.Do(req)
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
	if !now.Add(delay).Before(job.ServiceStartAt) {
		status = "cancelled"
	}
	if err := d.finish(ctx, job, map[string]any{"status": status, "next_attempt_at": now.Add(delay), "last_error": cause.Error()}); err != nil {
		return err
	}
	return cause
}
