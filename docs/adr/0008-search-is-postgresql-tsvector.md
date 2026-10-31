# 0008. Search is PostgreSQL tsvector

**Status:** accepted

## Context

Document retrieval needs full-text search over titles and extracted text combined with
arbitrary metadata filters, result totals, and prefix-scoped filter options. The
operational budget is a single database: backups, restores, and migrations must cover all
state as one unit.

Rejected alternatives:

- **An external search engine.** A second service to
  install, monitor, and back up; the index updates out of band, so search state can
  silently diverge from the rows it mirrors; the richer ranking is not worth the split at
  this system's scale.

## Decision

Full-text search is a generated column: `document.text_search_vector`
(`GENERATED ALWAYS AS to_tsvector('simple', …) STORED`) with a GIN index and
`plainto_tsquery` matching. Retrieval is served in two tiers — a simple `q` search, and a
filter endpoint whose dynamic SQL builder returns results and total in one round trip —
plus prefix-scanned B-tree lookups for the progressive filter options, which need no
full-text index. Enrichment updates the vector with the row it belongs to.

## Consequences

Zero additional infrastructure and an index that is transactionally consistent with the
documents it serves — ingestion and search share one write. The accepted constraints: the
`simple` dictionary gives no stemming and no CJK word segmentation, and ranking is
lexical rather than semantic.

Standing constraint: do not introduce an external search engine for document retrieval;
extend the tsvector and filter tiers instead — a dictionary, tokenizer, or ranking change
is an index migration, not a service decision.
