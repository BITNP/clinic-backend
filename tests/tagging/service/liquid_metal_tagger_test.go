package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tagger "clinic-backend/internal/agent/tagger"
	taggingservice "clinic-backend/internal/tagging/service"
)

func newLiquidMetalTagger(t *testing.T, tagAgent taggingservice.TagAgent) *taggingservice.LiquidMetalTagger {
	t.Helper()
	set, err := taggingservice.LiquidMetalTagSet()
	if err != nil {
		t.Fatalf("liquid metal tag set: %v", err)
	}
	return taggingservice.NewLiquidMetalTagger(tagAgent, set)
}

func TestLiquidMetalTagSet(t *testing.T) {
	set, err := taggingservice.LiquidMetalTagSet()
	if err != nil {
		t.Fatalf("liquid metal tag set: %v", err)
	}
	if set.Name != "clinic_laptop_liquid_metal" {
		t.Errorf("tag set name = %q, want clinic_laptop_liquid_metal", set.Name)
	}
	if strings.Contains(set.Prompt, "{{TABLE}}") {
		t.Error("prompt still contains the {{TABLE}} placeholder")
	}
	for _, want := range []string{"液金机型表", "GX701L", "RTX 2080 SUPER", "【判定目标】", "【常见普通款边界】", "【判定示例】"} {
		if !strings.Contains(set.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	if len(set.Tags) != 6 {
		t.Fatalf("tags = %d, want 6", len(set.Tags))
	}
	byName := make(map[string]tagger.Tag, len(set.Tags))
	for _, tag := range set.Tags {
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
			t.Errorf("tag %q not found", want)
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
			agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
				return tagger.Result{Name: tc.tag, Reason: "reason"}, nil
			}}
			svc := newLiquidMetalTagger(t, agent)

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
	agent := &fakeTagAgent{}
	svc := newLiquidMetalTagger(t, agent)

	if _, err := svc.Classify(context.Background(), "   ", "", ""); !errors.Is(err, taggingservice.ErrLiquidMetalModelRequired) {
		t.Fatalf("error = %v, want ErrLiquidMetalModelRequired", err)
	}
	if got := agent.hitCount(); got != 0 {
		t.Errorf("tagger was called %d times for a blank model", got)
	}
}

func TestLiquidMetalTagger_ClassifyPropagatesAgentError(t *testing.T) {
	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{}, errors.New("agent down")
	}}
	svc := newLiquidMetalTagger(t, agent)

	if _, err := svc.Classify(context.Background(), "GX701L", "", ""); err == nil || !strings.Contains(err.Error(), "agent down") {
		t.Fatalf("error = %v, want agent error", err)
	}
}

func TestLiquidMetalTagger_ClassifySendsFormattedText(t *testing.T) {
	agent := &fakeTagAgent{tagFn: func(string) (tagger.Result, error) {
		return tagger.Result{Name: taggingservice.LiquidMetalTagCPU, Reason: "reason"}, nil
	}}
	svc := newLiquidMetalTagger(t, agent)

	if _, err := svc.Classify(context.Background(), " GA401Q ", " RTX 3060 ", " 2021 幻14 "); err != nil {
		t.Fatalf("classify: %v", err)
	}
	want := "型号: GA401Q\nGPU: RTX 3060\n其他信息: 2021 幻14"
	if got := agent.lastText(); got != want {
		t.Errorf("tag text = %q, want %q", got, want)
	}

	if _, err := svc.Classify(context.Background(), "GA401Q", "  ", ""); err != nil {
		t.Fatalf("classify without optional fields: %v", err)
	}
	if got, want := agent.lastText(), "型号: GA401Q"; got != want {
		t.Errorf("tag text = %q, want %q", got, want)
	}
}
