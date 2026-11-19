package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"text/template"
	"time"

	"clinic-backend/internal/database"
	notificationclient "clinic-backend/internal/notification/client"
	recordmodel "clinic-backend/internal/record/model"
)

// TicketStatusNotification describes a ticket transition that should be sent
// to the guest.
type TicketStatusNotification struct {
	RecordID     uint
	User         string
	Status       recordmodel.RecordStatus
	Reason       string
	RoomName     string
	RoomAddress  string
	ServiceStart time.Time
	ServiceEnd   time.Time
}

// TicketStatusNotifier delivers a ticket status change to the guest.
type TicketStatusNotifier interface {
	Notify(ctx context.Context, notification TicketStatusNotification) error
	NotifyReminder(ctx context.Context, notification TicketStatusNotification) error
}

// DingTalkStatusSender submits a DingTalk message and verifies its delivery
// status before returning.
type DingTalkStatusSender interface {
	SendAndCheckStatus(
		ctx context.Context,
		input notificationclient.SendRequest,
	) (notificationclient.SendResult, notificationclient.MessageStatus, error)
}

// DingTalkTicketNotifierConfig contains configurable guest notification values
// and templates.
type DingTalkTicketNotifierConfig struct {
	Title             string
	Header            string
	QQAccount         string
	TransferRoom      string
	TransferAddress   string
	ConfirmedTemplate string
	RejectedTemplate  string
	ReferredTemplate  string
	NoShowTemplate    string
	ReminderTemplate  string
}

// DingTalkTicketNotifier formats ticket status notifications and sends them to
// the DingTalk user id stored on the record.
type DingTalkTicketNotifier struct {
	sender           DingTalkStatusSender
	location         *time.Location
	title            string
	header           string
	qqAccount        string
	transferRoom     string
	transferAddress  string
	statusTemplates  map[recordmodel.RecordStatus]*template.Template
	reminderTemplate *template.Template
}

// NewDingTalkTicketNotifier constructs a ticket notifier using location for
// appointment dates in message text.
func NewDingTalkTicketNotifier(
	sender DingTalkStatusSender,
	location *time.Location,
	cfg DingTalkTicketNotifierConfig,
) (*DingTalkTicketNotifier, error) {
	if location == nil {
		location = time.Local
	}
	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		return nil, fmt.Errorf("ticket notification: title is required")
	}
	header := strings.TrimSpace(cfg.Header)
	if header == "" {
		return nil, fmt.Errorf("ticket notification: header is required")
	}

	confirmTemplate, err := parseTicketNotificationTemplate("confirmed", cfg.ConfirmedTemplate)
	if err != nil {
		return nil, err
	}
	rejectedTemplate, err := parseTicketNotificationTemplate("rejected", cfg.RejectedTemplate)
	if err != nil {
		return nil, err
	}
	referredTemplate, err := parseTicketNotificationTemplate("referred", cfg.ReferredTemplate)
	if err != nil {
		return nil, err
	}
	noShowTemplate, err := parseTicketNotificationTemplate("no_show", cfg.NoShowTemplate)
	if err != nil {
		return nil, err
	}
	reminderTemplate, err := parseTicketNotificationTemplate("reminder", cfg.ReminderTemplate)
	if err != nil {
		return nil, err
	}

	return &DingTalkTicketNotifier{
		sender:          sender,
		location:        location,
		title:           title,
		header:          header,
		qqAccount:       strings.TrimSpace(cfg.QQAccount),
		transferRoom:    strings.TrimSpace(cfg.TransferRoom),
		transferAddress: strings.TrimSpace(cfg.TransferAddress),
		statusTemplates: map[recordmodel.RecordStatus]*template.Template{
			recordmodel.RecordStatusConfirmed: confirmTemplate,
			recordmodel.RecordStatusRejected:  rejectedTemplate,
			recordmodel.RecordStatusReferred:  referredTemplate,
			recordmodel.RecordStatusNoShow:    noShowTemplate,
		},
		reminderTemplate: reminderTemplate,
	}, nil
}

// Notify sends one verified DingTalk text message for a supported ticket
// status change.
func (n *DingTalkTicketNotifier) Notify(ctx context.Context, notification TicketStatusNotification) error {
	if n == nil || n.sender == nil {
		return fmt.Errorf("ticket notification: dingtalk sender is not configured")
	}
	if !isNotifiableTicketStatus(notification.Status) {
		return nil
	}
	tmpl, ok := n.statusTemplates[notification.Status]
	if !ok {
		return nil
	}
	return n.send(ctx, tmpl, notification)
}

// NotifyReminder sends the configured pre-shift reminder for a ticket.
func (n *DingTalkTicketNotifier) NotifyReminder(ctx context.Context, notification TicketStatusNotification) error {
	if n == nil || n.sender == nil {
		return fmt.Errorf("ticket reminder: dingtalk sender is not configured")
	}
	return n.send(ctx, n.reminderTemplate, notification)
}

func (n *DingTalkTicketNotifier) send(
	ctx context.Context,
	tmpl *template.Template,
	notification TicketStatusNotification,
) error {
	if strings.TrimSpace(notification.User) == "" {
		return fmt.Errorf("ticket notification for record %d: user id is required", notification.RecordID)
	}
	content, err := n.render(tmpl, notification)
	if err != nil {
		return fmt.Errorf("ticket notification for record %d: %w", notification.RecordID, err)
	}
	if _, _, err := n.sender.SendAndCheckStatus(ctx, notificationclient.SendRequest{
		Receivers:   notification.User,
		Title:       n.title,
		Content:     content,
		MessageType: "text",
		Level:       "normal",
	}); err != nil {
		return fmt.Errorf("ticket notification for record %d: %w", notification.RecordID, err)
	}
	return nil
}

func isNotifiableTicketStatus(status recordmodel.RecordStatus) bool {
	switch status {
	case recordmodel.RecordStatusConfirmed,
		recordmodel.RecordStatusRejected,
		recordmodel.RecordStatusReferred,
		recordmodel.RecordStatusNoShow:
		return true
	default:
		return false
	}
}

type ticketNotificationTemplateData struct {
	HEDR string
	DATE string
	ID   string
	ROOM string
	TRRO string
	LOCA string
	TRLO string
	REAS string
	QQAC string
}

func (n *DingTalkTicketNotifier) render(tmpl *template.Template, notification TicketStatusNotification) (string, error) {
	start := notification.ServiceStart.In(n.location)
	end := notification.ServiceEnd.In(n.location)
	data := ticketNotificationTemplateData{
		HEDR: n.header,
		DATE: fmt.Sprintf("%s的%s-%s", start.Format("2006-01-02"), start.Format("15:04"), end.Format("15:04")),
		ID:   fmt.Sprintf("#%d", notification.RecordID),
		ROOM: notification.RoomName,
		TRRO: n.transferRoom,
		LOCA: notification.RoomAddress,
		TRLO: n.transferAddress,
		REAS: strings.TrimSpace(notification.Reason),
		QQAC: n.qqAccount,
	}
	var content bytes.Buffer
	if err := tmpl.Execute(&content, data); err != nil {
		return "", fmt.Errorf("render ticket notification: %w", err)
	}
	if strings.TrimSpace(content.String()) == "" {
		return "", fmt.Errorf("render ticket notification: content is empty")
	}
	return content.String(), nil
}

func parseTicketNotificationTemplate(name, raw string) (*template.Template, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\n`, "\n"))
	if raw == "" {
		return nil, fmt.Errorf("ticket notification: %s template is required", name)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("ticket notification: parse %s template: %w", name, err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, ticketNotificationTemplateData{
		HEDR: "header",
		DATE: "date",
		ID:   "#1",
		ROOM: "room",
		TRRO: "transfer room",
		LOCA: "room address",
		TRLO: "transfer address",
		REAS: "reason",
		QQAC: "QQ",
	}); err != nil {
		return nil, fmt.Errorf("ticket notification: execute %s template: %w", name, err)
	}
	return tmpl, nil
}

// notifyTicketStatusChange is best-effort. The ticket transition has already
// committed, so a delivery failure must not roll it back or fail the caller.
func (s *AdminRecordService) notifyTicketStatusChange(
	ctx context.Context,
	record recordmodel.ClinicRecord,
	reason string,
) {
	if s.notifier == nil || !isNotifiableTicketStatus(record.Status) {
		return
	}
	details, err := s.loadTicketNotificationDetails(ctx, record)
	if err != nil {
		log.Printf("record %d %s notification: %v", record.ID, record.Status, err)
		return
	}
	if err := s.notifier.Notify(ctx, TicketStatusNotification{
		RecordID:     record.ID,
		User:         record.User,
		Status:       record.Status,
		Reason:       reason,
		RoomName:     details.RoomName,
		RoomAddress:  details.RoomAddress,
		ServiceStart: details.ServiceStart,
		ServiceEnd:   details.ServiceEnd,
	}); err != nil {
		log.Printf("record %d %s notification: %v", record.ID, record.Status, err)
	}
}

type ticketNotificationDetails struct {
	RoomName     string
	RoomAddress  string
	ServiceStart time.Time
	ServiceEnd   time.Time
}

func (s *AdminRecordService) loadTicketNotificationDetails(
	ctx context.Context,
	record recordmodel.ClinicRecord,
) (ticketNotificationDetails, error) {
	var details ticketNotificationDetails
	datePredicate := "d.date = $2"
	if s.db.Driver == database.DriverSQLite {
		datePredicate = "date(d.date) = date($2)"
	}
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT r.name, r.address, d.start_time, d.end_time
		 FROM clinic_room r
		 JOIN clinic_service_date d ON r.id = $1 AND d.room_id = r.id AND %s
		 ORDER BY d.id
		 LIMIT 1`, datePredicate),
		record.RoomID,
		record.AppointmentTime,
	).Scan(&details.RoomName, &details.RoomAddress, &details.ServiceStart, &details.ServiceEnd)
	if err == sql.ErrNoRows {
		return ticketNotificationDetails{}, fmt.Errorf("notification room or service date is missing")
	}
	if err != nil {
		return ticketNotificationDetails{}, fmt.Errorf("load notification details: %w", err)
	}
	return details, nil
}
