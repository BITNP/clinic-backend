// Package tagger implements the clinic tagger agent on top of the generic
// agent session.
package tagger

// Tag is one candidate label supplied to the tagger. ApplyRule is a
// natural-language rule describing when the tag applies.
type Tag struct {
	Name        string
	Description string
	ApplyRule   string
}

// TagSet is a named collection of tags. Prompt is a general instruction
// describing the function of the set and the notice the tagger must follow
// when marking text within it.
type TagSet struct {
	Name   string
	Prompt string
	Tags   []Tag
}

// Result is the tagger's answer. Name is empty when no tag matches.
type Result struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}
