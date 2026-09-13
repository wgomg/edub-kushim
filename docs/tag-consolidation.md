# Tag Consolidation — Policy, Adjudication, Merge Review & Reconciliation

After the LLM assigns tags to a document, every emitted name is mapped onto the
existing tag vocabulary before anything is saved: spelling and casing variants
collapse onto their canonical form (`machine learning` → the existing
`machine-learning`), synonyms are recognized, and genuinely new concepts are
born as new tags. This page documents the consolidation subsystem end to end —
the deterministic replacement policy that runs on every enrichment, the opt-in
LLM adjudication of ambiguous pairs, the merge-review surface for pre-existing
duplicates, and the scheduled reconciliation task that heals older damage.

The principle throughout: **creation is the default; replacement requires
positive evidence of concept identity.** A wrongly kept tag is a visible,
mergeable duplicate; a wrongly replaced tag silently and permanently erases a
concept from the vocabulary. Every failure, ambiguity, or outage defaults to
*keep*.

- [The problem it solves](#the-problem-it-solves)
- [How a tag is decided](#how-a-tag-is-decided)
- [LLM adjudication (opt-in)](#llm-adjudication-opt-in)
- [Merge review](#merge-review)
- [Scheduled reconciliation](#scheduled-reconciliation)
- [Configuration](#configuration)
- [Operating notes](#operating-notes)

---

## The problem it solves

Consolidation exists to keep the tag vocabulary canonical: the LLM may emit
`Machine Learning`, `machine-learning`, or `ml`, and the store should end up
with one tag, not three. Before this subsystem was rewritten, the rule was
purely similarity-based: any emitted tag whose nearest existing tag scored at
or above 0.80 was replaced by it. Over 47 days of production logs that rule
misfired in four ways:

| Failure | Evidence from the logs |
| ------- | ---------------------- |
| Antonym flips | `capitalism` replaced by `anti capitalism` 502× (sim 0.872); `anti fascism` by `fascism` 111× (0.927) — the absorbed true term can never be born |
| Specific-term absorption | `revolution` replaced by `world revolution` 208×; `labor movement` by `japanese labor movement` 119× |
| Parent starvation | `machine learning`, `democracy`, `computer graphics` were each emitted dozens of times and never became tags — a related tag always absorbed them first |
| Lexical collisions | `access control` replaced by `remote control` (0.825); ~15,000 events shared a token with their target |

The root cause: in the 0.80–0.95 similarity band, *relatedness* and *synonymy*
score identically (`democracy` vs `direct democracy`: 0.892; `abortion` vs
`abortion rights`: 0.912), while the error costs are asymmetric — replacement
is silent and permanent, a keep is visible and fixable. 87% of consolidation
events were exact identity (sim 1.000) and worked fine; the defect was confined
to the non-identity band and its decision rule. The rewrite keeps the identity
fast path and replaces the bare-threshold rule with guards, bands, adjudication,
and review.

---

## How a tag is decided

Per enrichment, after the LLM emits up to 8 tags (trimmed to 5):

1. Emitted names are canonicalized (`utils.NormalizeTag`: NFKC, lowercase,
   accent-folded; `+`, `#`, `.`, digits, and non-Latin scripts preserved —
   `c++` is one token). The same normalizer runs at the tag-service save
   chokepoints, so store names are always canonical and identity is exact
   string equality.
2. The matcher daemon ranks each name against the tag store and returns up to
   two candidates with similarity scores, all at or above the **consideration
   floor** (`consolidation_similarity`, 0.80 for bge-m3).
3. The enricher's policy decides, in order:

| Rule | Outcome | Catches |
| ---- | ------- | ------- |
| Exact identity with a store tag | replace | the 87% — casing, hyphenation, and symbol-form fixes |
| Negation guard — exactly one side carries `anti`/`contra`/`non` | keep | antonym flips in both directions, including closed compounds (`antifascism` → `fascism`) |
| Shared-token guard — the two names share tokens | keep | lexical collisions (`access control` / `remote control`) |
| Direction guard — query tokens a strict subset of the target's | keep | parent starvation (`democracy` kept over `direct democracy`); the broader term is born |
| Guard-free candidate at or above `auto_replace_similarity` (0.95, bge-m3) | replace | true synonyms close to identity |
| Anything else — the ambiguous band | keep (or adjudicate, below) | the 0.80–0.95 residual |

Both thresholds are model-derived internals (not YAML-settable); see
[tag-matcher.md](tag-matcher.md) for the per-model values and the derivation.

Every decision is logged in the kushim (enrichment) log as
`consolidation[v1]: replace|keep|passthrough <tag> … reason=…`, so the policy's
behavior is auditable after the fact — and so the reconciliation task can
recover history the database never captured.

---

## LLM adjudication (opt-in)

The ambiguous band is where similarity alone cannot decide. With
`enricher.tag_adjudicator.enabled: true` (default `false`), the enricher
resolves it with one cheap LLM classification per document:

- **Verdicts**: `same` (one concept → replace the emitted tag with the
  canonical target), `variant` (broader/narrower → keep, the tag is born),
  `related` (different concepts → keep).
- **Cache**: every verdict is stored in the `tag_verdict_event` table keyed by
  `(query, target, policy_version)`. A repeat pair resolves from the cache with
  no LLM call, whichever document produced it.
- **Evidence, not just names**: the prompt carries the pair's embedding
  similarity, the query's runner-up similarity, and the target's own nearest
  other store tag.
- **Failure safety**: any timeout, provider error, or ambiguous response keeps
  the emitted tag; enrichment never blocks or fails on adjudication. With the
  adjudicator disabled, the band behaves as pure keep — the Stage-1 policy.

---

## Merge review

Nothing merges automatically. The conservative policy deliberately keeps
near-duplicates visible, and the merge-review surface is where an operator
resolves them:

- **Tags page → "Review merges"** (editor-gated): fetches suggestions on
  demand; each pair shows similarity, population overlap, and verdict history,
  carries a survivor selector (defaulting to the higher-document-count tag),
  Merge with confirmation naming the tag to be deleted, and a session-only
  Dismiss.
- **Suggestions** come from the matcher daemon's pair cache — every pair of
  store tags at or above the consideration floor, maintained incrementally on
  tag CRUD — ranked by document-population overlap and verdict counts from
  `tag_verdict_event`. Pairs without adjudication history are ranked by
  similarity alone and flagged `similarity_only` (lower confidence); with the
  adjudicator disabled that is the common case.
- **`POST /api/v1/tags/{id}/merge`** hard-merges source into target in one
  transaction: `document_tag` rows re-pointed (documents already carrying the
  target keep a single row), leftovers cleared, source tag deleted, source name
  dropped from the embedding store. Editor/admin only; suggestions are
  viewer-readable via `GET /api/v1/tags/merge`.

---

## Scheduled reconciliation

`enricher.tag_reconcile` schedules a recurring `tagreconcile` task that
re-judges stored verdicts and repairs old damage — bounded and
anti-oscillating:

- **What it re-examines:** pairs whose verdicts predate the current policy
  version (the policy changed under them); fresh verdicts older than 90 days;
  kept pairs whose document-population overlap drifted; pairs with a dead side
  (a merged or deleted tag — dead-target verdicts are invalidated so a cached
  `same` verdict can never resurrect a deleted tag); and, for history the table
  never captured, pairs parsed from the daemon's rotated logs
  (`hugot.log*`/`kushim.log*`, `.gz` rotations included).
- **How it flips:** each pair is re-evaluated through the current policy on a
  fresh rank; band survivors are re-adjudicated, and a verdict flip must be
  confirmed by a second, orientation-reversed adjudication before it applies.
  A merge flip runs the full merge operation; a keep flip creates the tag and
  re-points only the affected documents.
- **Bounds:** `max_rejudge` pairs and `max_retag` documents per run; the first
  run is always **report-only** (it lists would-be actions and touches
  nothing), regardless of the `report_only` flag.
- With the adjudicator disabled, runs log the worklist snapshot and complete
  without LLM calls or writes. The queue daemon warns when reconciliation is
  enabled without the adjudicator.

---

## Configuration

```yaml
enricher:
  # tag_adjudicator:             # optional LLM verdicts for ambiguous tag pairs
  #   enabled: false             # disabled by default — enable after configuring LLM
  #   timeout: 120               # defaults to contentanalyzer.timeout when 0
  #   llm:                       # defaults to contentanalyzer.llm when provider/model are empty
  #     adapter: 'openai-compatible'
  #     provider: 'openai'
  #     model: 'gpt-4o'
  #     token: ''
  #     request_delay: 1
  # tag_reconcile:               # scheduled re-adjudication of stored tag verdicts
  #   enabled: false             # disabled by default
  #   report_only: true          # snapshot-only runs (no writes); the first run is always report-only
  #   interval: 7                # days between runs
  #   time: "02:00"              # preferred time of day (HH:MM)
  #   max_rejudge: 200           # max pairs re-evaluated per run
  #   max_retag: 1000            # max documents re-pointed per run
```

Both blocks are also editable on the Settings page ("Tag adjudicator" and "Tag
reconciliation" cards), including an "Inherit from content analyzer" option for
the adjudicator's LLM — leaving the LLM fields empty follows the content
analyzer's model even after it changes. The similarity thresholds
(`consolidation_similarity`, `auto_replace_similarity`) are model-derived
internals — see [tag-matcher.md](tag-matcher.md).

---

## Operating notes

- **Rollout order**: after upgrading, restart `kushim hugot` first (the
  similar-pairs endpoint has no legacy fallback — merge suggestions return 503
  against an old daemon). Optionally enable the adjudicator, use Review merges
  on demand, and enable reconciliation when you want automatic healing. Both
  features default to off so an upgrade never silently changes tag behavior or
  adds LLM cost.
- **While the adjudicator is off**, the ambiguous band keeps: the store grows
  faster with near-duplicates. That is the accepted safe direction, and the
  merge-review surface is the manual valve.
- **Matcher dependency**: with the daemon down, tag CRUD and merge suggestions
  return 503 and enrichment degrades to LLM-only tags (no consolidation) — the
  pipeline degrades, it does not fail.
- **Monitoring**: consolidation decisions live in the kushim log
  (`consolidation[v1]: … reason=…`); reconciliation results in the task's JSON
  report (worklist counts, judged/confirmed, flips, merges, re-tags,
  invalidations), visible in the tasks UI.

---

## See Also

- [Tag matcher](tag-matcher.md) — embedding model, thresholds, memory/CPU sizing
- [Developer guide: tag consolidation](developer-guide/tag-consolidation.md) — internals for developers
- [User manual](user-manual.md) — merge API, full config reference, task types
- [Architecture](architecture.md) — pipeline placement and process model
