package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tagger "clinic-backend/internal/agent/tagger"
)

const (
	defaultLiquidMetalTagSetName = "clinic_laptop_liquid_metal"

	LiquidMetalTagCPU       = "liquid_metal_cpu"
	LiquidMetalTagGPU       = "liquid_metal_gpu"
	LiquidMetalTagCPUGPU    = "liquid_metal_cpu_gpu"
	LiquidMetalTagToVerify  = "liquid_metal_to_verify"
	LiquidMetalTagNone      = "non_liquid_metal"
	LiquidMetalTagUnmatched = "unmatched"
)

// ErrLiquidMetalModelRequired reports that a classification request did not
// include a laptop model, which is the minimum needed to look the machine up.
var ErrLiquidMetalModelRequired = errors.New("liquid metal tagger: model is required")

// LiquidMetalStatus is the liquid-metal verdict for a laptop.
type LiquidMetalStatus string

const (
	LiquidMetalStatusYes     LiquidMetalStatus = "liquid_metal"
	LiquidMetalStatusVerify  LiquidMetalStatus = "to_verify"
	LiquidMetalStatusNo      LiquidMetalStatus = "non_liquid_metal"
	LiquidMetalStatusUnknown LiquidMetalStatus = "unknown"
)

// liquidMetalStatusOrder is the persisted order of LiquidMetalStatus values.
// The indexes are stored on records, so append new statuses at the end and
// never reorder this slice.
var liquidMetalStatusOrder = []LiquidMetalStatus{
	LiquidMetalStatusUnknown,
	LiquidMetalStatusYes,
	LiquidMetalStatusVerify,
	LiquidMetalStatusNo,
}

// LiquidMetalStatusIndex returns the persisted index for a status. Unknown or
// unrecognized statuses use index 0.
func LiquidMetalStatusIndex(s LiquidMetalStatus) uint {
	for i, v := range liquidMetalStatusOrder {
		if v == s {
			return uint(i)
		}
	}
	return 0
}

// LiquidMetalStatusFromIndex returns the status for a persisted index. Values
// outside the table are reported as unknown.
func LiquidMetalStatusFromIndex(i uint) LiquidMetalStatus {
	if i >= uint(len(liquidMetalStatusOrder)) {
		return LiquidMetalStatusUnknown
	}
	return liquidMetalStatusOrder[i]
}

// LiquidMetalComponent identifies which parts use liquid metal.
type LiquidMetalComponent string

const (
	LiquidMetalComponentCPU     LiquidMetalComponent = "cpu"
	LiquidMetalComponentGPU     LiquidMetalComponent = "gpu"
	LiquidMetalComponentCPUGPU  LiquidMetalComponent = "cpu_gpu"
	LiquidMetalComponentNone    LiquidMetalComponent = "none"
	LiquidMetalComponentUnknown LiquidMetalComponent = "unknown"
)

// LiquidMetalCategory records which part of the source table the verdict came
// from, so callers can tell confirmed data from uncertain or negative rows.
type LiquidMetalCategory string

const (
	LiquidMetalCategoryConfirmed  LiquidMetalCategory = "confirmed"
	LiquidMetalCategoryToVerify   LiquidMetalCategory = "to_verify"
	LiquidMetalCategoryConfusable LiquidMetalCategory = "confusable"
	LiquidMetalCategoryUnmatched  LiquidMetalCategory = "unmatched"
)

// LiquidMetalResult is the outcome of one classification.
type LiquidMetalResult struct {
	Status    LiquidMetalStatus
	Component LiquidMetalComponent
	Category  LiquidMetalCategory
	Tag       string
}

// LiquidMetalTagger classifies laptops with a tag set built from the embedded
// liquid-metal table.
type LiquidMetalTagger struct {
	tagAgent TagAgent
	set      tagger.TagSet
}

// NewLiquidMetalTagger builds a liquid-metal tagger from a prepared tag set.
func NewLiquidMetalTagger(tagAgent TagAgent, set tagger.TagSet) *LiquidMetalTagger {
	return &LiquidMetalTagger{tagAgent: tagAgent, set: set}
}

// LiquidMetalTagSet builds the tag set from the embedded liquid-metal table
// and tag catalog.
func LiquidMetalTagSet() (tagger.TagSet, error) {
	rows, err := LiquidMetalRows()
	if err != nil {
		return tagger.TagSet{}, fmt.Errorf("build liquid metal tag set: %w", err)
	}
	prompt, err := buildLiquidMetalPrompt(rows)
	if err != nil {
		return tagger.TagSet{}, fmt.Errorf("build liquid metal tag set: %w", err)
	}
	tags, err := LiquidMetalTags()
	if err != nil {
		return tagger.TagSet{}, fmt.Errorf("build liquid metal tag set: %w", err)
	}
	return tagger.TagSet{
		Name:   defaultLiquidMetalTagSetName,
		Prompt: prompt,
		Tags:   tags,
	}, nil
}

// Classify asks the tagger whether the described laptop uses liquid metal.
// gpu and extra may be empty.
func (t *LiquidMetalTagger) Classify(ctx context.Context, model, gpu, extra string) (LiquidMetalResult, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return LiquidMetalResult{}, ErrLiquidMetalModelRequired
	}
	if t.tagAgent == nil {
		return LiquidMetalResult{}, errors.New("liquid metal tagger: no tag agent configured")
	}
	text := liquidMetalInputText(model, gpu, extra)
	result, err := t.tagAgent.Tag(ctx, text)
	if err != nil {
		return LiquidMetalResult{}, err
	}
	return liquidMetalResultForTag(result.Name), nil
}

// Tags returns the tag definitions used by the tagger.
func (t *LiquidMetalTagger) Tags() []tagger.Tag {
	return append([]tagger.Tag(nil), t.set.Tags...)
}

// Prompt returns the rendered prompt used by the tagger.
func (t *LiquidMetalTagger) Prompt() string {
	return t.set.Prompt
}

// liquidMetalInputText builds the text sent to the tagger. Empty optional
// fields are omitted.
func liquidMetalInputText(model, gpu, extra string) string {
	var b strings.Builder
	b.WriteString("型号: ")
	b.WriteString(strings.TrimSpace(model))
	if g := strings.TrimSpace(gpu); g != "" {
		b.WriteString("\nGPU: ")
		b.WriteString(g)
	}
	if e := strings.TrimSpace(extra); e != "" {
		b.WriteString("\n其他信息: ")
		b.WriteString(e)
	}
	return b.String()
}

// liquidMetalResultForTag maps the tagger's tag name back to a structured
// result. Unknown or empty names are reported as unmatched.
func liquidMetalResultForTag(name string) LiquidMetalResult {
	switch name {
	case LiquidMetalTagCPU:
		return LiquidMetalResult{
			Status:    LiquidMetalStatusYes,
			Component: LiquidMetalComponentCPU,
			Category:  LiquidMetalCategoryConfirmed,
			Tag:       name,
		}
	case LiquidMetalTagGPU:
		return LiquidMetalResult{
			Status:    LiquidMetalStatusYes,
			Component: LiquidMetalComponentGPU,
			Category:  LiquidMetalCategoryConfirmed,
			Tag:       name,
		}
	case LiquidMetalTagCPUGPU:
		return LiquidMetalResult{
			Status:    LiquidMetalStatusYes,
			Component: LiquidMetalComponentCPUGPU,
			Category:  LiquidMetalCategoryConfirmed,
			Tag:       name,
		}
	case LiquidMetalTagToVerify:
		return LiquidMetalResult{
			Status:    LiquidMetalStatusVerify,
			Component: LiquidMetalComponentUnknown,
			Category:  LiquidMetalCategoryToVerify,
			Tag:       name,
		}
	case LiquidMetalTagNone:
		return LiquidMetalResult{
			Status:    LiquidMetalStatusNo,
			Component: LiquidMetalComponentNone,
			Category:  LiquidMetalCategoryConfusable,
			Tag:       name,
		}
	default:
		return LiquidMetalResult{
			Status:    LiquidMetalStatusUnknown,
			Component: LiquidMetalComponentUnknown,
			Category:  LiquidMetalCategoryUnmatched,
			Tag:       name,
		}
	}
}

// buildLiquidMetalPrompt renders the table rows into the prompt template.
func buildLiquidMetalPrompt(rows []LiquidMetalRow) (string, error) {
	const placeholder = "{{TABLE}}"
	if !strings.Contains(liquidMetalPromptTemplate, placeholder) {
		return "", errors.New("liquid metal prompt: missing {{TABLE}} placeholder")
	}
	if len(rows) == 0 {
		return "", errors.New("liquid metal prompt: no rows")
	}

	var b strings.Builder
	b.WriteString("（每行格式：[所属列表] 品牌 | 年份 | 型号或情形 | 型号代码或CPU限定 | GPU配置 | 配置限制与提醒）\n")
	for _, row := range rows {
		b.WriteString("- [")
		b.WriteString(row.List)
		b.WriteString("] ")
		b.WriteString(strings.Join([]string{
			row.Brand,
			row.Year,
			row.Model,
			row.Code,
			row.GPU,
			row.Notes,
		}, " | "))
		b.WriteByte('\n')
	}
	table := strings.TrimRight(b.String(), "\n")
	return strings.Replace(liquidMetalPromptTemplate, placeholder, table, 1), nil
}
