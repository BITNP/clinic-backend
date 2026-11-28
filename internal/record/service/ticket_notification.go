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
	"clinic-backend/internal/journal"
	notificationclient "clinic-backend/internal/notification/client"
	recordmodel "clinic-backend/internal/record/model"
)

// TicketStatusNotification describes a ticket transition that should be sent
// to the guest.
type TicketStatusNotification struct {
	RecordID         uint
	User             string
	Status           recordmodel.RecordStatus
	Reason           string
	RoomName         string
	RoomAddress      string
	RoomTransfer     bool
	NoShowBanEnabled bool
	ServiceStart     time.Time
	ServiceEnd       time.Time
}

// TicketNotificationDelivery describes a message accepted and verified by the
// notification provider. Skipped is set when notifications are disabled or the
// transition has no configured notification.
type TicketNotificationDelivery struct {
	MessageID string
	Skipped   bool
}

const ticketNotificationJournalTimeout = 5 * time.Second

// TicketStatusNotifier delivers a ticket status change to the guest.
type TicketStatusNotifier interface {
	Notify(ctx context.Context, notification TicketStatusNotification) (TicketNotificationDelivery, error)
	NotifyReminder(ctx context.Context, notification TicketStatusNotification) (TicketNotificationDelivery, error)
}

type ticketNotificationAvailability interface {
	NotificationsEnabled() bool
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
	Title               string
	Header              string
	QQAccount           string
	ConfirmedTemplate   string
	RejectedTemplate    string
	ReferredTemplate    string
	TransferTitle       string
	TransferTemplate    string
	NoShowBanTemplate   string
	NoShowNoBanTemplate string
	ReminderTemplate    string
}

// DingTalkTicketNotifier formats ticket status notifications and sends them to
// the DingTalk user id stored on the record.
type DingTalkTicketNotifier struct {
	sender              DingTalkStatusSender
	location            *time.Location
	title               string
	header              string
	qqAccount           string
	transferTitle       string
	transferTemplate    *template.Template
	statusTemplates     map[recordmodel.RecordStatus]*template.Template
	noShowBanTemplate   *template.Template
	noShowNoBanTemplate *template.Template
	reminderTemplate    *template.Template
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
	transferTemplate, err := parseTicketNotificationTemplate("transfer", cfg.TransferTemplate)
	if err != nil {
		return nil, err
	}
	noShowBanTemplate, err := parseTicketNotificationTemplate("no_show_ban", cfg.NoShowBanTemplate)
	if err != nil {
		return nil, err
	}
	noShowNoBanTemplate, err := parseTicketNotificationTemplate("no_show_no_ban", cfg.NoShowNoBanTemplate)
	if err != nil {
		return nil, err
	}
	reminderTemplate, err := parseTicketNotificationTemplate("reminder", cfg.ReminderTemplate)
	if err != nil {
		return nil, err
	}

	return &DingTalkTicketNotifier{
		sender:              sender,
		location:            location,
		title:               title,
		header:              header,
		qqAccount:           strings.TrimSpace(cfg.QQAccount),
		transferTitle:       strings.TrimSpace(cfg.TransferTitle),
		transferTemplate:    transferTemplate,
		noShowBanTemplate:   noShowBanTemplate,
		noShowNoBanTemplate: noShowNoBanTemplate,
		statusTemplates: map[recordmodel.RecordStatus]*template.Template{
			recordmodel.RecordStatusConfirmed: confirmTemplate,
			recordmodel.RecordStatusRejected:  rejectedTemplate,
			recordmodel.RecordStatusReferred:  referredTemplate,
		},
		reminderTemplate: reminderTemplate,
	}, nil
}

// Notify sends one verified DingTalk text message for a supported ticket
// status change.
func (n *DingTalkTicketNotifier) Notify(
	ctx context.Context,
	notification TicketStatusNotification,
) (TicketNotificationDelivery, error) {
	if n == nil || n.sender == nil {
		return TicketNotificationDelivery{}, fmt.Errorf("ticket notification: dingtalk sender is not configured")
	}
	if notification.RoomTransfer {
		if n.transferTitle == "" {
			return TicketNotificationDelivery{}, fmt.Errorf("ticket notification: transfer title is required")
		}
		return n.send(ctx, n.transferTemplate, n.transferTitle, notification)
	}
	if !isNotifiableTicketStatus(notification.Status) {
		return TicketNotificationDelivery{Skipped: true}, nil
	}
	if notification.Status == recordmodel.RecordStatusNoShow {
		tmpl := n.noShowNoBanTemplate
		if notification.NoShowBanEnabled {
			tmpl = n.noShowBanTemplate
		}
		return n.send(ctx, tmpl, n.title, notification)
	}
	tmpl, ok := n.statusTemplates[notification.Status]
	if !ok {
		return TicketNotificationDelivery{Skipped: true}, nil
	}
	return n.send(ctx, tmpl, n.title, notification)
}

// NotifyReminder sends the configured pre-shift reminder for a ticket.
func (n *DingTalkTicketNotifier) NotifyReminder(
	ctx context.Context,
	notification TicketStatusNotification,
) (TicketNotificationDelivery, error) {
	if n == nil || n.sender == nil {
		return TicketNotificationDelivery{}, fmt.Errorf("ticket reminder: dingtalk sender is not configured")
	}
	return n.send(ctx, n.reminderTemplate, n.title, notification)
}

func (n *DingTalkTicketNotifier) send(
	ctx context.Context,
	tmpl *template.Template,
	title string,
	notification TicketStatusNotification,
) (TicketNotificationDelivery, error) {
	if strings.TrimSpace(notification.User) == "" {
		return TicketNotificationDelivery{}, fmt.Errorf(
			"ticket notification for record %d: user id is required",
			notification.RecordID,
		)
	}
	content, err := n.render(tmpl, notification)
	if err != nil {
		return TicketNotificationDelivery{}, fmt.Errorf("ticket notification for record %d: %w", notification.RecordID, err)
	}
	sent, _, err := n.sender.SendAndCheckStatus(ctx, notificationclient.SendRequest{
		Receivers:   notification.User,
		Title:       title,
		Content:     content,
		MessageType: "text",
		Level:       "normal",
	})
	if err != nil {
		return TicketNotificationDelivery{}, fmt.Errorf("ticket notification for record %d: %w", notification.RecordID, err)
	}
	return TicketNotificationDelivery{MessageID: sent.MsgID}, nil
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
		TRRO: notification.RoomName,
		LOCA: notification.RoomAddress,
		TRLO: notification.RoomAddress,
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
	roomTransfer bool,
	causedByEventID uint64,
) {
	if s.notifier == nil || !isNotifiableTicketStatus(record.Status) {
		return
	}
	if availability, ok := s.notifier.(ticketNotificationAvailability); ok &&
		!availability.NotificationsEnabled() {
		return
	}
	details, err := s.loadTicketNotificationDetails(ctx, record)
	if err != nil {
		log.Printf("record %d %s notification: %v", record.ID, record.Status, err)
		s.journalTicketNotificationOutcome(
			record,
			roomTransfer,
			false,
			causedByEventID,
			TicketNotificationDelivery{},
			"notification_context_unavailable",
		)
		return
	}
	delivery, err := s.notifier.Notify(ctx, TicketStatusNotification{
		RecordID:         record.ID,
		User:             record.User,
		Status:           record.Status,
		Reason:           reason,
		RoomName:         details.RoomName,
		RoomAddress:      details.RoomAddress,
		RoomTransfer:     roomTransfer,
		NoShowBanEnabled: details.NoShowBanEnabled,
		ServiceStart:     details.ServiceStart,
		ServiceEnd:       details.ServiceEnd,
	})
	if err != nil {
		log.Printf("record %d %s notification: %v", record.ID, record.Status, err)
		s.journalTicketNotificationOutcome(
			record,
			roomTransfer,
			details.NoShowBanEnabled,
			causedByEventID,
			TicketNotificationDelivery{},
			"dingtalk_delivery_failed",
		)
		return
	}
	if delivery.Skipped {
		return
	}
	s.journalTicketNotificationOutcome(record, roomTransfer, details.NoShowBanEnabled, causedByEventID, delivery, "")
}

func (s *AdminRecordService) journalTicketNotificationOutcome(
	record recordmodel.ClinicRecord,
	roomTransfer bool,
	noShowBanEnabled bool,
	causedByEventID uint64,
	delivery TicketNotificationDelivery,
	failureCode string,
) {
	if s.journal == nil {
		return
	}
	operationID, err := journal.NewOperationID()
	if err != nil {
		log.Printf("record %d notification journal: %v", record.ID, err)
		return
	}
	action := "notification.sent"
	details := map[string]any{
		"version":             1,
		"kind":                "record_status",
		"channel":             "dingtalk",
		"recipient":           map[string]any{"type": "customer"},
		"template_key":        ticketNotificationTemplateKey(record.Status, roomTransfer, noShowBanEnabled),
		"record_status":       string(record.Status),
		"room_transfer":       roomTransfer,
		"no_show_ban_enabled": noShowBanEnabled,
	}
	if failureCode == "" {
		details["confirmation"] = "message_center_send_success"
		if delivery.MessageID != "" {
			details["message_center_msg_id"] = delivery.MessageID
		}
	} else {
		action = "notification.failed"
		details["reason"] = failureCode
		details["error"] = map[string]any{
			"code":    failureCode,
			"message": "The DingTalk status notification did not complete.",
		}
	}
	var cause *uint64
	if causedByEventID != 0 {
		id := causedByEventID
		cause = &id
	}
	journalCtx, cancel := context.WithTimeout(context.Background(), ticketNotificationJournalTimeout)
	defer cancel()
	err = database.WithTx(journalCtx, s.db.DB, func(tx *sql.Tx) error {
		_, err := journal.InsertTx(journalCtx, tx, journal.EventInput{
			OccurredAt:      time.Now().UTC(),
			ActorType:       journal.ActorSystem,
			ActorID:         "dingtalk_notifier",
			Action:          action,
			EntityType:      "record",
			EntityID:        recordEntityID(record.ID),
			OperationID:     operationID,
			CausedByEventID: cause,
			Details:         details,
		})
		return err
	})
	if err != nil {
		log.Printf("record %d notification journal: %v", record.ID, err)
		return
	}
	bumpJournalSync(journalCtx, s.journal, "record notification outcome")
}

func ticketNotificationTemplateKey(status recordmodel.RecordStatus, roomTransfer, noShowBanEnabled bool) string {
	if roomTransfer {
		return "DINGTALK_TEMPLATE_TRANSFER"
	}
	switch status {
	case recordmodel.RecordStatusConfirmed:
		return "DINGTALK_TEMPLATE_CONFIRMED"
	case recordmodel.RecordStatusRejected:
		return "DINGTALK_TEMPLATE_REJECTED"
	case recordmodel.RecordStatusReferred:
		return "DINGTALK_TEMPLATE_REFERRED"
	case recordmodel.RecordStatusNoShow:
		if noShowBanEnabled {
			return "DINGTALK_TEMPLATE_NO_SHOW"
		}
		return "DINGTALK_TEMPLATE_NO_SHOW_NO_BAN"
	default:
		return ""
	}
}

type ticketNotificationDetails struct {
	RoomName         string
	RoomAddress      string
	ServiceStart     time.Time
	ServiceEnd       time.Time
	NoShowBanEnabled bool
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
		fmt.Sprintf(`SELECT r.name, r.address, d.start_time, d.end_time, COALESCE(c.no_show_ban_enabled, FALSE)
		 FROM clinic_room r
		 LEFT JOIN clinic_campus c ON c.id = r.campus_id
		 JOIN clinic_service_date d ON r.id = $1 AND d.room_id = r.id AND %s
		 ORDER BY d.id
		 LIMIT 1`, datePredicate),
		record.RoomID,
		record.AppointmentTime,
	).Scan(&details.RoomName, &details.RoomAddress, &details.ServiceStart, &details.ServiceEnd, &details.NoShowBanEnabled)
	if err == sql.ErrNoRows {
		return ticketNotificationDetails{}, fmt.Errorf("notification room or service date is missing")
	}
	if err != nil {
		return ticketNotificationDetails{}, fmt.Errorf("load notification details: %w", err)
	}
	return details, nil
}
