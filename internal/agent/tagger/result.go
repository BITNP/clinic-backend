package tagger

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseResult decodes the JSON object produced by the model, tolerating
// surrounding whitespace or markdown fences.
func parseResult(content string) (Result, error) {
	s := strings.TrimSpace(content)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return Result{}, fmt.Errorf("no JSON object found in: %.100q", content)
	}
	var r Result
	if err := json.Unmarshal([]byte(s[start:end+1]), &r); err != nil {
		return Result{}, fmt.Errorf("decode tagger JSON: %w", err)
	}
	return r, nil
}

// normalizeResult rejects hallucinated tag names and requires a reason when a
// tag was selected. A name that is not in the candidate set is treated as no
// match.
func normalizeResult(r Result, tags []Tag) (Result, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Name == "" {
		return Result{}, nil
	}

	for _, tag := range tags {
		if tag.Name == r.Name {
			if r.Reason == "" {
				return Result{}, fmt.Errorf("reason is required when tag %q is selected", r.Name)
			}
			return r, nil
		}
	}
	return Result{}, nil
}
