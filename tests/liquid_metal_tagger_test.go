package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"clinic-backend/services"
)

func newLiquidMetalTagger(t *testing.T, client services.TaggerClient) *services.LiquidMetalTagger {
	t.Helper()
	svc, err := services.NewLiquidMetalTagger(client, "")
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
	for _, want := range []string{"液金机型表", "GX701L", "RTX 2080 SUPER", "同系列", "完全没有任何相关线索"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	if len(tags) != 6 {
		t.Fatalf("registered %d tags, want 6", len(tags))
	}
	byName := make(map[string]services.TaggerTag, len(tags))
	for _, tag := range tags {
		byName[tag.Name] = tag
	}
	for _, want := range []string{
		services.LiquidMetalTagCPU,
		services.LiquidMetalTagGPU,
		services.LiquidMetalTagCPUGPU,
		services.LiquidMetalTagToVerify,
		services.LiquidMetalTagNone,
		services.LiquidMetalTagUnmatched,
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
		status    services.LiquidMetalStatus
		component services.LiquidMetalComponent
		category  services.LiquidMetalCategory
	}{
		{"cpu", services.LiquidMetalTagCPU, services.LiquidMetalStatusYes, services.LiquidMetalComponentCPU, services.LiquidMetalCategoryConfirmed},
		{"gpu", services.LiquidMetalTagGPU, services.LiquidMetalStatusYes, services.LiquidMetalComponentGPU, services.LiquidMetalCategoryConfirmed},
		{"cpu and gpu", services.LiquidMetalTagCPUGPU, services.LiquidMetalStatusYes, services.LiquidMetalComponentCPUGPU, services.LiquidMetalCategoryConfirmed},
		{"to verify", services.LiquidMetalTagToVerify, services.LiquidMetalStatusVerify, services.LiquidMetalComponentUnknown, services.LiquidMetalCategoryToVerify},
		{"none", services.LiquidMetalTagNone, services.LiquidMetalStatusNo, services.LiquidMetalComponentNone, services.LiquidMetalCategoryConfusable},
		{"unmatched", services.LiquidMetalTagUnmatched, services.LiquidMetalStatusUnknown, services.LiquidMetalComponentUnknown, services.LiquidMetalCategoryUnmatched},
		{"empty", "", services.LiquidMetalStatusUnknown, services.LiquidMetalComponentUnknown, services.LiquidMetalCategoryUnmatched},
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
		status services.LiquidMetalStatus
		index  uint
	}{
		{services.LiquidMetalStatusUnknown, 0},
		{services.LiquidMetalStatusYes, 1},
		{services.LiquidMetalStatusVerify, 2},
		{services.LiquidMetalStatusNo, 3},
	}
	for _, tc := range cases {
		if got := services.LiquidMetalStatusIndex(tc.status); got != tc.index {
			t.Errorf("index of %q = %d, want %d", tc.status, got, tc.index)
		}
		if got := services.LiquidMetalStatusFromIndex(tc.index); got != tc.status {
			t.Errorf("status at index %d = %q, want %q", tc.index, got, tc.status)
		}
	}
	if got := services.LiquidMetalStatusFromIndex(99); got != services.LiquidMetalStatusUnknown {
		t.Errorf("out-of-range index = %q, want unknown", got)
	}
	if got := services.LiquidMetalStatusIndex("not-a-status"); got != 0 {
		t.Errorf("unknown status index = %d, want 0", got)
	}
}

func TestLiquidMetalTagger_ClassifyRequiresModel(t *testing.T) {
	client := &fakeTaggerClient{}
	svc := newLiquidMetalTagger(t, client)

	if _, err := svc.Classify(context.Background(), "   ", "", ""); !errors.Is(err, services.ErrLiquidMetalModelRequired) {
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
			return "", services.ErrTagSetNotFound
		}
		return services.LiquidMetalTagCPU, nil
	}
	svc := newLiquidMetalTagger(t, client)

	got, err := svc.Classify(context.Background(), "GX701L", "", "")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Tag != services.LiquidMetalTagCPU {
		t.Errorf("tag = %q, want %q", got.Tag, services.LiquidMetalTagCPU)
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
	client.tagFn = func(string) (string, error) { return services.LiquidMetalTagCPU, nil }
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
