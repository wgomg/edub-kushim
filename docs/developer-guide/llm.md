# Developer Guide — LLM Integration and Enrichment

This guide explains how edub-kushim talks to LLM providers: the content
analyzer adapters (Anthropic + OpenAI-compatible), the prompt contract, JSON
result parsing, the error taxonomy (with the token/credit handling that
drives batch pausing), the model catalog registry, and the enricher pipeline
that ties it all to documents.

Audience: developers touching `internal/llm/analyzer/`,
`internal/llm/`, `internal/enrichment/`, or the `enricher.*` config, or anyone
adding a new model/provider.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [The ContentAnalyzer interface](#2-the-contentanalyzer-interface)
3. [The prompt contract](#3-the-prompt-contract)
4. [The Anthropic adapter](#4-the-anthropic-adapter)
5. [The OpenAI-compatible adapter](#5-the-openai-compatible-adapter)
6. [JSON parsing and validation](#6-json-parsing-and-validation)
7. [The error taxonomy](#7-the-error-taxonomy)
8. [Token estimation](#8-token-estimation)
9. [The model catalog and registry](#9-the-model-catalog-and-registry)
10. [The enricher pipeline](#10-the-enricher-pipeline)
11. [Configuration knobs](#11-configuration-knobs)
12. [Gotchas](#12-gotchas)

---

## 1. Orientation

The enrichment step analyzes a document's text and produces structured
metadata — title, document type, tags, people, language — via an LLM. The
pieces:

| Piece | Files | Role |
|---|---|---|
| `ContentAnalyzer` interface | `analyzer/adapter.go` | the capability contract |
| Anthropic adapter | `analyzer/llm_anthropic.go` | Claude via `POST /messages` |
| OpenAI-compatible adapter | `analyzer/openai_compatible.go` | anything with a `/chat/completions` API |
| Prompt, parse and gate logic | `analyzer/prompt.go` · `analyzer/facets.go` · `analyzer/gates.go` · the per-step files | system prompt, template, JSON cleaning, pre-flight errors, judged-step prompts/parsers |
| Model catalogs | `llm/registry.go` + `provider_catalog.json`/`model_catalog.json` | provider endpoints and per-model capabilities |
| The enricher | `internal/enrichment/enricher.go` | orchestrates: reduce → match → analyze → persist |
| Task handler | `internal/enrichment/enrich.go` | the `enrich` task type |

Flow: a document's text is reduced (TextRank — see
`algorithms.md`), tag-matched (see
`semantic-matching.md`), analyzed by the LLM, and the result
is validated and persisted. If the LLM provider reports exhausted credits,
the whole batch pauses (§10).

---

## 2. The ContentAnalyzer interface

`internal/llm/analyzer/adapter.go:33-82`:

```go
type TypeRef struct {
  Name        string
  Description string
}

type ContentAnalyzer interface {
  Analyze(ctx context.Context, text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string) (*AnalysisResult, error)
  AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []TypeRef, metadata DocMetadata) (string, error)
  Adjudicate(ctx context.Context, pairs []TagPairEvidence) ([]TagVerdictResult, error)
  JudgeTagParts(ctx context.Context, values []string) ([]TagPartResult, error)
  JudgeTagCompounds(ctx context.Context, candidates []string) ([]TagCompoundResult, error)
  JudgeTagQuality(ctx context.Context, text string, candidates []TagQualityCandidate) ([]TagQualityResult, error)
  JudgeTagIdentity(ctx context.Context, candidates []TagIdentityCandidate) ([]TagIdentityResult, error)
  Name() string
}

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
```

- `Analyze` — the main call: full text (already reduced), the known document
  types and people types (to constrain the answer), and tag suggestions (the
  semantic matcher's output). Both type lists are `TypeRef` values — the name
  and description each prompt line renders — so the interface carries no
  persistence type.
- `AnalyzeDocType` — a **second, cheaper call** that re-evaluates only the
  document type using the head/tail of the full text (§10). It receives the
  previous result so it can replay the conversation (§4).
- `Adjudicate` — classifies gate subjects against the same-intent test as
  `same` / `variant` / `related` (plus a direction for `same`) with a
  temperature-0 request built from `BuildAdjudicationPrompt`
  and parsed by `ParseAdjudicationResponse` (structured output where the
  provider supports it). Used by the tag adjudicator when
  `enricher.tag_adjudicator.enabled` is true; see the
  [semantic matching guide](semantic-matching.md#identity-adjudication).
- `JudgeTagParts` — the split gate's parts pass: decides whether each value, on
  its own, names a subject a searcher would filter by, with a temperature-0
  request built from `BuildTagPartPrompt` and parsed by
  `ParseTagPartResponse` (an entry without a complete dimensional answer is
  missing; a truncated reply carries its entries alongside
  `*IncompleteReplyError`). Used by the enricher's decomposition step when
  `enricher.tag_adjudicator.enabled` is true.
- `JudgeTagCompounds` — the split gate's compound pass: reads what each
  compound phrase denotes (`named_unit` / `composition` / `no_dimension`, plus
  an abstention) with a temperature-0 request built from
  `BuildTagCompoundPrompt` and parsed by `ParseTagCompoundResponse` (an entry
  outside the trichotomy is missing; a truncated reply carries its entries
  alongside `*IncompleteReplyError`). Used by the enricher's decomposition step
  and the reconciliation compound sweep under the same toggle.
- `JudgeTagIdentity` — answers the three binary canonical-form criteria for a
  flagged candidate (`fold_to_base`, `fold_preserves_denotation`,
  `conventional_form`) with a temperature-0 request built from
  `BuildTagIdentityPrompt` and parsed by `ParseTagIdentityResponse` (a
  candidate without a complete verdict is missing, and a truncated reply
  returns its answers alongside `*IncompleteReplyError`). Used by the tag gate
  when `enricher.tag_adjudicator.enabled` is true.
- `JudgeTagQuality` — judges novel tags against the dimension rubric
  (`subject_matter`, `dimension`, `not_person`, `atomic`,
  `not_form_genre`, plus the optional `defer` abstention) with a temperature-0
  request built from
  `BuildTagQualityPrompt` (the reduced document text scoped to the
  `subject_matter` question, plus one line per candidate
  with its term and typed category) and
  parsed by `ParseTagQualityResponse` (a missing or partially specified
  verdict for any candidate is an incomplete reply, never a silent rejection;
  an omitted `defer` reads as false). Used by the enricher when
  `enricher.tag_quality.enabled` is true; see the
  [semantic matching guide](semantic-matching.md#rubric-judge-for-novel-tags).
- `TagDetails` — the typed mirror of `Tags`, one `{term, category}` entry per
  annotated tag. `category` is folded to `topic` / `form_genre` / `place` /
  `person` / `work` / `other` (a missing or unknown value becomes `other`),
  and a plain-string tag entry yields no detail at all — an unannotated
  emission, not a defaulted category. Consumed once by the enricher's facet
  gate (`SeparateFacets`, §10) and the quality judge's feature pass — never
  persisted or returned to clients.
- `PassContext` (`json:"-"`) carries the exact system+user prompts from the
  first call so the second call can replay them — never serialized to the
  API response.
- `Stats` carries the token usage, persisted as the `stats` member of the
  enrich task result — the stored payload is the
  `{"stats": …, "counts": …}` envelope, where `counts` carries the
  per-document admission tallies (assignments, births, promotions, rejects,
  defers, composition dispositions) the batch settle bookend aggregates.

The factory (`adapter.go:84-100`) requires the model registry, looks up the
configured provider/model capabilities, and switches on `Llm.Adapter`:
`"anthropic"` → the Anthropic adapter, anything else → the
OpenAI-compatible adapter (the default branch — unknown adapter names
silently mean "openai-compatible").

---

## 3. The prompt contract

### The system message

`prompt.go:11`:

```go
const SystemMessage = "You are a helpful assistant specialized in document analysis and metadata extraction"
```

### The default prompt template

`prompt.go:13-21` — a Go `text/template` (not a string concat), the heart
of the extraction contract:

```
Analyze the excerpts of a document provided below and extract the following data:
- Document title: The title as written in the document, in excerpts language, copied verbatim — never invent or append wording of your own; colons and subtitles are legitimate only when the original title itself has one. If no title line appears in the excerpts, use the first substantive line of the text as the title, still copied verbatim. Truncate to 127 characters if longer
{{.DocTypePrompt}}
- Tags: At least {{.MinTags}} tags naming the broad subjects the document is about — the topics a reader would use to find other documents like this one. English only. ... Tags describe subject matter only; what the document is as a publication — its publishing format (a book, article, tutorial, report) — belongs in the document type field, never in tags. A genre or art form (poetry, science fiction, autobiography) is subject matter and may be a tag. Tags are never specific people or works. A place may be a tag only when it denotes a country or a continent — including contested or partially recognized states such as 'palestine' or 'taiwan' — never a city, province, region, or other sub-national place. ... When the parts of a phrase each name a subject that would work as a tag on its own, emit the parts as separate tags instead of the phrase — 'russia' and 'history', not 'russian history'; ... A phrase that names one subject its parts cannot compose stays whole ('cold war', 'internet of things', 'black bloc', '19th century', 'air fryer'). ... Each tag is an object with keys: term, category. Category is exactly one of: topic (subject matter), form_genre (the document's publishing format), place, person, work, other. Do not emit tags like these: 'literary anthology' (a form — it belongs in the document type), 'racial scapegoating' (an argument specific to this document), 'newfoundland' (a sub-national place — only countries and continents may be tags).{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in their own native script/language ...), name_romanized (...), and a type from the list below. ...
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn',...). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort ...

Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags (array of objects with keys: term, category), people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}
```

Design points:

- **The output contract is spelled out in the prompt**: "Return ONLY a json
  string ... with keys: title, type, tags, people, language" — with tags as
  typed objects (`term`, `category`). The adapters parse
  that shape and the older plain-string array alike (§6).
- **Titles are copied, never composed**: the model returns the document's
  own title verbatim — no invented subtitles, summaries, or appended
  clauses; colons/subtitles are legitimate only when the original title has
  one. With no title line in the excerpts, it falls back to the first
  substantive line of the text, also verbatim. The 127-char cap in the
  prompt is guidance; `enricher.go` truncates server-side regardless.
- **The constraint lists are injected** as template sub-blocks:
  `documentTypePrompt` (`"- Document type: choose one of the following:\n  -
  %s (%s)\n"` per type, `prompt.go:79-86`), `peopleTypePrompt`
  (`"  Available types:\n    - %s (%s)\n"`, `prompt.go:70-77`), and
  `tagsPrompt` (`" Every tag must either come from this list of existing tags
  or be a defensible standard subject term that this document specifically
  warrants: '%s'"`, `prompt.go:88-97` — the semantic-matcher suggestions,
  comma-joined).
- **Tag count is a floor, not a range**: the prompt asks for "At least
  `min_tags`" tags (`enricher.contentanalyzer.min_tags`, default 3) and the
  post-processing filter applies no count cap; `max_tags` no longer bounds
  emissions (it still scales the matcher's suggestion list, `TopN = 3 ×
  max_tags`). The prompt forbids merging concepts with a conjunction and
  instructs compound decomposition — when each part of a phrase would work as
  a tag on its own, the parts are emitted instead of the phrase, while fixed
  names and set phrases stay whole. It also states the facet rule — tags
  describe subject matter, publishing formats belong in the document type, and a
  place is a tag only for countries and continents — plus one-line negative
  exemplars for each known failure mode (`literary anthology`,
  `racial scapegoating`, `newfoundland`).
- **`customTemplate` overrides** when set in config
  (`ContentAnalyzer.PromptTemplate`); **any parse/execute error falls back
  silently to the default** (`prompt.go:45-48, 60-65`) — a broken custom
  template degrades, it doesn't crash.
- **Head/tail and metadata** feed the second call (§10):
  `BuildDocTypePrompt` (`prompt.go:143-159`) asks to re-evaluate the type
  from "the opening and closing sections of the full document", with context
  like `"600 total words, 12 pages, application/pdf"` (`DocMetadata.Format`,
  `prompt.go:124-139`).

---

## 4. The Anthropic adapter

`llm_anthropic.go`. Request shape highlights (`llm_anthropic.go:50-62`): the
struct has fields for `stop_sequences`, `stream`, `top_k` — **declared but
never set** (streaming is hardcoded off, no stop sequences, no top-k).
`buildRequest` (`llm_anthropic.go:167-179`) sets the envelope — model, max
tokens, messages, system, `stream: false`, `cache_control: {type:
"ephemeral"}` — then applies the reasoning and temperature gates; every call
site goes through it:

```go
reqBody := l.buildRequest(SystemMessage,
  []anthropicMessage{
    {Role: "user", Content: prompt},
  },
  clampOutputBudget(l.caps, analysisOutputBudget),
  l.llmCfg.Temperature)
```

`doRequest` (`llm_anthropic.go:181-244`) sets the headers (`x-api-key`,
`anthropic-version: 2023-06-01`, `Content-Type`, `Accept`, plus the
catalog-declared session header when present) and decodes the response.

Anthropic-specific rules the adapter encodes:

- **`max_tokens` is mandatory** — every call passes a per-call-type output
  budget: the analysis family (`Analyze`, `AnalyzeDocType`) 4096, each judged
  step `512 + 128 × candidates`. `clampOutputBudget` caps the budget at the
  catalog's `MaxOutputTokens` when set; an uncataloged model keeps the budget
  as-is, so every call is bounded even without a catalog entry.
- **`system` is a top-level field**, not a message role.
- **`anthropic-version: 2023-06-01`** header, `x-api-key` auth (only when a
  token is set).
- **Prompt caching**: `cache_control: {type: "ephemeral"}` on both calls —
  the system prompt + first user turn are cached across requests.
- **Thinking modes** (`applyReasoning`, `llm_anthropic.go:129-153`),
  catalog-gated by `caps.CanDisableReasoning` and `ReasoningEffortLevels`:
  - reasoning off (the programmatic-only toggle defaults to off) →
    `thinking: disabled`;
  - reasoning on, model without effort levels → `thinking: enabled` with a
    `budget_tokens` of `min(maxTokens/2, 32000)` (floor 1024,
    `llm_anthropic.go:75-81`);
  - reasoning on, model with effort levels → `output_config: {effort}` at the
    **lowest cataloged level**, plus `thinking: enabled` **only for
    `claude-opus-4-5`**, which requires a manual budget alongside
    `output_config` (`anthropicManualThinkingModels`,
    `llm_anthropic.go:66-68`).
- **Temperature is catalog-gated**: only sent when
  `caps.SupportsTemperature` (`applyTemperature`, `llm_anthropic.go:158-162`)
  — newer models don't accept it. The doc-type call passes `temp = 0`.

Response parsing (`llm_anthropic.go:181-244`): content blocks with
`block.Type == "text"` are joined in `doRequest`, then `Analyze`
(`llm_anthropic.go:246-283`) runs `ParseAnalysisResponse` (§6) over the
cleaned text; usage → `Stats`; the full prompt is persisted in `Prompt`;
`PassContext` serializes `{system, user_prompt}` for the replay.

**`AnalyzeDocType` replays the conversation** (`llm_anthropic.go:286-337`):

```go
Messages: []anthropicMessage{
  {Role: "user", Content: passCtx.UserPrompt},
  {Role: "assistant", Content: string(assistantJSON)},
  {Role: "user", Content: docTypePrompt},
},
```

— original user turn, then the previous assistant answer (built from the
first result), then the new head/tail question. The call carries the
analysis-family budget (`clampOutputBudget(l.caps, analysisOutputBudget)`)
even though the answer is a single `type` field. Parsing tolerates both a bare
`{"type": "..."}` and a full `AnalysisResult` (`llm_anthropic.go:331-336`).

---

## 5. The OpenAI-compatible adapter

`openai_compatible.go` targets any `/chat/completions` API, plus the Responses
API (`/responses`) for catalog entries that set `uses_responses_endpoint`.
Request build (`buildRequestBody`, `openai_compatible.go:116`, delegating to
`buildRequest`, `openai_compatible.go:125`):

```go
func (l *LlmOpenAiCompatible) buildRequestBody(system, user, assistant string, temp float64, maxOutput int) map[string]any {
  return l.buildRequest(l.buildRequestMessages(system, user, assistant), temp, maxOutput)
}

func (l *LlmOpenAiCompatible) buildRequest(messages []openaiChatMessage, temp float64, maxOutput int) map[string]any {
  if l.caps != nil && l.caps.UsesResponsesEndpoint {
    return l.buildResponsesRequest(messages, temp, maxOutput)
  }
  body := map[string]any{
    "model":    l.llmCfg.Model,
    "messages": messages,
    "stream":   false,
  }
  outputField := "max_tokens"
  if l.caps != nil && l.caps.UsesMaxCompletionTokens {
    outputField = "max_completion_tokens"
  }
  body[outputField] = maxOutput
  if l.caps != nil && l.caps.SupportsTemperature {
    body["temperature"] = temp
    body["top_p"] = 1
  }
  if l.caps != nil && l.caps.SupportsStructuredOutput {
    body["response_format"] = &openaiResponseFormat{Type: "json_object"}
  }
  l.addThinkingConfig(body)
  return body
}
```

- Auth: `Authorization: Bearer <token>`; endpoint `baseURL() + "/chat/completions"`,
  or `baseURL() + "/responses"` for the Responses dialect.
- **`response_format: {type: "json_object"}`** only when the catalog says the
  model supports structured output — the API-level enforcement of the
  "return ONLY json" prompt instruction. Responses-dialect entries express the
  same intent as `text.format: {format: {type: "json_object"}}`.
- **The output-bound field is catalog-gated** — every chat call sends its
  output budget as `max_tokens`, or as `max_completion_tokens` when the
  catalog entry sets `uses_max_completion_tokens` (the OpenAI GPT-5 family
  rejects `max_tokens`). Responses-dialect entries always send
  `max_output_tokens`.
- **The Responses dialect** (`buildResponsesRequest`,
  `openai_compatible.go:161-186`) moves the system message to the top-level
  `instructions`, turns the remaining messages into `input`, and pins
  `store: false` (the pipeline is stateless and replays its own context).
  `decodeResponse` (`openai_compatible.go:293-338`) folds the Responses body
  back into the chat-completions shape — joining every `output_text` block of
  the `message` output items, skipping reasoning items, mapping usage, and
  reading an `incomplete` reply whose `incomplete_details.reason` is
  `max_output_tokens` as `finish_reason: "length"` so the output-cap check
  fires — leaving `Analyze`, `AnalyzeDocType` and the judged helper
  dialect-agnostic.
- **Reasoning knobs follow a two-state catalog policy** (`addThinkingConfig`,
  `openai_compatible.go:188-207`; the provider switch lives in
  `enableThinking`/`disableThinking`, `openai_compatible.go:210-241`) — a switch
  on `Provider` because every vendor names it differently:

```go
switch l.llmCfg.Provider {
case "deepseek":
  body["thinking"] = map[string]string{"type": "enabled"}
  body["reasoning_effort"] = effort
case "qwen":
  body["enable_thinking"] = true
case "zhipu":
  body["thinking"] = map[string]string{"type": "enabled"}
default:
  body["reasoning_effort"] = effort
}
```

  A model that can disable reasoning (`disable_reasoning: true`) takes the
  provider's disable form when the toggle is off (`thinking: {type: disabled}`
  for deepseek/zhipu, `enable_thinking: false` for qwen, and
  `reasoning_effort: "none"` otherwise). A model that cannot disable reasoning
  always runs at its lowest cataloged effort — `reasoning_effort` for the chat
  dialect, `reasoning: {effort}` for the Responses dialect — or sends no knob
  when it catalogs no effort levels. The programmatic-only `Reasoning` toggle
  forces the enable form. Adding a provider with a different knob means
  extending this switch.

`Analyze` and `AnalyzeDocType` mirror the Anthropic flow: same template,
same 3-turn replay, `temperature = 0` for refinement, same lenient `Type`
fallback.

---

## 6. JSON parsing and validation

Both adapters parse with the same three steps — no regex extraction, no JSON
Schema:

1. `strings.TrimSpace` on the response text.
2. `cleanCodeBlock` (`internal/llm/analyzer/text.go`) — strips
   `` ```json `` / `` ``` `` fences and trims.
3. `ParseAnalysisResponse` (`facets.go:51-91`) — decodes the wire shape
   (`title`, `type`, `tags`, `people`, `language`) and normalizes `tags`
   element by element: a JSON string becomes a tag with `category: other`; an
   object must carry a non-empty `term`, and its `category` is folded to the
   known set (missing or unknown → `other`). Mixed arrays parse element-wise
   and unparseable elements are skipped; a `tags` value that is not an array
   fails the whole parse, and a missing/`null` `tags` key yields empty slices.

If the top-level decode fails, the error embeds the raw text:
`"LLM returned invalid JSON: %w\nraw: %s"` (`llm_anthropic.go:265-268`) — the
raw response goes into the error so the failure is debuggable from the task
log. The `AnalyzeDocType` fallback for a response with no `type` key re-parses
through the same helper, so an object-shaped response still yields its
`DocType`.

The five judged parsers (`ParseAdjudicationResponse`, `ParseTagPartResponse`,
`ParseTagCompoundResponse`, `ParseTagQualityResponse`, `ParseTagIdentityResponse`)
run the same clean-then-decode steps with two tolerances for replies that drift
from the prompt's envelope: a bare top-level JSON array is wrapped under the
step's envelope key (`pairs`, `values`, `compounds`, `tags`, `terms`) before
decoding, and an entry that omits `index` is placed at its array position. A
present index is still range-checked and entries beyond the candidate count are
dropped; an entry that lacks a complete verdict counts as missing, and all five
parsers return one result per entry alongside a shared `*IncompleteReplyError`
(`Missing` lists the missing indices) whenever any is missing, so callers
salvage the entries they did read and defer only the rest. Both bridges log a
DEBUG drift line naming the model when the reply
is a bare array, and every judged call logs one DEBUG line with its step
label, model, candidate count and duration. Before parsing, the OpenAI side
turns a reply stopped at the output bound (`finish_reason: "length"`) into an
`OutputCapError` carrying the model and the effective cap. A parse failure is
a `ReplyParseError` carrying the step, the model, the raw reply's total length
and its first 2048 bytes — never the full body.

Validation happens *upstream* of parsing, in the enricher (§10): tags are
filtered/normalized, people canonicalized, doc type checked against the
known list, language checked. The LLM's word is never persisted raw.

---

## 7. The error taxonomy

Five typed errors in `gates.go`, each with a distinct recovery path:

```go
type ContentTooLargeError struct {       // pre-flight: estimated tokens > model budget
  EstimatedTokens int
  MaxInputTokens  int
}
type TokenLimitError struct {            // provider rejected: context length exceeded
  MaxTokens       int
  RequestedTokens int
  RawBody         string
}
type OutputCapError struct {             // provider stopped at the request's output bound
  Model string
  Cap   int
}
type ReplyParseError struct {            // judged reply failed to parse
  Step      string
  Model     string
  RawLen    int
  RawPrefix string
}
type InsufficientCreditsError struct {   // 402/429: billing problem
  Provider   string
  HTTPStatus int
  RawBody    string
}
```

- **`ContentTooLargeError`** — thrown *before* the HTTP call by
  `checkContentTooLarge` (`gates.go:144`): `estimateTokens(prompt) >
  caps.MaxInputTokens`. Skipped when the catalog lacks a max-input value.
- **`TokenLimitError`** — parsed from the provider's error body with a regex
  (`gates.go:140`):

```go
var tokenLimitRE = regexp.MustCompile(
  `maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens`,
)
```

- **`InsufficientCreditsError`** (`gates.go:117`) — **HTTP 402 or 429** is
  a credit error for any provider; plus a qwen special case: HTTP 400 with
  `"arrearage"` in the body.
- **`OutputCapError`** — the provider stopped at the requested output bound
  (`finish_reason: "length"`); the judged call fails instead of handing the
  truncated body to the parser. Never retried.
- **`ReplyParseError`** — a judged reply failed to parse; it carries the
  step, the model, the raw reply's total length and a bounded 2048-byte
  prefix, so a degenerate reply cannot flood the log. Never retried.

Both adapters return these typed errors; **there is no retry/backoff inside
the adapters** — each call is a single HTTP request. Retry decisions live in
the callers: the enricher decides for analysis calls (§10), and the admission
layer for the four judged steps (below). Timeout enforcement comes from
`runWithTimeout` at the runner level (`internal/tools/runner.go:135-152`); the
adapters' `http.Client` deliberately has no timeout of its own.

**Provider fallback** — when `enricher.contentanalyzer.fallbacks` is configured
(a list of `{enabled, llm}` blocks), the Runner builds one `ContentAnalyzer`
per **enabled** entry (same factory, same prompt template) and keeps them in
list order. `AnalyzeContent` and `AnalyzeDocType` classify each primary error
with `isProviderError` (`runner.go:668-675`): anything that is *not* a
request-side or lifecycle failure — i.e. not `ContentTooLargeError`,
`TokenLimitError`, `context.Canceled`, or `context.DeadlineExceeded` — walks
the fallback chain in order, retrying the same request through each enabled
fallback until one succeeds. Each fallback runs under the same
context/deadline as the primary; its own `request_delay` applies after its
request. If every fallback fails too, the **last** error (e.g. an
`InsufficientCreditsError` naming the last failing provider) is what the
enricher sees — so the batch pauses on credit errors only when the primary
**and all** enabled fallbacks end in a credit error.

**Judged-call retry and credit policy** — the judged steps bypass the
provider fallback chain. The admission layer retries a failed judged call
**once**, and only when `retryableJudgeFailure` classifies the failure as
transient (transport, generic provider and empty-response failures).
`ReplyParseError`, `OutputCapError`, `IncompleteReplyError`, credit exhaustion,
`TokenLimitError`, `ContentTooLargeError` and context errors are never retried
(an incomplete reply's omission is systematic, so a retry would re-omit). A
credit exhaustion propagates out of the step so the enricher's `creditPause`
turns it into the batch pause (governed by `pause_on_credit_error`), and the
identity and pair loops abort immediately instead of spending more calls on the
dead account. Every other non-retryable failure degrades per step — the
affected tags are kept, born provisional or deferred — with a WARN naming the
candidates; an `IncompleteReplyError` instead salvages the entries the reply
did return and defers only the missing ones, with a WARN naming them.

---

## 8. Token estimation

`estimateTokens` (`internal/llm/analyzer/text.go`) approximates token
counts without a tokenizer; it feeds the pre-flight `checkContentTooLarge` and
the enricher's shrink-and-retry loop (§10). Full mechanics and accuracy
caveats: `algorithms.md` §12.

---

## 9. The model catalog and registry

The provider and model catalogs live in the config directory as
`provider_catalog.json` and `model_catalog.json`; `internal/llm/registry.go`
reads them from there and nowhere else. The binary embeds one default copy of
each (`//go:embed`) and uses it only to seed a missing file: when a catalog
file does not exist yet, its default is validated together with the other
catalog and written to the config directory once the combined result loads,
so a failing load never creates files. An existing file is never overwritten
or replaced — user edits are authoritative. `provider_catalog.json` defines
providers — `provider`, `adapter`, `official_url`, optional `session_header`
— and `model_catalog.json` defines their models: `provider`, `model_id`,
`display_name`, the token limits, and the capability flags
(`max_input_tokens`, `max_output_tokens`, `disable_reasoning`,
`reasoning_efforts`, `supports_structured_output`, `supports_temperature`,
`supports_prompt_caching`, `uses_max_completion_tokens` (the OpenAI GPT-5
family rejects `max_tokens`; the flag makes the adapters send the output
budget as `max_completion_tokens`), and `uses_responses_endpoint` (the entry
is served only over the Responses API)). Both files are versioned wrapper
objects (`{"version": 1, "providers": [...]}` / `{"version": 1, "models":
[...]}`) and may carry an ignored `_comment` hint key; the seeded defaults
point at [models.dev/providers](https://models.dev/providers/) for provider
and model documentation. Eight providers ship in the default catalog: openai,
deepseek, mistral, anthropic, qwen, zhipu, opencode-go, opencode — the
provider catalog's `official_url` is how the adapters know the endpoint, and
providers that declare `session_header` receive the process-stable
`llm.SessionID()` in that header on every request (both adapters set it; the
opencode gateways ask for `x-opencode-session`, which they use for request
routing and prompt caching):

```go
key := modelKey(provider, modelID)
i, ok := r.index[key]
...
return entryToCapability(entry)
```

(`registry.go:374-385`). Loading validates each file's version (`1`, any
other or missing value fails before entry validation), the required fields
(`provider`, `adapter`, `official_url` for providers; `provider`,
`model_id` for models), and rejects a model whose provider is absent from
the provider catalog. Duplicate keys within one file follow array order (the
last entry wins). Lookups feed the API surface (`ModelsForProvider`,
`ProvidersForAdapter`) and the capability gating in both adapters. **Unknown
model → nil capabilities** → adapters degrade gracefully: no temperature, no
reasoning knob, no structured output, no token pre-check. `Reload`
re-resolves both catalogs — seeding any file that has since been deleted —
and swaps them in only on success; a failed reload keeps the previous
catalogs.

### 9.1 Mutating the catalog

`catalog_edit.go` adds the management layer on the same registry:
`AddProvider`/`SetProvider`/`RemoveProvider`/`AddModel`/`SetModel`/
`RemoveModel` mutate the catalog files and the in-memory state in one
operation, `ResetCatalogs` restores the embedded defaults for the selected
scope, and `Catalog()` returns a full snapshot (every provider including
model-less ones, every model entry) for the editing surfaces. Every edit
takes the cross-process catalog lock first — an exclusive `flock` on
`<config dir>/catalog.lock`, taken before the in-memory mutex so a wait on
another writer never blocks in-process readers — then re-reads both files
from disk, applies and validates the change against that fresh state, and
persists it. A single-file change is atomic (temp file + rename); a change
touching both files (`writeCatalogPair`, used by the cascade removal and the
full reset) stages both temp files before either rename so write errors
surface before a file is replaced, installs the model file first — which
keeps a pruning mutation's intermediate pair referentially valid — and rolls
the model file back from its previous bytes when the provider rename fails.
Each file's `_comment` hint survives rewrites, and the mode stays 0644.

Validation mirrors the loader's rules and tightens two: required fields
(`provider`/`adapter`/an absolute `http(s)` `official_url`;
`provider`/`model_id`), non-negative token limits, and — new — provider
names may not contain `.` or `/`, both catalog-key delimiters (the CLI keys
are `providers.<name>[.<field>]` and `models.<provider>/<model>[.<field>]`).
Removal carries two guards: a provider that still owns models is rejected
unless `cascade` deletes them together, and `llmInUse` rejects a provider or
model the enricher configuration names (the content analyzer, its fallbacks,
the tag adjudicator and the tag quality judge — enabled or not; a nil config
lifts the guard). `ResetCatalogFiles` is the package-level recovery path: it
works without a loadable registry (a mangled catalog cannot be loaded), each
partial scope validates its result against the catalog it leaves in place —
reading only that file, so it can repair its own mangled one — and the full
reset restores both files.

The surfaces on top: the API handler (`internal/api/handlers/llm.go`) maps
kinds to statuses (409 duplicate/guard, 404 unknown, 400 invalid) and answers
every mutation with the fresh `Catalog()`; its registry sits in an
`atomic.Pointer`, so reads answer 503 when the startup load failed and a
successful reset installs a fresh registry live. The `kushim llm` CLI verb
mirrors `kushim config`'s grammar (dump/get/set/`--unset`), and the settings
page's Models section drives the same endpoints. Consumer processes and the
queue's consume forks load the catalogs fresh per batch, so file edits reach
the pipeline without any restart.

Note: neither catalog nor `ModelCapability` models per-token costs —
`entryToCapability` (`registry.go:358-370`) maps only the capability flags
and limits.

---

## 10. The enricher pipeline

`Enricher.Enrich` (`internal/enrichment/enricher.go:113-641`) is the
orchestrator. Step by step:

1. **Reduce for the LLM** (`enricher.go:128-137`): `ReduceContent(text, 150,
   targetWordCount)` — chunk size 150, target from config (2000) or a
   fraction of the document for negative config values
   (`matcher.TargetWordCount`, `internal/matcher/client.go:64`, floor 2000). On
   reduction error, **falls back to raw text** — a broken reducer never blocks
   analysis.
2. **Reduce for the tag matcher** (`enricher.go:139-147`): same, target 4000.
3. **Load context** (`enricher.go:150-199`): document types, people types, all
   tags (unfiltered; the enricher splits the rows into the promoted trust set
   and the all-rows novel set).
4. **Tag matching** (`enricher.go:201-229`): `runner.MatchTags`; only matches
   at or above `tagmatcher.min_suggestion_similarity` are kept as suggestions
   (an empty list when none clear it), and on error or zero matches the
   fallback is the promoted tag-name list.
5. **Analyze with a 2-attempt loop** (`enricher.go:231-272`):

```go
for i := range 2 {
  // analyze...
  switch {
  case errors.As(err, &tooLarge), errors.As(err, &tokenLimit):
    ratio := float64(maxTokens)/float64(actualTokens) * tokenBudgetRatio   // 0.9
    newTarget := int(float64(llmContent.TargetWordCount) * ratio)
    // re-reduce and retry once
  case errors.As(err, &credErr):
    return nil, DocumentCounts{}, e.creditPause(logId, err)   // batch pause flag
  }
}
```

   - Too-large → **shrink the reduced text by the token ratio × 0.9 and
      retry once**; a second failure (or below `minTargetWords` = 100) errors
      with `"document too large for model %s/%s: %d tokens exceeds budget
      (max_input_tokens=%d)"` (`enricher.go:263`).
   - Credit error → `e.creditPause(logId, err)` builds the typed `task.Error`
      with `PauseBatch` from `contentanalyzer.pause_on_credit_error` — the
      task runner pauses the whole batch (§12 of the task-system guide). With
      a fallback configured, the batch pauses only when **both** primary and
      fallback failed with a credit error (the runner retries through the
      fallback first, §7); the reported provider in the error is the actual
      failing one (`credErr.Provider`, which may be the fallback).
6. **Empty-result retry** (`enricher.go:274-283`): if the analysis came back
   with every field empty (`isEmptyAnalysis`, `enricher.go:814`), one
   more `AnalyzeContent`; still empty → error.
7. **Doc-type refinement** (`enricher.go:285-306`): only when enabled and the
   text was actually reduced; head/tail sampled via
   `ExtractHeadTailWords(text, HeadWords, TailWords)` (600/400) with
   `DocMetadata`; `AnalyzeDocType` failure keeps the first-pass type (logged,
   not fatal).
8. **Normalize tags** (`enricher.go:309`): `NormalizeTags` — the
   normalization pipeline from `tagfilter.go:156-167`.
9. **Canonicalize people** (`enricher.go:311-318`): Latin names as-is;
   non-Latin names use `NameRomanized` or an **anyascii transliteration**
   (`canonicalPersonName`, `enricher.go:797`); `NormalizedName =
   textnorm.NormalizeForDB(canonical)`.
10. **Facet separation, time gate, and tag filtering** (`enricher.go:332-338`):
    `SeparateFacets` gates typed emissions by the model's own `category`:
    `form_genre` tags are dropped (they belong to `document_type`) and
    `person` tags are dropped; `place` and `work` tags are ordinary subject
    candidates and pass. A deterministic time-designation gate then drops a
    year, decade, month, month+year, or unseeded ordinal period unit the store
    does not hold (`reason=time_designation`). Every drop is logged as
    `tags[facet]: drop <term> (category=… reason=…)` with `reason` one of
    `matches_document_type`, `model_self_classification`, `people_facet`;
    `topic`/`other` and
    untyped emissions pass through. `FilterTags` (`tagfilter.go:121-132`) then
    drops whole-emission tags that are >3 words, overlap with LLM people names,
    are multi-token subsets of known normalized names, or overlap doc-type names,
    with no count cap; whole emissions and split components share the same
    rules. Before the split gate the language guard
    (`Adjudicator.ResolveLanguages`, `Runner.JudgeTagTranslations`, criteria
    pinned by `analyzer.TagTranslationVersion`) rewrites detected non-English
    emissions to their judged English canonical form (a value returned unchanged
    is already English; an uncertain translation is held for review by the
    admission gate with `reason=language_review`). `Adjudicator.DecomposeTags`
    (`adjudicator.go`) then replaces compound tags with their segments —
    LLM-adjudicated when the tag adjudicator is enabled, conjunction joints
    deterministically otherwise — except a seeded period
    (`tags.IsPeriodClass` over the house form) the store already holds, which is a
    store application that stays whole, is never judged, and never decomposes, while an unseeded
    time designation is dropped on the emission and parts paths. The fan is
    `tags.DeriveSegments` (content words plus contiguous bigrams); the segments are first
    fold-resolved through the
    attested seam fold (the generated WordNet pertainym table's adjective→noun
    pairs in sense order, or a pinned variant class the table does not cover, anchored only on
    **promoted** store values; a multi-base part folds only onto its primary-sense base and
    only while that base is live — a live sibling sense neither attracts nor blocks the fold,
    and the residue is the phrase-context fold judge's question; provisional births are never
    seam anchors) and
    then pass the hard per-document rules — length/word bounds, people,
    known-name, doc-type checks, and the unseeded time-designation gate. Compounds whose key already exists in the
    store (names and aliases) are applications and are skipped before
    judgment only once a current-version verdict decides them (a missing or
    degraded verdict re-enters judgment, and a decided keep applies its
    judged-dimensional parts alongside from the cache), and every judged
    compound records one `tag_composition_event`
    verdict row (split with its resolved parts, keep with the parts applied
    alongside pruned of rejected ones, or the reason it could
    not be judged — a split that lost every admitted part is a `part_rejected`
    drop that keeps the parts it resolved, and one that lost some but not all is a
    `partial_parts` keep that stands whole with its survivors applied alongside)
    with a matching `tag split[...]` log line. Under the shared
    judged-call policy (§7) a transient split-judge failure is retried once, a
    credit exhaustion aborts the document so the batch pauses, and every other
    failure keeps the compound whole. `Adjudicator.ApplyDeletedTagPolicy`
    applies the deleted-tag blocklist after it.
11. **Identity** (`enricher.go`, `identity.go`): `Runner.RankTags` ranks LLM
    tags against canonical existing tags, then the identity pipeline applies
    house form, aliases, and key equality, and `policy.Evaluate` generates
    identity + gate subjects. Proposed folds are confirmed by the **identity
    judge** and every remaining candidate neighbour is resolved by **tag
    adjudication** when `enricher.tag_adjudicator.enabled` is true
    (`Runner.JudgeTagIdentity` / `Runner.AdjudicateTags`: caches → evidence →
    batched LLM calls; `same` aliases the losing name, `variant`/`related`
    register both). A partial reply decides the claims it returned and leaves
    only the missing ones pending; a disabled, failing, or ambiguous
    adjudication leaves the claim pending — the candidate registers
    provisionally; a credit-exhaustion failure pauses the batch instead.
12. **Tag admission decision** (`internal/tags/admission/adjudicator.go`, `Adjudicator.ApplyTagQuality`): only when
    `enricher.tag_quality.enabled` is true. Novel tags — identity-planned outputs
    whose normalized names are absent from the vocabulary — are resolved by the
    pinned admission decision (`tags.DecideAdmission`) over their features: the
    rubric judge's answers (from the `tag_quality_event` cache or
    temperature-0 `JudgeTagQuality` calls over batches of up to 40 candidates
    per document) plus the emission's
    deterministic signals (a detail annotated `other`, and a best store
    similarity below the internal `no_anchor_similarity` 0.60 cut). The rubric
    asks `subject_matter`, `not_person`, `atomic` and
    `not_form_genre`, with `defer` as the judge's abstention, while the
    `dimension` verdict is the parts pass's (read from the shared parts cache,
    judged on a miss, and stamped onto the row; a missing or abstained verdict
    defers); the decision
    defers on abstention,
    rejects on two or more failing signals, defers on exactly one (the gray
    band), and admits when nothing fails. Rejections drop the tag for that
    document — the registration and its pending identity evidence never reach
    the store — and log `tagquality[v6]: reject … (reason=corroborated_failures
    source=judge|cache …)`; defers are kept and logged too, and admitted
    candidates journal at DEBUG. Works and places are judged by the ordinary
    dimension criteria, so neither is rejected for being that kind of tag. Fresh verdicts
    record the judging model and the recorded outcome, cache-applied ones a
    NULL model; cached rows re-run the decision over the stored rubric with the
    current emission's deterministic signals. A transient judge failure retries
    once; parse failures, incomplete replies, output-cap hits, credit
    exhaustion, token-limit, content-too-large and context errors are never
    retried (§7). A reply that omits verdicts for some candidates salvages the
    entries it returned — each is decided and cached — and defers only the
    missing candidates with `judge_unavailable` (no verdict row; re-judged on
    re-emission). Every other non-credit failure keeps every novel tag with a
    defer decision, logged as a WARN naming the kept candidates — under the gate
    those births are **provisional** (no verdict row); a credit-exhaustion
    failure aborts the document so the batch pauses.
13. **Persist metadata** (`enricher.go:456-463`): title truncated to 127
    (`truncate`), doc type validated against the DB list (fallback
    `"undetermined"`), `UpdateDocumentMetadata`.
14. **OCR language auto-detect** (`enricher.go:465`,
    `ensureOCRLanguage` `enricher.go:818`): the detected 3-letter ISO
    code is added to `consumer.ocr.languages` and persisted via
    `config.SaveMap` — for the gosseract engine the tessdata download runs
    in a goroutine and config persists only on success.
15. **Tags to DB** (`enricher.go:470-553`): plan application
    `Tag.CreatePlannedTx` on the persist
    transaction (`quarantine = enricher.tag_quality.enabled`), tolerating
    conflicts; the plan carries the registrations, alias actions, and identity
    evidence from the identity step, and a rejected novel name is dropped from
    it before persist, so it is never stored. `ClearEnrichDocumentTags` +
    `AddDocumentTag` (`source = 'enrich'`)
    per tag, plus orphan-mark clearing, all commit or roll back together. With
    the layer off every created tag is promoted atomically with its insert and
    the batch's new names are added to the embedding store after the commit;
    with the layer on births land provisional, `promoteEligible` runs inside the
    transaction (the emission's admission decision, or the cached verdict row
    for provisionals already in the store, plus assignments that clear the
    promotion floor — `tags.RecurrenceFloor`, 10 live documents →
    `PromoteTags`; trashed carriers do not count, and the same rule backs the
    reconciliation sweep), and only the promoted names are added to the store.
    Verdict-less provisionals are resolved later by the scheduled sweep or the
    review queue (see the
    [semantic matching promotion section](semantic-matching.md#promotion-out-of-quarantine)).
16. **People to DB** (`enricher.go:555-640`): dedupe by normalized name,
    `CreatePeople` with a race fallback (`sql.ErrNoRows` →
    `GetPeopleByNormalizedName` + `UpdatePeopleNative`), unknown LLM types →
    `"unknown"`, then `ClearDocumentPeople` + `AddDocumentPeople`.
17. **Return** the stats JSON (the task result).

---

## 11. Configuration knobs

Under `enricher` (`internal/config/config.go:164-195`):

| Key | Default | Meaning |
|---|---|---|
| `workers` | `1` | enrich pool size |
| `textreducer.engine` / `timeout` / `target_words` | `textrank` / `120` / `2000` | the reduction step |
| `contentanalyzer.enabled` | `false` | master switch (setup enables it) |
| `contentanalyzer.timeout` | `120` (s) | per-call budget |
| `contentanalyzer.prompt_template` | `""` | custom template (fallback on error) |
| `contentanalyzer.pause_on_credit_error` | `true` | pause the batch on credit exhaustion |
| `contentanalyzer.doc_type_refinement.enabled` | `true` | the second head/tail call |
| `contentanalyzer.doc_type_refinement.head_words` / `tail_words` | `600` / `400` | sampling window |
| `contentanalyzer.llm.adapter` | — | `anthropic` or anything → openai-compatible |
| `contentanalyzer.llm.provider` / `model` / `token` | — | endpoint + model + auth (token omitted from YAML on read) |
| `contentanalyzer.llm.temperature` | — | catalog-gated |
| `contentanalyzer.llm.request_delay` | `1` (s) | seconds to sleep after each LLM request; `0` = off, max `60` |
| `contentanalyzer.fallbacks` | `[]` | ordered list of `{enabled, llm.*}` fallbacks tried on provider errors; each enabled entry requires adapter/provider/model (with `request_delay` 0–60) |
| `tagmatcher.reduce_target_words` | `4000` | matcher reduction target |
| `tagmatcher.min_suggestion_similarity` | model-derived (`0.52` bge-m3) | injection bar: only matched tags at or above it reach the emission prompt; an explicit `0` reverts to the model default |
| `tagmatcher.hugot.*` | — | model/backend (§9 of the semantic-matching guide) |
| `tag_adjudicator.enabled` / `timeout` / `llm.*` | `false` / analyzer timeout / analyzer llm | identity gate: fold confirmation and same-intent pair verdicts (semantic-matching guide) |
| `tag_quality.enabled` / `timeout` / `llm.*` | `false` / analyzer timeout / analyzer llm | rubric verdicts for novel tags; the decision thresholds are internal pinned constants (`no_anchor_similarity = 0.60`, promotion floor of 10 live documents), not config keys |

Validation (`finalizeConfig`, `config.go:447-460`): when content analysis is
enabled, `adapter`, `provider`, and `model` are all **required** — three
separate errors, so setup can't silently half-configure. `Reasoning` is
programmatic-only (`yaml:"-"`); the reasoning behavior otherwise comes from the
catalog's `disable_reasoning` flag and `reasoning_efforts` list.

---

## 12. Gotchas

- **Output budgets are per call type, catalog-capped, and doubled for
  reasoning-by-default models** — the analysis family sends 4096, each judged
  step `512 + 128 × candidates`; a model that cannot disable reasoning
  (`RunsAtEffortLevel`) doubles the budget first because its reasoning tokens
  share the output bound, then the result is clamped to the catalog's
  `MaxOutputTokens`; an entry without one keeps the budget. Anthropic requires
  `max_tokens` on every call; OpenAI's GPT-5 family requires
  `max_completion_tokens` (the `uses_max_completion_tokens` catalog flag);
  Responses-dialect entries always send `max_output_tokens`.
- **Temperature is catalog-gated** — sending `temperature` to a model whose
  entry says `supports_temperature: false` breaks the request. When adding a
  model, set the flag correctly.
- **The adapters never retry** — the 2-attempt loop and the shrink-and-retry
  live in the enricher, the judged steps' single classified retry lives in the
  admission layer, and the credit pause is the enricher's `creditPause`.
  Adding retry inside an adapter would fight that design (and the
  `runWithTimeout` guard).
- **`http.Client` has no timeout** — enforcement is
  `runWithTimeout` + the caller's context. The adapter calls are
  context-cancellable, so a stuck provider eventually surfaces as a timeout
  error; don't add a client timeout without considering long models.
- **Structured output is catalog-gated** — models without
  `supports_structured_output` rely on prompt discipline alone; chat calls send
  `response_format: json_object` and Responses-dialect entries send
  `text.format`.
- **The prompt is the schema** — the "Return ONLY a json string with keys:
  ..." contract is enforced by parsing, not by a schema. Prompt changes are
  breaking changes for stored results; the `Prompt` field persists what was
  sent for debugging.
- **Custom templates fail silently to the default** — a typo in
  `prompt_template` means your carefully crafted prompt is *not* what runs;
  check the task logs for the persisted `Prompt`.
- **Empty results are retried once** (`isEmptyAnalysis`) — models that return
  empty JSON get one more chance before the task fails.
- **People names: the LLM's "name" field may be the document's romanization,
  not the person's native script** — the prompt says so explicitly
  ("only when you can independently determine it"), and `canonicalPersonName`
  falls back through `name_romanized` → anyascii.
- **Batch pausing is contagious by design** — one credit error pauses the
  whole batch (`PauseOnCreditError`), and paused batches refuse to run until
  explicitly resumed. Toggling it off makes credit errors fail per-task
  instead. With a fallback configured, a primary credit error alone does not
  pause the batch — the fallback is tried first, and only a fallback credit
  error (or a primary credit error with no fallback) pauses it.
- **`tokenBudgetRatio = 0.9` is a safety margin** — the shrink target is
  90% of the budget ratio, absorbing token-estimate error. Tightening it
  invites a second too-large failure.
- **String-shaped tag arrays bypass the facet gate** — older/weaker models and
  custom templates that return `"tags": ["…"]` parse fine, but every entry
  carries `category: other`, so no tag is dropped by `SeparateFacets`; the
  form/genre, person, work, and place gates only see object-shaped emissions.

---

*Last verified against the tree: 2026-10-05. If code and doc disagree, code wins.*
