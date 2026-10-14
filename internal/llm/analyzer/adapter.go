// Package analyzer is the LLM integration for document enrichment and the
// tag subsystem's judged decisions: the document-analysis adapter family
// (Anthropic and OpenAI-compatible), the prompt builders and response
// parsers of every judged step, and the provider gates (content size,
// token limits, credits). The tools runner fronts this package; the
// pipeline's policy layer stays free of LLM specifics.
package analyzer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
)

// PeopleResult is one person the analysis attributed to the document: the
// native-script name when it could be determined, its romanized form
// (always provided), and the type from the offered people-type list.
// NormalizedName is the lookup form filled by the enricher, never
// serialized.
type PeopleResult struct {
	Name           string `json:"name"`
	NameRomanized  string `json:"name_romanized,omitempty"`
	Type           string `json:"type"`
	NormalizedName string `json:"-"`
}

// TypeRef is one option of a type list offered to the model, carrying the
// fields the prompts render: the type's name and its description.
type TypeRef struct {
	Name        string
	Description string
}

// ContentAnalyzer is the LLM analysis and judging seam the tools runner
// holds; both client implementations satisfy it behind the model
// catalog's capability gates.
type ContentAnalyzer interface {
	// Analyze extracts title, document type, tags with details, people and
	// language from the reduced text, against the offered type options and
	// the existing-tag suggestions.
	Analyze(ctx context.Context, text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string) (*AnalysisResult, error)
	// AnalyzeDocType refines the document type from the text's head and
	// tail, given the previous analysis pass.
	AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []TypeRef, metadata DocMetadata) (string, error)
	// Adjudicate answers the same-intent tag pairs: one verdict per pair,
	// with the direction when the verdict is same.
	Adjudicate(ctx context.Context, pairs []TagPairEvidence) ([]TagVerdictResult, error)
	// JudgeTagParts decides whether each value, on its own, names a subject a searcher would filter by: the
	// context-free dimensional question the split gate's parts pass asks.
	JudgeTagParts(ctx context.Context, values []string) ([]TagPartResult, error)
	// JudgeTagCompounds decides what each compound phrase denotes: the context-free named_unit/composition/no_dimension trichotomy the split
	// gate's compound pass asks.
	JudgeTagCompounds(ctx context.Context, candidates []string) ([]TagCompoundResult, error)
	// JudgeTagQuality considers the document text for the subject-matter criterion and the tag alone for
	// the remaining criteria, answering the rubric and the judge's abstention. The dimension question is
	// not asked here: the parts pass owns it and the gate stamps its verdict.
	JudgeTagQuality(ctx context.Context, text string, candidates []TagQualityCandidate) ([]TagQualityResult, error)
	// JudgeTagTranslations returns each value's English canonical form, or the value unchanged when it is
	// already English: the language guard's judged translation.
	JudgeTagTranslations(ctx context.Context, values []string) ([]TagTranslationResult, error)
	// JudgeTagAudit decides whether each flagged dimension is a subject the document is about — the
	// completeness audit's applicability call, reading about-ness from the document and abstaining when it
	// is unclear.
	JudgeTagAudit(ctx context.Context, text string, candidates []TagAuditCandidate) ([]TagAuditResult, error)
	// JudgeTagPhraseFolds decides whether each part, in the context of its phrase, denotes the candidate
	// anchor value: the residual phrase-context fold the seam asks when a multi-base part's primary-sense
	// base is not live while another attested base is.
	JudgeTagPhraseFolds(ctx context.Context, candidates []TagPhraseFoldCandidate) ([]TagPhraseFoldResult, error)
	// JudgeTagIdentity decides the canonical written form of candidate
	// tags: folds, denotation preservation, conventional form.
	JudgeTagIdentity(ctx context.Context, candidates []TagIdentityCandidate) ([]TagIdentityResult, error)
	// Name identifies the implementation.
	Name() string
}

// AnalysisResult is one analysis pass's outcome: title, document type,
// tags with per-tag details, people, language, the raw provider stats,
// the prompt that produced it, and the pass context carried across
// refinement passes (never serialized).
type AnalysisResult struct {
	Title       string           `json:"title"`
	DocType     string           `json:"type"`
	Tags        []string         `json:"tags"`
	TagDetails  []TagDetail      `json:"tag_details,omitempty"`
	People      []PeopleResult   `json:"people"`
	Language    string           `json:"language"`
	Stats       *json.RawMessage `json:"stats"`
	Prompt      string           `json:"prompt"`
	PassContext *json.RawMessage `json:"-"`
}

// NewContentAnalyzer builds the adapter the LLM configuration names:
// anthropic maps to the Anthropic adapter, anything else to the
// OpenAI-compatible one. The registry supplies the capability gates; the
// adapters skip them when the model is not cataloged.
func NewContentAnalyzer(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, promptTemplate string, minTags, maxTags int, reg *llm.Registry) (ContentAnalyzer, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is required for %q adapter", llmCfg.Adapter)
	}
	caps := reg.Lookup(llmCfg.Provider, llmCfg.Model)

	switch llmCfg.Adapter {
	case "anthropic":
		return NewLlmAnthropic(logger, cfg, llmCfg, caps, promptTemplate, minTags, maxTags, reg)
	default:
		return NewLlmOpenAiCompatible(logger, cfg, llmCfg, caps, promptTemplate, minTags, maxTags, reg)
	}
}
