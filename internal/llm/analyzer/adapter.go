// Package analyzer is the LLM integration for document enrichment and the tag subsystem's judged decisions:
// the four contracts the tools runner holds (Analyzer, TagAdjudicator, TagLexicalGate, TagQualityJudge), the
// Anthropic and OpenAI-compatible implementations behind them, the prompt builders and response parsers of
// every judged step, and the provider gates (content size, token limits, credits). The tools runner fronts
// this package; the pipeline's policy layer stays free of LLM specifics.
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

// Analyzer is the document-analysis contract the tools runner holds: the passes that turn a document's
// reduced text into its title, type, tags, people and language, behind the model catalog's capability
// gates.
type Analyzer interface {
	// Analyze extracts title, document type, tags with details, people and
	// language from the reduced text, against the offered type options and
	// the existing-tag suggestions.
	Analyze(ctx context.Context, text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string) (*AnalysisResult, error)
	// AnalyzeDocType refines the document type from the text's head and
	// tail, given the previous analysis pass.
	AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []TypeRef, metadata DocMetadata) (string, error)
}

// TagAdjudicator is the tag subsystem's semantic judging contract: the passes that compare tag values
// against each other or against the store — pair adjudication, conflict arbitration, residual
// phrase-context folds, canonical-form identity and fold-relation gating. It is served by the single
// model configured under enricher.tag_adjudicator; the context-free gate passes and the quality rubric
// are not part of it.
type TagAdjudicator interface {
	// Adjudicate answers the same-intent tag pairs: one verdict per pair,
	// with the direction when the verdict is same.
	Adjudicate(ctx context.Context, pairs []TagPairEvidence) ([]TagVerdictResult, error)
	// JudgeTagConflicts arbitrates the compounds whose constituent verdicts disagree with the compound
	// reading, choosing split, keep, or defer for each.
	JudgeTagConflicts(ctx context.Context, candidates []TagConflictCandidate) ([]TagConflictResult, error)
	// JudgeTagPhraseFolds decides whether each part, in the context of its phrase, denotes the candidate
	// anchor value: the residual phrase-context fold the seam asks when a multi-base part's primary-sense
	// base is not live while another attested base is.
	JudgeTagPhraseFolds(ctx context.Context, candidates []TagPhraseFoldCandidate) ([]TagPhraseFoldResult, error)
	// JudgeTagIdentity decides the canonical written form of candidate
	// tags: folds, denotation preservation, conventional form.
	JudgeTagIdentity(ctx context.Context, candidates []TagIdentityCandidate) ([]TagIdentityResult, error)
	// JudgeTagRelations gates fold proposals: it decides only whether the candidate and its store anchor
	// are semantically related enough that a canonical-form judgment is meaningful, never whether the fold
	// itself is correct.
	JudgeTagRelations(ctx context.Context, candidates []TagRelationCandidate) ([]TagRelationResult, error)
}

// TagLexicalGate is the tag subsystem's context-free classification contract: the dimensional parts
// pass, the compound reading and the language guard's translation, judged on the values themselves and
// never in a sense inferred from a phrase. It is served by the model configured under
// enricher.tag_lexical_gate.
type TagLexicalGate interface {
	// JudgeTagParts decides whether each value, on its own, names a subject a searcher would filter by: the
	// context-free dimensional question the split gate's parts pass asks.
	JudgeTagParts(ctx context.Context, values []string) ([]TagPartResult, error)
	// JudgeTagCompounds decides what each compound phrase denotes: the context-free
	// named_unit/composition/no_dimension trichotomy the split gate's compound pass asks.
	JudgeTagCompounds(ctx context.Context, candidates []string) ([]TagCompoundResult, error)
	// JudgeTagTranslations returns each value's English canonical form, or the value unchanged when it is
	// already English: the language guard's judged translation.
	JudgeTagTranslations(ctx context.Context, values []string) ([]TagTranslationResult, error)
}

// TagQualityJudge is the tag subsystem's document-reading judging contract: the quality rubric over novel
// tags and the coverage audit's applicability call. It is served by the model configured under
// enricher.tag_quality. The dimension question belongs to the gate's parts pass and is not asked here.
type TagQualityJudge interface {
	// JudgeTagQuality considers the document text for the subject-matter criterion and the tag alone for
	// the remaining criteria, answering the rubric and the judge's abstention. The dimension question is
	// not asked here: the parts pass owns it and the gate stamps its verdict.
	JudgeTagQuality(ctx context.Context, text string, candidates []TagQualityCandidate) ([]TagQualityResult, error)
	// JudgeTagAudit decides whether each flagged dimension is a subject the document is about — the
	// completeness audit's applicability call, reading about-ness from the document and abstaining when it
	// is unclear.
	JudgeTagAudit(ctx context.Context, text string, candidates []TagAuditCandidate) ([]TagAuditResult, error)
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

// adapter is the constructible union of the four contracts: both client implementations satisfy every
// one of them, so a single constructor can serve any role while each factory narrows the returned value
// to the contract its role consumes.
type adapter interface {
	Analyzer
	TagAdjudicator
	TagLexicalGate
	TagQualityJudge
}

// newAdapter builds the client implementation the LLM configuration names: anthropic maps to the Anthropic
// adapter, anything else to the OpenAI-compatible one. The registry supplies the capability gates; the
// adapters skip them when the model is not cataloged. The prompt template and minimum tag count only feed
// the analysis calls.
func newAdapter(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, promptTemplate string, minTags int, reg *llm.Registry) (adapter, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is required for %q adapter", llmCfg.Adapter)
	}
	caps := reg.Lookup(llmCfg.Provider, llmCfg.Model)

	switch llmCfg.Adapter {
	case "anthropic":
		return NewLlmAnthropic(logger, cfg, llmCfg, caps, promptTemplate, minTags, reg)
	default:
		return NewLlmOpenAiCompatible(logger, cfg, llmCfg, caps, promptTemplate, minTags, reg)
	}
}

// NewAnalyzer builds the document-analysis adapter the LLM configuration names, carrying the prompt
// template and minimum tag count its calls render.
func NewAnalyzer(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, promptTemplate string, minTags int, reg *llm.Registry) (Analyzer, error) {
	a, err := newAdapter(logger, cfg, llmCfg, promptTemplate, minTags, reg)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// NewTagAdjudicator builds the semantic judging adapter the LLM configuration names; the analysis-only
// prompt settings do not apply to this role.
func NewTagAdjudicator(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, reg *llm.Registry) (TagAdjudicator, error) {
	a, err := newAdapter(logger, cfg, llmCfg, "", 0, reg)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// NewTagLexicalGate builds the context-free gate adapter the LLM configuration names; the analysis-only
// prompt settings do not apply to this role.
func NewTagLexicalGate(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, reg *llm.Registry) (TagLexicalGate, error) {
	a, err := newAdapter(logger, cfg, llmCfg, "", 0, reg)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// NewTagQualityJudge builds the document-reading judge adapter the LLM configuration names; the
// analysis-only prompt settings do not apply to this role.
func NewTagQualityJudge(logger *log.Logger, cfg config.ToolConfig, llmCfg *config.LlmConfig, reg *llm.Registry) (TagQualityJudge, error) {
	a, err := newAdapter(logger, cfg, llmCfg, "", 0, reg)
	if err != nil {
		return nil, err
	}
	return a, nil
}
