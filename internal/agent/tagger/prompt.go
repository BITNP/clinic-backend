package tagger

import (
	_ "embed"
	"fmt"
	"strings"
)

// The system prompts are embedded from data/ and loaded when the package
// starts. Keep the template's placeholders in sync with buildSystemPrompt.
var (
	//go:embed data/system_prompt.txt
	systemPrompt string

	//go:embed data/system_prompt_template.txt
	systemPromptTemplate string
)

var systemPromptPlaceholders = []string{
	"{{SYSTEM_PROMPT}}",
	"{{TAG_SET_NAME}}",
	"{{SET_INSTRUCTIONS}}",
	"{{TAGS}}",
}

// prepareTagSet validates and trims a tag set, then renders the system prompt
// shared by every session of the set. The returned set is a cleaned copy.
func prepareTagSet(set TagSet) (TagSet, string, error) {
	set.Name = strings.TrimSpace(set.Name)
	set.Prompt = strings.TrimSpace(set.Prompt)
	if set.Name == "" {
		return TagSet{}, "", fmt.Errorf("tagger: tag set name is required")
	}
	if set.Prompt == "" {
		return TagSet{}, "", fmt.Errorf("tagger: tag set prompt is required")
	}
	if len(set.Tags) == 0 {
		return TagSet{}, "", fmt.Errorf("tagger: tag set must contain at least one tag")
	}

	tags := make([]Tag, 0, len(set.Tags))
	seen := make(map[string]struct{}, len(set.Tags))
	for i, tag := range set.Tags {
		tag.Name = strings.TrimSpace(tag.Name)
		tag.Description = strings.TrimSpace(tag.Description)
		tag.ApplyRule = strings.TrimSpace(tag.ApplyRule)
		if tag.Name == "" {
			return TagSet{}, "", fmt.Errorf("tagger: tag %d name is required", i)
		}
		if tag.ApplyRule == "" {
			return TagSet{}, "", fmt.Errorf("tagger: tag %q apply rule is required", tag.Name)
		}
		if _, ok := seen[tag.Name]; ok {
			return TagSet{}, "", fmt.Errorf("tagger: tag %q appears more than once", tag.Name)
		}
		seen[tag.Name] = struct{}{}
		tags = append(tags, tag)
	}
	set.Tags = tags

	rendered, err := buildSystemPrompt(set)
	if err != nil {
		return TagSet{}, "", err
	}
	return set, rendered, nil
}

// buildSystemPrompt renders the embedded system prompt template for a
// normalized tag set.
func buildSystemPrompt(set TagSet) (string, error) {
	for _, placeholder := range systemPromptPlaceholders {
		if !strings.Contains(systemPromptTemplate, placeholder) {
			return "", fmt.Errorf("tagger: system prompt template is missing %s", placeholder)
		}
	}

	replacer := strings.NewReplacer(
		"{{SYSTEM_PROMPT}}", strings.TrimSpace(systemPrompt),
		"{{TAG_SET_NAME}}", set.Name,
		"{{SET_INSTRUCTIONS}}", set.Prompt,
		"{{TAGS}}", renderTags(set.Tags),
	)
	return replacer.Replace(systemPromptTemplate), nil
}

func renderTags(tags []Tag) string {
	var b strings.Builder
	for i, tag := range tags {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d. name: %s\n", i+1, tag.Name)
		if tag.Description != "" {
			fmt.Fprintf(&b, "   description: %s\n", tag.Description)
		}
		fmt.Fprintf(&b, "   apply_rule: %s", tag.ApplyRule)
	}
	return b.String()
}
