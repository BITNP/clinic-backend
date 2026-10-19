package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"clinic-backend/services"
)

func TestGeneralTagger_RegisterTagSet(t *testing.T) {
	client := &fakeTaggerClient{}
	tagger := services.NewGeneralTagger(client, "clinic_test", "  test prompt  ", []services.TaggerTag{
		{Name: "tag_a", Description: "desc", ApplyRule: "rule"},
	})

	if err := tagger.RegisterTagSet(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}

	name, prompt, tags, registerHits, _ := client.snapshot()
	if name != "clinic_test" {
		t.Errorf("tag set name = %q, want clinic_test", name)
	}
	if prompt != "test prompt" {
		t.Errorf("prompt = %q, want trimmed prompt", prompt)
	}
	if registerHits != 1 {
		t.Errorf("register hits = %d, want 1", registerHits)
	}
	if len(tags) != 1 || tags[0].Name != "tag_a" {
		t.Errorf("tags = %+v, want tag_a", tags)
	}
}

func TestGeneralTagger_RegisterTagSetValidation(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		tags   []services.TaggerTag
	}{
		{"empty prompt", "   ", []services.TaggerTag{{Name: "tag"}}},
		{"empty tags", "prompt", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTaggerClient{}
			tagger := services.NewGeneralTagger(client, "clinic_test", tc.prompt, tc.tags)

			if err := tagger.RegisterTagSet(context.Background()); err == nil {
				t.Fatal("expected registration to fail")
			}
			_, _, _, registerHits, _ := client.snapshot()
			if registerHits != 0 {
				t.Errorf("client was called %d times for invalid input", registerHits)
			}
		})
	}
}

func TestGeneralTagger_TagRegistersOnFirstCall(t *testing.T) {
	client := &fakeTaggerClient{}
	client.tagFn = func(string) (string, error) { return "tag_a", nil }
	tagger := services.NewGeneralTagger(client, "clinic_test", "prompt", []services.TaggerTag{{Name: "tag_a"}})

	name, err := tagger.Tag(context.Background(), "text")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if name != "tag_a" {
		t.Errorf("name = %q, want tag_a", name)
	}

	_, _, _, registerHits, tagHits := client.snapshot()
	if registerHits != 1 {
		t.Errorf("register hits = %d, want 1", registerHits)
	}
	if tagHits != 1 {
		t.Errorf("tag hits = %d, want 1", tagHits)
	}
	if tagger.TagSetName() != "clinic_test" {
		t.Errorf("tag set name = %q, want clinic_test", tagger.TagSetName())
	}
}

func TestGeneralTagger_TagRetriesAfterNotFound(t *testing.T) {
	client := &fakeTaggerClient{}
	attempts := 0
	client.tagFn = func(string) (string, error) {
		attempts++
		if attempts == 1 {
			return "", services.ErrTagSetNotFound
		}
		return "tag_a", nil
	}
	tagger := services.NewGeneralTagger(client, "clinic_test", "prompt", []services.TaggerTag{{Name: "tag_a"}})
	if err := tagger.RegisterTagSet(context.Background()); err != nil {
		t.Fatalf("initial register: %v", err)
	}

	name, err := tagger.Tag(context.Background(), "text")
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if name != "tag_a" {
		t.Errorf("name = %q, want tag_a", name)
	}

	_, _, _, registerHits, tagHits := client.snapshot()
	if registerHits != 2 {
		t.Errorf("register hits = %d, want 2 (initial + after 404)", registerHits)
	}
	if tagHits != 2 {
		t.Errorf("tag hits = %d, want 2", tagHits)
	}
}

func TestGeneralTagger_PromptAndTags(t *testing.T) {
	tags := []services.TaggerTag{{Name: "tag_a", ApplyRule: "rule"}}
	tagger := services.NewGeneralTagger(&fakeTaggerClient{}, "clinic_test", "prompt", tags)

	if tagger.Prompt() != "prompt" {
		t.Errorf("prompt = %q, want prompt", tagger.Prompt())
	}
	got := tagger.Tags()
	if len(got) != 1 || got[0].Name != "tag_a" {
		t.Errorf("tags = %+v, want tag_a", got)
	}
	got[0].Name = "mutated"
	if tagger.Tags()[0].Name != "tag_a" {
		t.Error("Tags must return a copy")
	}
}

func TestGeneralTagger_RegisterError(t *testing.T) {
	client := &fakeTaggerClient{registerErr: errors.New("tagger down")}
	tagger := services.NewGeneralTagger(client, "clinic_test", "prompt", []services.TaggerTag{{Name: "tag_a"}})

	err := tagger.RegisterTagSet(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tagger down") {
		t.Fatalf("error = %v, want the client error", err)
	}
}
