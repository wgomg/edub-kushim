# Developer Guide — LLM Integration and Enrichment

This guide explains how edub-kushim talks to LLM providers: the content
analyzer adapters (Anthropic + OpenAI-compatible), the prompt contract, JSON
result parsing, the error taxonomy (with the token/credit handling that
drives batch pausing), the model catalog registry, and the enricher pipeline
that ties it all to documents.

Audience: developers touching `internal/tools/adapters/contentanalyzer/`,
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
| `ContentAnalyzer` interface | `contentanalyzer/adapter.go` | the capability contract |
| Anthropic adapter | `contentanalyzer/llm_anthropic.go` | Claude via `POST /messages` |
| OpenAI-compatible adapter | `contentanalyzer/openai_compatible.go` | anything with a `/chat/completions` API |
| Shared prompt/parse logic | `contentanalyzer/shared.go` | system prompt, template, JSON cleaning, errors |
| Model catalog | `llm/registry.go` + `model_catalog.json` | per-model capabilities |
| The enricher | `internal/enrichment/enricher.go` | orchestrates: reduce → match → analyze → persist |
| Task handler | `internal/task/handlers/enrich.go` | the `enrich` task type |

Flow: a document's text is reduced (TextRank — see
`algorithms.md`), tag-matched (see
`semantic-matching.md`), analyzed by the LLM, and the result
is validated and persisted. If the LLM provider reports exhausted credits,
the whole batch pauses (§10).

---

## 2. The ContentAnalyzer interface

`internal/tools/adapters/contentanalyzer/adapter.go:21-36`:

```go
type ContentAnalyzer interface {
	Analyze(ctx context.Context, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error)
	AnalyzeDocType(ctx context.Context, prevResult *AnalysisResult, headTailText string, docTypes []database.DocumentType, metadata DocMetadata) (string, error)
	Name() string
}

type AnalysisResult struct {
	Title       string           `json:"title"`
	DocType     string           `json:"type"`
	Tags        []string         `json:"tags"`
	People      []PeopleResult   `json:"people"`
	Language    string           `json:"language"`
	Stats       *json.RawMessage `json:"stats"`
	Prompt      string           `json:"prompt"`
	PassContext *json.RawMessage `json:"-"`
}
```

- `Analyze` — the main call: full text (already reduced), the known document
  types and people types (to constrain the answer), and tag suggestions (the
  semantic matcher's output).
- `AnalyzeDocType` — a **second, cheaper call** that re-evaluates only the
  document type using the head/tail of the full text (§10). It receives the
  previous result so it can replay the conversation (§4).
- `PassContext` (`json:"-"`) carries the exact system+user prompts from the
  first call so the second call can replay them — never serialized to the
  API response.
- `Stats` carries the token usage, persisted as `task.result`.

The factory (`adapter.go:38-49`) requires the model registry, looks up the
configured provider/model capabilities, and switches on `Llm.Adapter`:
`"anthropic"` → the Anthropic adapter, anything else → the
OpenAI-compatible adapter (the default branch — unknown adapter names
silently mean "openai-compatible").

---

## 3. The prompt contract

### The system message

`shared.go:31`:

```go
const SystemMessage = "You are a helpful assistant specialized in document analysis and metadata extraction"
```

### The default prompt template

`shared.go:123-131` — a Go `text/template` (not a string concat), the heart
of the extraction contract:

```
Analyze the excerpts of a document provided below and extract the following data:
- Document title: In excerpts language, truncate to 127 characters if longer
{{.DocTypePrompt}}
- Tags: At most {{.RequestedTags}} thematic tags describing the document's topics and domains. English only. ...
{{.TagsPrompt}}
- People: People associated with the document. For each person provide: name (the person's name in their own native script/language ...), name_romanized (...), and a type from the list below. ...
{{.PeoplePrompt}}- Language: 3-letter ISO 639-2 code (e.g. 'eng','spa','jpn',...). Detect the primary language even from noisy or mixed text. Only use 'und' as a last resort ...

Return ONLY a json string without any explanations, numbers, additional text, text formatting or text/code blocks, with keys: title, type, tags, people (array of objects with keys: name, name_romanized, type), language.

Document Excerpts: {{.Text}}
```

Design points:

- **The output contract is spelled out in the prompt**: "Return ONLY a json
  string ... with keys: title, type, tags, people, language" — the adapters
  parse exactly that shape (§6).
- **The constraint lists are injected** as template sub-blocks:
  `documentTypePrompt` (`"- Document type: choose one of the following:\n  -
  %s (%s)\n"` per type, `shared.go:183-190`), `peopleTypePrompt`
  (`"  Available types:\n    - %s (%s)\n"`, `shared.go:174-181`), and
  `tagsPrompt` (`" Prefer tags from the following list if thematically
  related: '%s'"`, `shared.go:192-197` — the semantic-matcher suggestions,
  comma-joined).
- **Tag count is a buffer**: `requestedTagCount = 8` is requested but only
  `maxTags = 5` survive post-processing (`shared.go:24-29`) — ask for more
  than you keep, so filtering (§10) doesn't starve the result.
- **`customTemplate` overrides** when set in config
  (`ContentAnalyzer.PromptTemplate`); **any parse/execute error falls back
  silently to the default** (`shared.go:151-154, 166-169`) — a broken custom
  template degrades, it doesn't crash.
- **Head/tail and metadata** feed the second call (§10):
  `BuildDocTypePrompt` (`shared.go:265-281`) asks to re-evaluate the type
  from "the opening and closing sections of the full document", with context
  like `"600 total words, 12 pages, application/pdf"` (`DocMetadata.Format`,
  `shared.go:242-263`).

---

## 4. The Anthropic adapter

`llm_anthropic.go`. Request shape highlights (`llm_anthropic.go:46-58`): the
struct has fields for `stop_sequences`, `stream`, `top_k` — **declared but
never set** (streaming is hardcoded off, no stop sequences, no top-k). What
is actually sent (`llm_anthropic.go:169-198`):

```go
reqBody := anthropicRequest{
	Model:     l.llmCfg.Model,
	MaxTokens: l.defaultMaxTokens(),
	Messages: []anthropicMessage{
		{Role: "user", Content: prompt},
	},
	System:       SystemMessage,                     // top-level field, not a message
	Stream:       false,
	CacheControl: &cacheControlEphemeral{Type: "ephemeral"},   // prompt caching
}
l.applyReasoning(&reqBody, reqBody.MaxTokens)
l.applyTemperature(&reqBody, l.llmCfg.Temperature)
...
httpReq.Header.Set("x-api-key", l.llmCfg.Token)
httpReq.Header.Set("anthropic-version", "2023-06-01")
httpReq.Header.Set("Content-Type", "application/json")
```

Anthropic-specific rules the adapter encodes:

- **`max_tokens` is mandatory** — `defaultMaxTokens()` uses the catalog's
  `MaxOutputTokens` or 4096 (`llm_anthropic.go:119-124`).
- **`system` is a top-level field**, not a message role.
- **`anthropic-version: 2023-06-01`** header, `x-api-key` auth (only when a
  token is set).
- **Prompt caching**: `cache_control: {type: "ephemeral"}` on both calls —
  the system prompt + first user turn are cached across requests.
- **Thinking modes** (`applyReasoning`, `llm_anthropic.go:126-154`),
  catalog-gated by `caps.SupportsReasoning` and `ReasoningEffortLevels`:
  - reasoning off → `thinking: disabled`;
  - model without effort levels → `thinking: enabled` with a
    `budget_tokens` of `min(maxTokens/2, 32000)` (floor 1024,
    `llm_anthropic.go:64-75`);
  - model with effort levels → `output_config: {effort}` (default
    `"high"`), plus `thinking: enabled` **only for
    `claude-opus-4-5`**, which requires a manual budget alongside
    `output_config` (`anthropicManualThinkingModels`,
    `llm_anthropic.go:60-62`).
- **Temperature is catalog-gated**: only sent when
  `caps.SupportsTemperature` (`applyTemperature`, `llm_anthropic.go:156-160`)
  — newer models don't accept it. The doc-type call passes `temp = 0`.

Response parsing (`llm_anthropic.go:216-254`): content blocks with
`block.Type == "text"` are joined, then `CleanCodeBlock` + `json.Unmarshal`
into `AnalysisResult`; usage → `Stats`; the full prompt is persisted in
`Prompt`; `PassContext` serializes `{system, user_prompt}` for the replay.

**`AnalyzeDocType` replays the conversation** (`llm_anthropic.go:259-374`):

```go
Messages: []anthropicMessage{
	{Role: "user", Content: passCtx.UserPrompt},
	{Role: "assistant", Content: string(assistantJSON)},
	{Role: "user", Content: docTypePrompt},
},
```

— original user turn, then the previous assistant answer (built from the
first result), then the new head/tail question. `MaxTokens: 256` since the
answer is a single `type` field. Parsing tolerates both a bare `{"type": "..."}`
and a full `AnalysisResult` (`llm_anthropic.go:361-373`).

---

## 5. The OpenAI-compatible adapter

`openai_compatible.go` targets any `/chat/completions` API. Request build
(`openai_compatible.go:79-105`):

```go
body := map[string]any{
	"model":    l.llmCfg.Model,
	"messages": messages,          // system, user, optional assistant
	"stream":   false,
}
if l.caps != nil && l.caps.SupportsTemperature {
	body["temperature"] = temp
	body["top_p"] = 1
}
if l.caps != nil && l.caps.SupportsStructuredOutput {
	body["response_format"] = &openaiResponseFormat{Type: "json_object"}
}
```

- Auth: `Authorization: Bearer <token>`; endpoint `baseURL() + "/chat/completions"`.
- **`response_format: {type: "json_object"}`** only when the catalog says the
  model supports structured output — the API-level enforcement of the
  "return ONLY json" prompt instruction.
- **Provider-specific reasoning knobs** (`openai_compatible.go:107-147`) — a
  switch on `Provider` because every vendor names it differently:

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

  (with the corresponding disable forms, default effort `"high"`). Adding a
  provider with a different knob means extending this switch.

`Analyze` and `AnalyzeDocType` mirror the Anthropic flow: same template,
same 3-turn replay, `temperature = 0` for refinement, same lenient `Type`
fallback.

---

## 6. JSON parsing and validation

Both adapters parse with the same three steps — no regex extraction, no JSON
Schema:

1. `strings.TrimSpace` on the response text.
2. `utils.CleanCodeBlock` (`internal/utils/text.go:67-75`) — strips
   `` ```json `` / `` ``` `` fences and trims.
3. `json.Unmarshal` into `AnalysisResult`.

If unmarshal fails, the error embeds the raw text:
`"LLM returned invalid JSON: %w\nraw: %s"` (`llm_anthropic.go:236-242`) — the
raw response goes into the error so the failure is debuggable from the task
log.

Validation happens *upstream* of parsing, in the enricher (§10): tags are
filtered/normalized, people canonicalized, doc type checked against the
known list, language checked. The LLM's word is never persisted raw.

---

## 7. The error taxonomy

Three typed errors in `shared.go:33-101`, each with a distinct recovery path
in the enricher:

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
type InsufficientCreditsError struct {   // 402/429: billing problem
	Provider   string
	HTTPStatus int
	RawBody    string
}
```

- **`ContentTooLargeError`** — thrown *before* the HTTP call by
  `checkContentTooLarge` (`shared.go:79-88`): `EstimateTokens(prompt) >
  caps.MaxInputTokens`. Skipped when the catalog lacks a max-input value.
- **`TokenLimitError`** — parsed from the provider's error body with a regex
  (`shared.go:90-101`):

```go
var tokenLimitRE = regexp.MustCompile(
	`maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens`,
)
```

- **`InsufficientCreditsError`** (`shared.go:62-73`) — **HTTP 402 or 429** is
  a credit error for any provider; plus a qwen special case: HTTP 400 with
  `"arrearage"` in the body.

Both adapters return these typed errors; **there is no retry/backoff inside
the adapters** — each call is a single HTTP request, and the
enricher decides what to do (§10). Timeout enforcement comes from
`runWithTimeout` at the runner level (`internal/tools/runner.go:96-113`); the
adapters' `http.Client` deliberately has no timeout of its own.

**Provider fallback** — when `enricher.contentanalyzer.fallbacks` is configured
(a list of `{enabled, llm}` blocks), the Runner builds one `ContentAnalyzer`
per **enabled** entry (same factory, same prompt template) and keeps them in
list order. `AnalyzeContent` and `AnalyzeDocType` classify each primary error
with `isProviderError` (`runner.go:438-445`): anything that is *not* a
request-side or lifecycle failure — i.e. not `ContentTooLargeError`,
`TokenLimitError`, `context.Canceled`, or `context.DeadlineExceeded` — walks
the fallback chain in order, retrying the same request through each enabled
fallback until one succeeds. Each fallback runs under the same
context/deadline as the primary; its own `request_delay` applies after its
request. If every fallback fails too, the **last** error (e.g. an
`InsufficientCreditsError` naming the last failing provider) is what the
enricher sees — so the batch pauses on credit errors only when the primary
**and all** enabled fallbacks end in a credit error.

---

## 8. Token estimation

`utils.EstimateTokens` (`internal/utils/text.go:25-43`) approximates token
counts without a tokenizer; it feeds the pre-flight `checkContentTooLarge` and
the enricher's shrink-and-retry loop (§10). Full mechanics and accuracy
caveats: `algorithms.md` §12.

---

## 9. The model catalog and registry

`internal/llm/registry.go` loads `model_catalog.json` — from disk
(`<config-dir>/model_catalog.json` if present) or the embedded copy
(`//go:embed model_catalog.json`, `registry.go:13-14`) — and indexes it.

Catalog entry fields (`registry.go:34-47`): `provider`, `adapter`,
`model_id`, `display_name`, `official_url`, `max_input_tokens`,
`max_output_tokens`, `reasoning`, `reasoning_efforts`,
`supports_structured_output`, `supports_temperature`,
`supports_prompt_caching`. Six providers: openai, deepseek, mistral,
anthropic, qwen, zhipu — each entry carries its own `official_url`, which is
how the adapters know the endpoint:

```go
key := provider + "/" + modelID
i, ok := r.index[key]
...
return entryToCapability(entry)
```

(`registry.go:160-172`). Lookups feed the API surface
(`ModelsForProvider`, `ProvidersForAdapter`) and the capability gating in
both adapters. **Unknown model → nil capabilities** → adapters degrade
gracefully: no temperature, no reasoning, no structured output, no token
pre-check.

Note: the catalog has **no cost fields** — `entryToCapability`
(`registry.go:146-158`) hardcodes zero input/output cost per token.

---

## 10. The enricher pipeline

`Enricher.Enrich` (`internal/enrichment/enricher.go:51-396`) is the
orchestrator. Step by step:

1. **Reduce for the LLM** (`enricher.go:62-73`): `ReduceContent(text, 150,
   targetWordCount)` — chunk size 150, target from config (2000) or a
   fraction of the document for negative config values (`targetWordCount`,
   `enricher.go:398-404`, floor 2000). On reduction error, **falls back to
   raw text** — a broken reducer never blocks analysis.
2. **Reduce for the tag matcher** (`enricher.go:75-84`): same, target 4000.
3. **Load context** (`enricher.go:86-97`): document types, people types, all
   tags.
4. **Tag matching** (`enricher.go:106-118`): `runner.MatchTags`; on error or
   zero matches, falls back to the full tag-name list as suggestions.
5. **Analyze with a 2-attempt loop** (`enricher.go:120-168`):

```go
for i := range 2 {
	// analyze...
	switch {
	case errors.As(err, &tooLarge), errors.As(err, &tokenLimit):
		ratio := float64(maxTokens)/float64(actualTokens) * tokenBudgetRatio   // 0.9
		newTarget := int(float64(llmContent.TargetWordCount) * ratio)
		// re-reduce and retry once
	case errors.As(err, &credErr):
		return nil, &task.Error{
			ReqID:      logId,
			Err:        fmt.Errorf("LLM credit exhausted (%s): %w", provider, credErr),
			PauseBatch: e.config.Enricher.ContentAnalyzer.PauseOnCreditError,   // default true
		}
	}
}
```

   - Too-large → **shrink the reduced text by the token ratio × 0.9 and
      retry once**; a second failure (or below `minTargetWords` = 100) errors
      with `"document too large for model %s/%s: %d tokens exceeds budget
      (max_input_tokens=%d)"` (`enricher.go:156-162`).
   - Credit error → the typed `task.Error` with `PauseBatch` — the task
      runner pauses the whole batch (§12 of the task-system guide). With a
      fallback configured, the batch pauses only when **both** primary and
      fallback failed with a credit error (the runner retries through the
      fallback first, §7); the reported provider in the error is the actual
      failing one (`credErr.Provider`, which may be the fallback).
6. **Empty-result retry** (`enricher.go:170-179`): if the analysis came back
   with every field empty (`isEmptyAnalysis`, `enricher.go:435-437`), one
   more `AnalyzeContent`; still empty → error.
7. **Doc-type refinement** (`enricher.go:181-203`): only when enabled and the
   text was actually reduced; head/tail sampled via
   `ExtractHeadTailWords(text, HeadWords, TailWords)` (600/400) with
   `DocMetadata`; `AnalyzeDocType` failure keeps the first-pass type (logged,
   not fatal).
8. **Normalize tags** (`enricher.go:205`): `NormalizeTags` — the
   normalization pipeline from `shared.go:385-397`.
9. **Canonicalize people** (`enricher.go:207-213`): Latin names as-is;
   non-Latin names use `NameRomanized` or an **anyascii transliteration**
   (`canonicalPersonName`, `enricher.go:418-433`); `NormalizedName =
   utils.NormalizeForDB(canonical)`.
10. **Filter tags** (`enricher.go:215-228`): `FilterTags` (`shared.go:283-383`)
    drops tags that are >3 words, overlap with LLM people names, are
    multi-token subsets of known normalized names, overlap doc-type names, or
    are contained in the title; caps at 5.
11. **Consolidate** (`enricher.go:230-237`): `service.Tag.Consolidate` maps
    LLM tags onto canonical existing tags via the semantic matcher
    (`semantic-matching.md` §7).
12. **Persist metadata** (`enricher.go:247-265`): title truncated to 127
    (`utils.Truncate`), doc type validated against the DB list (fallback
    `"undetermined"`), `UpdateDocumentMetadata`.
13. **OCR language auto-detect** (`enricher.go:267`,
    `ensureOCRLanguage` `enricher.go:439-491`): the detected 3-letter ISO
    code is added to `consumer.ocr.languages` and persisted via
    `config.SaveMap` — for the gosseract engine the tessdata download runs
    in a goroutine and config persists only on success.
14. **Tags to DB** (`enricher.go:269-297`): batch `Tag.Create` (tolerating
    conflicts), then `ClearDocumentTags` + `AddDocumentTag` per tag.
15. **People to DB** (`enricher.go:299-389`): dedupe by normalized name,
    `CreatePeople` with a race fallback (`sql.ErrNoRows` →
    `GetPeopleByNormalizedName` + `UpdatePeopleNative`), unknown LLM types →
    `"unknown"`, then `ClearDocumentPeople` + `AddDocumentPeople`.
16. **Return** the stats JSON (the task result).

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
| `tagmatcher.hugot.*` | — | model/backend (§9 of the semantic-matching guide) |

Validation (`finalizeConfig`, `config.go:447-460`): when content analysis is
enabled, `adapter`, `provider`, and `model` are all **required** — three
separate errors, so setup can't silently half-configure. `Reasoning` /
`ReasoningEffort` are programmatic-only (`yaml:"-"`), set through the API.

---

## 12. Gotchas

- **`max_tokens` is mandatory for Anthropic, and the catalog's
  `MaxOutputTokens` decides it** — a catalog entry without it falls back to
  4096, which can truncate long analyses.
- **Temperature is catalog-gated** — sending `temperature` to a model whose
  entry says `supports_temperature: false` breaks the request. When adding a
  model, set the flag correctly.
- **The adapters never retry** — the 2-attempt loop, the shrink-and-retry,
  and the credit pause all live in the enricher. Adding retry inside an
  adapter would fight that design (and the `runWithTimeout` guard).
- **`http.Client` has no timeout** — enforcement is
  `runWithTimeout` + the caller's context. The adapter calls are
  context-cancellable, so a stuck provider eventually surfaces as a timeout
  error; don't add a client timeout without considering long models.
- **`response_format: json_object` is catalog-gated** — models without
  `supports_structured_output` rely on prompt discipline alone.
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

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*
