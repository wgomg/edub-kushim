package analyzer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// SystemMessage is the system prompt of the document analysis call.
const SystemMessage = "You are a helpful assistant specialized in document analysis and metadata extraction"

const defaultPromptTemplate = `Analyze the excerpts of a document provided below and extract the following data:
- Document title: The title as written in the document, in excerpts language, copied verbatim — never invent or append wording of your own; colons and subtitles are legitimate only when the original title itself has one. If no title line appears in the excerpts, use the first substantive line of the text as the title, still copied verbatim. Truncate to 127 characters if longer
{{.DocTypePrompt}}
- Tags: At least {{.MinTags}} tags naming the broad subjects the document is about — the topics a reader would use to find other documents like this one. English only. Each tag is one to three words naming one subject, field, or period. Write each tag in house form: base/lemma form for countable nouns and verbs ('building', not 'buildings'), while established inflected names stand ('data', 'human rights') and lexicalized derivations and gerunds stand ('programming', 'learning'); ordinals and numerals in numeric style ('world war 2', '19th century', never 'world war ii' or 'second world war'); the conventional written form of the term ('c++', not 'c plus plus'); never a dictionary headword substituted for the term's usual name ('data', not 'datum'). Tags are conceptual categories — fields, domains, disciplines, periods, methods. Tags describe subject matter only; what the document is as a publication — its publishing format (a book, article, tutorial, report) — belongs in the document type field, never in tags. A genre or art form (poetry, science fiction, autobiography) is subject matter and may be a tag. Tags are never specific people or works. A place may be a tag only when it denotes a country or a continent — including contested or partially recognized states such as 'palestine' or 'taiwan' — never a city, province, region, or other sub-national place. Never merge two distinct concepts into a single tag with a conjunction such as "and", "or", or "vs": emit each part as its own tag. When the parts of a phrase each name a subject that would work as a tag on its own, emit the parts as separate tags instead of the phrase — 'russia' and 'history', not 'russian history'; 'philosophy' and 'mind', not 'philosophy of mind'; 'air fryer' and 'repair', not 'air fryer repair'; 'sql' and 'programming', not 'sql programming'. A phrase that names one subject its parts cannot compose stays whole ('cold war', 'internet of things', 'black bloc', '19th century', 'air fryer'). Never emit the document's own argument, thesis, or unique details as tags. If an existing suggestion tag captures the concept adequately, prefer it over inventing a narrower label. Each tag is an object with keys: term, category. Category is exactly one of: topic (subject matter), form_genre (the document's publishing format), place, person, work, other. Do not emit tags like these: 'literary anthology' (a form — it belongs in the document type), 'racial scapegoating' (an argument specific to this document), 'newfoundland' (a sub-national place — only countries and continents may be tags).{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in their own native script/language — only when you can independently determine it; if you are not confident of the person's actual native form, leave this empty rather than copying how this document renders the name), name_romanized (a Latin-script/romanized form of the name — always provide this, even when name is already in Latin script), and a type from the list below. Only include individuals who play a substantive role in the document's creation, execution, or primary subject matter — exclude incidental mentions. Note: names captured here must NOT be re-used as tags.
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn','fra','deu','zho','kor','ara','por','rus'). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort if the text is truly too short or ambiguous to determine.
Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags (array of objects with keys: term, category), people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}`

type promptData struct {
	DocTypePrompt string
	TagsPrompt    string
	PeoplePrompt  string
	Text          string
	MinTags       int
	MaxTags       int
}

// BuildPrompt renders the analysis prompt through the custom template
// when one is given (falling back to the default on template errors),
// with the excerpts, the offered type options and the tag bounds; the
// default template renders only the minimum count.
func BuildPrompt(text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string, customTemplate string, minTags, maxTags int) string {
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
		// Best-effort fallback: a failed render leaves the buffer as written.
		_ = fallback.Execute(&buf, data)
	}

	return buf.String()
}

func peopleTypePrompt(types []TypeRef) string {
	var sb strings.Builder
	sb.WriteString("  Available types:\n")
	for _, t := range types {
		fmt.Fprintf(&sb, "    - %s (%s)\n", t.Name, t.Description)
	}
	return sb.String()
}

func documentTypePrompt(types []TypeRef) string {
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

// ExtractHeadTailWords reduces content to its first and last words, the
// only sections the document-type refinement reads.
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

// DocMetadata is the document-level context the type-refinement prompt
// renders: word and page counts and the MIME type, all optional.
type DocMetadata struct {
	WordCount int32
	PageCount int32
	MimeType  string
}

// Format renders the metadata for the prompt, skipping the zero values;
// empty when nothing is known.
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

// BuildDocTypePrompt renders the type-refinement prompt: the offered
// types, the document's metadata, and the head and tail excerpts.
func BuildDocTypePrompt(headTailText string, docTypes []TypeRef, metadata DocMetadata) string {
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
