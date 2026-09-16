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
- [Joint tags](#joint-tags)
- [LLM adjudication (opt-in)](#llm-adjudication-opt-in)
- [Tag quality judge (opt-in)](#tag-quality-judge-opt-in)
- [Quarantine and promotion](#quarantine-and-promotion)
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

Per enrichment, after the LLM emits its tags (prompted for between `min_tags`
and `max_tags`, default 3–5, and capped at `max_tags` by the filter):

1. Emitted names are canonicalized (`utils.NormalizeTag`: NFKC, lowercase,
   accent-folded; `+`, `#`, `.`, digits, and non-Latin scripts preserved —
   `c++` is one token). The same normalizer runs at the tag-service save
   chokepoints, so store names are always canonical and identity is exact
   string equality.
2. Typed tags are gated by the model's own `category`: `form_genre`,
   `person`, and `work` emissions are dropped, and `place` emissions survive
   only as countries or continents — every drop is logged as
   `tags[facet]: drop …`. `topic`/`other` and untyped emissions pass through.
   `FilterTags` then drops deterministic garbage (see below), and a split pass
   replaces tags that join concepts with a conjunction (`and`, `or`, `vs`,
   `vs.`, `versus`) by their components — see
   [Joint tags](#joint-tags) below.
3. The matcher daemon ranks each name against the tag store and returns up to
   two candidates with similarity scores, all at or above the **consideration
   floor** (`consolidation_similarity`, 0.80 for bge-m3).
4. The enricher's policy decides, in order:

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

## Joint tags

A tag that joins concepts with a conjunction (`and`, `or`, `vs`, `vs.`,
`versus` — after normalization `&` is stripped, so "rock & roll" is not joint)
is detected by `utils.SplitJointTag` and replaced by its components before
consolidation, so "supply and demand" becomes `supply` + `demand` instead of a
single tag that the matcher can never match cleanly.

- **Detection**: a canonical tag is joint when it has at least two segments
  around a conjunction token that is neither leading nor trailing. Tags of
  more than three tokens never reach the split pass — `FilterTags` drops them
  first, so detectable joint tags are exactly the 2–3-word single-conjunction
  forms.
- **Decision**: with the tag adjudicator enabled, the split decision is
  LLM-adjudicated (`AdjudicateTagSplits`, same adjudicator as tag pairs) —
  the LLM distinguishes a genuine joint ("supply and demand") from a proper
  name or set phrase ("dungeons and dragons", "rock and roll") and returns the
  components when splitting. Verdicts are memoized per tag name within the
  enricher process, so a joint tag seen across documents is adjudicated once.
  With the adjudicator disabled, all detected joint tags split
  deterministically.
- **Validation**: components pass the same rules as emitted tags — normalized,
  deduplicated, ≤3 words, ≤64 characters, no person/title/doc-type token
  matches — and a split must yield between 2 and `max_tags` valid components;
  anything else keeps the joint tag as-is. Split products may push a document
  past `max_tags` — accepted; worst-case bound is 2 × `max_tags`.
- **Retroactive cleanup**: the scheduled reconciliation task scans the store
  for joint tags and splits them with document re-pointing — see
  [Scheduled reconciliation](#scheduled-reconciliation).

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

## Tag quality judge (opt-in)

Adjudication decides what is a duplicate of an existing tag; the quality judge
decides what deserves to be born. With `enricher.tag_quality.enabled: true`
(default `false`), the novel tags that survive consolidation — names absent
from the vocabulary — are filtered before they are created:

- **Weak band**: each candidate is scored from its typed emission —
  `is_standard_term` +1 when true, 0 when absent, −1 when false; category
  `other` −1; best store similarity below the internal floor (0.60) −1. A
  score below the internal threshold (−1, i.e. ≤ −2) is dropped without
  an LLM call, and the rejection is never cached — a later emission with
  stronger evidence re-scores.
- **Rubric verdict**: the survivors are judged in one temperature-0 LLM call
  per document against four binary criteria — the tag is a subject matter (not
  a form or genre), standard, widely-recognized terminology (not a coinage
  specific to the document), not a person/work/non-whitelisted place, and
  appropriately broad for a shared vocabulary. A verdict passes only when all
  four are true. Whitelisted countries and continents are never rejected for
  being a place, so the typed facet gate and this judge cannot disagree.
- **Cache**: verdicts are stored in the `tag_quality_event` table keyed by
  `(tag_name, policy_version)`. A re-emitted name resolves from the cache with
  no LLM call, whichever document produced it; a model or policy change is
  reflected by bumping the version.
- **Failure safety**: any timeout, provider error, or incomplete response
  retries once and then keeps every novel tag — under the gate the birth is
  provisional and carries no verdict, and enrichment never blocks or fails on
  the judge.
- **Logging**: rejections are logged as `tagquality[v1]: reject …` lines
  (weak-band and judged variants both carry the reason), siblings of the
  `consolidation[v1]` and `tags[facet]:` lines.

Both band thresholds are internal v1 constants, not configuration keys; the
judge LLM and timeout inherit the content analyzer's unless the `llm` block
overrides them, and a warning is logged when the resolved judge equals the
generator.

---

## Quarantine and promotion

With the gate on, the novel tags that survive the judge are **provisional**:
they are attached to the document and searchable from birth, but
`tag.promoted_at` is NULL and the name is excluded from the embedding store —
so it is never suggested, never a consolidation target, and never part of the
all-names fallback vocabulary (`BuildTagCache` embeds promoted tags only, and
the enricher composes its fallback and consolidation sets from the promoted
subset). Promotion is inline, one-way, and runs inside the persist
transaction: a provisional name with a cached passing verdict at the current
`TagQualityVersion` and assignments on at least two distinct **live** documents
(trashed carriers are not evidence, in this check and in the reconciliation
sweep alike) gets `promoted_at = now()` and enters the store after the commit.
The `document_tag` primary key makes the count distinct-document, so a single
document re-enriching itself cannot promote a name.

Verdict-less provisionals — judge outages and re-emitted legacy rows — are not
re-judged in flight: a name already present in the tag table is not novel, so
the judge never sees it again. They are re-judged by the scheduled
reconciliation sweep once they pass `provisional_grace_days` (default 30): the
sweep promotes names that already hold a cached passing verdict and ≥ 2 live
documents, re-judges the rest oldest-first under the `max_rejudge` cap (one
judge call over the tag's first live document, one retry on failure, the
verdict recorded through the same fresh-verdict/cache path the enricher uses),
promotes a fresh pass at ≥ 2 live documents, and leaves a rejection queued for
review. A missing judge, a tag with no live document text, and lookup or write
failures are counted and skipped rather than failing the run, and the sweep
never deletes a tag.

Operators resolve what remains from the tags page's "New tags" tab or
the API: `GET /api/v1/tags/provisional` lists provisional tags with a live
`document_count` and the latest verdict (`null` when no verdict row exists),
and `POST /api/v1/tags/{id}/promote` promotes one through the same path the
sweep uses. Rejecting a provisional tag is a normal tag delete, so the name
lands in the `deleted_tag` blocklist and enrichment cannot resurrect it. The
trust marker was backfilled from `created_at` for tags that already had
assignments, and a corrective migration returns the backfill-promoted singletons
(fewer than two distinct documents) to provisional so the sweep re-judges them;
zero-assignment rows stay provisional and the seeded vocabulary is inserted
promoted. Layer off ⇒ newborns are born trusted exactly as before.

---

## Merge review

Nothing merges automatically. The conservative policy deliberately keeps
near-duplicates visible, and the merge-review surface is where an operator
resolves them:

- **Tags page → "Merge review" tab** (editor-gated): fetches suggestions on
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
  (`hugot.log*`/`kushim.log*`, `.gz` rotations included). A joint-tag scan
  (appended after the pair scans, same caps) detects store tags that join
  concepts with a conjunction (`and`, `or`, `vs`, `vs.`, `versus`) and splits
  them into their components.
- **How it flips:** each pair is re-evaluated through the current policy on a
  fresh rank; band survivors are re-adjudicated, and a verdict flip must be
  confirmed by a second, orientation-reversed adjudication before it applies.
  A merge flip runs the full merge operation; a keep flip creates the tag and
  re-points only the affected documents. A joint-tag split ranks the components
  against the store excluding the joint tag itself, consolidates them through
  the policy, creates them via `Tag.Create`, adds them to every document
  carrying the joint tag (single set-based insert), and deletes the joint tag
  with a post-commit store removal — idempotent and crash-tolerant (component
  creation and document assignment are additive, deletion is last). Pair
  entries whose query is itself a joint tag are dropped from the pair worklist
  so a merge verdict can never resurrect a tag the split scan is deleting.
- **Bounds:** `max_rejudge` pairs and `max_retag` documents per run; the
  provisional re-judge shares the `max_rejudge` budget. The first run is always
  **report-only** (it lists would-be actions and touches nothing), regardless
  of the `report_only` flag. The joint-tag scan shares both caps.
- **Provisional hygiene:** the same run re-checks provisional tags older than
  `provisional_grace_days` (default 30), oldest first. A stored passing verdict
  plus ≥ 2 live documents promotes the name with no judge call; verdict-less
  names are re-judged one at a time (the tag's first live document, reduced
  with the tag-matcher arithmetic, one retry on failure) and promoted when they
  pass; a rejection stays queued for the review surface. Zero-assignment rows
  are only counted — the orphan sweep owns them — and a missing judge, a tag
  with no live document text, or a failed lookup/write is counted
  (`no_judge`/`skipped`/`failed`) without failing the run. Report-only runs
  count would-promote/would-re-judge and touch nothing.
- **Dead-tag hygiene:** the same run sweeps zero-assignment tags — a tag whose
  `orphaned_since` mark is older than `dead_tag_grace_days` (default 7) is
  deleted and removed from the embedding store, and every assignment path
  clears the mark so the window tracks real last usage. Names blocked or
  merged away by an operator are skipped by the keep-flip path (and dropped or
  rewritten by enrichment), so they cannot be resurrected outside the restore
  flow.
- **Report:** each run persists a full JSON report as its task result — worklist
  counts (including the joint-tag category), judged/confirmed, flips,
  planned/applied pair entries (each carrying the decision reason and whether
  it creates a tag), planned/applied joint splits (query, components,
  reassigned documents, reason), skip reasons, and the provisional-sweep
  counts (`worklist`, `zero_assignment`, `promoted`, `rejudged`, `deferred`,
  `no_judge`, `skipped`, `failed`) — surfaced in the web UI on
  the task detail page and as a status strip on the tags page.
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
  # tag_quality:                 # optional rubric judge for newly emitted tags
  #   enabled: false             # disabled by default — enable after configuring LLM
  #   timeout: 120               # defaults to contentanalyzer.timeout when 0
  #   llm:                       # defaults to contentanalyzer.llm when provider/model are empty (use a different model: judge hygiene)
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
  #   dead_tag_grace_days: 7     # days a tag must stay without documents before the sweep removes it
  #   provisional_grace_days: 30 # days a provisional tag may stay unreviewed before the sweep re-checks it
```

All three blocks are also editable on the Settings page ("Tag adjudicator",
"Tag quality", and "Tag reconciliation" cards), including an "Inherit from
content analyzer" option for the judge LLMs — leaving the LLM fields empty
follows the content analyzer's model even after it changes. The similarity
thresholds (`consolidation_similarity`, `auto_replace_similarity`) and the
quality band constants are internals — see [tag-matcher.md](tag-matcher.md).

---

## Operating notes

- **Rollout order**: after upgrading, restart `kushim hugot` first (the
  similar-pairs endpoint has no legacy fallback — merge suggestions return 503
  against an old daemon). Optionally enable the adjudicator, use the Merge review
  on demand, and enable reconciliation when you want automatic healing. Both
  features default to off so an upgrade never silently changes tag behavior or
  adds LLM cost.
- **While the adjudicator is off**, the ambiguous band keeps: the store grows
  faster with near-duplicates. That is the accepted safe direction, and the
  merge-review surface is the manual valve.
- **Provisional review**: with the quality judge on, new names stay provisional
  until promoted and show up in the tags page's "New tags" tab. The
  reconciliation run's provisional sweep promotes or defers them automatically
  once they pass `provisional_grace_days`, so the panel is a manual override
  rather than a required step, and a rejection recorded there is a normal tag
  delete (blocklist included).
- **Matcher dependency**: with the daemon down, tag CRUD and merge suggestions
  return 503 and enrichment degrades to LLM-only tags (no consolidation) — the
  pipeline degrades, it does not fail.
- **Monitoring**: consolidation decisions live in the kushim log
  (`consolidation[v1]: … reason=…`); reconciliation results in the task's JSON
  report (worklist counts, judged/confirmed, flips, merges, re-tags,
  invalidations), visible in the web UI — the task detail page shows the full
  report with pair-level breakdowns (would-be merges, would-be re-tags,
  invalidated decisions, skip reasons), and the tags page shows a status strip
  for the latest run.

---

## See Also

- [Tag matcher](tag-matcher.md) — embedding model, thresholds, memory/CPU sizing
- [Developer guide: tag consolidation](developer-guide/tag-consolidation.md) — internals for developers
- [User manual](user-manual.md) — merge API, full config reference, task types
- [Architecture](architecture.md) — pipeline placement and process model
