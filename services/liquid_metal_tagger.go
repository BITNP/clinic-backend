package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	defaultLiquidMetalTagSetName = "laptop_liquid_metal"

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

// LiquidMetalTagger classifies laptops through the external tagger API using a
// tag set built from the embedded liquid-metal table. The tag set lives in
// memory on the tagger side, so it is registered once at startup and again
// whenever the tagger reports it is missing.
type LiquidMetalTagger struct {
	client     TaggerClient
	tagSetName string
	prompt     string
	tags       []TaggerTag

	mu         sync.Mutex
	registered bool
}

// NewLiquidMetalTagger builds a tagger from the embedded liquid-metal table.
// client performs the calls to the tagger API; tagSetName defaults to
// "laptop_liquid_metal" when empty.
func NewLiquidMetalTagger(client TaggerClient, tagSetName string) (*LiquidMetalTagger, error) {
	if tagSetName == "" {
		tagSetName = defaultLiquidMetalTagSetName
	}
	rows, err := LiquidMetalRows()
	if err != nil {
		return nil, fmt.Errorf("build liquid metal tagger: %w", err)
	}
	prompt, err := buildLiquidMetalPrompt(rows)
	if err != nil {
		return nil, fmt.Errorf("build liquid metal tagger: %w", err)
	}
	return &LiquidMetalTagger{
		client:     client,
		tagSetName: tagSetName,
		prompt:     prompt,
		tags:       liquidMetalLookupTags(),
	}, nil
}

// RegisterTagSet registers (or replaces) the liquid-metal tag set on the
// tagger API.
func (t *LiquidMetalTagger) RegisterTagSet(ctx context.Context) error {
	if strings.TrimSpace(t.prompt) == "" {
		return errors.New("liquid metal tagger: empty prompt")
	}
	if err := t.client.RegisterTagSet(ctx, t.tagSetName, t.prompt, t.tags); err != nil {
		return err
	}
	t.mu.Lock()
	t.registered = true
	t.mu.Unlock()
	return nil
}

// Classify asks the tagger whether the described laptop uses liquid metal.
// gpu and extra may be empty. When the tagger reports the tag set is missing,
// the set is registered again and the call retried once.
func (t *LiquidMetalTagger) Classify(ctx context.Context, model, gpu, extra string) (LiquidMetalResult, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return LiquidMetalResult{}, ErrLiquidMetalModelRequired
	}
	if err := t.ensureRegistered(ctx); err != nil {
		return LiquidMetalResult{}, err
	}

	text := liquidMetalInputText(model, gpu, extra)
	name, err := t.client.Tag(ctx, t.tagSetName, text)
	if errors.Is(err, ErrTagSetNotFound) {
		if regErr := t.RegisterTagSet(ctx); regErr != nil {
			return LiquidMetalResult{}, fmt.Errorf("liquid metal tagger: re-register tag set: %w", regErr)
		}
		name, err = t.client.Tag(ctx, t.tagSetName, text)
	}
	if err != nil {
		return LiquidMetalResult{}, err
	}
	return liquidMetalResultForTag(name), nil
}

// Tags returns the tag definitions registered with the tagger.
func (t *LiquidMetalTagger) Tags() []TaggerTag {
	return t.tags
}

// Prompt returns the rendered prompt registered with the tagger.
func (t *LiquidMetalTagger) Prompt() string {
	return t.prompt
}

func (t *LiquidMetalTagger) ensureRegistered(ctx context.Context) error {
	t.mu.Lock()
	registered := t.registered
	t.mu.Unlock()
	if registered {
		return nil
	}
	return t.RegisterTagSet(ctx)
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

// liquidMetalLookupTags returns the fixed tag set. Each rule is short because
// the full lookup table lives in the shared prompt.
func liquidMetalLookupTags() []TaggerTag {
	return []TaggerTag{
		{
			Name:        LiquidMetalTagCPU,
			Description: "原厂确认 CPU 使用液金，GPU 不使用液金。",
			ApplyRule:   "命中液金正表，且该配置仅 CPU 使用液金时输出。",
		},
		{
			Name:        LiquidMetalTagGPU,
			Description: "原厂确认 GPU 使用液金，CPU 不使用液金。",
			ApplyRule:   "命中液金正表，且该配置仅 GPU 使用液金时输出。",
		},
		{
			Name:        LiquidMetalTagCPUGPU,
			Description: "原厂确认 CPU 和 GPU 都使用液金。",
			ApplyRule:   "命中液金正表，且该配置的 CPU 和 GPU 都使用液金时输出。",
		},
		{
			Name:        LiquidMetalTagToVerify,
			Description: "液金使用情况尚未确认，需要进一步核实。",
			ApplyRule:   "命中待核实附表，或配置边界、证据、改装情况无法确认时输出。",
		},
		{
			Name:        LiquidMetalTagNone,
			Description: "原厂使用普通导热材料，不使用液金。",
			ApplyRule:   "命中易混淆列表且明确为普通导热膏、相变材料等非液金方案时输出。",
		},
		{
			Name:        LiquidMetalTagUnmatched,
			Description: "表中没有匹配记录，无法判断。",
			ApplyRule:   "没有匹配记录，或型号缺失、无法识别时输出。",
		},
	}
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
