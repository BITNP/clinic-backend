package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	notificationclient "clinic-backend/internal/notification/client"
	recordmodel "clinic-backend/internal/record/model"
)

const ticketNotificationTitle = "电脑诊所工单状态更新"

// TicketStatusNotification describes a ticket transition that should be sent
// to the guest.
type TicketStatusNotification struct {
	RecordID        uint
	User            string
	Status          recordmodel.RecordStatus
	AppointmentTime time.Time
	Reason          string
}

// TicketStatusNotifier delivers a ticket status change to the guest.
type TicketStatusNotifier interface {
	Notify(ctx context.Context, notification TicketStatusNotification) error
}

// DingTalkStatusSender submits a DingTalk message and verifies its delivery
// status before returning.
type DingTalkStatusSender interface {
	SendAndCheckStatus(
		ctx context.Context,
		input notificationclient.SendRequest,
	) (notificationclient.SendResult, notificationclient.MessageStatus, error)
}

// DingTalkTicketNotifier formats ticket status notifications and sends them to
// the DingTalk user id stored on the record.
type DingTalkTicketNotifier struct {
	sender   DingTalkStatusSender
	location *time.Location
}

// NewDingTalkTicketNotifier constructs a ticket notifier using location for
// appointment dates in message text.
func NewDingTalkTicketNotifier(sender DingTalkStatusSender, location *time.Location) *DingTalkTicketNotifier {
	if location == nil {
		location = time.Local
	}
	return &DingTalkTicketNotifier{sender: sender, location: location}
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
	if strings.TrimSpace(notification.User) == "" {
		return fmt.Errorf("ticket notification for record %d: user id is required", notification.RecordID)
	}
	if _, _, err := n.sender.SendAndCheckStatus(ctx, notificationclient.SendRequest{
		Receivers:   notification.User,
		Title:       ticketNotificationTitle,
		Content:     ticketNotificationContent(notification, n.location),
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

func ticketNotificationContent(notification TicketStatusNotification, location *time.Location) string {
	ticketNo := fmt.Sprintf("%08d", notification.RecordID)
	appointmentDate := notification.AppointmentTime.In(location).Format("2006-01-02")
	reason := strings.TrimSpace(notification.Reason)

	switch notification.Status {
	case recordmodel.RecordStatusConfirmed:
		return fmt.Sprintf("您的工单 #%s（预约日期 %s）已确认。", ticketNo, appointmentDate)
	case recordmodel.RecordStatusRejected:
		content := fmt.Sprintf("您的工单 #%s（预约日期 %s）已驳回。", ticketNo, appointmentDate)
		if reason != "" {
			content += "\n驳回原因：" + reason
		}
		return content
	case recordmodel.RecordStatusReferred:
		content := fmt.Sprintf("您的工单 #%s（预约日期 %s）建议返厂。", ticketNo, appointmentDate)
		if reason != "" {
			content += "\n说明：" + reason
		}
		return content
	case recordmodel.RecordStatusNoShow:
		return fmt.Sprintf("您的工单 #%s（预约日期 %s）因未到诊所已关闭。", ticketNo, appointmentDate)
	default:
		return ""
	}
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
	if err := s.notifier.Notify(ctx, TicketStatusNotification{
		RecordID:        record.ID,
		User:            record.User,
		Status:          record.Status,
		AppointmentTime: record.AppointmentTime,
		Reason:          reason,
	}); err != nil {
		log.Printf("record %d %s notification: %v", record.ID, record.Status, err)
	}
}
