package services

import (
	"context"
	"errors"
	"log"
	"strings"

	"clinic-backend/models"

	"gorm.io/gorm"
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

// RecordTagger tags newly created records by calling an external tagger API
// and, when configured, classifies their laptop for liquid metal. Work is
// queued by Enqueue and processed asynchronously by a single worker started
// with Run, so ticket creation never blocks on the tagger. Tag-set registration
// and tag calls are handled by the shared GeneralTagger.
type RecordTagger struct {
	tagger      *GeneralTagger
	liquidMetal *LiquidMetalTagger
	tagSvc      *RecordTagService
	db          *gorm.DB
	syncSvc     *SyncService
	jobs        chan recordTagJob
}

// NewRecordTagger builds a record tagger worker. client performs the actual
// talk to the tagger; db and tagSvc resolve a matched tag name back to a
// clinic_record_tag id.
func NewRecordTagger(client TaggerClient, tagSetName string, db *gorm.DB, tagSvc *RecordTagService) *RecordTagger {
	if tagSetName == "" {
		tagSetName = defaultTaggerTagSetName
	}
	return &RecordTagger{
		tagger: NewGeneralTagger(client, tagSetName, tagSvc.Prompt(), buildRecordTags(tagSvc)),
		tagSvc: tagSvc,
		db:     db,
		jobs:   make(chan recordTagJob, recordTaggerQueueSize),
	}
}

// SetSyncService wires the sync counter so that tag updates made by the
// background worker bump the record counter.
func (s *RecordTagger) SetSyncService(syncSvc *SyncService) {
	s.syncSvc = syncSvc
}

// SetLiquidMetalTagger enables liquid-metal classification for queued records.
func (s *RecordTagger) SetLiquidMetalTagger(t *LiquidMetalTagger) {
	s.liquidMetal = t
}

// buildRecordTags maps the in-memory record tag catalog onto tagger tag
// definitions. The default fallback tag and tags without an apply rule are
// skipped because the tagger API rejects blank rules.
func buildRecordTags(tagSvc *RecordTagService) []TaggerTag {
	var tags []TaggerTag
	for _, t := range tagSvc.All() {
		name := strings.TrimSpace(t.Title)
		if name == "" || name == DefaultRecordTagTitle {
			continue
		}
		rule := normalizeApplyRule(t.ApplyRule)
		if rule == "" {
			continue
		}
		tags = append(tags, TaggerTag{
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

// RegisterTagSet registers (or replaces) the record tag set.
func (s *RecordTagger) RegisterTagSet(ctx context.Context) error {
	return s.tagger.RegisterTagSet(ctx)
}

// Tag applies the record tag set to text and returns the matched tag name, or
// an empty string when no tag applies.
func (s *RecordTagger) Tag(ctx context.Context, text string) (string, error) {
	return s.tagger.Tag(ctx, text)
}

// Enqueue schedules a record for tagging. text is used by the record tagger;
// model and extra (the record's question description) are used by the
// liquid-metal tagger. It never blocks the caller: if the queue is full the job
// is dropped and the record keeps its default tag.
func (s *RecordTagger) Enqueue(recordID uint, text, model, extra string) {
	if s == nil {
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

// Run processes queued jobs until ctx is cancelled. It is intended to be
// started as a single goroutine. The tag set is normally registered by the
// caller at startup; when that has not happened yet, the worker registers it
// before processing jobs.
func (s *RecordTagger) Run(ctx context.Context) {
	if err := s.tagger.EnsureRegistered(ctx); err != nil {
		log.Printf("tagger: initial tag-set registration failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			s.process(ctx, job)
		}
	}
}

func (s *RecordTagger) process(ctx context.Context, job recordTagJob) {
	// Run both classifiers independently: a failure in one must not prevent the
	// other from updating the record.
	updated := s.applyRecordTag(ctx, job)
	if s.applyLiquidMetal(ctx, job) {
		updated = true
	}
	if updated && s.syncSvc != nil {
		if err := s.syncSvc.Bump(ctx, SyncGroupRecord); err != nil {
			log.Printf("tagger: bump record sync counter failed: %v", err)
		}
	}
}

// applyRecordTag runs the record tagger and stores the matched tag id. It
// reports whether the record was updated.
func (s *RecordTagger) applyRecordTag(ctx context.Context, job recordTagJob) bool {
	if strings.TrimSpace(job.text) == "" {
		return false
	}
	name, err := s.Tag(ctx, job.text)
	if err != nil {
		log.Printf("tagger: tag record %d failed: %v", job.recordID, err)
		return false
	}
	if name == "" {
		return false
	}

	tag, ok := s.tagSvc.ByTitle(name)
	if !ok {
		log.Printf("tagger: record %d matched unknown tag %q", job.recordID, name)
		return false
	}
	if err := s.db.Model(&models.ClinicRecord{}).
		Where("id = ?", job.recordID).
		Update("tag_id", tag.ID).Error; err != nil {
		log.Printf("tagger: update record %d tag_id failed: %v", job.recordID, err)
		return false
	}
	return true
}

// applyLiquidMetal classifies the record's laptop and stores the status index.
// It reports whether the record was updated. Records without a model cannot be
// classified and keep the default unknown index.
func (s *RecordTagger) applyLiquidMetal(ctx context.Context, job recordTagJob) bool {
	if s.liquidMetal == nil {
		return false
	}
	result, err := s.liquidMetal.Classify(ctx, job.model, "", job.extra)
	if errors.Is(err, ErrLiquidMetalModelRequired) {
		return false
	}
	if err != nil {
		log.Printf("tagger: liquid metal record %d failed: %v", job.recordID, err)
		return false
	}
	if err := s.db.Model(&models.ClinicRecord{}).
		Where("id = ?", job.recordID).
		Update("liquid_metal_status", LiquidMetalStatusIndex(result.Status)).Error; err != nil {
		log.Printf("tagger: update record %d liquid_metal_status failed: %v", job.recordID, err)
		return false
	}
	return true
}
