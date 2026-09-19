package tests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"clinic-backend/models"
	"clinic-backend/services"

	"gorm.io/gorm"
)

// fakeTaggerClient records the calls made by TaggerService and lets each test
// script the behavior of Tag.
type fakeTaggerClient struct {
	mu           sync.Mutex
	registerName string
	registerPrmt string
	registered   []services.TaggerTag
	registerHits int
	registerErr  error
	tagHits      int
	tagFn        func(text string) (string, error)
}

func (f *fakeTaggerClient) RegisterTagSet(_ context.Context, name, prompt string, tags []services.TaggerTag) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerHits++
	f.registerName = name
	f.registerPrmt = prompt
	f.registered = tags
	return f.registerErr
}

func (f *fakeTaggerClient) Tag(_ context.Context, _, text string) (string, error) {
	f.mu.Lock()
	f.tagHits++
	fn := f.tagFn
	f.mu.Unlock()
	if fn != nil {
		return fn(text)
	}
	return "", nil
}

func (f *fakeTaggerClient) snapshot() (name, prompt string, tags []services.TaggerTag, registerHits, tagHits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerName, f.registerPrmt, f.registered, f.registerHits, f.tagHits
}

// seedTaggerTags loads the default and catalog tags into a fresh in-memory DB
// and returns the database and loaded tag service.
func seedTaggerTags(t *testing.T) (*gorm.DB, *services.RecordTagService) {
	t.Helper()
	db := setupTagTestDB(t)
	tagSvc := services.NewRecordTagService(db)
	if _, err := tagSvc.SeedDefault(); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := tagSvc.SeedCatalog(); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if err := tagSvc.SeedPrompt(); err != nil {
		t.Fatalf("seed prompt: %v", err)
	}
	if err := tagSvc.Load(); err != nil {
		t.Fatalf("load tags: %v", err)
	}
	return db, tagSvc
}

func TestTaggerService_RegisterTagSet(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	client := &fakeTaggerClient{}
	svc := services.NewTaggerService(client, "clinic_record", nil, tagSvc)

	if err := svc.RegisterTagSet(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}

	name, prompt, tags, _, _ := client.snapshot()
	if name != "clinic_record" {
		t.Errorf("expected set name clinic_record, got %q", name)
	}
	if prompt != tagSvc.Prompt() || prompt == "" {
		t.Errorf("expected the loaded prompt to be registered, got %q", prompt)
	}
	if len(tags) != 16 {
		t.Errorf("expected 16 tags, got %d", len(tags))
	}
	byName := make(map[string]services.TaggerTag, len(tags))
	for _, tag := range tags {
		byName[tag.Name] = tag
	}
	if _, ok := byName["其它问题"]; !ok {
		t.Error("其它问题 now has an apply rule and must be registered")
	}
	if _, ok := byName[services.DefaultRecordTagTitle]; ok {
		t.Error("default no_pending tag must be skipped")
	}
	if tag := byName["G15"]; tag.ApplyRule == "" || strings.HasPrefix(tag.ApplyRule, "model:") {
		t.Errorf("expected a natural-language G15 rule, got %q", tag.ApplyRule)
	}
}

func TestTaggerService_Tag_RetriesAfterNotFound(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	client := &fakeTaggerClient{}
	attempts := 0
	client.tagFn = func(string) (string, error) {
		attempts++
		if attempts == 1 {
			return "", services.ErrTagSetNotFound
		}
		return "风扇故障", nil
	}
	svc := services.NewTaggerService(client, "clinic_record", nil, tagSvc)

	name, err := svc.Tag(context.Background(), "风扇异响")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if name != "风扇故障" {
		t.Errorf("expected 风扇故障, got %q", name)
	}
	_, _, _, registerHits, tagHits := client.snapshot()
	if registerHits != 2 {
		t.Errorf("expected tag set registered twice (initial + after 404), got %d", registerHits)
	}
	if tagHits != 2 {
		t.Errorf("expected tag retried once, got %d calls", tagHits)
	}
}

func TestTaggerService_WorkerAssignsTag(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	matched, ok := tagSvc.ByTitle("风扇故障")
	if !ok {
		t.Fatal("expected 风扇故障 tag in catalog")
	}

	client := &fakeTaggerClient{}
	client.tagFn = func(string) (string, error) { return "风扇故障", nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := services.NewTaggerService(client, "clinic_record", db, tagSvc)
	go tagger.Run(ctx)

	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	svc := services.NewTicketService(db, nil)
	svc.SetDefaultTagID(tagSvc.DefaultID())
	svc.SetTagger(tagger)

	rec, err := svc.Create(services.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: futureTruncatedDate(7),
		Description:     "风扇一直响",
		Model:           "G15",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var got models.ClinicRecord
		if err := db.First(&got, rec.ID).Error; err != nil {
			t.Fatalf("reload record: %v", err)
		}
		if got.TagID == matched.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tag_id not updated: got %d, want %d", got.TagID, matched.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTaggerService_WorkerKeepsDefaultOnError(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	def, ok := tagSvc.ByTitle(services.DefaultRecordTagTitle)
	if !ok {
		t.Fatal("expected default tag")
	}

	called := make(chan struct{}, 1)
	client := &fakeTaggerClient{}
	client.tagFn = func(string) (string, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return "", errors.New("tagger down")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := services.NewTaggerService(client, "clinic_record", db, tagSvc)
	go tagger.Run(ctx)

	seedOpenServiceDate(t, db, time.UTC, futureTruncatedDate(7), 5)
	svc := services.NewTicketService(db, nil)
	svc.SetDefaultTagID(def.ID)
	svc.SetTagger(tagger)

	rec, err := svc.Create(services.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: futureTruncatedDate(7),
		Description:     "unknown problem",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("tagger was never called")
	}

	var got models.ClinicRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if got.TagID != def.ID {
		t.Errorf("expected default tag %d, got %d", def.ID, got.TagID)
	}
}

func TestTaggerService_RunRegistersWhenUnregistered(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	client := &fakeTaggerClient{}
	svc := services.NewTaggerService(client, "clinic_record", nil, tagSvc)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.Run(ctx)

	_, _, _, registerHits, _ := client.snapshot()
	if registerHits != 1 {
		t.Errorf("expected the tag set registered once, got %d", registerHits)
	}
}

func TestTaggerService_RunSkipsRegistrationWhenAlreadyRegistered(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	client := &fakeTaggerClient{}
	svc := services.NewTaggerService(client, "clinic_record", nil, tagSvc)

	if err := svc.RegisterTagSet(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.Run(ctx)

	_, _, _, registerHits, _ := client.snapshot()
	if registerHits != 1 {
		t.Errorf("expected no redundant registration, got %d", registerHits)
	}
}
