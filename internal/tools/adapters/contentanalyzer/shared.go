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
- Tags: Between {{.MinTags}} and {{.MaxTags}} thematic tags describing the document's topics and domains. English only. Each tag is one to three words naming a recognizable subject, field, or period. Write each tag in house form: base/lemma form for countable nouns and verbs ('building', not 'buildings'), while established inflected names stand ('data', 'human rights') and lexicalized derivations and gerunds stand ('programming', 'learning'); ordinals and numerals in numeric style ('world war 2', '19th century', never 'world war ii' or 'second world war'); the conventional written form of the term ('c++', not 'c plus plus'); never a dictionary headword substituted for the term's usual name ('data', not 'datum'). Tags describe facets the title does not already state. Tags are conceptual categories — fields, domains, disciplines, periods, methods. Tags describe subject matter only; what the document is — its form or genre — belongs in the document type field, never in tags. Tags are never specific people or works. A place may be a tag only when it denotes a country or a continent — including contested or partially recognized states such as 'palestine' or 'taiwan' — never a city, province, region, or other sub-national place. Never merge two distinct concepts into a single tag with a conjunction such as "and", "or", or "vs" — emit each concept as its own tag. Use only widely-recognized standard terminology a general educated audience would know. Prefer the most standard, widely-recognized term for the subject; use a narrower term only when the document is specifically about that subfield. If an existing suggestion tag captures the concept adequately, prefer it over inventing a narrower label. Each tag is an object with keys: term, category, is_standard_term. Category is exactly one of: topic (subject matter), form_genre (what the document is), place, person, work, other. is_standard_term is true only when the term is widely-recognized standard terminology, not a coinage specific to this document. Do not emit tags like these: 'literary anthology' (a form — it belongs in the document type), 'racial scapegoating' (an argument specific to this document, not standard terminology), 'newfoundland' (a sub-national place — only countries and continents may be tags), 'bail spring replacement' (a device detail too narrow for a shared vocabulary).{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in their own native script/language — only when you can independently determine it; if you are not confident of the person's actual native form, leave this empty rather than copying how this document renders the name), name_romanized (a Latin-script/romanized form of the name — always provide this, even when name is already in Latin script), and a type from the list below. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions. Note: names captured here must NOT be re-used as tags.
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.
Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags (array of objects with keys: term, category, is_standard_term), people (array of objects with keys: name, name_romanized, type), language.

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
		" Every tag must either come from this list of existing tags or be a defensible standard subject term that this document specifically warrants: '%s'",
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

type TagDetail struct {
	Term           string `json:"term"`
	Category       string `json:"category"`
	IsStandardTerm *bool  `json:"is_standard_term"`
}

const (
	TagCategoryTopic     = "topic"
	TagCategoryFormGenre = "form_genre"
	TagCategoryPlace     = "place"
	TagCategoryPerson    = "person"
	TagCategoryWork      = "work"
	TagCategoryOther     = "other"
)

func normalizeTagCategory(raw string) string {
	c := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch c {
	case TagCategoryTopic, TagCategoryFormGenre, TagCategoryPlace, TagCategoryPerson, TagCategoryWork:
		return c
	default:
		return TagCategoryOther
	}
}

type analysisWire struct {
	Title    string          `json:"title"`
	DocType  string          `json:"type"`
	Tags     json.RawMessage `json:"tags"`
	People   []PeopleResult  `json:"people"`
	Language string          `json:"language"`
}

func ParseAnalysisResponse(content string) (*AnalysisResult, error) {
	var wire analysisWire
	if err := json.Unmarshal([]byte(content), &wire); err != nil {
		return nil, fmt.Errorf("parse analysis response: %w", err)
	}
	var elements []json.RawMessage
	if len(wire.Tags) > 0 {
		if err := json.Unmarshal(wire.Tags, &elements); err != nil {
			return nil, fmt.Errorf("parse tags: %w", err)
		}
	}
	tags := make([]string, 0, len(elements))
	details := make([]TagDetail, 0, len(elements))
	for _, el := range elements {
		var term string
		if err := json.Unmarshal(el, &term); err == nil {
			tags = append(tags, term)
			details = append(details, TagDetail{Term: term, Category: TagCategoryOther})
			continue
		}
		var d TagDetail
		if err := json.Unmarshal(el, &d); err != nil {
			continue
		}
		if strings.TrimSpace(d.Term) == "" {
			continue
		}
		d.Category = normalizeTagCategory(d.Category)
		tags = append(tags, d.Term)
		details = append(details, d)
	}
	return &AnalysisResult{
		Title:      wire.Title,
		DocType:    wire.DocType,
		Tags:       tags,
		TagDetails: details,
		People:     wire.People,
		Language:   wire.Language,
	}, nil
}

var continentNames = []string{
	"africa", "antarctica", "asia", "europe",
	"north america", "south america", "oceania", "america",
}

var countryNames = []string{
	// UN member and observer states.
	"afghanistan",
	"albania",
	"algeria",
	"andorra",
	"angola",
	"antigua and barbuda",
	"argentina",
	"armenia",
	"australia",
	"austria",
	"azerbaijan",
	"bahamas",
	"bahrain",
	"bangladesh",
	"barbados",
	"belarus",
	"belgium",
	"belize",
	"benin",
	"bhutan",
	"bolivia",
	"bosnia and herzegovina",
	"botswana",
	"brazil",
	"brunei",
	"bulgaria",
	"burkina faso",
	"burundi",
	"cabo verde",
	"cambodia",
	"cameroon",
	"canada",
	"central african republic",
	"chad",
	"chile",
	"china",
	"colombia",
	"comoros",
	"congo",
	"costa rica",
	"croatia",
	"cuba",
	"cyprus",
	"czechia",
	"democratic republic of the congo",
	"denmark",
	"djibouti",
	"dominica",
	"dominican republic",
	"ecuador",
	"egypt",
	"el salvador",
	"equatorial guinea",
	"eritrea",
	"estonia",
	"eswatini",
	"ethiopia",
	"fiji",
	"finland",
	"france",
	"gabon",
	"gambia",
	"georgia",
	"germany",
	"ghana",
	"greece",
	"grenada",
	"guatemala",
	"guinea",
	"guinea-bissau",
	"guyana",
	"haiti",
	"holy see",
	"honduras",
	"hungary",
	"iceland",
	"india",
	"indonesia",
	"iran",
	"iraq",
	"ireland",
	"israel",
	"italy",
	"ivory coast",
	"jamaica",
	"japan",
	"jordan",
	"kazakhstan",
	"kenya",
	"kiribati",
	"kuwait",
	"kyrgyzstan",
	"laos",
	"latvia",
	"lebanon",
	"lesotho",
	"liberia",
	"libya",
	"liechtenstein",
	"lithuania",
	"luxembourg",
	"madagascar",
	"malawi",
	"malaysia",
	"maldives",
	"mali",
	"malta",
	"marshall islands",
	"mauritania",
	"mauritius",
	"mexico",
	"micronesia",
	"moldova",
	"monaco",
	"mongolia",
	"montenegro",
	"morocco",
	"mozambique",
	"myanmar",
	"namibia",
	"nauru",
	"nepal",
	"netherlands",
	"new zealand",
	"nicaragua",
	"niger",
	"nigeria",
	"north korea",
	"north macedonia",
	"norway",
	"oman",
	"pakistan",
	"palau",
	"palestine",
	"panama",
	"papua new guinea",
	"paraguay",
	"peru",
	"philippines",
	"poland",
	"portugal",
	"qatar",
	"romania",
	"russia",
	"rwanda",
	"saint kitts and nevis",
	"saint lucia",
	"saint vincent and the grenadines",
	"samoa",
	"san marino",
	"sao tome and principe",
	"saudi arabia",
	"senegal",
	"serbia",
	"seychelles",
	"sierra leone",
	"singapore",
	"slovakia",
	"slovenia",
	"solomon islands",
	"somalia",
	"south africa",
	"south korea",
	"south sudan",
	"spain",
	"sri lanka",
	"sudan",
	"suriname",
	"sweden",
	"switzerland",
	"syria",
	"tajikistan",
	"tanzania",
	"thailand",
	"timor-leste",
	"togo",
	"tonga",
	"trinidad and tobago",
	"tunisia",
	"turkey",
	"turkmenistan",
	"tuvalu",
	"uganda",
	"ukraine",
	"united arab emirates",
	"united kingdom",
	"united states",
	"uruguay",
	"uzbekistan",
	"vanuatu",
	"vatican city",
	"venezuela",
	"vietnam",
	"yemen",
	"zambia",
	"zimbabwe",
	// Contested and partially recognized states.
	"abkhazia",
	"kosovo",
	"northern cyprus",
	"sahrawi arab democratic republic",
	"somaliland",
	"south ossetia",
	"taiwan",
	"transnistria",
	"western sahara",
	// Short and alternate names.
	"antigua barbuda",
	"bosnia herzegovina",
	"britain",
	"brunei darussalam",
	"burma",
	"cape verde",
	"congo-brazzaville",
	"congo-kinshasa",
	"cote d'ivoire",
	"cote d ivoire",
	"czech republic",
	"democratic republic of congo",
	"dprk",
	"drc",
	"east timor",
	"federated states of micronesia",
	"great britain",
	"holland",
	"macedonia",
	"palestinian territories",
	"republic of korea",
	"republic of the congo",
	"russian federation",
	"st kitts and nevis",
	"st lucia",
	"st vincent and the grenadines",
	"st. kitts and nevis",
	"st. lucia",
	"st. vincent and the grenadines",
	"state of palestine",
	"swaziland",
	"trinidad tobago",
	"turkiye",
	"u.a.e.",
	"u.k.",
	"u.s.",
	"u.s.a.",
	"uae",
	"uk",
	"united states of america",
	"us",
	"usa",
	"vatican",
	"viet nam",
}

var placeWhitelist = buildPlaceWhitelist()

func placeWhitelistKey(term string) string {
	return strings.TrimPrefix(normalizeCore(term), "the ")
}

func buildPlaceWhitelist() map[string]struct{} {
	names := make([]string, 0, len(continentNames)+len(countryNames))
	names = append(names, continentNames...)
	names = append(names, countryNames...)
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[placeWhitelistKey(n)] = struct{}{}
	}
	return m
}

func IsWhitelistedPlace(term string) bool {
	_, ok := placeWhitelist[placeWhitelistKey(term)]
	return ok
}

type FacetDrop struct {
	Term     string
	Category string
	Reason   string
}

func classifyCategory(tag, category string, docTypes map[string]struct{}) (string, bool) {
	switch category {
	case TagCategoryFormGenre:
		if _, ok := docTypes[tag]; ok {
			return "matches_document_type", true
		}
		return "model_self_classification", true
	case TagCategoryPerson:
		return "people_facet", true
	case TagCategoryWork:
		return "forbidden_category", true
	case TagCategoryPlace:
		if !IsWhitelistedPlace(tag) {
			return "non_whitelisted_place", true
		}
	}
	return "", false
}

func SeparateFacets(tags []string, details []TagDetail, docTypeNames []string) ([]string, []FacetDrop) {
	if len(details) == 0 {
		return tags, nil
	}
	categoryByTerm := make(map[string]string, len(details))
	for _, d := range details {
		categoryByTerm[normalizeCore(d.Term)] = d.Category
	}
	docTypes := make(map[string]struct{}, len(docTypeNames))
	for _, name := range docTypeNames {
		docTypes[normalizeCore(name)] = struct{}{}
	}
	kept := make([]string, 0, len(tags))
	var dropped []FacetDrop
	for _, tag := range tags {
		category, ok := categoryByTerm[tag]
		if !ok {
			kept = append(kept, tag)
			continue
		}
		if reason, drop := classifyCategory(tag, category, docTypes); drop {
			dropped = append(dropped, FacetDrop{Term: tag, Category: category, Reason: reason})
			continue
		}
		kept = append(kept, tag)
	}
	return kept, dropped
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
	Index     int
	Verdict   types.TagVerdict
	Direction string
}

// direction answers for a same-intent pair: which term's standard sense is
// exactly the dimension; a tie leaves the choice to the deterministic
// tie-break
const (
	TagDirectionQuery  = "query"
	TagDirectionTarget = "target"
	TagDirectionTie    = "tie"
)

const AdjudicationSystemMessage = "You are a tag-taxonomy classifier. Decide whether pairs of tag terms denote one value."

func BuildAdjudicationPrompt(pairs []TagPairEvidence) string {
	var sb strings.Builder
	sb.WriteString("For each pair below, decide whether the two terms denote one value: faithful replacement holds when either term can stand for the other without loss or change of meaning.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"pairs\":[{\"index\":0,\"verdict\":\"same\",\"direction\":\"query\"},...]}, one entry per pair, no explanations.\n")
	sb.WriteString("Verdict must be exactly one of:\n")
	sb.WriteString("- \"same\": the terms denote one value (morphographic variants, spelling variants, or intent-equivalent derivations)\n")
	sb.WriteString("- \"variant\": closely related but distinct concepts (one is a narrower or broader form of the other)\n")
	sb.WriteString("- \"related\": related but different concepts\n")
	sb.WriteString("Use \"same\" only when faithful replacement holds; when in doubt, answer \"variant\" or \"related\".\n")
	sb.WriteString("When the verdict is \"same\", also answer which term is the precise one: \"direction\":\"query\" when the query term's standard sense is exactly the dimension, \"direction\":\"target\" when the target term's is, \"direction\":\"tie\" when neither is more precise. A polysemous term loses to the precise one.\n\n")
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
			Index     int    `json:"index"`
			Verdict   string `json:"verdict"`
			Direction string `json:"direction"`
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
		switch p.Direction {
		case TagDirectionQuery, TagDirectionTarget, TagDirectionTie:
			results[p.Index].Direction = p.Direction
		}
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

type TagQualityCandidate struct {
	Index            int
	Term             string
	Category         string
	WhitelistedPlace bool
}

type TagQualityResult struct {
	Index              int
	SubjectMatter      bool
	StandardTerm       bool
	NotPersonPlaceWork bool
	AppropriatelyBroad bool
}

func (r TagQualityResult) Passed() bool {
	return r.SubjectMatter && r.StandardTerm && r.NotPersonPlaceWork && r.AppropriatelyBroad
}

// TagQualityCriteria is the persisted wire shape of one candidate's rubric
// verdict. Pointer fields distinguish an omitted key from an explicit false.
type TagQualityCriteria struct {
	SubjectMatter      *bool `json:"subject_matter"`
	StandardTerm       *bool `json:"standard_term"`
	NotPersonPlaceWork *bool `json:"not_person_place_work"`
	AppropriatelyBroad *bool `json:"appropriately_broad"`
}

func (r TagQualityResult) Criteria() TagQualityCriteria {
	return TagQualityCriteria{
		SubjectMatter:      &r.SubjectMatter,
		StandardTerm:       &r.StandardTerm,
		NotPersonPlaceWork: &r.NotPersonPlaceWork,
		AppropriatelyBroad: &r.AppropriatelyBroad,
	}
}

func (c TagQualityCriteria) Complete() bool {
	return c.SubjectMatter != nil && c.StandardTerm != nil &&
		c.NotPersonPlaceWork != nil && c.AppropriatelyBroad != nil
}

func (c TagQualityCriteria) String() string {
	return fmt.Sprintf("subject_matter=%t standard_term=%t not_person_place_work=%t appropriately_broad=%t",
		c.SubjectMatter != nil && *c.SubjectMatter,
		c.StandardTerm != nil && *c.StandardTerm,
		c.NotPersonPlaceWork != nil && *c.NotPersonPlaceWork,
		c.AppropriatelyBroad != nil && *c.AppropriatelyBroad)
}

// TagQualityVersion pins the measurement generation: bump it whenever the
// criteria text or the prompt changes, so cached verdicts from an older
// generation never apply to a newer one.
const TagQualityVersion = "v1"

const TagQualitySystemMessage = "You are a tag-quality judge. Decide whether each candidate tag is fit for a shared document vocabulary."

func BuildTagQualityPrompt(text string, candidates []TagQualityCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate tag below, answer four yes/no questions about its fitness as a tag for the document.\n")
	sb.WriteString("1. subject_matter: is the tag a subject matter of the document, not its form or genre?\n")
	sb.WriteString("2. standard_term: is the tag standard, widely-recognized terminology, not a coinage specific to this document?\n")
	sb.WriteString("3. not_person_place_work: is the tag neither a person, nor a work, nor a place other than a whitelisted country or continent? A whitelisted country or continent is a legitimate place tag and passes this criterion; only a person, a work, or a sub-national place (a city, province, region, or other non-sovereign territory) fails it.\n")
	sb.WriteString("4. appropriately_broad: is the tag appropriately broad for a shared vocabulary, not a per-document detail?\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"tags\":[{\"index\":0,\"subject_matter\":true,\"standard_term\":true,\"not_person_place_work\":true,\"appropriately_broad\":true},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Document text:\n")
	sb.WriteString(text)
	sb.WriteString("\n\nCandidate tags:\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Tag %d: term=%q category=%s", c.Index, c.Term, c.Category)
		if c.WhitelistedPlace {
			sb.WriteString(" (whitelisted country or continent — a legitimate place tag; answer not_person_place_work: true)")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseTagQualityResponse returns an error when any candidate lacks a fully
// specified verdict: a zero-valued result must never be read as an
// all-criteria-false rejection, or a truncated response would cache a spurious
// permanent rejection.
func ParseTagQualityResponse(content string, n int) ([]TagQualityResult, error) {
	content = utils.CleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Tags []struct {
			Index int `json:"index"`
			TagQualityCriteria
		} `json:"tags"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag quality response: %w", err)
	}
	results := make([]TagQualityResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for _, t := range parsed.Tags {
		if t.Index < 0 || t.Index >= n || !t.Complete() {
			continue
		}
		results[t.Index] = TagQualityResult{
			Index:              t.Index,
			SubjectMatter:      *t.SubjectMatter,
			StandardTerm:       *t.StandardTerm,
			NotPersonPlaceWork: *t.NotPersonPlaceWork,
			AppropriatelyBroad: *t.AppropriatelyBroad,
		}
		present[t.Index] = true
	}
	var missing []int
	for i, ok := range present {
		if !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		return results, fmt.Errorf("incomplete tag quality response: no verdict for candidate indices %v", missing)
	}
	return results, nil
}

type TagIdentityCandidate struct {
	Index        int
	Term         string
	ProposedFold string
}

type TagIdentityResult struct {
	Index                   int
	FoldToBase              bool
	FoldPreservesDenotation bool
	ConventionalForm        bool
}

// the persisted wire shape of one candidate's identity verdict; pointer
// fields distinguish an omitted key from an explicit false
type TagIdentityCriteria struct {
	FoldToBase              *bool `json:"fold_to_base"`
	FoldPreservesDenotation *bool `json:"fold_preserves_denotation"`
	ConventionalForm        *bool `json:"conventional_form"`
}

func (r TagIdentityResult) Criteria() TagIdentityCriteria {
	return TagIdentityCriteria{
		FoldToBase:              &r.FoldToBase,
		FoldPreservesDenotation: &r.FoldPreservesDenotation,
		ConventionalForm:        &r.ConventionalForm,
	}
}

// the three criterion answers plus the fold the mechanism proposed
func (r TagIdentityResult) CriteriaRecord(proposedFold string) []byte {
	record := struct {
		TagIdentityCriteria
		ProposedFold string `json:"proposed_fold,omitempty"`
	}{TagIdentityCriteria: r.Criteria(), ProposedFold: proposedFold}
	raw, err := json.Marshal(record)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func (c TagIdentityCriteria) Complete() bool {
	return c.FoldToBase != nil && c.FoldPreservesDenotation != nil && c.ConventionalForm != nil
}

func (c TagIdentityCriteria) String() string {
	return fmt.Sprintf("fold_to_base=%t fold_preserves_denotation=%t conventional_form=%t",
		c.FoldToBase != nil && *c.FoldToBase,
		c.FoldPreservesDenotation != nil && *c.FoldPreservesDenotation,
		c.ConventionalForm != nil && *c.ConventionalForm)
}

const TagIdentitySystemMessage = "You are a tag-identity judge. Decide the canonical written form of each tag term."

func BuildTagIdentityPrompt(candidates []TagIdentityCandidate) string {
	var sb strings.Builder
	sb.WriteString("For each candidate tag term below, answer three yes/no questions about its canonical written form.\n")
	sb.WriteString("1. fold_to_base: should the term be written in its base form? A relational adjective falls to its noun when it denotes the same thing (chilean -> chile). An inflected form stands when it is the established name of the sense (data, human rights) or denotes a different sense than the base (arms is not arm). Lexicalized derivations and gerunds stand (programming, learning, running); a derived word that names its own concept is never reduced to a root. Answer false when the term already stands or its proposed fold would cross denotation.\n")
	sb.WriteString("2. fold_preserves_denotation: does the proposed fold preserve the term's denotation, without generalizing, specializing, or merging sibling concepts? Answer true when no fold is proposed.\n")
	sb.WriteString("3. conventional_form: is the term written in the conventional form its domain uses? Spelled-out symbols are not conventional (c plus plus should be c++). Answer true when the term is already conventional.\n")
	sb.WriteString("Answer with ONLY a JSON object: {\"terms\":[{\"index\":0,\"fold_to_base\":false,\"fold_preserves_denotation\":true,\"conventional_form\":true},...]}, one entry per candidate, no explanations.\n\n")
	sb.WriteString("Candidate terms:\n")
	for _, c := range candidates {
		fmt.Fprintf(&sb, "Tag %d: term=%q", c.Index, c.Term)
		if c.ProposedFold != "" && c.ProposedFold != c.Term {
			fmt.Fprintf(&sb, " proposed_fold=%q", c.ProposedFold)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// returns an error when any candidate lacks a fully specified verdict: a
// zero-valued result must never be read as an all-criteria-false rejection
func ParseTagIdentityResponse(content string, n int) ([]TagIdentityResult, error) {
	content = utils.CleanCodeBlock(strings.TrimSpace(content))
	var parsed struct {
		Terms []struct {
			Index int `json:"index"`
			TagIdentityCriteria
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse tag identity response: %w", err)
	}
	results := make([]TagIdentityResult, n)
	present := make([]bool, n)
	for i := range results {
		results[i].Index = i
	}
	for _, t := range parsed.Terms {
		if t.Index < 0 || t.Index >= n || !t.Complete() {
			continue
		}
		results[t.Index] = TagIdentityResult{
			Index:                   t.Index,
			FoldToBase:              *t.FoldToBase,
			FoldPreservesDenotation: *t.FoldPreservesDenotation,
			ConventionalForm:        *t.ConventionalForm,
		}
		present[t.Index] = true
	}
	var missing []int
	for i, ok := range present {
		if !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		return results, fmt.Errorf("incomplete tag identity response: no verdict for candidate indices %v", missing)
	}
	return results, nil
}
