package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"clinic-backend/models"

	"gorm.io/gorm"
)

const (
	defaultTaggerTagSetName = "clinic_record"
	taggerQueueSize         = 256
)

// TaggerTag is a tag definition registered with the tagger.
type TaggerTag struct {
	Name        string
	Description string
	ApplyRule   string
}

// ErrTagSetNotFound reports that the tagger does not know the tag set yet, for
// example after it restarted and lost its in-memory sets. The service reacts by
// registering the set again.
var ErrTagSetNotFound = errors.New("tagger tag set not found")

// TaggerClient is the outbound transport to the tagger API. Implementations
// live in the web layer so this package stays free of HTTP details.
type TaggerClient interface {
	RegisterTagSet(ctx context.Context, name string, tags []TaggerTag) error
	Tag(ctx context.Context, name, text string) (string, error)
}

// tagJob is one queued record-to-tag pairing.
type tagJob struct {
	recordID uint
	text     string
}

// TaggerService tags newly created records by calling an external tagger API.
// Work is queued by Enqueue and processed asynchronously by a single worker
// started with Run, so ticket creation never blocks on the tagger. Tag sets
// live in memory on the tagger side, so the set is registered on boot and again
// whenever the tagger reports it is missing.
type TaggerService struct {
	client     TaggerClient
	tagSetName string
	tagSvc     *RecordTagService
	db         *gorm.DB
	jobs       chan tagJob
	registered bool
}

// NewTaggerService builds a tagger worker. client performs the actual talk to
// the tagger; db and tagSvc resolve a matched tag name back to a
// clinic_record_tag id.
func NewTaggerService(client TaggerClient, tagSetName string, db *gorm.DB, tagSvc *RecordTagService) *TaggerService {
	if tagSetName == "" {
		tagSetName = defaultTaggerTagSetName
	}
	return &TaggerService{
		client:     client,
		tagSetName: tagSetName,
		tagSvc:     tagSvc,
		db:         db,
		jobs:       make(chan tagJob, taggerQueueSize),
	}
}

// buildTags maps the in-memory record tag catalog onto tagger tag definitions.
// The default fallback tag and tags without an apply rule are skipped because
// the tagger API rejects blank rules.
func (s *TaggerService) buildTags() []TaggerTag {
	var tags []TaggerTag
	for _, t := range s.tagSvc.All() {
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

// RegisterTagSet registers (or replaces) the tag set built from the catalog.
func (s *TaggerService) RegisterTagSet(ctx context.Context) error {
	tags := s.buildTags()
	if len(tags) == 0 {
		return errors.New("tagger: no tags to register")
	}
	if err := s.client.RegisterTagSet(ctx, s.tagSetName, tags); err != nil {
		return err
	}
	s.registered = true
	return nil
}

// Tag applies the registered tag set to text and returns the matched tag name,
// or an empty string when no tag applies. When the tagger reports the set is
// missing (it is in-memory), the set is re-registered and the call retried once.
func (s *TaggerService) Tag(ctx context.Context, text string) (string, error) {
	if !s.registered {
		if err := s.RegisterTagSet(ctx); err != nil {
			return "", err
		}
	}

	name, err := s.client.Tag(ctx, s.tagSetName, text)
	if errors.Is(err, ErrTagSetNotFound) {
		if regErr := s.RegisterTagSet(ctx); regErr != nil {
			return "", fmt.Errorf("tagger: re-register tag set: %w", regErr)
		}
		return s.client.Tag(ctx, s.tagSetName, text)
	}
	return name, err
}

// Enqueue schedules a record for tagging. It never blocks the caller: if the
// queue is full the job is dropped and the record keeps its default tag.
func (s *TaggerService) Enqueue(recordID uint, text string) {
	if s == nil || strings.TrimSpace(text) == "" {
		return
	}
	select {
	case s.jobs <- tagJob{recordID: recordID, text: text}:
	default:
		log.Printf("tagger: queue full, dropping record %d", recordID)
	}
}

// Run processes queued jobs until ctx is cancelled. It is intended to be
// started as a single goroutine, which keeps the registered flag race-free.
func (s *TaggerService) Run(ctx context.Context) {
	if err := s.RegisterTagSet(ctx); err != nil {
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

func (s *TaggerService) process(ctx context.Context, job tagJob) {
	name, err := s.Tag(ctx, job.text)
	if err != nil {
		log.Printf("tagger: tag record %d failed: %v", job.recordID, err)
		return
	}
	if name == "" {
		return
	}

	tag, ok := s.tagSvc.ByTitle(name)
	if !ok {
		log.Printf("tagger: record %d matched unknown tag %q", job.recordID, name)
		return
	}
	if err := s.db.Model(&models.ClinicRecord{}).
		Where("id = ?", job.recordID).
		Update("tag_id", tag.ID).Error; err != nil {
		log.Printf("tagger: update record %d tag_id failed: %v", job.recordID, err)
	}
}
