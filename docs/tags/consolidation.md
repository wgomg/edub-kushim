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
- [Compound tags](#compound-tags)
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

Per enrichment, after the LLM emits its tags (prompted for at least `min_tags`,
default 3; the filter applies no count cap):

1. Emitted names are canonicalized (`textnorm.NormalizeTag`: NFKC, lowercase,
   accent-folded; `+`, `#`, `.`, digits, and non-Latin scripts preserved —
   `c++` is one token). The same normalizer runs at the tag-service save
   chokepoints, so store names are always canonical and identity is exact
   string equality.
2. Typed tags are gated by the model's own `category`: `form_genre`,
   `person`, and `work` emissions are dropped, and `place` emissions survive
   only as countries or continents — every drop is logged as
   `tags[facet]: drop …`. `topic`/`other` and untyped emissions pass through.
   `FilterTags` then drops deterministic garbage (see below), and a
   decomposition pass replaces compound tags by their parts — see
   [Compound tags](#compound-tags) below.
3. The matcher daemon ranks each name against the tag store and returns up to
   two candidates with similarity scores, all at or above the **consideration
   floor** (`consolidation_similarity`, 0.80 for bge-m3).
4. The enricher resolves identity before any judging: names are normalized and
   brought to house form (M1: word ordinals and roman numerals become numeric,
   `world war ii` → `world war 2`), alias names resolve to their canonical
   value, and key-equal names (M3) apply the existing label. Similarity ranks
   candidate neighbours but never replaces or keeps on its own:

| Rule | Outcome | Catches |
| ---- | ------- | ------- |
| Alias lookup — the name is a `tag_alias` row | apply the canonical value | names that already lost a same-intent decision |
| Key equality (M3) — house form matches a store tag | apply the store label | style variants (`nineteenth century` / `19th century`) |
| Exact identity with a store tag | apply the store label | casing, hyphenation, and symbol-form fixes after normalization |
| Flagged fold — a mechanically proposed reduction (M2) | judged: base form (J1), fold gate (J2), conventional form (J5); the fold applies only when all three confirm | inflections (`buildings` → `building`), relational adjectives, bounded morphographic variants (`mexican` → `mexico`), conventional forms (`c plus plus` → `c++`) |
| Pinned variant class — a pinned orthographic spelling variant or a literal token permutation | mechanical `same`, no judge call: the deterministic tie-break (M4) picks the winner, the other name aliases | `decolonisation`/`decolonization`, `military history`/`history military` |
| Any other candidate neighbour — the same-intent gate | judged (J3) with a direction (J4): `same` aliases the loser onto the winner (a store label that loses becomes the alias), `variant`/`related` register both | the residual band and shape-related pairs |
| No judge available | the claim waits: the candidate registers provisionally with a pending identity-evidence row | adjudicator disabled or unreachable |

The consideration floor (`consolidation_similarity`, 0.80 for bge-m3) is a
model-derived internal (not YAML-settable); the former auto-replace cutoff no
longer decides anything. See [matcher.md](matcher.md) for the per-model value
and the derivation.

Every identity disposition is logged in the kushim (enrichment) log
(`identity[v6]: apply|alias|defer|separate <name> …`), so the pipeline's
behavior is auditable after the fact — and so the reconciliation task can
recover history the database never captured.

---

## Compound tags

A compound tag whose parts each name a subject — `russian history`,
`philosophy of mind`, `air fryer repair`, `supply and demand` — is replaced by
its parts before consolidation, so `russian history` becomes `russia` +
`history` instead of a single tag that the matcher can never match cleanly.
Fixed names and set phrases that name one subject their parts cannot compose
(`cold war`, `internet of things`, `black bloc`) stay whole, and a compound
whose key already exists in the store (names and aliases) is an application:
it is skipped before judgment and never split.

- **Detection**: every canonical tag of two to three words in Latin script
  with at least one content part is a candidate (`tags.ClassifyShape` =
  `ShapeCompound`); single-word, non-Latin, and function-word-only names never
  reach the pass.
- **Decision**: with the tag adjudicator enabled, the split decision runs two
  context-free judged passes (same adjudicator as tag pairs) — a **parts
  pass** (`analyzer.TagPartDimensionVersion`) judges each fold-resolved content
  word on its own, and a **compound pass** (`analyzer.TagSplitVersion`) reads
  the phrase as a named unit, a plain composition, or a phrase with no
  retrievable dimension — and the two answer sets fold into one decision
  (`tags.DecideComposition`). The gate splits when every part names a subject a
  searcher would filter by on its own and the phrase is just their combination,
  including a modifier before a generic head (`labor movement`, `bike
  repair`); it keeps the tag when the phrase names one established subject the
  parts cannot compose (`urban planning`), and drops a phrase that names no
  retrievable dimension. Both verdicts are cached per value and per compound,
  so a value or compound seen across documents is judged once. With the
  adjudicator disabled, only conjunction joints (`and`, `or`, `vs`, `vs.`,
  `versus` — detected by `textnorm.SplitJointTag`; after normalization `&` is
  stripped, so "rock & roll" is not joint) split deterministically; every
  other compound passes whole.
- **Evidence**: every judged compound appends one `tag_composition_event`
  verdict row — candidate, validated parts, `disposition`
  (`split`/`keep`/`drop`), `reason`
  (`judge`/`cached`/`conjunction`/`invalid_components`/`judge_unavailable`, or
  `part_rejected` when an admitted part was rejected downstream and the split
  degrades to `drop`, keeping the row's resolved parts), the criteria version,
  and the judge model (`NULL` for cached verdicts and the conjunction
  fallback) — with a matching `tag split[...]` log line per candidate.
- **Validation**: components are first fold-resolved onto **promoted** store
  values through the store-anchored net (derivational proposals and
  suffix-local morphographic proposals of at most two edits; lemma folds stay
  out; provisional births are not seam anchors) and then pass the hard
  per-document rules — normalized, deduplicated, ≤3 words, ≤64 characters, no
  person or doc-type token matches; the title-containment echo rule does not
  apply to parts — and a split must yield between 2 and
  `tags.MaxCompoundWords` (3) valid components; anything else keeps the
  compound as-is. Emissions are not count-capped.
- **Retroactive cleanup**: the scheduled reconciliation task scans the store
  for conjunction joints and splits them with document re-pointing — see
  [Scheduled reconciliation](#scheduled-reconciliation).

---

## LLM adjudication (opt-in)

Identity is where similarity alone cannot decide. With
`enricher.tag_adjudicator.enabled: true` (default `false`), the gate resolves
proposed folds and candidate neighbours with LLM calls:

- **Folds**: one identity call per flagged candidate answers the three binary
  canonical-form criteria (base form, fold preservation, conventional written
  form); the fold applies only when all three confirm.
- **Verdicts**: `same` (one value → the losing name becomes an alias of the
  canonical target), `variant` (broader/narrower → keep, the tag is born),
  `related` (different concepts → keep); for `same` the call also names the
  precise term (`query`, `target`, or `tie`, with lexical order breaking ties).
- **Cache**: every verdict is stored in the `tag_verdict_event` table keyed by
  `(query, target, policy_version)`. A repeat pair resolves from the cache with
  no LLM call, whichever document produced it.
- **Evidence, not just names**: the prompt carries the pair's embedding
  similarity, the query's runner-up similarity, and the target's own nearest
  other store tag.
- **Failure safety**: a transient failure (transport, generic provider error
  or empty response) retries once; parse failures, incomplete replies,
  output-cap hits, credit exhaustion, token-limit, content-too-large and
  context errors are never retried. A truncated reply salvages the verdicts it
  returned and leaves only the pairs or candidates it omitted pending. Every
  non-credit failure keeps the emitted tag — enrichment never blocks on
  adjudication that way — and a credit-exhaustion failure aborts the document
  so the batch pauses. With the
  adjudicator disabled, the band behaves as pure keep — the Stage-1 policy.

---

## Tag quality judge (opt-in)

Adjudication decides what is a duplicate of an existing tag; the admission
decision decides what deserves to be born. With
`enricher.tag_quality.enabled: true` (default `false`), the novel tags that
survive consolidation — names absent from the vocabulary — are decided before
they are created:

- **Features**: each candidate carries the judge's five rubric answers —
  `subject_matter`, `dimension` (a reusable dimension of content, not a
  per-document detail or container word), `not_person_place_work`, `atomic`
  (one dimension, not a combination whose parts each work as tags), and
  `not_form_genre` (not a publishing format — genres and art
  forms are subject-side and pass) —
  plus the judge's optional `defer` abstention, and the emission's
  deterministic signals: a typed detail annotated `other`, and a best store
  similarity below the internal no-anchor floor (0.60).
- **Decision**: the pinned rule set (`tags.DecideAdmission`) defers on the
  judge's abstention, rejects on two or more failing signals, defers on
  exactly one (the gray band — no single signal decides), and admits when
  nothing fails. Rejected names are dropped for that document and never
  stored — their plan registrations and pending identity evidence are removed
  before persist. A missing detail or similarity is absence of evidence, not
  a failing signal.
- **Judge call**: the uncached candidates are judged in temperature-0 LLM
  calls over batches of up to 40 candidates per document; the reduced document
  text serves the `subject_matter` question alone, the remaining criteria
  answering on the candidate; whitelisted
  countries and continents are never rejected for being a place, so the typed
  facet gate and the judge cannot disagree.
- **Cache**: verdicts are stored in the `tag_quality_event` table keyed by
  `(tag_name, policy_version)` with the recorded outcome
  (`reject`/`defer`/`admit`). A re-emitted name resolves from the cache with
  no LLM call — the decision re-runs over the stored rubric with the current
  emission's deterministic signals — whichever document produced it; a model
  or policy change is reflected by bumping the version. Rows predating the
  rubric or the outcome column fall back to their `pass` flag until the store
  reset retires them.
- **Failure safety**: a transient failure (transport, generic provider error
  or empty response) retries once; parse failures, incomplete replies,
  output-cap hits, credit exhaustion, token-limit, content-too-large and
  context errors are never retried. A reply that omits verdicts for some
  candidates salvages the entries it returned — each is decided and cached
  under `(tag_name, policy_version)` — and defers only the missing candidates
  with `judge_unavailable` (no row; re-judged on re-emission); every other
  non-credit failure keeps every novel tag with a defer decision — under the
  gate the birth is provisional and carries no verdict — and a
  credit-exhaustion failure pauses the batch instead.
- **Logging**: rejections and defers are logged as `tagquality[v4]:` lines
  carrying the outcome and reason, siblings of the `consolidation[v1]` and
  `tags[facet]:` lines.

The decision thresholds are pinned v1 constants, not configuration keys; the
judge LLM and timeout inherit the content analyzer's unless the `llm` block
overrides them, and a warning is logged when the resolved judge equals the
generator.

---

## Judge criteria versions

Every LLM judge pins its criteria with a version constant, and a cached verdict
applies only under the version that produced it — a criteria or prompt change
bumps the constant so older rows never apply. The current constants:

- `analyzer.TagQualityVersion` (`v4`) — the tag-quality judge's dimension
  rubric and prompt.
- `analyzer.TagPartDimensionVersion` (`v2`) — the split gate's parts pass: one
  context-free dimensional question per value, judged in its ordinary
  standalone sense.
- `analyzer.TagSplitVersion` (`v3`) — the split gate's compound pass: the
  context-free `named_unit`/`composition`/`no_dimension` trichotomy plus an
  abstention, and the `criteria_version` every composition row carries.

---

## Quarantine and promotion

With the gate on, the novel tags the decision keeps are **provisional**:
they are attached to the document and searchable from birth, but
`tag.promoted_at` is NULL and the name is excluded from the embedding store —
so it is never suggested, never a consolidation target, and never part of the
all-names fallback vocabulary (`BuildTagCache` embeds promoted tags only, and
the enricher composes its fallback and consolidation sets from the promoted
subset). Promotion is inline, one-way, and runs inside the persist
transaction: a provisional name the admission decision admits — its emission
decision, or the cached verdict row mapped through the same decision for
names the judge skipped — with assignments on at least ten distinct **live**
documents
(trashed carriers are not evidence, in this check and in the reconciliation
sweep alike) gets `promoted_at = now()` and enters the store after the commit.
The `document_tag` primary key makes the count distinct-document, so a single
document re-enriching itself cannot promote a name. An identity-held name — a
fold the gate refused beside a live store value — and a composition-held name —
the latest composition verdict at the current split criteria version defers,
conflicts, disagrees, or is a reconstructible named unit — are skipped by both
the inline check and the sweep until an operator resolves them.

Verdict-less provisionals — judge outages and re-emitted legacy rows — are not
re-judged in flight: a name already present in the tag table is not novel, so
the judge never sees it again. They are re-judged by the scheduled
reconciliation sweep once they pass `provisional_grace_days` (default 30): the
sweep promotes names whose stored verdict the decision re-derives as admit
with ≥ 10 live documents, leaves defer and reject verdicts for review,
re-judges the rest oldest-first under the `max_rejudge` cap (one
judge call over the tag's first live document, one retry on failure, the
verdict recorded through the same fresh-verdict/cache path the enricher uses),
promotes an admitted name at ≥ 10 live documents, and leaves a deferral or
rejection queued for review. A missing judge, a tag with no live document text,
and lookup or write
failures are counted and skipped rather than failing the run, and the sweep
never deletes a tag.

Operators resolve what remains from the tags page's "New tags" tab or
the API: `GET /api/v1/tags/provisional` lists provisional tags with a live
`document_count` and the latest verdict (`null` when no verdict row exists,
labelled from the recorded outcome),
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
  Merge with confirmation naming the tag to be deleted, and Dismiss/Restore.
  Both document counts link to the documents page pre-filtered by the tag, and
  the vocabulary table expands an editor row into the same pair cards for the
  pairs involving that tag.
- **Dismissals are persisted** in the global `tag_merge_dismissal` table
  (`POST`/`DELETE /api/v1/tags/merge/dismissals`, editor/admin), so a dismissed
  pair stays out of the queue across sessions and for every operator. The
  "Hide dismissed" switch (on by default, remembered per browser) decides
  whether dismissed pairs are listed; when listed they carry a "dismissed" pill
  and a Restore action, and the `include_dismissed` parameter returns them
  without letting them consume the live queue's slots.
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
  viewer-readable via `GET /api/v1/tags/merge`, and dismissals are global
  (shared by all operators), keyed by the two normalized tag names.

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
  `provisional_grace_days` (default 30), oldest first. A stored verdict the
  decision re-derives as admit
  plus ≥ 10 live documents promotes the name with no judge call, while defer and
  reject verdicts stay queued for review; verdict-less
  names are re-judged one at a time (the tag's first live document, reduced
  with the tag-matcher arithmetic, one retry on failure) and promoted when
  admitted; a deferral or rejection stays queued for the review surface. Zero-assignment rows
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
floor (`consolidation_similarity`), the identity rule-set version, and the
quality band constants are internals — see [matcher.md](matcher.md).

---

## Operating notes

- **Rollout order**: after upgrading, restart `kushim hugot` first (the
  similar-pairs endpoint has no legacy fallback — merge suggestions return 503
  against an old daemon). Optionally enable the adjudicator, use the Merge review
  on demand, and enable reconciliation when you want automatic healing. Both
  features default to off so an upgrade never silently changes tag behavior or
  adds LLM cost.
- **While the adjudicator is off**, claims wait: folds are not applied and
  same-intent pairs leave pending identity evidence with their candidates born
  provisional (resolvable later by `POST /api/v1/tags/identity/reevaluate` or
  the reconciliation sweep). The store grows faster with near-duplicates — the
  accepted safe direction — and the merge-review surface is the manual valve.
- **Provisional review**: with the quality judge on, new names stay provisional
  until promoted and show up in the tags page's "New tags" tab. Identity-held
  names (a refused fold beside a live store value) show their store anchor and
  wait for review — the sweep never promotes them. The
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

- [Tag matcher](matcher.md) — embedding model, thresholds, memory/CPU sizing
- [Developer guide: tag consolidation](../developer-guide/tag-consolidation.md) — internals for developers
- [User manual](../user-manual.md) — merge API, full config reference, task types
- [Architecture](../architecture.md) — pipeline placement and process model
