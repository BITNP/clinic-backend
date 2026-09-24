package services

import (
	"context"
	"fmt"
	"time"

	"clinic-backend/models"
)

// RecordAction identifies a staff operation that can be reverted. The string
// values are part of the persisted action journal, so they must stay stable.
type RecordAction string

const (
	RecordActionConfirm    RecordAction = "confirm"
	RecordActionReject     RecordAction = "reject"
	RecordActionRefer      RecordAction = "refer"
	RecordActionArrive     RecordAction = "arrive"
	RecordActionInProgress RecordAction = "in_progress"
	RecordActionComplete   RecordAction = "complete"
	RecordActionNoShow     RecordAction = "no_show"
	RecordActionUpdate     RecordAction = "update"
)

// WorkerSnapshot captures the clinic_record_worker row before an action.
type WorkerSnapshot struct {
	WorkerID   uint      `json:"worker_id"`
	WorkerDesc string    `json:"worker_desc"`
	FinishTime time.Time `json:"finish_time"`
}

// RecordSnapshot captures every piece of mutable record state an action may
// overwrite, so reverting an action is a plain restore. A nil pointer means the
// corresponding side-table row did not exist.
type RecordSnapshot struct {
	Status         models.RecordStatus `json:"status"`
	ApproverID     *uint               `json:"approver_id"`
	ArriveTime     *time.Time          `json:"arrive_time,omitempty"`
	Worker         *WorkerSnapshot     `json:"worker,omitempty"`
	RejectReason   *string             `json:"reject_reason,omitempty"`
	ReferralReason *string             `json:"referral_reason,omitempty"`
}

// RecordActionEntry is one reversible action, stored in the acting staff
// member's action stack.
type RecordActionEntry struct {
	RecordID uint         `json:"record_id"`
	ActorID  int          `json:"actor_id"`
	Action   RecordAction `json:"action"`
	// PrevSeq and ActionSeq are the record's action sequence before and after
	// this action. Reverting restores PrevSeq.
	PrevSeq   uint           `json:"prev_seq"`
	ActionSeq uint           `json:"action_seq"`
	CreatedAt time.Time      `json:"created_at"`
	Prev      RecordSnapshot `json:"prev"`
}

// RecordActionStore is the action-stack backend. The Redis implementation lives
// in the web layer so this package stays free of Redis details and tests can
// substitute an in-memory fake.
type RecordActionStore interface {
	// Push appends entry to the actor's stack. ttl bounds the stack lifetime.
	Push(ctx context.Context, actorID int, entry RecordActionEntry, ttl time.Duration) error
	// Window returns the actor's entries created after cutoff, newest first.
	// Implementations should lazily drop entries that fall outside the window.
	Window(ctx context.Context, actorID int, cutoff time.Time) ([]RecordActionEntry, error)
	// Remove deletes a single entry from the actor's stack.
	Remove(ctx context.Context, actorID int, entry RecordActionEntry) error
}

// RecordActionService maintains a sliding-window stack of reversible actions
// per staff member.
type RecordActionService struct {
	store  RecordActionStore
	window time.Duration
}

func NewRecordActionService(store RecordActionStore, window time.Duration) *RecordActionService {
	return &RecordActionService{store: store, window: window}
}

// Window is the sliding window during which an action stays revertible.
func (s *RecordActionService) Window() time.Duration {
	return s.window
}

// Record appends a completed action to the actor's stack. It is best-effort:
// a failed push only means the action is no longer revertible.
func (s *RecordActionService) Record(ctx context.Context, entry RecordActionEntry) error {
	if s == nil || s.store == nil {
		return nil
	}
	ttl := s.window + time.Minute
	if err := s.store.Push(ctx, entry.ActorID, entry, ttl); err != nil {
		return fmt.Errorf("push record action: %w", err)
	}
	return nil
}

// RecordWindow summarizes an actor's in-window actions for one record.
type RecordWindow struct {
	Count  int
	Latest RecordActionEntry
}

// Windows returns the actor's in-window actions grouped by record. The latest
// action per record is the only one that can be reverted (strict LIFO).
func (s *RecordActionService) Windows(ctx context.Context, actorID int, now time.Time) (map[uint]RecordWindow, error) {
	if s == nil || s.store == nil {
		return map[uint]RecordWindow{}, nil
	}
	entries, err := s.store.Window(ctx, actorID, now.Add(-s.window))
	if err != nil {
		return nil, fmt.Errorf("read record actions: %w", err)
	}
	out := make(map[uint]RecordWindow)
	for _, e := range entries {
		w, ok := out[e.RecordID]
		if !ok {
			// Entries arrive newest first, so the first one seen is the latest.
			out[e.RecordID] = RecordWindow{Count: 1, Latest: e}
			continue
		}
		w.Count++
		out[e.RecordID] = w
	}
	return out, nil
}

// Latest returns the actor's most recent in-window action for a record.
func (s *RecordActionService) Latest(ctx context.Context, actorID int, recordID uint, now time.Time) (RecordActionEntry, bool, error) {
	windows, err := s.Windows(ctx, actorID, now)
	if err != nil {
		return RecordActionEntry{}, false, err
	}
	w, ok := windows[recordID]
	if !ok {
		return RecordActionEntry{}, false, nil
	}
	return w.Latest, true, nil
}

// Consume removes an action after it has been reverted.
func (s *RecordActionService) Consume(ctx context.Context, entry RecordActionEntry) error {
	if s == nil || s.store == nil {
		return nil
	}
	if err := s.store.Remove(ctx, entry.ActorID, entry); err != nil {
		return fmt.Errorf("remove record action: %w", err)
	}
	return nil
}
