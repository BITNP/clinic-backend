package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"

	"clinic-backend/internal/agent/tagger"
	"clinic-backend/internal/changes"
	"clinic-backend/internal/database"
)

const (
	defaultTaggerTagSetName = "clinic_record"
	recordTaggerQueueSize   = 256
)

// recordTagJob is one queued record-to-tag pairing.
type recordTagJob struct {
	recordID uint
	text     string
	model    string
	extra    string
}

// RecordTagger tags newly created records with the in-process tagger agent
// and, when configured, classifies their laptop for liquid metal. Work is
// queued by Enqueue and processed asynchronously by Run, so ticket creation
// never blocks on the tagger.
type RecordTagger struct {
	stateMu     sync.RWMutex
	tagAgent    TagAgent
	liquidMetal *LiquidMetalTagger
	tagSvc      *RecordTagService
	db          *database.DB
	syncSvc     *changes.SyncService
	jobs        chan recordTagJob
	workerLimit *dynamicLimiter
}

// NewRecordTagger builds a record tagger worker. tagAgent performs the actual
// tagging; db and tagSvc resolve a matched tag name back to a
// clinic_record_tag id.
func NewRecordTagger(tagAgent TagAgent, db *database.DB, tagSvc *RecordTagService) *RecordTagger {
	return &RecordTagger{
		tagAgent:    tagAgent,
		tagSvc:      tagSvc,
		db:          db,
		jobs:        make(chan recordTagJob, recordTaggerQueueSize),
		workerLimit: newDynamicLimiter(1),
	}
}

// SetSyncService wires the sync counter so that tag updates made by the
// background worker bump the record counter.
func (s *RecordTagger) SetSyncService(syncSvc *changes.SyncService) {
	s.syncSvc = syncSvc
}

// SetLiquidMetalTagger enables liquid-metal classification for queued records.
func (s *RecordTagger) SetLiquidMetalTagger(t *LiquidMetalTagger) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.liquidMetal = t
}

// SetWorkers sets how many queued jobs are processed concurrently. Values
// below one fall back to one worker.
func (s *RecordTagger) SetWorkers(n int) {
	s.workerLimit.SetLimit(n)
}

// SetTaggers replaces the tagger generation and its worker limit. In-flight
// work keeps using the generation it read before the swap.
func (s *RecordTagger) SetTaggers(tagAgent TagAgent, liquidMetal *LiquidMetalTagger, workers int) {
	if workers < 1 {
		workers = 1
	}
	s.stateMu.Lock()
	s.tagAgent = tagAgent
	s.liquidMetal = liquidMetal
	s.stateMu.Unlock()
	s.workerLimit.SetLimit(workers)
}

// RecordTagSet builds the record tagger tag set from the in-memory catalog.
// The default fallback tag and tags without an apply rule are skipped.
func RecordTagSet(tagSvc *RecordTagService) tagger.TagSet {
	return tagger.TagSet{
		Name:   defaultTaggerTagSetName,
		Prompt: strings.TrimSpace(tagSvc.Prompt()),
		Tags:   buildRecordTags(tagSvc),
	}
}

// buildRecordTags maps the in-memory record tag catalog onto tagger tag
// definitions. The default fallback tag and tags without an apply rule are
// skipped.
func buildRecordTags(tagSvc *RecordTagService) []tagger.Tag {
	var tags []tagger.Tag
	for _, t := range tagSvc.All() {
		name := strings.TrimSpace(t.Title)
		if name == "" || name == DefaultRecordTagTitle {
			continue
		}
		rule := normalizeApplyRule(t.ApplyRule)
		if rule == "" {
			continue
		}
		tags = append(tags, tagger.Tag{
			Name:        name,
			Description: strings.TrimSpace(t.Description),
			ApplyRule:   rule,
		})
	}
	return tags
}

// normalizeApplyRule drops the internal "model:" marker so the rule reads as
// natural language to the tagger agent.
func normalizeApplyRule(rule string) string {
	rule = strings.TrimSpace(rule)
	rule = strings.TrimPrefix(rule, "model:")
	return strings.TrimSpace(rule)
}

// Tag applies the record tag set to text and returns the matched tag name, or
// an empty string when no tag applies.
func (s *RecordTagger) Tag(ctx context.Context, text string) (string, error) {
	s.stateMu.RLock()
	tagAgent := s.tagAgent
	s.stateMu.RUnlock()
	if tagAgent == nil {
		return "", errors.New("tagger: no tag agent configured")
	}
	result, err := tagAgent.Tag(ctx, text)
	if err != nil {
		return "", err
	}
	return result.Name, nil
}

// Enqueue schedules a record for tagging. text is used by the record tagger;
// model and extra (the record's question description) are used by the
// liquid-metal tagger. It never blocks the caller: if the queue is full the job
// is dropped and the record keeps its default tag.
func (s *RecordTagger) Enqueue(recordID uint, text, model, extra string) {
	if s == nil {
		return
	}
	s.stateMu.RLock()
	configured := s.tagAgent != nil || s.liquidMetal != nil
	s.stateMu.RUnlock()
	if !configured {
		return
	}
	if strings.TrimSpace(text) == "" && strings.TrimSpace(model) == "" {
		return
	}
	select {
	case s.jobs <- recordTagJob{recordID: recordID, text: text, model: model, extra: extra}:
	default:
		log.Printf("tagger: queue full, dropping record %d", recordID)
	}
}

// Run dispatches queued jobs until ctx is cancelled. The dynamic limiter is
// adjusted by SetWorkers or SetTaggers.
func (s *RecordTagger) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			if !s.workerLimit.Acquire(ctx) {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer s.workerLimit.Release()
				s.process(ctx, job)
			}()
		}
	}
}

func (s *RecordTagger) process(ctx context.Context, job recordTagJob) {
	s.stateMu.RLock()
	tagAgent := s.tagAgent
	liquidMetal := s.liquidMetal
	s.stateMu.RUnlock()
	if tagAgent == nil && liquidMetal == nil {
		return
	}
	// Run both classifiers independently: a failure in one must not prevent the
	// other from updating the record.
	updated := s.applyRecordTag(ctx, tagAgent, job)
	if s.applyLiquidMetal(ctx, liquidMetal, job) {
		updated = true
	}
	if updated && s.syncSvc != nil {
		if err := s.syncSvc.Bump(ctx, changes.SyncGroupRecord); err != nil {
			log.Printf("tagger: bump record sync counter failed: %v", err)
		}
	}
}

// applyRecordTag runs the record tagger and stores the matched tag id. It
// reports whether the record was updated.
func (s *RecordTagger) applyRecordTag(ctx context.Context, tagAgent TagAgent, job recordTagJob) bool {
	if tagAgent == nil || strings.TrimSpace(job.text) == "" {
		return false
	}
	result, err := tagAgent.Tag(ctx, job.text)
	if err != nil {
		log.Printf("tagger: tag record %d failed: %v", job.recordID, err)
		return false
	}
	name := result.Name
	if name == "" {
		return false
	}

	tag, ok := s.tagSvc.ByTitle(name)
	if !ok {
		log.Printf("tagger: record %d matched unknown tag %q", job.recordID, name)
		return false
	}
	if _, err := s.db.ExecContext(
		ctx,
		"UPDATE clinic_record SET tag_id = $1 WHERE id = $2",
		tag.ID, job.recordID,
	); err != nil {
		log.Printf("tagger: update record %d tag_id failed: %v", job.recordID, err)
		return false
	}
	return true
}

type dynamicLimiter struct {
	mu      sync.Mutex
	limit   int
	active  int
	changed chan struct{}
}

func newDynamicLimiter(limit int) *dynamicLimiter {
	if limit < 1 {
		limit = 1
	}
	return &dynamicLimiter{limit: limit, changed: make(chan struct{})}
}

func (l *dynamicLimiter) SetLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
	l.signalLocked()
}

func (l *dynamicLimiter) Acquire(ctx context.Context) bool {
	for {
		l.mu.Lock()
		if l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return true
		}
		changed := l.changed
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
}

func (l *dynamicLimiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active > 0 {
		l.active--
	}
	l.signalLocked()
}

func (l *dynamicLimiter) signalLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// applyLiquidMetal classifies the record's laptop and stores the status index.
// It reports whether the record was updated. Records without a model cannot be
// classified and keep the default unknown index.
func (s *RecordTagger) applyLiquidMetal(ctx context.Context, liquidMetal *LiquidMetalTagger, job recordTagJob) bool {
	if liquidMetal == nil {
		return false
	}
	result, err := liquidMetal.Classify(ctx, job.model, "", job.extra)
	if errors.Is(err, ErrLiquidMetalModelRequired) {
		return false
	}
	if err != nil {
		log.Printf("tagger: liquid metal record %d failed: %v", job.recordID, err)
		return false
	}
	if _, err := s.db.ExecContext(
		ctx,
		"UPDATE clinic_record SET liquid_metal_status = $1 WHERE id = $2",
		LiquidMetalStatusIndex(result.Status), job.recordID,
	); err != nil {
		log.Printf("tagger: update record %d liquid_metal_status failed: %v", job.recordID, err)
		return false
	}
	return true
}
