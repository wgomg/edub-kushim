# 0010. Document type: described candidates, LLM choice, head/tail refinement

**Status:** accepted

## Context

Enrichment assigns each document a type. Structural markers — DOIs, invoice numbers,
ISBNs, salutations, step numbering — are the strongest type signals, but TextRank
reduction scores them low, so they rarely survive in the excerpt the analysis call sees.
The corpus is multilingual: with 100+ OCR languages in play, any signal must work for
`invoice`, `facture`, `Rechnung`, and `請求書` alike.

Rejected alternative:

- **Deterministic per-type keyword/regex rules.** Each type would
  need per-language keywords an admin maintains across every supported language; a
  description-driven choice subsumes the same ground with zero per-type engineering; and
  the rules would rot — types are assigned silently during enrichment, so there is no
  test-and-fix feedback loop to sustain them.

## Decision

Type choice rides on the content-analysis call: candidates are passed as
`analyzer.TypeRef{Name, Description}`, so the admin-written `DocumentType.Description`
carries what each type means. When `Enricher.ContentAnalyzer.DocTypeRefinement` is
enabled, a second pass (`AnalyzeDocType`) re-decides the type from the head and tail of
the raw full text — the structural markers the reduction dropped — carrying the
conversation history for provider caching; any failure in the second pass falls back to
the first pass's type.

## Consequences

Editing one description retunes classification for every language at once; the refinement
recovers structural evidence without schema changes, costing an extra call only when
enabled; type quality tracks the analysis model's judgment instead of maintained rule
tables.

Standing constraint: do not add per-type keyword or regex rule tables for classification;
type semantics live in `DocumentType.Description`, and structural markers belong to the
head/tail refinement pass.
