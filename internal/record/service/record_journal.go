package service

import (
	"reflect"
	"strconv"
	"time"
)

func recordEventAction(action RecordAction) string {
	switch action {
	case RecordActionConfirm:
		return "record.confirmed"
	case RecordActionReject:
		return "record.rejected"
	case RecordActionRefer:
		return "record.referred"
	case RecordActionArrive:
		return "record.arrived"
	case RecordActionInProgress:
		return "record.started"
	case RecordActionComplete:
		return "record.completed"
	case RecordActionNoShow:
		return "record.no_show"
	case RecordActionUpdate:
		return "record.notes_updated"
	default:
		return "record.updated"
	}
}

func recordSnapshotDetails(before, after RecordSnapshot) map[string]any {
	changes := recordSnapshotChanges(before, after)
	return map[string]any{"version": 1, "changes": changes}
}

func recordSnapshotChanges(before, after RecordSnapshot) map[string]any {
	changes := make(map[string]any)
	add := func(name string, oldValue, newValue any) {
		if !reflect.DeepEqual(oldValue, newValue) {
			changes[name] = map[string]any{"before": oldValue, "after": newValue}
		}
	}

	add("status", string(before.Status), string(after.Status))
	if before.RoomID != after.RoomID {
		add("room_id", before.RoomID, after.RoomID)
	}
	add("approver_id", nullableUintValue(before.ApproverID), nullableUintValue(after.ApproverID))
	add("arrival", arrivalSnapshotValue(before.ArriveTime), arrivalSnapshotValue(after.ArriveTime))
	add("worker", workerSnapshotValue(before.Worker), workerSnapshotValue(after.Worker))
	add("rejection", reasonSnapshotValue(before.RejectReason, "reject_reason"),
		reasonSnapshotValue(after.RejectReason, "reject_reason"))
	add("referral", reasonSnapshotValue(before.ReferralReason, "referral_reason"),
		reasonSnapshotValue(after.ReferralReason, "referral_reason"))
	return changes
}

func nullableUintValue(value *uint) any {
	if value == nil {
		return nil
	}
	return *value
}

func arrivalSnapshotValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return map[string]any{"arrive_time": value.UTC().Format(time.RFC3339Nano)}
}

func workerSnapshotValue(value *WorkerSnapshot) any {
	if value == nil {
		return nil
	}
	var finishTime any
	if !value.FinishTime.IsZero() {
		finishTime = value.FinishTime.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"worker_id":   value.WorkerID,
		"worker_desc": value.WorkerDesc,
		"finish_time": finishTime,
	}
}

func reasonSnapshotValue(value *string, name string) any {
	if value == nil {
		return nil
	}
	return map[string]any{name: *value}
}

func staffActorID(id int) string {
	return strconv.Itoa(id)
}

func recordEntityID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
