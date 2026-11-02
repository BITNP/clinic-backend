package tagger_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tagger "clinic-backend/internal/agent/tagger"

	"github.com/cloudwego/eino/schema"
)

func newTaggerWithGenerate(t *testing.T, gen generateFunc) (*tagger.Tagger, *fakeChatModel) {
	t.Helper()
	m := &fakeChatModel{}
	m.setGenerate(gen)
	tg, err := tagger.NewTagger(m, testTagSet(), 10000)
	if err != nil {
		t.Fatalf("new tagger: %v", err)
	}
	return tg, m
}

func TestTaggerTag(t *testing.T) {
	tg, model := newTaggerWithGenerate(t, func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantMessage(`{"name":"tag_a","reason":"matches a"}`), nil
	})

	got, err := tg.Tag(context.Background(), "  hello  ")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if got.Name != "tag_a" || got.Reason != "matches a" {
		t.Errorf("result = %+v, want tag_a with reason", got)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 1 {
		t.Fatalf("model calls = %d, want 1", len(inputs))
	}
	if len(inputs[0]) != 2 {
		t.Fatalf("messages = %d, want system + user", len(inputs[0]))
	}
	if inputs[0][0].Role != schema.System {
		t.Errorf("first role = %q, want system", inputs[0][0].Role)
	}
	for _, want := range []string{
		"You are a precise text tagger.",
		"Tag set: clinic_test",
		"Set instructions: classify test records",
		"1. name: tag_a",
		"description: first tag",
		"apply_rule: when a applies",
		"2. name: tag_b",
		"apply_rule: when b applies",
	} {
		if !strings.Contains(inputs[0][0].Content, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	if want := "Text to tag:\nhello"; inputs[0][1].Content != want {
		t.Errorf("user message = %q, want %q", inputs[0][1].Content, want)
	}
}

func TestTaggerTagNormalizesResult(t *testing.T) {
	cases := []struct {
		name       string
		response   string
		wantName   string
		wantReason string
		wantErr    bool
	}{
		{"known tag", `{"name":"tag_a","reason":"because a"}`, "tag_a", "because a", false},
		{"unknown tag", `{"name":"missing","reason":"because"}`, "", "", false},
		{"empty name", `{"name":"","reason":"because"}`, "", "", false},
		{"markdown fenced", "```json\n{\"name\":\"tag_b\",\"reason\":\"because b\"}\n```", "tag_b", "because b", false},
		{"missing reason", `{"name":"tag_a"}`, "", "", true},
		{"invalid json", `not json`, "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg, _ := newTaggerWithGenerate(t, func(context.Context, []*schema.Message) (*schema.Message, error) {
				return assistantMessage(tc.response), nil
			})

			got, err := tg.Tag(context.Background(), "text")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("tag: %v", err)
			}
			if got.Name != tc.wantName || got.Reason != tc.wantReason {
				t.Errorf("result = %+v, want name=%q reason=%q", got, tc.wantName, tc.wantReason)
			}
		})
	}
}

func TestTaggerTagRollsBackOnModelError(t *testing.T) {
	tg, model := newTaggerWithGenerate(t, func(context.Context, []*schema.Message) (*schema.Message, error) {
		return nil, errors.New("model down")
	})

	if _, err := tg.Tag(context.Background(), "failed"); err == nil || !strings.Contains(err.Error(), "model down") {
		t.Fatalf("error = %v, want model error", err)
	}

	model.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantMessage(`{"name":"tag_a","reason":"reason"}`), nil
	})
	if _, err := tg.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	inputs := model.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("second call messages = %d, want system + second user only", len(inputs[1]))
	}
}

func TestTaggerSessionResetAndUsage(t *testing.T) {
	m := &fakeChatModel{}
	m.setGenerate(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return assistantWithUsage(`{"name":"tag_a","reason":"reason"}`, 100, 40), nil
	})
	tg, err := tagger.NewTagger(m, testTagSet(), 100)
	if err != nil {
		t.Fatalf("new tagger: %v", err)
	}

	if _, err := tg.Tag(context.Background(), "first"); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if !tg.IsExhausted() {
		t.Error("session should be exhausted at the token limit")
	}
	if tg.Tokens() != 100 {
		t.Errorf("tokens = %d, want 100", tg.Tokens())
	}
	if tg.CachedTokens() != 40 {
		t.Errorf("cached tokens = %d, want 40", tg.CachedTokens())
	}

	tg.Reset()
	if tg.IsExhausted() {
		t.Error("session should not be exhausted after reset")
	}
	if tg.Tokens() != 0 || tg.CachedTokens() != 0 {
		t.Errorf("tokens/cached after reset = %d/%d, want 0/0", tg.Tokens(), tg.CachedTokens())
	}

	if _, err := tg.Tag(context.Background(), "second"); err != nil {
		t.Fatalf("second tag: %v", err)
	}
	inputs := m.inputsSnapshot()
	if len(inputs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inputs))
	}
	if len(inputs[1]) != 2 {
		t.Errorf("messages after reset = %d, want system + user", len(inputs[1]))
	}
}

func TestTaggerTrimsTagSet(t *testing.T) {
	set := tagger.TagSet{
		Name:   "  clinic_test  ",
		Prompt: "  classify test records  ",
		Tags: []tagger.Tag{
			{Name: "  tag_a  ", Description: "  first  ", ApplyRule: "  when a applies  "},
		},
	}
	m := &fakeChatModel{}
	tg, err := tagger.NewTagger(m, set, 10000)
	if err != nil {
		t.Fatalf("new tagger: %v", err)
	}

	got, err := tg.Tag(context.Background(), "text")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if got.Name != "tag_a" {
		t.Errorf("name = %q, want trimmed tag_a", got.Name)
	}

	inputs := m.inputsSnapshot()
	prompt := inputs[0][0].Content
	for _, want := range []string{
		"Tag set: clinic_test",
		"Set instructions: classify test records",
		"1. name: tag_a",
		"description: first",
		"apply_rule: when a applies",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	for _, unwanted := range []string{"  clinic_test", "  tag_a", "  when a applies"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("system prompt still contains untrimmed %q", unwanted)
		}
	}
}

func TestTaggerValidation(t *testing.T) {
	if _, err := tagger.NewTagger(nil, testTagSet(), 1000); err == nil {
		t.Error("expected an error for a nil chat model")
	}
	if _, err := tagger.NewTagger(&fakeChatModel{}, testTagSet(), 0); err == nil {
		t.Error("expected an error for a zero token limit")
	}

	base := testTagSet()
	cases := []struct {
		name string
		set  tagger.TagSet
	}{
		{"empty name", func() tagger.TagSet { s := base; s.Name = " "; return s }()},
		{"empty prompt", func() tagger.TagSet { s := base; s.Prompt = " "; return s }()},
		{"no tags", func() tagger.TagSet { s := base; s.Tags = nil; return s }()},
		{"empty tag name", func() tagger.TagSet {
			s := base
			s.Tags = []tagger.Tag{{Name: " ", ApplyRule: "rule"}}
			return s
		}()},
		{"empty apply rule", func() tagger.TagSet {
			s := base
			s.Tags = []tagger.Tag{{Name: "tag", ApplyRule: " "}}
			return s
		}()},
		{"duplicate tag", func() tagger.TagSet {
			s := base
			s.Tags = []tagger.Tag{
				{Name: "tag", ApplyRule: "one"},
				{Name: "tag", ApplyRule: "two"},
			}
			return s
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tagger.NewTagger(&fakeChatModel{}, tc.set, 1000); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
