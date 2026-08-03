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
	"unicode"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/utils"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var nonAlphaKeepSpaces = regexp.MustCompile(`[^a-z ]`)
var multiSpaceRE = regexp.MustCompile(` +`)

const (
	maxTags          = 5
	tagRequestBuffer = 3
)

const requestedTagCount = maxTags + tagRequestBuffer // 8

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
- Document title: In excerpts language, truncate to 127 characters if longer
{{.DocTypePrompt}}
- Tags: At most {{.RequestedTags}} thematic tags describing the document's topics and domains. English only. Each tag is one to three words naming a recognizable subject, field, or period. Tags describe facets the title does not already state. For names containing symbols (e.g., C++, C#), use the conventional spelled-out form (e.g., c plus plus, c sharp). Tags are conceptual categories — fields, domains, disciplines, periods, methods. Tags are never specific people, works, or places. Use only widely-recognized standard terminology a general educated audience would know. If an existing suggestion tag captures the concept adequately, prefer it over inventing a narrower label.{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in their own native script/language — only when you can independently determine it; if you are not confident of the person's actual native form, leave this empty rather than copying how this document renders the name), name_romanized (a Latin-script/romanized form of the name — always provide this, even when name is already in Latin script), and a type from the list below. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions. Note: names captured here must NOT be re-used as tags.
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.
Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}`

type promptData struct {
	DocTypePrompt string
	TagsPrompt    string
	PeoplePrompt  string
	Text          string
	RequestedTags int
}

func BuildPrompt(text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string, customTemplate string) string {
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
		RequestedTags: requestedTagCount,
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
	return fmt.Sprintf(
		" Prefer tags from the following list if thematically related to document excerpts: '%s'",
		strings.Join(tags, ","),
	)
}

var accentFolder transform.Transformer = transform.Chain(
	norm.NFD,
	runes.Remove(runes.In(unicode.Mn)),
	norm.NFC,
)

func foldAccents(s string) string {
	out, _, err := transform.String(accentFolder, s)
	if err != nil {
		return s
	}
	return out
}

func normalizeCore(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = foldAccents(s)
	s = nonAlphaKeepSpaces.ReplaceAllString(s, "")
	s = multiSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
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

func FilterTags(tags []string, people []PeopleResult, knownPeopleNormalized []string, title string, docTypeNames []string) []string {
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

	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagTokens := strings.Fields(tag)
		if len(tagTokens) > 3 || len(tagTokens) == 0 {
			continue
		}

		matchesName := false
		for _, tok := range tagTokens {
			if _, ok := nameTokens[tok]; ok {
				matchesName = true
				break
			}
		}
		if matchesName {
			continue
		}

		matchesKnownPerson := false
		if len(tagTokens) >= 2 {
			for _, kset := range knownTokenSets {
				allInKnown := true
				for _, tok := range tagTokens {
					if _, ok := kset[tok]; !ok {
						allInKnown = false
						break
					}
				}
				if allInKnown {
					matchesKnownPerson = true
					break
				}
			}
		}
		if matchesKnownPerson {
			continue
		}

		matchesDocType := false
		for _, tok := range tagTokens {
			if _, ok := docTypeTokens[tok]; ok {
				matchesDocType = true
				break
			}
		}
		if matchesDocType {
			continue
		}

		allInTitle := true
		for _, tok := range tagTokens {
			if _, ok := titleTokens[tok]; !ok {
				allInTitle = false
				break
			}
		}
		if allInTitle {
			continue
		}

		result = append(result, tag)
	}

	if len(result) > maxTags {
		result = result[:maxTags]
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
