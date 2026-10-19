package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// TaggerTag is a tag definition registered with the tagger.
type TaggerTag struct {
	Name        string
	Description string
	ApplyRule   string
}

// ErrTagSetNotFound reports that the tagger does not know the tag set yet, for
// example after it restarted and lost its in-memory sets. Callers react by
// registering the set again.
var ErrTagSetNotFound = errors.New("tagger tag set not found")

// TaggerClient is the outbound transport to the tagger API. Implementations
// live in the web layer so this package stays free of HTTP details.
type TaggerClient interface {
	RegisterTagSet(ctx context.Context, name, prompt string, tags []TaggerTag) error
	Tag(ctx context.Context, name, text string) (string, error)
}

// GeneralTagger wraps a tagger tag set: its client, name, prompt and tag
// definitions. It registers the set on first use and re-registers it when the
// tagger reports that its in-memory copy is gone. It is safe for concurrent
// use.
type GeneralTagger struct {
	client     TaggerClient
	tagSetName string
	prompt     string
	tags       []TaggerTag

	mu         sync.Mutex
	registered bool
}

// NewGeneralTagger builds a tag set wrapper. client performs the calls to the
// tagger API; prompt and tags describe the set.
func NewGeneralTagger(client TaggerClient, tagSetName, prompt string, tags []TaggerTag) *GeneralTagger {
	return &GeneralTagger{
		client:     client,
		tagSetName: tagSetName,
		prompt:     prompt,
		tags:       tags,
	}
}

// RegisterTagSet registers (or replaces) the tag set on the tagger API.
func (t *GeneralTagger) RegisterTagSet(ctx context.Context) error {
	prompt := strings.TrimSpace(t.prompt)
	if prompt == "" {
		return errors.New("tagger: no prompt to register")
	}
	if len(t.tags) == 0 {
		return errors.New("tagger: no tags to register")
	}
	if err := t.client.RegisterTagSet(ctx, t.tagSetName, prompt, t.tags); err != nil {
		return err
	}
	t.mu.Lock()
	t.registered = true
	t.mu.Unlock()
	return nil
}

// EnsureRegistered registers the tag set if it has not been registered yet.
func (t *GeneralTagger) EnsureRegistered(ctx context.Context) error {
	t.mu.Lock()
	registered := t.registered
	t.mu.Unlock()
	if registered {
		return nil
	}
	return t.RegisterTagSet(ctx)
}

// Tag applies the tag set to text and returns the matched tag name, or an
// empty string when no tag applies. When the tagger reports the set is missing
// (it is in-memory), the set is registered again and the call retried once.
func (t *GeneralTagger) Tag(ctx context.Context, text string) (string, error) {
	if err := t.EnsureRegistered(ctx); err != nil {
		return "", err
	}

	name, err := t.client.Tag(ctx, t.tagSetName, text)
	if errors.Is(err, ErrTagSetNotFound) {
		if regErr := t.RegisterTagSet(ctx); regErr != nil {
			return "", fmt.Errorf("tagger: re-register tag set: %w", regErr)
		}
		return t.client.Tag(ctx, t.tagSetName, text)
	}
	return name, err
}

// TagSetName returns the name of the tag set.
func (t *GeneralTagger) TagSetName() string {
	return t.tagSetName
}

// Prompt returns the prompt registered with the tag set.
func (t *GeneralTagger) Prompt() string {
	return t.prompt
}

// Tags returns a copy of the tag definitions registered with the tag set.
func (t *GeneralTagger) Tags() []TaggerTag {
	return append([]TaggerTag(nil), t.tags...)
}
