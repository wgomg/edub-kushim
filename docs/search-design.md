# Search Interface — Design & Implementation Plan

## Current Situation

The system supports rich document metadata — tags (many-to-many), people with types (many-to-many), document types, language, MIME type, file size, and dates — alongside full-text content via SQLite FTS5. The frontend and backend, however, only expose a limited search surface:

### Frontend

- The header has a search `<input>` that is **not wired to any endpoint**.
- The documents page (`/documents`) provides a sortable, paginated table with **no filtering** at all.
- The API client (`api.js`) has a `search(q, limit, offset)` method that sends a raw `?q=` parameter.

### Backend

- `GET /api/v1/documents/search?q=...` calls `search.Engine.Search()`.
- `sanitizeQuery()` wraps the input in quotes before passing it to the FTS5 `MATCH` operator, meaning only exact full-text matching works — no boolean operators, no field-specific filters.
- `SearchDocumentsFTSWithFilters()` exists but only supports a MIME type filter — no tags, people, dates, or other dimensions.
- The document list endpoint (`GET /api/v1/documents`) supports sorting and pagination but **no filtering parameters** at all.

In short: the data model supports rich querying, but the search interface and backend both treat search as a single opaque text string going into FTS5, ignoring the structured metadata entirely.

---

## Proposed Architecture

### Core Principle: Single Source of Truth

There must be exactly one representation of the active search state at any time. Both the search bar and the filter panel modify this shared state. There are **no parallel, potentially conflicting states**.

```
                   ┌──────────────────────┐
                   │   Filter State Object │
                   │  (frontend, in-memory)│
                   └──────┬───────┬───────┘
                          │       │
              ┌───────────┘       └───────────┐
              ▼                                 ▼
   ┌──────────────────────┐       ┌──────────────────────┐
   │    Search Bar View   │       │   Filter Panel View  │
   │  typed text + chips  │       │  dropdowns + pickers │
   └──────────────────────┘       └──────────────────────┘
```

The user interacts with either view. Both write to the same filter state. The single output is a **JSON request body** sent to the API (the same shape that `POST /api/v1/documents/search` accepts).

Filter state is purely in-memory. There is no URL serialization — saved searches (Step 8) replace the need for bookmarkable URLs.

### Frontend Architecture

#### Filter State Object

```typescript
interface SearchFilter {
  // Free-text going to FTS5
  query: string;

  // Structured filters
  tags: string[]; // tag names
  people: { name: string; type: string }[];
  documentType: string; // document type name
  language: string;
  mimeType: string;
  dateCreated: { from?: string; to?: string };
  dateModified: { from?: string; to?: string };
  fileSize: { min?: number; max?: number }; // bytes

  // Pagination & sort (separate from filters, but sent together)
  sortBy: string;
  sortOrder: 'asc' | 'desc';
  limit: number;
  offset: number;
}
```

#### Search Bar

The search bar is a **rich text input** that visually distinguishes two kinds of content:

| Content type | Visual style                  | Interaction                        |
| ------------ | ----------------------------- | ---------------------------------- |
| Free text    | Plain text, editable inline   | User types/edits freely            |
| Field tokens | Styled chip (`tag:finance ✕`) | Fixed text, removable via ✕ button |

Field tokens are **never editable inline** — they are produced by the filter panel (or by typing `field:value` syntax in the search bar) and can only be removed. This prevents ambiguity between the raw string and structured state.

The search bar understands the following syntax for power users:

| Syntax                     | Result                                   |
| -------------------------- | ---------------------------------------- |
| `word`                     | Free-text FTS5 search on title + content |
| `tag:finance`              | Filter by tag name "finance"             |
| `author:"John Doe"`        | Filter by person name with type "author" |
| `type:invoice`             | Filter by document type name             |
| `lang:en`                  | Filter by language code                  |
| `mime:pdf`                 | Filter by MIME type                      |
| `created:>2024-01-01`      | Date range (created after)               |
| `created:2024-01..2024-06` | Date range (created between)             |
| `size:>1MB`                | File size filter (supports KB, MB, GB)   |
| `-tag:budget`              | Exclusion (not tagged "budget")          |
| `tag:(finance budget)`     | Multiple values for same field (OR)      |

As the user types, autocomplete suggestions appear:

- After 2+ characters of a word: suggest available tags, people, document types whose names match.
- After typing a field name followed by `:`: filter the suggestion list to values of that field type.
- Selecting a suggestion inserts a styled token into the search bar.

#### Filter Panel

A collapsible panel below the search bar, organized by section:

| Section       | Widget type                                                  | Notes                                |
| ------------- | ------------------------------------------------------------ | ------------------------------------ |
| Tags          | Autocomplete input (type to search) + multi-select chips     | Large cardinality — no flat dropdown |
| People        | Two-level: select person type, then autocomplete person name | e.g. "Author > John Doe"             |
| Document Type | Single-select dropdown                                       | Small cardinality                    |
| Language      | Single-select dropdown                                       | Small cardinality                    |
| MIME Type     | Single-select dropdown                                       | Small cardinality                    |
| Date Created  | Dual date picker (from / to)                                 |                                      |
| Date Modified | Dual date picker (from / to)                                 |                                      |
| File Size     | Range slider or min/max number inputs                        |                                      |

Every interaction with the panel updates the filter state, which in turn updates the chip display in the search bar. The reverse is also true: typing `tag:finance` in the search bar highlights "finance" as selected in the tag section of the panel.

#### Data Flow

```
User types in search bar
        │
        ▼
Frontend query parser (runs on keystroke)
  - tokenizes into fields + free text
  - checks for completions against local caches (tags, people, etc.)
  - updates filter state object
        │
        ▼
Filter state object changes
  - triggers debounced API call (300ms)
  - re-renders chips in search bar
  - re-renders filter panel selections
        │
        ▼
Debounce fires → API call
  - serializes filter state to JSON body
  - POST /api/v1/documents/search
        │
        ▼
Backend responds with results
  - updates document table in the main content area
```

### Backend Architecture

#### New endpoint: `POST /api/v1/documents/search`

Takes a JSON body (the filter state) and returns paginated, filtered, sorted results.

**Request:**

```json
{
  "query": "quarterly report",
  "tags": ["finance", "budget"],
  "people": [{ "name": "John Doe", "type": "author" }],
  "document_type": "invoice",
  "language": "en",
  "mime_type": "application/pdf",
  "date_created": { "from": "2024-01-01", "to": "2024-12-31" },
  "date_modified": { "from": null, "to": "2024-06-01" },
  "file_size": { "min": 0, "max": 10485760 },
  "sort_by": "created_at",
  "sort_order": "desc",
  "limit": 50,
  "offset": 0
}
```

**Response:** Same `FTSDocumentResponse` array as `GET /api/v1/documents/search`, but enriched with tags and people in each result (matching what `GET /api/v1/documents` already does).

**How the backend builds the query (pseudocode):**

```
1. Start with: SELECT d.* FROM document d

2. If query is non-empty:
   JOIN document_fts ON d.id = document_fts.document_id
   WHERE document_fts MATCH ?
   (sanitize for FTS5 — wrap in quotes, escape internal quotes)

3. For each additional filter, add a WHERE clause:
   - Tags:     AND d.id IN (SELECT document_id FROM document_tag
                JOIN tag ON tag.id = document_tag.tag_id
                WHERE tag.name IN (?, ?, ...))

   - People:   AND d.id IN (SELECT document_id FROM document_people
                JOIN people ON people.id = document_people.people_id
                JOIN people_type ON people_type.id = document_people.people_type_id
                WHERE people.name = ? AND people_type.name = ?)

   - Doc type: AND d.document_type_id = (SELECT id FROM document_type WHERE name = ?)

   - Language: AND d.language = ?

   - MIME:     AND d.mime_type = ?

   - Dates:    AND d.created_at >= ? AND d.created_at <= ?

   - Size:     AND d.file_size >= ? AND d.file_size <= ?

4. If no FTS query, paginate and sort directly on document table.
   If FTS query, use FTS rank for ordering, fall back to sort_by.

5. After getting document IDs, fetch tags and people in batch queries
   (one query for all tags, one for all people) to avoid N+1.
```

#### Supporting endpoints (for autocomplete)

| Endpoint                     | Purpose                           | Currently exists           |
| ---------------------------- | --------------------------------- | -------------------------- |
| `GET /api/v1/tags?q=`        | Autocomplete tag names            | No (queries exist in sqlc) |
| `GET /api/v1/people?q=`      | Autocomplete person names         | No (queries exist in sqlc) |
| `GET /api/v1/people-types`   | List person types for filter UI   | No (queries exist in sqlc) |
| `GET /api/v1/document-types` | List document types for filter UI | No (queries exist in sqlc) |

These return simple `{ id, name }` arrays and can be cached in the frontend.

#### Saved searches as bookmarkability replacement

Search state is purely in-memory in the frontend — there is no `?q=` serialization, no URL parsing on page load. Instead, saved searches (Step 8) provide the equivalent functionality: name a filter set once and reload it from the "Saved" dropdown.

The existing `GET /api/v1/documents/search?q=...` route still works as a legacy endpoint for simple FTS-only queries, but the structured search UI always uses `POST /api/v1/documents/search` with a JSON body.

---

## Implementation Plan

### Step 1 — Backend: Autocomplete endpoints

Add the missing read-only API endpoints that the frontend needs for the filter panel and autocomplete:

- `GET /api/v1/tags?q=<prefix>&limit=20` — search tag names by prefix (sqlc `ListAllTags` exists, add prefix search)
- `GET /api/v1/people?q=<prefix>&limit=20` — search people names by prefix (sqlc `ListAllPeople` exists, add prefix search)
- `GET /api/v1/people-types` — list all person types (sqlc `ListAllPeopleTypes` exists)
- `GET /api/v1/document-types` — list all document types (sqlc `ListAllDocumentTypes` exists)

**Dependencies:** None. These are simple read queries on existing tables.

**What to build:**

- New sqlc queries for prefix search on `tag.name` and `people.name`
- Handler functions for each endpoint
- Route registration in `server.go`
- Frontend API client methods in `api.js`

---

### Step 2 — Backend: Structured search endpoint

Add `POST /api/v1/documents/search` that accepts a JSON body with the full filter state.

**Dependencies:** Step 1 (the autocomplete endpoints are independent but the search result format should be consistent with the rest of the API).

**What to build:**

- A new `SearchStructured` method on `search.Engine` (or a separate `FilterEngine`)
- Request/response types in `internal/api/types/`
- SQL query builder that dynamically composes `WHERE` clauses with proper parameterization
- Batch fetch for tags and people on the result set (avoid N+1)
- Handler function and route registration

**Key design decisions to make here:**

- How to handle combined FTS + metadata filtering (the pseudocode in the architecture section above resolves this: join FTS when there's a query, skip it when there isn't)
- Pagination strategy when FTS rank ordering is mixed with `sort_by` from metadata (simple approach: when a FTS query is present, always order by rank; when it's absent, use the requested `sort_by`/`sort_order`)

---

### Step 3 — Frontend: Filter state and query parser

Build the core frontend infrastructure:

**Dependencies:** Step 1 (frontend needs the cache data to be available). Step 2 is not required — the frontend can send the structured object to the current FTS endpoint as a stepping stone, or wait until step 2 is done.

**What to build:**

- A `SearchFilter` type and reactive store (Svelte writable store or $state) to hold the filter state
- A query string parser that tokenizes `tag:finance author:"John Doe" report` into `SearchFilter`
- API client methods for the new endpoints (step 1) and the new search endpoint (step 2, or a fallback)

**Test the parser in isolation** — it's the most logic-dense piece of the frontend and the most likely source of bugs.

---

### Step 4 — Frontend: Search bar with autocomplete

Build the rich search bar component:

**Dependencies:** Step 3 (needs the filter state and parser).

**What to build:**

- A `SearchBar.svelte` component that:
  - Renders typed free text as plain inline text
  - Renders field tokens as styled chips with ✕ buttons
  - Handles keyboard navigation (arrow keys through suggestions, Enter to select, Backspace to remove last chip, comma to tokenize)
  - Shows an autocomplete dropdown as the user types
- Autocomplete logic:
  - After `tag:` → query `GET /api/v1/tags?q=...` and show matching tag names
  - After `author:` → query `GET /api/v1/people?q=...` with type filter
  - After 2+ plain characters → show a mixed suggestion list (tag names, person names, document types)
- Debounced fetch on input changes (300ms)
- The component must sit nicely inside the existing header layout in `+layout.svelte`

---

### Step 5 — Frontend: Filter panel

Build the collapsible advanced filter panel:

**Dependencies:** Step 3 (needs filter state), Step 1 (needs the data from autocomplete endpoints).

**What to build:**

- A `FilterPanel.svelte` component with sections for each filter dimension
- Tag selector: autocomplete input that fetches tag names, displays selected tags as chips
- People selector: two-stage (select person type, then select person name)
- Document type, language, MIME type: dropdown selects
- Date pickers: native `<input type="date">` or a lightweight date picker
- File size: two number inputs (min/max) with unit suffix (KB/MB/GB)
- "Clear all filters" button
- The panel should be collapsed by default, toggled by an "Advanced Filters" button near the search bar
- All interactions write to the shared filter state (step 3)

---

### Step 6 — Frontend: Wire everything into the documents page

Replace the current simple `fetch()` in the documents page with the full search interface:

**Dependencies:** Steps 4 and 5 (both components need the filter state from step 3).

**What to build:**

- Replace the page-level DataTable with a layout that has the search bar + filter panel at the top and the results below
- The existing `DataTable.svelte` can be reused for the results table, but its `fetch` function now calls `POST /api/v1/documents/search` with the serialized filter state
- Wire pagination (the existing Previous/Next buttons in DataTable) to the filter state's offset
- Show total result count when available from the API
- Show a loading state during search

**User-facing behavior after this step:**

- The search bar in the header is functional
- Typing in the search bar shows suggestions
- Clicking a suggestion inserts a chip
- Opening the filter panel shows all dimensions
- Selecting a filter in the panel adds a chip in the search bar
- The document table updates reactively with every change

---

### Step 7 — Frontend: Search results view (snippets, highlighting)

Enhance the search results display beyond the current table:

**Dependencies:** Step 6.

**What to build:**

- When search results come from the FTS endpoint, display the snippet with `<b>` highlighting (already returned by the backend)
- Show which terms matched in each result
- Consider a card-based layout as an alternative to the table layout for search results (more space for snippets, tags, people badges)

---

### Step 8 — Bonus: Saved searches

Add the ability to save and name the current search configuration.

**Dependencies:** Step 6 (search must be fully functional first).

**Design decision:** The frontend no longer persists filters in the URL (`?q=`), so the original plan to save a query string doesn't apply. Instead, save the exact JSON body that `POST /api/v1/documents/search` accepts — the same object the `fetch()` function builds from the filter store. This avoids any serialize/parse round-trip and keeps the saved state identical to the live API contract.

#### Step 8a — Backend: Schema

Add to `internal/database/sql/schema/schema.sql`:

```sql
CREATE TABLE saved_search (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  filter_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Run `sqlc generate` after adding queries (next step).

#### Step 8b — Backend: sqlc queries

Add to `internal/database/sql/queries/saved_search.sql`:

```sql
-- name: CreateSavedSearch :execresult
INSERT INTO saved_search (name, filter_json) VALUES (?, ?);

-- name: ListSavedSearches :many
SELECT id, name, filter_json, created_at FROM saved_search ORDER BY created_at DESC;

-- name: DeleteSavedSearch :exec
DELETE FROM saved_search WHERE id = ?;
```

Run `sqlc generate`.

#### Step 8c — Backend: API types + handler + routes

**Types** in `internal/api/types/saved_search.go`:

- `CreateSavedSearchRequest` — `Name string`, `Filter json.RawMessage`
- `SavedSearchResponse` — `ID int64`, `Name string`, `Filter json.RawMessage`, `CreatedAt string`

**Handler** in `internal/api/handlers/saved_search.go`:

- `POST /api/v1/saved-searches` — decode request, call `CreateSavedSearch`, return `{ "id": <new_id> }`
- `GET /api/v1/saved-searches` — call `ListSavedSearches`, return array of `SavedSearchResponse`
- `DELETE /api/v1/saved-searches/{id}` — parse ID from path, call `DeleteSavedSearch`, return 204

**Routes** in `internal/api/server.go`: register the three routes with standard middleware chain.

#### Step 8d — Frontend: API client

Add to `web/src/lib/api.js`:

```js
savedSearches: {
  list: () => get('/api/v1/saved-searches'),
  create: (name, filter) => post('/api/v1/saved-searches', { name, filter }),
  delete: (id) => del(`/api/v1/saved-searches/${id}`)
}
```

#### Step 8e — Frontend: UI

**"Save search" button** — placed next to the "Filters" toggle button in `+page.svelte`:

- On click: prompt for a name (simple text input or `window.prompt`)
- Grab the current filter object (same shape `fetch()` builds — query, tags, people, documentType, language, mimeType, dateCreated, dateModified, fileSize)
- Call `api.savedSearches.create(name, filter)`
- Show brief success feedback

**"Saved Searches" dropdown** — toggled by an icon/button next to the save button:

- On open: fetch via `api.savedSearches.list()`
- Each entry: name (clickable) + delete ✕ button
- Click name → `JSON.parse(saved.filter_json)` → `filterStore.setPartial(...)` — sets all filter dimensions at once; the store subscription triggers `refreshKey++`, DataTable reloads automatically
- Click delete → confirm → `api.savedSearches.delete(id)` → refresh list

---

## Dependency Graph (Visual)

```
Step 1 (autocomplete API)
    │
    ├─────────┐
    │         │
    ▼         ▼
Step 2     Step 3 (filter state + parser)
(structured      │
search API)      ├─────────────┐
    │            │             │
    │            ▼             ▼
    │        Step 4        Step 5
    │        (search       (filter
    │         bar)         panel)
    │            │             │
    │            └──────┬──────┘
    │                   ▼
    │              Step 6 (wire into documents page)
    │                   │
    │                   ▼
    │              Step 7 (snippets, highlighting)
    │                   │
    │                   ▼
    │              Step 8 (saved searches)
    └──────────────────┘
```

Steps 1–2 can proceed in parallel with Step 3. Steps 4–5 depend on Step 3 and can proceed in parallel with each other. Everything converges at Step 6.

---

## Key Risks and Mitigations

| Risk                                                                                   | Mitigation                                                                                                                                                                                                                                       |
| -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Parser complexity** — the query string syntax is subtle and edge-case-prone          | Write the parser as a pure function with unit tests; fuzz test against all known valid inputs                                                                                                                                                    |
| **N+1 queries** — fetching tags and people per result row                              | Batch fetch: after getting document IDs, load all tags and people in two queries, then join in Go                                                                                                                                                |
| **FTS5 + metadata filter performance** — SQLite may not optimize combined queries well | Test with realistic data volumes (10K+ documents). Add SQLite indexes on filter columns (`language`, `mime_type`, `file_size`, `created_at`) if needed. Consider materializing tag names into the FTS5 content field for single-query filtering. |
| **Large tag cardinality** — hundreds of tags can't fit in a dropdown                   | The tag selector in the filter panel is an autocomplete input, not a dropdown. The data stays on the backend; only matching prefixes are fetched.                                                                                                |
