package model

// WorkingStatuses returns the active repair lifecycle states.
func WorkingStatuses() []RecordStatus {
	return []RecordStatus{RecordStatusPending, RecordStatusConfirmed, RecordStatusArrived, RecordStatusInProgress}
}

// FinishedStatuses returns the closed repair lifecycle states.
func FinishedStatuses() []RecordStatus {
	return []RecordStatus{RecordStatusRejected, RecordStatusCompleted, RecordStatusReferred, RecordStatusNoShow}
}
