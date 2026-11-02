package service

import (
	"context"

	tagger "clinic-backend/internal/agent/tagger"
)

// TagAgent tags text with the in-process tagger agent. *tagger.Pool
// implements it; tests use fakes.
type TagAgent interface {
	Tag(ctx context.Context, text string) (tagger.Result, error)
}
