package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tagger "clinic-backend/internal/agent/tagger"
	recordmodel "clinic-backend/internal/record/model"
	recordservice "clinic-backend/internal/record/service"
	taggingservice "clinic-backend/internal/tagging/service"
	"clinic-backend/internal/testutil"

	"gorm.io/gorm"
)

// fakeTagAgent records the calls made by the in-process taggers and lets each
// test script the behavior of Tag.
type fakeTagAgent struct {
	mu        sync.Mutex
	hits      int
	lastInput string
	tagFn     func(text string) (tagger.Result, error)
}

func (f *fakeTagAgent) Tag(_ context.Context, text string) (tagger.Result, error) {
	f.mu.Lock()
	f.hits++
	f.lastInput = text
	fn := f.tagFn
	f.mu.Unlock()
	if fn != nil {
		return fn(text)
	}
	return tagger.Result{Name: "tag_a", Reason: "reason"}, nil
}

func (f *fakeTagAgent) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func (f *fakeTagAgent) lastText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastInput
}

// seedTaggerTags loads the default and catalog tags into a fresh in-memory DB
// and returns the database and loaded tag service.
func seedTaggerTags(t *testing.T) (*gorm.DB, *taggingservice.RecordTagService) {
	t.Helper()
	db := testutil.SetupTagTestDB(t)
	tagSvc := taggingservice.NewRecordTagService(db)
	if _, err := tagSvc.SeedDefault(); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := tagSvc.SeedCatalog(); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if err := tagSvc.Load(); err != nil {
		t.Fatalf("load tags: %v", err)
	}
	return db, tagSvc
}

func TestRecordTagSet(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	set := taggingservice.RecordTagSet(tagSvc)

	if set.Name != "clinic_record" {
		t.Errorf("tag set name = %q, want clinic_record", set.Name)
	}
	if set.Prompt != strings.TrimSpace(tagSvc.Prompt()) || set.Prompt == "" {
		t.Errorf("expected the embedded prompt, got %q", set.Prompt)
	}
	if len(set.Tags) != 16 {
		t.Errorf("expected 16 tags, got %d", len(set.Tags))
	}
	byName := make(map[string]tagger.Tag, len(set.Tags))
	for _, tag := range set.Tags {
		byName[tag.Name] = tag
	}
	if _, ok := byName["其它问题"]; !ok {
		t.Error("其它问题 now has an apply rule and must be included")
	}
	if _, ok := byName[taggingservice.DefaultRecordTagTitle]; ok {
		t.Error("default no_pending tag must be skipped")
	}
	if tag := byName["G15"]; tag.ApplyRule == "" || strings.HasPrefix(tag.ApplyRule, "model:") {
		t.Errorf("expected a natural-language G15 rule, got %q", tag.ApplyRule)
	}
}

func TestRecordTagger_TagUsesAgent(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: "风扇故障", Reason: "fan issue"}, nil
	}}
	svc := taggingservice.NewRecordTagger(agent, nil, tagSvc)

	name, err := svc.Tag(context.Background(), "风扇异响")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if name != "风扇故障" {
		t.Errorf("name = %q, want 风扇故障", name)
	}
	if got := agent.lastText(); got != "风扇异响" {
		t.Errorf("agent text = %q, want 风扇异响", got)
	}
}

func TestRecordTagger_TagPropagatesAgentError(t *testing.T) {
	_, tagSvc := seedTaggerTags(t)
	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{}, errors.New("agent down")
	}}
	svc := taggingservice.NewRecordTagger(agent, nil, tagSvc)

	if _, err := svc.Tag(context.Background(), "text"); err == nil || !strings.Contains(err.Error(), "agent down") {
		t.Fatalf("error = %v, want agent error", err)
	}
}

func TestRecordTagger_WorkerAssignsTag(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	matched, ok := tagSvc.ByTitle("风扇故障")
	if !ok {
		t.Fatal("expected 风扇故障 tag in catalog")
	}

	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: "风扇故障", Reason: "fan issue"}, nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := taggingservice.NewRecordTagger(agent, db, tagSvc)
	go tagger.Run(ctx)

	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)
	svc.SetDefaultTagID(tagSvc.DefaultID())
	svc.SetTagger(tagger)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
		Description:     "风扇一直响",
		Model:           "G15",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var got recordmodel.ClinicRecord
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

func TestRecordTagger_WorkerKeepsDefaultOnError(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	def, ok := tagSvc.ByTitle(taggingservice.DefaultRecordTagTitle)
	if !ok {
		t.Fatal("expected default tag")
	}

	called := make(chan struct{}, 1)
	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return tagger.Result{}, errors.New("tagger down")
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := taggingservice.NewRecordTagger(agent, db, tagSvc)
	go tagger.Run(ctx)

	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)
	svc.SetDefaultTagID(def.ID)
	svc.SetTagger(tagger)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
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

	var got recordmodel.ClinicRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if got.TagID != def.ID {
		t.Errorf("expected default tag %d, got %d", def.ID, got.TagID)
	}
}

func TestRecordTagger_RunsConfiguredWorkers(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	matched, ok := tagSvc.ByTitle("风扇故障")
	if !ok {
		t.Fatal("expected 风扇故障 tag in catalog")
	}

	var mu sync.Mutex
	active := 0
	maxActive := 0
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{}, 2)
	var bothOnce sync.Once
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }

	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		if active == 2 {
			bothOnce.Do(func() { close(bothStarted) })
		}
		mu.Unlock()

		<-release

		mu.Lock()
		active--
		mu.Unlock()
		done <- struct{}{}
		return tagger.Result{Name: "风扇故障", Reason: "fan issue"}, nil
	}}

	first := recordmodel.ClinicRecord{
		Realname:        "r1",
		PhoneNum:        "p1",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: time.Now(),
		QuestionDesc:    "first",
		RoomID:          1,
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first record: %v", err)
	}
	second := recordmodel.ClinicRecord{
		Realname:        "r2",
		PhoneNum:        "p2",
		Status:          recordmodel.RecordStatusPending,
		AppointmentTime: time.Now(),
		QuestionDesc:    "second",
		RoomID:          1,
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second record: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	tagger := taggingservice.NewRecordTagger(agent, db, tagSvc)
	tagger.SetWorkers(2)
	go func() {
		defer close(runDone)
		tagger.Run(ctx)
	}()
	defer func() {
		releaseAll()
		cancel()
		<-runDone
	}()

	tagger.Enqueue(first.ID, "first", "", "")
	tagger.Enqueue(second.ID, "second", "", "")

	select {
	case <-bothStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("two workers did not run concurrently")
	}
	releaseAll()

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for workers")
		}
	}

	mu.Lock()
	gotMax := maxActive
	mu.Unlock()
	if gotMax != 2 {
		t.Errorf("max active agents = %d, want 2", gotMax)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int64
		if err := db.Model(&recordmodel.ClinicRecord{}).
			Where("id IN ?", []uint{first.ID, second.ID}).
			Where("tag_id = ?", matched.ID).
			Count(&count).Error; err != nil {
			t.Fatalf("count tagged records: %v", err)
		}
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tagged records = %d, want 2", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRecordTagger_WorkerAssignsTagAndLiquidMetalStatus(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	matched, ok := tagSvc.ByTitle("风扇故障")
	if !ok {
		t.Fatal("expected 风扇故障 tag in catalog")
	}

	recordAgent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: "风扇故障", Reason: "fan issue"}, nil
	}}
	liquidAgent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: taggingservice.LiquidMetalTagCPU, Reason: "cpu"}, nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := taggingservice.NewRecordTagger(recordAgent, db, tagSvc)
	tagger.SetLiquidMetalTagger(newLiquidMetalTagger(t, liquidAgent))
	go tagger.Run(ctx)

	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)
	svc.SetDefaultTagID(tagSvc.DefaultID())
	svc.SetTagger(tagger)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
		Description:     "风扇一直响",
		Model:           "GX701L",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	wantStatus := taggingservice.LiquidMetalStatusIndex(taggingservice.LiquidMetalStatusYes)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got recordmodel.ClinicRecord
		if err := db.First(&got, rec.ID).Error; err != nil {
			t.Fatalf("reload record: %v", err)
		}
		if got.TagID == matched.ID && got.LiquidMetalStatusIndex == wantStatus {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record not tagged: tag_id=%d want %d, liquid_metal_status=%d want %d",
				got.TagID, matched.ID, got.LiquidMetalStatusIndex, wantStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}

	text := liquidAgent.lastText()
	if !strings.Contains(text, "型号: GX701L") || !strings.Contains(text, "其他信息: 风扇一直响") {
		t.Errorf("liquid metal text = %q, want model and question description", text)
	}
}

func TestRecordTagger_LiquidMetalRunsWhenRecordTagFails(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	def, ok := tagSvc.ByTitle(taggingservice.DefaultRecordTagTitle)
	if !ok {
		t.Fatal("expected default tag")
	}

	recordAgent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{}, errors.New("record tagger down")
	}}
	liquidAgent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: taggingservice.LiquidMetalTagCPU, Reason: "cpu"}, nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := taggingservice.NewRecordTagger(recordAgent, db, tagSvc)
	tagger.SetLiquidMetalTagger(newLiquidMetalTagger(t, liquidAgent))
	go tagger.Run(ctx)

	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)
	svc.SetDefaultTagID(def.ID)
	svc.SetTagger(tagger)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
		Description:     "风扇一直响",
		Model:           "GX701L",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	wantStatus := taggingservice.LiquidMetalStatusIndex(taggingservice.LiquidMetalStatusYes)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got recordmodel.ClinicRecord
		if err := db.First(&got, rec.ID).Error; err != nil {
			t.Fatalf("reload record: %v", err)
		}
		if got.LiquidMetalStatusIndex == wantStatus {
			if got.TagID != def.ID {
				t.Errorf("tag_id = %d, want default %d", got.TagID, def.ID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("liquid_metal_status = %d, want %d", got.LiquidMetalStatusIndex, wantStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRecordTagger_NoModelSkipsLiquidMetal(t *testing.T) {
	db, tagSvc := seedTaggerTags(t)
	matched, ok := tagSvc.ByTitle("风扇故障")
	if !ok {
		t.Fatal("expected 风扇故障 tag in catalog")
	}

	recordAgent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: "风扇故障", Reason: "fan issue"}, nil
	}}
	liquidAgent := &fakeTagAgent{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tagger := taggingservice.NewRecordTagger(recordAgent, db, tagSvc)
	tagger.SetLiquidMetalTagger(newLiquidMetalTagger(t, liquidAgent))
	go tagger.Run(ctx)

	testutil.SeedOpenServiceDate(t, db, time.UTC, testutil.FutureTruncatedDate(7), 5)
	svc := recordservice.NewTicketService(db, nil)
	svc.SetDefaultTagID(tagSvc.DefaultID())
	svc.SetTagger(tagger)

	rec, err := svc.Create(recordservice.CreateTicketInput{
		User:            "u",
		Realname:        "r",
		PhoneNum:        "p",
		Campus:          "中关村",
		AppointmentTime: testutil.FutureTruncatedDate(7),
		Description:     "风扇一直响",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var got recordmodel.ClinicRecord
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

	if hits := liquidAgent.hitCount(); hits != 0 {
		t.Errorf("liquid metal tagger called %d times without a model", hits)
	}
	got := recordmodel.ClinicRecord{}
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if got.LiquidMetalStatusIndex != taggingservice.LiquidMetalStatusIndex(taggingservice.LiquidMetalStatusUnknown) {
		t.Errorf("liquid_metal_status = %d, want unknown", got.LiquidMetalStatusIndex)
	}
}
