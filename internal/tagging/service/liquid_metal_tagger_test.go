package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	taggingservice "clinic-backend/internal/tagging/service"
)

func newLiquidMetalTagger(t *testing.T, client taggingservice.TaggerClient) *taggingservice.LiquidMetalTagger {
	t.Helper()
	svc, err := taggingservice.NewLiquidMetalTagger(client, "")
	if err != nil {
		t.Fatalf("new liquid metal tagger: %v", err)
	}
	return svc
}

func TestLiquidMetalTagger_RegisterTagSet(t *testing.T) {
	client := &fakeTaggerClient{}
	svc := newLiquidMetalTagger(t, client)

	if err := svc.RegisterTagSet(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}

	name, prompt, tags, registerHits, _ := client.snapshot()
	if name != "clinic_laptop_liquid_metal" {
		t.Errorf("tag set name = %q, want clinic_laptop_liquid_metal", name)
	}
	if registerHits != 1 {
		t.Errorf("register hits = %d, want 1", registerHits)
	}
	if strings.Contains(prompt, "{{TABLE}}") {
		t.Error("prompt still contains the {{TABLE}} placeholder")
	}
	for _, want := range []string{"液金机型表", "GX701L", "RTX 2080 SUPER", "【判定目标】", "【常见普通款边界】", "【判定示例】"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	if len(tags) != 6 {
		t.Fatalf("registered %d tags, want 6", len(tags))
	}
	byName := make(map[string]taggingservice.TaggerTag, len(tags))
	for _, tag := range tags {
		byName[tag.Name] = tag
	}
	for _, want := range []string{
		taggingservice.LiquidMetalTagCPU,
		taggingservice.LiquidMetalTagGPU,
		taggingservice.LiquidMetalTagCPUGPU,
		taggingservice.LiquidMetalTagToVerify,
		taggingservice.LiquidMetalTagNone,
		taggingservice.LiquidMetalTagUnmatched,
	} {
		tag, ok := byName[want]
		if !ok {
			t.Errorf("tag %q not registered", want)
			continue
		}
		if tag.ApplyRule == "" {
			t.Errorf("tag %q has an empty apply rule", want)
		}
	}
}

func TestLiquidMetalTagger_ClassifyMapsTags(t *testing.T) {
	cases := []struct {
		name      string
		tag       string
		status    taggingservice.LiquidMetalStatus
		component taggingservice.LiquidMetalComponent
		category  taggingservice.LiquidMetalCategory
	}{
		{"cpu", taggingservice.LiquidMetalTagCPU, taggingservice.LiquidMetalStatusYes, taggingservice.LiquidMetalComponentCPU, taggingservice.LiquidMetalCategoryConfirmed},
		{"gpu", taggingservice.LiquidMetalTagGPU, taggingservice.LiquidMetalStatusYes, taggingservice.LiquidMetalComponentGPU, taggingservice.LiquidMetalCategoryConfirmed},
		{"cpu and gpu", taggingservice.LiquidMetalTagCPUGPU, taggingservice.LiquidMetalStatusYes, taggingservice.LiquidMetalComponentCPUGPU, taggingservice.LiquidMetalCategoryConfirmed},
		{"to verify", taggingservice.LiquidMetalTagToVerify, taggingservice.LiquidMetalStatusVerify, taggingservice.LiquidMetalComponentUnknown, taggingservice.LiquidMetalCategoryToVerify},
		{"none", taggingservice.LiquidMetalTagNone, taggingservice.LiquidMetalStatusNo, taggingservice.LiquidMetalComponentNone, taggingservice.LiquidMetalCategoryConfusable},
		{"unmatched", taggingservice.LiquidMetalTagUnmatched, taggingservice.LiquidMetalStatusUnknown, taggingservice.LiquidMetalComponentUnknown, taggingservice.LiquidMetalCategoryUnmatched},
		{"empty", "", taggingservice.LiquidMetalStatusUnknown, taggingservice.LiquidMetalComponentUnknown, taggingservice.LiquidMetalCategoryUnmatched},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTaggerClient{}
			client.tagFn = func(string) (string, error) { return tc.tag, nil }
			svc := newLiquidMetalTagger(t, client)

			got, err := svc.Classify(context.Background(), "GX701L", "RTX 2080 SUPER", "")
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			if got.Status != tc.status || got.Component != tc.component || got.Category != tc.category {
				t.Errorf("result = %+v, want status=%s component=%s category=%s",
					got, tc.status, tc.component, tc.category)
			}
		})
	}
}

func TestLiquidMetalStatusIndexRoundTrip(t *testing.T) {
	cases := []struct {
		status taggingservice.LiquidMetalStatus
		index  uint
	}{
		{taggingservice.LiquidMetalStatusUnknown, 0},
		{taggingservice.LiquidMetalStatusYes, 1},
		{taggingservice.LiquidMetalStatusVerify, 2},
		{taggingservice.LiquidMetalStatusNo, 3},
	}
	for _, tc := range cases {
		if got := taggingservice.LiquidMetalStatusIndex(tc.status); got != tc.index {
			t.Errorf("index of %q = %d, want %d", tc.status, got, tc.index)
		}
		if got := taggingservice.LiquidMetalStatusFromIndex(tc.index); got != tc.status {
			t.Errorf("status at index %d = %q, want %q", tc.index, got, tc.status)
		}
	}
	if got := taggingservice.LiquidMetalStatusFromIndex(99); got != taggingservice.LiquidMetalStatusUnknown {
		t.Errorf("out-of-range index = %q, want unknown", got)
	}
	if got := taggingservice.LiquidMetalStatusIndex("not-a-status"); got != 0 {
		t.Errorf("unknown status index = %d, want 0", got)
	}
}

func TestLiquidMetalTagger_ClassifyRequiresModel(t *testing.T) {
	client := &fakeTaggerClient{}
	svc := newLiquidMetalTagger(t, client)

	if _, err := svc.Classify(context.Background(), "   ", "", ""); !errors.Is(err, taggingservice.ErrLiquidMetalModelRequired) {
		t.Fatalf("error = %v, want ErrLiquidMetalModelRequired", err)
	}
	_, _, _, _, tagHits := client.snapshot()
	if tagHits != 0 {
		t.Errorf("tagger was called %d times for a blank model", tagHits)
	}
}

func TestLiquidMetalTagger_ClassifyRetriesAfterNotFound(t *testing.T) {
	client := &fakeTaggerClient{}
	attempts := 0
	client.tagFn = func(string) (string, error) {
		attempts++
		if attempts == 1 {
			return "", taggingservice.ErrTagSetNotFound
		}
		return taggingservice.LiquidMetalTagCPU, nil
	}
	svc := newLiquidMetalTagger(t, client)

	got, err := svc.Classify(context.Background(), "GX701L", "", "")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Tag != taggingservice.LiquidMetalTagCPU {
		t.Errorf("tag = %q, want %q", got.Tag, taggingservice.LiquidMetalTagCPU)
	}
	_, _, _, registerHits, tagHits := client.snapshot()
	if registerHits != 2 {
		t.Errorf("register hits = %d, want 2 (initial + after 404)", registerHits)
	}
	if tagHits != 2 {
		t.Errorf("tag hits = %d, want 2", tagHits)
	}
}

// recordingTaggerClient records the text passed to Tag.
type recordingTaggerClient struct {
	fakeTaggerClient
	lastText string
}

func (c *recordingTaggerClient) Tag(ctx context.Context, name, text string) (string, error) {
	c.lastText = text
	return c.fakeTaggerClient.Tag(ctx, name, text)
}

func TestLiquidMetalTagger_ClassifySendsFormattedText(t *testing.T) {
	client := &recordingTaggerClient{}
	client.tagFn = func(string) (string, error) { return taggingservice.LiquidMetalTagCPU, nil }
	svc := newLiquidMetalTagger(t, client)

	if _, err := svc.Classify(context.Background(), " GA401Q ", " RTX 3060 ", " 2021 幻14 "); err != nil {
		t.Fatalf("classify: %v", err)
	}
	want := "型号: GA401Q\nGPU: RTX 3060\n其他信息: 2021 幻14"
	if client.lastText != want {
		t.Errorf("tag text = %q, want %q", client.lastText, want)
	}

	if _, err := svc.Classify(context.Background(), "GA401Q", "  ", ""); err != nil {
		t.Fatalf("classify without optional fields: %v", err)
	}
	if want := "型号: GA401Q"; client.lastText != want {
		t.Errorf("tag text = %q, want %q", client.lastText, want)
	}
}
