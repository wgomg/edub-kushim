package contentanalyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/types"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const SystemMessage = "You are a helpful assistant specialized in document analysis and metadata extraction"

// sleepAfterRequest paces requests toward rate-limited providers. Aborts early
// when the batch context is cancelled.
func sleepAfterRequest(ctx context.Context, delaySeconds float64) {
	if delaySeconds <= 0 {
		return
	}
	select {
	case <-time.After(time.Duration(delaySeconds * float64(time.Second))):
	case <-ctx.Done():
	}
}

type ContentTooLargeError struct {
	EstimatedTokens int
	MaxInputTokens  int
}

func (e *ContentTooLargeError) Error() string {
	return fmt.Sprintf("estimated %d tokens exceeds model limit of %d", e.EstimatedTokens, e.MaxInputTokens)
}

type TokenLimitError struct {
	MaxTokens       int
	RequestedTokens int
	RawBody         string
}

func (e *TokenLimitError) Error() string {
	return fmt.Sprintf("token limit: requested %d tokens, model max is %d", e.RequestedTokens, e.MaxTokens)
}

type InsufficientCreditsError struct {
	Provider   string
	HTTPStatus int
	RawBody    string // retained for debugging; not surfaced in Error()
}

func (e *InsufficientCreditsError) Error() string {
	return fmt.Sprintf("insufficient credits (%s, HTTP %d): the provider account has run out of credits or has billing issues", e.Provider, e.HTTPStatus)
}

func parseInsufficientCreditsError(body []byte, statusCode int, provider string) error {
	if statusCode == 402 {
		return &InsufficientCreditsError{Provider: provider, HTTPStatus: statusCode, RawBody: string(body)}
	}
	if statusCode == 429 {
		return &InsufficientCreditsError{Provider: provider, HTTPStatus: statusCode, RawBody: string(body)}
	}
	if provider == "qwen" && statusCode == 400 && strings.Contains(strings.ToLower(string(body)), "arrearage") {
		return &InsufficientCreditsError{Provider: provider, HTTPStatus: statusCode, RawBody: string(body)}
	}
	return nil
}

var tokenLimitRE = regexp.MustCompile(
	`maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens`,
)

func checkContentTooLarge(caps *llm.ModelCapability, fullPrompt string) error {
	if caps == nil || caps.MaxInputTokens <= 0 {
		return nil
	}
	estimated := utils.EstimateTokens(fullPrompt)
	if estimated > caps.MaxInputTokens {
		return &ContentTooLargeError{EstimatedTokens: estimated, MaxInputTokens: caps.MaxInputTokens}
	}
	return nil
}

func parseTokenLimitError(body []byte) error {
	matches := tokenLimitRE.FindSubmatch(body)
	if matches == nil {
		return nil
	}
	max, _ := strconv.Atoi(string(matches[1]))
	req, _ := strconv.Atoi(string(matches[2]))
	if max == 0 || req == 0 {
		return nil
	}
	return &TokenLimitError{MaxTokens: max, RequestedTokens: req, RawBody: string(body)}
}

type tokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func buildTokenUsageStats(prompt, completion, total int) *json.RawMessage {
	stats := tokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return nil
	}
	rm := json.RawMessage(data)
	return &rm
}

const defaultPromptTemplate = `Analyze the excerpts of a document provided below and extract the following data:
- Document title: The title as written in the document, in excerpts language, copied verbatim — never invent or append wording of your own; colons and subtitles are legitimate only when the original title itself has one. If no title line appears in the excerpts, use the first substantive line of the text as the title, still copied verbatim. Truncate to 127 characters if longer
{{.DocTypePrompt}}
- Tags: Between {{.MinTags}} and {{.MaxTags}} thematic tags describing the document's topics and domains. English only. Each tag is one to three words naming a recognizable subject, field, or period. Tags describe facets the title does not already state. Tags are conceptual categories — fields, domains, disciplines, periods, methods. Tags are never specific people, works, or places. Never merge two distinct concepts into a single tag with a conjunction such as "and", "or", or "vs" — emit each concept as its own tag. Use only widely-recognized standard terminology a general educated audience would know. Prefer the most standard, widely-recognized term for the subject; use a narrower term only when the document is specifically about that subfield. If an existing suggestion tag captures the concept adequately, prefer it over inventing a narrower label.{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in their own native script/language — only when you can independently determine it; if you are not confident of the person's actual native form, leave this empty rather than copying how this document renders the name), name_romanized (a Latin-script/romanized form of the name — always provide this, even when name is already in Latin script), and a type from the list below. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions. Note: names captured here must NOT be re-used as tags.
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.
Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}`

type promptData struct {
	DocTypePrompt string
	TagsPrompt    string
	PeoplePrompt  string
	Text          string
	MinTags       int
	MaxTags       int
}

func BuildPrompt(text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string, customTemplate string, minTags, maxTags int) string {
	docTypePrompt := documentTypePrompt(docTypes)
	peoplePrompt := peopleTypePrompt(peopleTypes)
	tagsPrompt := tagsPrompt(tagSuggestions)

	tmplStr := strings.TrimSpace(customTemplate)
	if tmplStr == "" {
		tmplStr = defaultPromptTemplate
	}

	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		tmpl, _ = template.New("prompt").Parse(defaultPromptTemplate)
	}

	data := promptData{
		DocTypePrompt: docTypePrompt,
		TagsPrompt:    tagsPrompt,
		PeoplePrompt:  peoplePrompt,
		Text:          text,
		MinTags:       minTags,
		MaxTags:       maxTags,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		buf.Reset()
		fallback, _ := template.New("prompt").Parse(defaultPromptTemplate)
		fallback.Execute(&buf, data)
	}

	return buf.String()
}

func peopleTypePrompt(types []database.PeopleType) string {
	var sb strings.Builder
	sb.WriteString("  Available types:\n")
	for _, t := range types {
		fmt.Fprintf(&sb, "    - %s (%s)\n", t.Name, t.Description)
	}
	return sb.String()
}

func documentTypePrompt(types []database.DocumentType) string {
	var sb strings.Builder
	sb.WriteString("- Document type: choose one of the following:\n")
	for _, t := range types {
		fmt.Fprintf(&sb, "  - %s (%s)\n", t.Name, t.Description)
	}
	return sb.String()
}

func tagsPrompt(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return fmt.Sprintf(
		" Prefer tags from the following list if thematically related to document excerpts: '%s'",
		strings.Join(tags, ","),
	)
}

func normalizeCore(s string) string {
	return utils.NormalizeTag(s)
}

func normalizeToTokens(s string) []string {
	return strings.Fields(normalizeCore(s))
}

func ExtractHeadTailWords(content string, headWords, tailWords int) string {
	words := strings.Fields(content)
	total := len(words)
	head := min(max(headWords, 0), total)
	tail := min(max(tailWords, 0), total-head)
	var sb strings.Builder
	sb.WriteString(strings.Join(words[:head], " "))
	if tail > 0 {
		sb.WriteString("\n\n[...]\n\n")
		sb.WriteString(strings.Join(words[total-tail:], " "))
	}
	return sb.String()
}

type DocMetadata struct {
	WordCount int32
	PageCount int32
	MimeType  string
}

func (m DocMetadata) Format() string {
	var parts []string
	if m.WordCount > 0 {
		parts = append(parts, fmt.Sprintf("%d total words", m.WordCount))
	}
	if m.PageCount > 0 {
		parts = append(parts, fmt.Sprintf("%d pages", m.PageCount))
	}
	if m.MimeType != "" {
		parts = append(parts, m.MimeType)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func BuildDocTypePrompt(headTailText string, docTypes []database.DocumentType, metadata DocMetadata) string {
	var typesList strings.Builder
	for _, dt := range docTypes {
		fmt.Fprintf(&typesList, "  - %s (%s)\n", dt.Name, dt.Description)
	}
	metaStr := metadata.Format()
	prompt := "Now re-evaluate the document type using the opening and closing sections of the full document below."
	if metaStr != "" {
		prompt += " Document context: " + metaStr + "."
	}
	return fmt.Sprintf(
		"%s Choose one type from:\n%s\nReturn ONLY a json string with key: type, without any additional text or formatting.\n\nDocument head and tail sections:\n\n%s",
		prompt,
		typesList.String(),
		headTailText,
	)
}

const (
	maxTagWords  = 3
	maxTagLength = 64
)

// TagFilter carries the token sets FilterTags checks a candidate tag against,
// so other tag-producing paths (adjudicated split components) can apply the
// same per-document rules.
type TagFilter struct {
	nameTokens     map[string]struct{}
	knownTokenSets []map[string]struct{}
	docTypeTokens  map[string]struct{}
	titleTokens    map[string]struct{}
}

func NewTagFilter(people []PeopleResult, knownPeopleNormalized []string, title string, docTypeNames []string) *TagFilter {
	nameTokens := make(map[string]struct{})
	for _, p := range people {
		for _, tok := range normalizeToTokens(p.NormalizedName) {
			nameTokens[tok] = struct{}{}
		}
	}

	var knownTokenSets []map[string]struct{}
	for _, name := range knownPeopleNormalized {
		tokens := normalizeToTokens(name)
		if len(tokens) == 0 {
			continue
		}
		set := make(map[string]struct{}, len(tokens))
		for _, tok := range tokens {
			set[tok] = struct{}{}
		}
		knownTokenSets = append(knownTokenSets, set)
	}

	docTypeTokens := make(map[string]struct{})
	for _, name := range docTypeNames {
		for _, tok := range normalizeToTokens(name) {
			docTypeTokens[tok] = struct{}{}
		}
	}

	titleTokens := make(map[string]struct{})
	for _, tok := range normalizeToTokens(title) {
		titleTokens[tok] = struct{}{}
	}

	return &TagFilter{
		nameTokens:     nameTokens,
		knownTokenSets: knownTokenSets,
		docTypeTokens:  docTypeTokens,
		titleTokens:    titleTokens,
	}
}

func (f *TagFilter) Accept(tag string) bool {
	tag = normalizeCore(tag)
	tagTokens := strings.Fields(tag)
	if len(tagTokens) > maxTagWords || len(tagTokens) == 0 || len(tag) > maxTagLength {
		return false
	}

	for _, tok := range tagTokens {
		if _, ok := f.nameTokens[tok]; ok {
			return false
		}
	}

	if len(tagTokens) >= 2 {
		for _, kset := range f.knownTokenSets {
			allInKnown := true
			for _, tok := range tagTokens {
				if _, ok := kset[tok]; !ok {
					allInKnown = false
					break
				}
			}
			if allInKnown {
				return false
			}
		}
	}

	for _, tok := range tagTokens {
		if _, ok := f.docTypeTokens[tok]; ok {
			return false
		}
	}

	allInTitle := true
	for _, tok := range tagTokens {
		if _, ok := f.titleTokens[tok]; !ok {
			allInTitle = false
			break
		}
	}
	return !allInTitle
}

func FilterTags(tags []string, people []PeopleResult, knownPeopleNormalized []string, title string, docTypeNames []string, maxTags int) []string {
	filter := NewTagFilter(people, knownPeopleNormalized, title, docTypeNames)

	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !filter.Accept(tag) {
			continue
		}
		result = append(result, tag)
	}

	if len(result) > maxTags {
		result = result[:maxTags]
	}
	return result
}

func SplitComponents(raw []string, filter *TagFilter) []string {
	normalized := NormalizeTags(raw)
	result := make([]string, 0, len(normalized))
	for _, c := range normalized {
		if !filter.Accept(c) {
			continue
		}
		result = append(result, c)
	}
	return result
}

func NormalizeTags(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	result := make([]string, 0, len(raw))
	for _, t := range raw {
		t = normalizeCore(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		result = append(result, t)
	}
	return result
}

type TagPairEvidence struct {
	Query             string
	Target            string
	Sim               float64
	RunnerUpSim       float64
	TargetRunnerUpTag string
	TargetRunnerUpSim float64
}

type TagVerdictResult struct {
	Index   int
	Verdict types.TagVerdict
}

const AdjudicationSystemMessage = "You are a tag-taxonomy classifier. Decide whether pairs of tag terms denote the same concept."

func BuildAdjudicationPrompt(pairs []TagPairEvidence) string {
	var sb strings.Builder
	sb.WriteString("For each pair below, decide whether the two terms denote the same concept.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"pairs\":[{\"index\":0,\"verdict\":\"same\"},...]}, one entry per pair, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"same\": the terms denote one concept (synonyms, spelling variants, or exact equivalents)\n")
	sb.WriteString("- \"variant\": closely related but distinct concepts (one is a narrower or broader form of the other)\n")
	sb.WriteString("- \"related\": related but different concepts\n")
	sb.WriteString("Use \"same\" only when the terms denote one concept.\n\n")
	for i, p := range pairs {
		fmt.Fprintf(&sb, "Pair %d:\n", i)
		fmt.Fprintf(&sb, "  query: %q\n", p.Query)
		fmt.Fprintf(&sb, "  target: %q\n", p.Target)
		fmt.Fprintf(&sb, "  query-target embedding similarity: %.3f\n", p.Sim)
		if p.RunnerUpSim > 0 {
			fmt.Fprintf(&sb, "  query's runner-up similarity: %.3f\n", p.RunnerUpSim)
		}
		if p.TargetRunnerUpTag != "" {
			fmt.Fprintf(&sb, "  target's nearest other store tag: %q (similarity %.3f)\n", p.TargetRunnerUpTag, p.TargetRunnerUpSim)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func ParseAdjudicationResponse(content string, n int) ([]TagVerdictResult, error) {
	content = utils.CleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Pairs []struct {
			Index   int    `json:"index"`
			Verdict string `json:"verdict"`
		} `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse adjudication response: %w", err)
	}
	results := make([]TagVerdictResult, n)
	for i := range results {
		results[i].Index = i
	}
	for _, p := range parsed.Pairs {
		if p.Index < 0 || p.Index >= n {
			continue
		}
		v, err := types.ParseTagVerdict(p.Verdict)
		if err != nil {
			continue
		}
		results[p.Index].Verdict = v
	}
	return results, nil
}

type TagSplitVerdict string

const (
	TagSplitKeep  TagSplitVerdict = "keep"
	TagSplitSplit TagSplitVerdict = "split"
)

type TagSplitResult struct {
	Verdict    TagSplitVerdict
	Components []string
}

const TagSplitSystemMessage = "You are a tag-taxonomy classifier. Decide whether a tag denotes one concept or several distinct concepts."

func BuildTagSplitPrompt(tags []string) string {
	var sb strings.Builder
	sb.WriteString("For each tag below, decide whether it denotes one single concept or several distinct concepts that must become separate tags.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"splits\":[{\"index\":0,\"verdict\":\"split\",\"components\":[\"first concept\",\"second concept\"]},...]}, one entry per tag, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"keep\": the tag denotes one single concept, including established proper names and set phrases\n")
	sb.WriteString("- \"split\": the tag joins distinct concepts and must become separate tags; list the distinct concepts in \"components\"\n")
	sb.WriteString("Use \"split\" only when the joined concepts are genuinely distinct and each deserves its own tag.\n\n")
	for i, tag := range tags {
		fmt.Fprintf(&sb, "Tag %d: %q\n", i, tag)
	}
	return sb.String()
}

func ParseTagSplitResponse(content string, n int) ([]TagSplitResult, error) {
	content = utils.CleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Splits []struct {
			Index      int      `json:"index"`
			Verdict    string   `json:"verdict"`
			Components []string `json:"components"`
		} `json:"splits"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag split response: %w", err)
	}
	results := make([]TagSplitResult, n)
	for _, s := range parsed.Splits {
		if s.Index < 0 || s.Index >= n {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(s.Verdict)) {
		case string(TagSplitKeep):
			results[s.Index].Verdict = TagSplitKeep
		case string(TagSplitSplit):
			components := make([]string, 0, len(s.Components))
			for _, c := range s.Components {
				if c = strings.TrimSpace(c); c != "" {
					components = append(components, c)
				}
			}
			if len(components) < 2 {
				continue
			}
			results[s.Index].Verdict = TagSplitSplit
			results[s.Index].Components = components
		}
	}
	return results, nil
}
