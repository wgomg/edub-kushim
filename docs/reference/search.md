# Search System (`internal/search/`)

## Overview

The search system provides two tiers of document retrieval:

1. **Full-Text Search** (`GET /api/v1/documents/search`) — Keyword search using PostgreSQL tsvector with `ts_rank` ranking and `ts_headline` snippet highlighting.
2. **Structured Search** (`POST /api/v1/documents/search`) — Combines full-text search with metadata filters (tags, people, document type, language, MIME type, date ranges, file size) and returns total count for pagination.

Both are backed by the `search.Engine` struct which wraps database queries with sanitization and result mapping.

---

## `internal/search/search.go`

### Structs

#### `Result`
Maps database rows to API-friendly fields:

| Field             | Type      | Source                        |
| ----------------- | --------- | ----------------------------- |
| `ID`              | `int64`   | `FTSDocumentRow.ID`           |
| `DocumentID`      | `string`  | UUID from `fts5` query        |
| `Title`           | `string`  | Document title                |
| `MD5Checksum`     | `string`  | MD5 hash                      |
| `SHA512Checksum`  | `string`  | SHA512 hash                   |
| `MimeType`        | `string`  | File MIME type                |
| `FileSize`        | `int64`   | File size in bytes            |
| `PageCount`       | `int32`   | Number of pages               |
| `WordCount`       | `int32`   | Word count                    |
| `CharCount`       | `int32`   | Character count               |
| `Language`        | `string`  | Detected language code        |
| `DocumentTypeID`  | `int64`   | FK to document_type           |
| `CreatedAt`       | `time.Time` | Document creation timestamp   |
| `ModifiedAt`      | `time.Time` | Last modification timestamp   |
| `OriginalPath`    | `string`  | Path to original file         |
| `StoragePath`     | `string`  | Path to processed file        |
| `Snippet`         | `string`  | Highlighted FTS snippet (HTML-escaped, `<b>` highlighting preserved) |
| `Rank`            | `float64` | ts_rank relevance score        |

#### `Filter`
JSON-serializable request body for structured search:

```go
type Filter struct {
    Query           string         `json:"query"`           // tsquery plain text
    Tags            []string       `json:"tags"`            // Tag names to filter by (AND)
    People          []PersonFilter `json:"people"`          // Person name + type pairs
    DocumentType    string         `json:"document_type"`   // Document type name
    Language        string         `json:"language"`        // Language code (e.g. "eng")
    MimeType        string         `json:"mime_type"`       // File MIME type
    DateCreated     *DateRange     `json:"date_created"`    // Created date range
    DateModified    *DateRange     `json:"date_modified"`   // Modified date range
    FileSize        *FileSizeRange `json:"file_size"`       // File size range in bytes
    SortBy          string         `json:"sort_by"`         // Sort column
    SortOrder       string         `json:"sort_order"`      // "asc" or "desc"
    Limit           int32          `json:"limit"`           // Max results (default 50, max 100)
    Offset          int32          `json:"offset"`          // Pagination offset
    MissingLanguage bool           `json:"missing_language"` // Documents without detected language
    MissingType     bool           `json:"missing_type"`     // Documents with undetermined type
    Untagged        bool           `json:"untagged"`         // Documents without any tags
}
```

#### `PersonFilter`
```go
type PersonFilter struct {
    Name string `json:"name"`  // Person name
    Type string `json:"type"`  // Person type (e.g. "author", "sender")
}
```

#### `DateRange`
```go
type DateRange struct {
    From *string `json:"from"`  // ISO date string (inclusive)
    To   *string `json:"to"`    // ISO date string (inclusive)
}
```

#### `FileSizeRange`
```go
type FileSizeRange struct {
    Min *int64 `json:"min"`  // Minimum size in bytes
    Max *int64 `json:"max"`  // Maximum size in bytes
}
```

### Methods

#### `Engine.Search(ctx, query string, limit, offset int32) ([]Result, error)`
Simple tsvector search. Calls `database.SearchDocumentsStructured` with a minimal `SearchFilter`. Returns results with `ts_rank` and `ts_headline` snippet.

#### `Engine.SearchStructured(ctx, filter Filter) ([]Result, total int64, error)`
Structured search with metadata filters. First calls `database.CountDocumentsStructured()` for total count, then `database.SearchDocumentsStructured()` for paginated results. Translates the `Filter` struct into the database-layer `SearchFilter` struct.

### Functions

#### `sanitizeQuery(q string) string`
Trims whitespace from the user query before passing it to `plainto_tsquery`.

### Response Sanitization

Before returning search results to the client, the `Snippet` field is passed through `sanitizeSnippetHTML` which HTML-escapes all characters except `<b>` and `</b>` tags. This preserves the bold highlighting produced by PostgreSQL `ts_headline` while preventing XSS injection via document content that contains raw HTML or script tags.

---

## `internal/database/structured_search.go` — `FTSDocumentRow`

### Struct

`FTSDocumentRow` — Maps tsvector query results with these additional computed fields:

| Field     | Type      | Description                              |
| --------- | --------- | ---------------------------------------- |
| `Rank`    | `float64` | `ts_rank` relevance from tsvector        |
| `Snippet` | `string`  | `ts_headline` highlighted text (HTML-escaped, `<b>` preserving) |

The tsvector column is `GENERATED ALWAYS AS (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))) STORED` on the `document` table, backed by a GIN index (`idx_document_tsv`). Queries use `plainto_tsquery('simple', ...)` for tokenization and `@@` for matching.

---

## `internal/database/structured_search.go`

### Struct

`SearchFilter` — Internal representation of the search filter (mirrors `search.Filter` but uses database-friendly anonymous structs for ranges).

### Internal: `queryBuilder`

A dynamic SQL query builder that composes `WHERE` clauses with proper parameterization to prevent SQL injection.

#### Methods

| Method                              | Purpose                                     |
| ----------------------------------- | ------------------------------------------- |
| `add(clause string, args ...any)`   | Append raw clause + positional args          |
| `eq(col, val string)`               | `AND d.col = $N` (skipped if val empty)      |
| `subqueryIn(col, subquery, values)` | `AND d.col IN (SELECT ... WHERE IN ($1,$2,...))` |
| `rangeClause(col, min, max)`        | `AND d.col >= $N` / `AND d.col <= $N`       |
| `dateRange(col, range)`             | Date range filter with optional from/to     |
| `addMissingFilters(filter)`         | Adds filters for MissingLanguage, MissingType, Untagged |

### Functions

#### `SearchDocumentsStructured(ctx, filter) ([]FTSDocumentRow, error)`
Builds a SELECT query dynamically:

- If `filter.Query` is non-empty: adds `WHERE d.text_search_vector @@ plainto_tsquery('simple', $N)`, `ts_rank()` for rank, `ts_headline()` for highlighting
- Applies tag subquery (`document_tag JOIN tag`), people subqueries (`document_people JOIN people JOIN people_type`), document type subquery, language/MIME equality, date ranges, file size ranges
- Applies missing filters (`MissingLanguage` → `d.language IN ('und','')`, `MissingType` → `d.document_type_id = 1`, `Untagged` → `NOT EXISTS` subquery on `document_tag`)
- When query is present: ordered by `rank`; otherwise ordered by `sort_by`/`sort_order` (whitelisted: `title`, `mime_type`, `file_size`, `created_at`)
- Uses `LIMIT $N OFFSET $N` for pagination

#### `CountDocumentsStructured(ctx, filter) (int64, error)`
Same filter logic but `SELECT COUNT(*)` for total count without ordering. Uses the shared `addMissingFilters` helper to avoid drift between search and count queries.

---

## API Endpoints

| Endpoint                         | Method | Description                                                  |
| -------------------------------- | ------ | ------------------------------------------------------------ |
| `/api/v1/documents/search`       | GET    | tsvector full-text search with ts_rank and ts_headline       |
| `/api/v1/documents/search`       | POST   | Structured search with metadata filters, returns total count |
| `/api/v1/tags?q=`                | GET    | Autocomplete tag names (prefix search, LIKE + LIMIT)         |
| `/api/v1/people?q=`              | GET    | Autocomplete people names (prefix search, LIKE + LIMIT)      |
| `/api/v1/people-types`           | GET    | List all person types (for filter dropdowns)                 |
| `/api/v1/document-types`         | GET    | List all document types (for filter dropdowns)               |
| `/api/v1/saved-searches`         | GET    | List saved search configurations                             |
| `/api/v1/saved-searches`         | POST   | Save a search configuration (`name` + `filter` JSON)         |
| `/api/v1/saved-searches/{id}`    | DELETE | Delete a saved search                                        |

### Response Types

#### `SearchResponse`
```json
{
  "results": [
    {
      "document_id": "uuid",
      "title": "report.pdf",
      "rank": 0.4213,
      "snippet": "The <b>budget</b> forecast...",
      "tags": [{ "id": 1, "name": "finance" }],
      "people": [{ "id": 1, "name": "John Doe", "person_type_name": "author" }],
      "file_size": 102400,
      "created_at": "2024-03-19T10:30:00Z"
    }
  ],
  "total": 42
}
```

#### Autocomplete Responses
```json
// GET /api/v1/tags?q=fin
[{ "id": 1, "name": "finance", "document_count": 5 }, { "id": 2, "name": "financial", "document_count": 2 }]

// GET /api/v1/document-types
[{ "id": 1, "name": "invoice", "description": "Invoice document", "document_count": 12 }]
```

---

## Frontend Implementation

### `SearchBar.svelte`
Rich search input component with:
- **Chip display**: Active filters shown as colored pills (tag:name, type:name, lang:code, created:date, etc.)
- **Autocomplete suggestions**: Fetches from `/api/v1/tags`, `/api/v1/people`, `/api/v1/document-types` with debounce
- **Keyboard navigation**: Arrow keys to navigate suggestions, Enter to select, Backspace to remove last chip, Escape to close dropdown
- **`field:value` syntax**: `tag:finance`, `author:John`, `type:invoice`, `lang:eng`, `created:>2024-01-01`, `size:>1MB`, etc.

### `FilterPanel.svelte`
Collapsible panel with structured filter controls:
- **Tags**: Text input with autocomplete dropdown, chips for active selections
- **People**: Two-stage selector (person type dropdown + name autocomplete)
- **Document Type**: Dropdown populated from API
- **Language**: Dropdown with common language codes
- **MIME Type**: Dropdown with common MIME types
- **Date Created / Date Modified**: Dual date pickers (from/to)
- **File Size**: Text inputs with unit parsing (B, KB, MB, GB)
- **Missing Language / Missing Type / Untagged**: Checkboxes for documents lacking language, type, or tags
- **Clear All**: Resets all filters to defaults

### `filterStore.js`
Svelte writable store for shared filter state:
- `setPartial(partial)` — Merge partial filter updates
- `reset()` — Reset to defaults
- `fromQueryString(str)` — Parse query string into filter state
- `queryString` — Derived store for serialization

### `searchFilter.js`
Query string utility module:
- `tokenizeQuery(str)` — Tokenizes `field:value` syntax into structured tokens (including `missing:lang`, `missing:type`, `missing:tags`)
- `parseQueryString(str)` — Converts raw query string to complete filter object (sets `missingLanguage`, `missingType`, `untagged` from `missing:` tokens)
- `serializeFilter(filter)` — Converts filter object back to query string (appends `missing:lang`/`missing:type`/`missing:tags` when respective flags are set)
- `parseSize(raw)` / `formatSize(bytes)` — Parse/format human-readable file sizes
- `parseDateRange(raw)` — Parse date range from string (`from..to`, `>date`, `<date`)
- `setPersonTypes(types)` / `getPersonTypes()` — Shared person type set for validation

### `DataTable.svelte` (search-related changes)
- Supports `{results, total}` response format (used by structured search)
- Shows "X–Y of Z" pagination when total is available
- `refreshKey` prop triggers external reload (used when filters change)

---

## Query Pattern: `field:value` Syntax

Users can type structured queries directly into the search bar:

| Prefix     | Example                                | Effect                                     |
| ---------- | -------------------------------------- | ------------------------------------------ |
| `tag:`     | `tag:finance`                          | Filter by tag name                         |
| `type:`    | `type:invoice`                         | Filter by document type                    |
| `author:`  | `author:"John Doe"`                    | Filter by person (type: author)            |
| `sender:`  | `sender:acme`                          | Filter by person (type: sender)            |
| `lang:`    | `lang:eng`                             | Filter by language code                    |
| `mime:`    | `mime:application/pdf`                 | Filter by MIME type                        |
| `created:` | `created:>2024-01-01` / `created:2024-01-01..2024-06-30` | Filter by creation date |
| `modified:`| `modified:<2024-06-01`                 | Filter by modification date                |
| `size:`    | `size:>1MB` / `size:1MB..10MB`         | Filter by file size                        |
| `missing:` | `missing:lang` / `missing:type` / `missing:tags` | Filter by missing language, missing type, or untagged |

Quoted values are supported for names with spaces: `author:"Jane Smith"`.

---

## See Also

- [API](api.md) — Document and task response types
- [Database](database.md) — tsvector column, indexes, schema
- [Frontend](frontend.md) — SvelteKit UI implementation
