# Developer Guide — Tag Consolidation and Identity

This guide explains how the post-LLM tag-consolidation subsystem works
internally: the birth-time identity machinery, the pure candidate-generation
policy, the enricher flow that applies it, LLM adjudication of the same-intent
gate, the verdict and identity-evidence tables, the matcher RPC surface and
pair cache, the merge-review service, and the scheduled reconciler.

It is the internal-mechanics companion to `docs/tags/consolidation.md` and
`docs/tags/canonical-form-and-identity.md` (design). Audience: developers who
want to understand, tune, or change how emitted tags are mapped onto the
canonical vocabulary.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [The identity machinery and the policy layer](#2-the-identity-machinery-and-the-policy-layer)
3. [The enricher flow](#3-the-enricher-flow)
4. [LLM adjudication](#4-llm-adjudication)
5. [The verdict and evidence tables](#5-the-verdict-and-evidence-tables)
6. [Matcher RPC surface and the pair cache](#6-matcher-rpc-surface-and-the-pair-cache)
7. [Merge review](#7-merge-review)
8. [Scheduled reconciliation](#8-scheduled-reconciliation)
9. [Tag extension measurement](#9-tag-extension-measurement)
10. [Safety properties](#10-safety-properties)
11. [Gotchas](#11-gotchas)

---

## 1. Orientation

Consolidation runs at the end of enrichment, after the LLM emits tags
(architecture pipeline step 5). The seam is deliberate: **the matcher daemon
only ranks** — it embeds the emitted names and returns candidates with scores —
and **the enricher owns the identity decision**. That split lets the pipeline
(and everything built on it: adjudication, reconciliation) live in non-cgo
packages that run in both binaries, while the cgo-gated model stays in the
daemon.

The subsystem spans these packages:

| Package | Role |
| ------- | ---- |
| `internal/tags` | canonical-form identity mechanics: house form (M1), lemma/stem signatures (M2, evidence only), key equality (M3), tie-break (M4), alias/evidence types, deterministic planning |
| `internal/tags/policy` | pure candidate generation: identity + band subjects, no logging, no config |
| `internal/tags/admission` | admission seams: alias lookup, identity planning, judge/adjudicator orchestration, tag-quality gating, plan application |
| `internal/enrichment` (`enricher.go`) | coordinator: runs the pipeline and calls the admission seams |
| `internal/tags/reconcile` | scheduled re-adjudication + log bootstrap parser + identity-evidence sweep |
| `internal/tags/curation` | tag vocabulary services: tag CRUD, merge operation + merge suggestions, identity-run enqueue |
| `internal/matcher` | the daemon (`Rank`, pair cache, embedding store) and the RPC client (`edub`'s deployment) |
| `internal/database` | migrations 00017/00018/00029, verdict + evidence queries |

Two deployments share the `matcher.Matcher` interface: the in-process
`*Hugot` (kushim, cgo) and `*matcher.MatcherClient` (edub, Unix-socket RPC to
`kushim hugot`). Any interface change must update both plus the test doubles
(`internal/testutil`, `internal/tools/runner_test.go`).

Thresholds involved (`internal/config/config.go`): `consolidation_similarity`
is the **consideration floor** — the daemon's `Rank` candidate filter
(bge-m3 → 0.80). The former `auto_replace_similarity` cutoff is no longer
consumed by any decision: a similarity score never replaces a name on its own,
only identity or the same-intent gate do. The field is still derived per model
and serialized in config responses. Per-model values and the derivation
switches are documented in the [semantic matching guide](semantic-matching.md#10-thresholds-and-configuration).

---

## 2. The identity machinery and the policy layer

### 2.1 `internal/tags` — canonical-form mechanics

The mechanical body (`internal/tags/identity.go`) is pure and shared by the
admin and emission registration paths
(design: [canonical form and identity](../tags/canonical-form-and-identity.md)):

- **M1 — house form** (`HouseForm`): word ordinals (`nineteenth century` →
  `19th century`) and roman numerals (`world war ii` → `world war 2`, `xiv` →
  `14`) convert to numeric house style; English dictionary words that happen to
  parse as roman numerals (`mix`, `mid`) are left alone. The rewrite is a style
  rewrite and is written directly at registration.
- **M2 — shape signatures** (`ComputeSignature`): per-token lemma (golem) and
  stem (snowball) signatures. They are evidence only — the stemmer's output is
  never written, and a proposed fold (`ProposedFold`) is never applied
  unconfirmed.
- **M3 — key equality** (`Key`): the identity key is the house form, so style
  variants collapse without judging.
- **M4 — tie-break** (`TieBreak`): fixed lexical order decides when the
  direction criteria do not separate two labels.
- **Version**: `IdentityRuleSetVersion = "v0"` is stamped on every alias row and
  identity-evidence row.
- **`PlanDeterministic`** — the admin path's deterministic phase: normalization,
  M1 rewrites, alias lookup, key equality, M2 flagging. It never proposes a fold
  for application, never adjudicates, and never makes an alias; flagged shape
  relations register provisionally with pending evidence. `NameIndex` memoizes
  keys and signatures per name so the flagging loops compare cached values.
- **`ResolveAliases`** — the single alias resolver: normalizes and dedupes names
  and resolves them to canonical names in one query. It backs registration,
  suggestions, structured search, and the enricher.

### 2.2 Candidate generation (`internal/tags/policy`)

`policy` (`internal/tags/policy/policy.go`) is a pure function of
its inputs — no logging, no config, no I/O, no cgo:

```go
func Evaluate(queries []string, ranked []matcher.RankResult) []Decision
```

Each `Decision` records `Query`, `Output`, `Target`, `Sim`, `RunnerUpSim`,
`Action` (`Passthrough` / `Replace` / `Keep`) and `Reason` (`identity`,
`ambiguous_band`, `no_candidates`, `short_slice`). `Version = "v1"`
(`policy.go`) is the policy version stamped on stored verdicts — bumping it is
what marks all stored verdicts stale to the reconciler.

Per query, `Evaluate` runs in order:

1. **Passthrough** — short slices (ranked shorter than queries) and results
   with no candidates (legacy-daemon fallback, disabled consolidation) pass
   through untouched; they are already decided.
2. **Identity** — exact equality with a store tag (names are canonical on both
   sides, so this is string equality) → replace. This remains the bulk of real
   traffic: casing, hyphenation, and symbol-form fixes after normalization;
   key-equivalent style variants resolve earlier through M3.
3. **Band subjects** — every other candidate becomes a gate subject
   (`ambiguous_band`): the emitted `KeptName` is kept and the top candidate is
   recorded with its similarity and runner-up. Similarity ranks and filters
   candidates; it never replaces or keeps on its own.

The runner-up similarity is recorded on every decision and never affects it —
it exists as adjudication evidence.

### 2.3 The gate and its dispositions

Flagged candidates and band subjects are resolved before the persist
transaction — adjudication stays outside it:

- **Folds (J1/J2/J5)** — one identity-judge call per flagged candidate answers
  three binary criteria: fold to base form, fold preserves denotation,
  conventional written form. A fold applies only when all three confirm; a
  candidate whose criteria have no judge answer stays provisional with the
  three criteria recorded as pending evidence.
- **Same-intent pairs (J3/J4)** — the pair adjudicator answers `same` /
  `variant` / `related`, plus a direction for `same` (`query`, `target`, or
  `tie`; a tie falls back to M4). `same` yields the alias disposition;
  `variant`/`related` keep both names.
- **Dispositions** — `apply` (the name resolves to an existing value by alias
  or key equality), `register` (new value), `alias` (the losing name becomes a
  `tag_alias` row; when the loser already exists as a tag, its assignments are
  re-pointed, its row is removed, and its own aliases are retargeted onto the
  canonical — no chains), `defer` (gray: the candidate registers provisionally
  for review). A candidate that wins a pair against a store label makes the
  store label the alias (candidate-wins).
- **Pending evidence** — with the adjudicator disabled or unavailable, flagged
  candidates stay provisional and pending rows accumulate in
  `tag_identity_event`. The triggered run
  (`kushim tagidentity --run <batch-id>`, enqueued by
  `POST /api/v1/tags/identity/reevaluate`) and the reconciler's identity sweep
  resolve them through the same cache; whichever runs first records the
  verdict.

---

## 3. The enricher flow

`internal/enrichment/enricher.go` + `internal/tags/admission/identity.go`:

1. **Suggestions** — the suggestion set (matched tags or the vocabulary
   fallback) is alias-resolved to canonical names before injection.
2. `e.runner.RankTags(ctx, docId, analysis.Tags)` — timeout wrapper in
   `internal/tools/runner.go`; on error the enricher logs, keeps the
   unconsolidated names, and falls back to the deterministic planner
   (`PlanRegistration`) for registration (enrichment never fails on
   consolidation).
3. **Planning** (`Adjudicator.PlanIdentity`) runs outside the transaction: one alias-map
   query, M1 house form, M3 key-equality application, M2 shape flagging through
   a `NameIndex`, band subjects from the ranked decisions, and same-emission
   shape pairs. Flagged candidates go into one batched identity-judge call
   (cache first: `tag_identity_event` at `IdentityRuleSetVersion`); flagged
   pairs go through the pair adjudicator. The result is a `tags.Plan` carrying
   registrations, alias actions, and evidence rows, and per-disposition log
   lines (`identity[v0]: apply|alias|defer|separate …`).
4. **Application** (`CreatePlannedTx`) runs inside the caller's persist
   transaction: registrations (write-boundary normalization, blocklist
   re-check, provisional births, promotion when not quarantined), scheduled
   alias actions (re-point, delete, flatten, alias row), and identity-evidence
   rows. `RenameTagDetails` keeps the analyzer's typed metadata attached to
   folded or applied outputs.
5. **Store hygiene** after the commit: promoted/new names enter the embedding
   store and removed alias names leave it.

A trusted birth is immediately part of the store, and later emissions of the
same name hit M3 or the alias resolver — the self-healing loop.

When `enricher.tag_quality.enabled` is true, `Adjudicator.ApplyTagQuality`
(`enricher.go`) filters that kept set once more before persist: novel names
(absent from the tag table) pass the deterministic weak band, resolve from the
`tag_quality_event` cache, or go into one rubric call per document; rejected
names are dropped for that document only. Survivors are born **provisional**
and are promoted inline once a cached passing verdict and assignments on ≥2
distinct live documents coincide; only promoted names enter the store. The judge's
prompt, criteria, and cache semantics are documented in the
[semantic matching guide](semantic-matching.md#rubric-judge-for-novel-tags),
and the promotion rule — including the review queue and the reconciliation
sweep that resolve the names the inline path cannot — in its
[promotion section](semantic-matching.md#promotion-out-of-quarantine).

---

## 4. LLM adjudication

Configuration lives in `enricher.tag_adjudicator.{enabled, timeout, llm}`
(`TagAdjudicatorConfig`, `config.go:248`). `enabled` defaults to `false` — an
upgrade never silently starts an extra LLM call. Inheritance is resolved **at
use time**, not at config finalize: `Config.ResolvedAdjudicatorLlm()`
(`config.go:272`) and `Config.ResolvedAdjudicatorTimeout()` (`config.go:279`)
fall back to the content analyzer's `llm`/`timeout` when the block's own values
are empty, so the inherit state survives settings saves and analyzer model
changes keep propagating.

The adjudicator gate covers two calls, both bounded by the adjudicator timeout
and both temperature 0 with structured output where the provider supports it:

- **Identity judge** — `Runner.JudgeTagIdentity` →
  `ContentAnalyzer.JudgeTagIdentity` on both LLM adapters. One call per batch of
  flagged candidates answers the three binary criteria; the prompt is built by
  `BuildTagIdentityPrompt` and the response parsed by
  `ParseTagIdentityResponse`, which requires a complete verdict per candidate —
  an omitted criterion errors rather than reading as an all-false rejection.
  The evidence payload (`CriteriaRecord`) carries the criteria plus the fold the
  mechanism proposed.
- **Pair adjudication** — `Runner.AdjudicateTags` →
  `ContentAnalyzer.Adjudicate`. The prompt (`BuildAdjudicationPrompt`) carries
  the same-intent test, the pair terms, the pair's embedding similarity, the
  query-side runner-up, and the target's nearest other store tag; for a `same`
  verdict it also asks which term is the precise one (`direction`:
  `query`/`target`/`tie`). `ParseAdjudicationResponse` maps unknown verdicts,
  out-of-range indices, and short slices to ambiguous entries and drops unknown
  direction values. See the [LLM guide](llm.md) for the adapter contract.

Resolution order per document:

1. **Identity criteria cache** — the latest verdict per
   `(kind, candidate, partner)` in `tag_identity_event` at
   `IdentityRuleSetVersion` resolves the three criteria without a call; only
   candidates without a complete cached set enter the judge batch.
2. **Pair cache** — the latest `same_intent` row per `(candidate, partner)`
   resolves the pair, with the direction carried in its criteria payload; only
   unresolved pairs enter the adjudicator batch.
3. **Evidence** — one batched target-as-query rank (a rank failure drops the
   evidence, not the pair).
4. **Verdict** — batched `AdjudicateTags` calls for all unresolved pairs. Any
   error, empty result, or ambiguous entry leaves those pairs pending: the
   candidates register provisionally with a pending `same_intent` row, resolved
   later by the triggered run or the reconciler's identity sweep. A disabled or
   failed-to-construct adjudicator skips both calls entirely — candidates
   register provisionally, folds are not applied, and every claim waits as
   pending evidence.

Decided pairs are recorded in the identity table (criteria + direction + model)
and, when the emission carries a document, also as a document-anchored
`tag_verdict_event` row so the reconciler's pair view stays complete.

---

## 5. The verdict and evidence tables

Migration `00017_tag_verdict_events.sql` creates the `tag_verdict` enum
(`same`/`variant`/`related`, wired into `internal/types` with a sqlc
`db_type` override) and the append-only `tag_verdict_event` table:

```
(document_id, query, target, sim, verdict, verdict_model, policy_version, verdict_at)
```

Indexes: `idx_tag_verdict_event_pair` (latest-verdict-per-pair reads),
`idx_tag_verdict_event_stale` (policy-version staleness scans), and the partial
unique `idx_tag_verdict_event_fresh_pair` on `(query, target, policy_version)
WHERE verdict_model IS NOT NULL` — concurrent workers (including separate
`kushim consume` processes) deduplicate fresh verdicts per pair/version with no
in-flight coordination: `InsertTagVerdictEvent` is `:execrows` with `ON CONFLICT
DO NOTHING`, and a losing 0-row insert re-reads the winning verdict and applies
it as cache-applied. Fresh rows carry the judging model in `verdict_model`;
cache-applied rows store NULL.

The table is deliberately a shared schema object, not a KV cache:
`TagPairEvidenceStats` aggregates per-pair population overlap and verdict
counts across both orientations and all policy versions (it feeds merge
suggestions and the reconciliation drift scan), and the reconciler's worklist
scans (§8) all query it. Query reference: [database reference](../reference/database.md).

Migration `00029_tag_identity.sql` adds the two identity objects:

- **`tag_alias`** — `(alias_name UNIQUE, canonical_tag_id REFERENCES tag(id) ON
  DELETE CASCADE, rule_set_version, created_at)`. The canonical-only rule is
  structural: alias rows never enter the embedding store, so a losing name can
  never be suggested or become a consolidation target. Alias resolution reads
  through one joined query (`ResolveTagAliasNames`) that returns the canonical
  name directly.
- **`tag_identity_event`** — `(kind, candidate, partner, criteria JSONB NOT
  NULL, verdict, model, rule_set_version, created_at)` with kinds `base_form`,
  `fold_gate`, `conventional_form` (candidate criteria, partner NULL) and
  `same_intent` (pair rows; `direction` lives in the criteria payload). The
  table serves both the identity verdict cache (latest row per
  `(kind, candidate, partner)` at the current rule-set version with a non-NULL
  verdict) and the pending-evidence queue (rows with NULL verdict, deduplicated
  by a partial unique index over `(kind, candidate, COALESCE(partner,''),
  rule_set_version)`; a second partial index on `created_at WHERE verdict IS
  NULL` serves the pending backlog scan and count). Verdicts settle in place
  through `ResolveTagIdentityEvent`, which only touches rows that are still
  pending, so the triggered run and the sweep cannot overwrite each other.

---

## 6. Matcher RPC surface and the pair cache

The daemon (`internal/ops/cli/hugot.go`) serves:

| Endpoint | Purpose |
| -------- | ------- |
| `POST /rpc/v1/rank` | Per query: `{kept_name, candidates: [{tag, similarity}]}` — top-2 candidates (`rankCandidates`, `hugot.go:115`), all ≥ the consideration floor. The daemon makes no replacement decision. |
| `GET /rpc/v1/similar-pairs` | The full pair cache — every unordered pair of store entries ≥ the floor. No legacy fallback: an old daemon 404s → matcher-unavailable → suggestions 503 until the daemon restarts. |
| `POST /rpc/v1/consolidate` | Legacy endpoint (`Hugot.Consolidate`, `hugot.go:299`) kept for pre-upgrade clients only. |

`MatcherClient.Rank` (`internal/matcher/client.go:165`) prefers `/rpc/v1/rank`
and falls back to the legacy endpoint on 404 via `rankViaConsolidate`
(`client.go:184`), wrapping each returned name as a no-candidates
`RankResult` — which the policy passes through verbatim. Both skew directions
therefore keep working without lock-step restarts; only `similar-pairs`
requires the daemon restart.

The **pair cache** (`Hugot.pairs`, guarded by a mutex because the HTTP handlers
run concurrently with store mutations) mirrors the embedding store exactly:
`SetStore` rebuilds it with a full N²/2 cosine pass at boot (after
`BuildTagCache`; sub-second to a few seconds at realistic tag scale, zero
encoding), `AddToStore` adds the new name's pairs with a 1×N pass,
`RemoveFromStore` drops every pair mentioning the removed name — tag delete and
rename both flow through it, so renames stay correct. With
`consolidationSim == 0.0` (consolidation disabled) the cache stays empty,
matching `Rank`'s early-return. `Hugot.SimilarPairs` (`hugot.go:232`) snapshots
the map; `MatcherClient.SimilarPairs` (`client.go:213`) GETs the endpoint.

---

## 7. Merge review

`TagMerge` (`internal/tags/curation/tagmerge.go`) —
nothing merges automatically; this is the operator's explicit action.

**`Merge(ctx, sourceID, targetID)`** runs one transaction:

1. `GetTagForUpdate` on both tag rows, `FOR UPDATE` in ascending id order —
   an unlocked concurrent delete of the target would cascade away the rows
   just re-pointed (`document_tag.tag_id` is `ON DELETE CASCADE`), and the
   id-ordered lock makes opposite-direction concurrent merges deadlock-free.
2. `ReassignDocumentTags` re-points the source's `document_tag` rows to the
   target, skipping documents that already carry the target (`NOT EXISTS`
   guard).
3. `DeleteDocumentTagsForTag` clears the skipped leftovers; `DeleteTag` removes
   the source tag.
4. Post-commit, `RemoveFromStore` drops the source name from the embedding
   store (log-only failure, same contract as tag delete). The target's
   embedding is untouched.

Event-table rows mentioning the merged source are left as history — the
reconciler's dead-name pass (§8) is what prevents a cached verdict from
resurrecting it. Errors: same id → invalid (400), missing source/target →
not-found (404).

**`Suggestions(ctx, SuggestionsParams{Limit, ForTag, IncludeDismissed})`**: one
`SimilarPairs` RPC → `ListAllTagsWithDocumentCount` (one query for the canonical
tag set and document counts) → drop pairs whose names are no longer in the tag
table → `ListTagMergeDismissals` into a set keyed by `pairKey` (canonical
normalized names) → `TagPairEvidenceStats` over the survivors → shape, canonical
two-tag ordering (higher document count first, tie lexicographic — a display
default, not a merge decision), confidence `"population"` when the pair has
event rows else `"similarity_only"`, `Dismissed` from the set, sort
population-first, then keep up to `Limit` entries per class (live vs dismissed)
so dismissed pairs cannot displace live suggestions; `ForTag` (normalized)
narrows the scan to pairs involving one tag for the vocabulary row expansion.
The endpoint 503s when the matcher is down (`isMatcherUnavailable`), surfaced as
a distinct state in the UI ("Merge review" on the tags page).

**`DismissPair(ctx, tagA, tagB)` / `RestorePair(ctx, tagA, tagB)`** touch only
the database, never the matcher: both normalize the names, reject empty or
identical inputs (invalid → 400) and sort them into canonical order (the pair
identity is the sorted normalized names, not the display order), then dismiss
(both tags must exist — `GetTagByName` → 404 otherwise — then upsert with
`ON CONFLICT DO NOTHING`) or restore (`DeleteTagMergeDismissal`, a no-op for an
absent row). Dismissals are global and are never pruned, so they survive tag
delete/recreate cycles.

---

## 8. Scheduled reconciliation

`internal/tags/reconcile` + the task plumbing. Migration
`00018_tag_reconcile_vocabulary.sql` adds `tagreconcile` to the `task_type` and
`batch_source` enums; the `internal/types` registry declares it **blocking**
(acquires the maintenance lock at claim time, drains in-flight tasks, and
heartbeats the lock during the run), and it joins `nonConsumeBatchSources` so
its batches never occupy a consume slot.

**Scheduling** — `maybeScheduleTagReconcile` (`internal/ops/queue/queue.go`)
runs on the queue daemon's housekeeping ticker (thumbnail-backfill precedent):
the daemon starts a 1-worker/60s pool for the source; `lastRun` is anchored on
the most recent `tagreconcile` batch's `created_at`; a run is skipped while one
is queued/processing or the maintenance lock is held. With no prior batch,
`FirstRunAnchor` (`internal/backup/scheduler.go`) anchors to the *next*
occurrence of the preferred time minus the interval, so a freshly-enabled
schedule lands its first run at the next preferred time (not a full interval
out); later runs count the interval from the previous run. The daemon keeps
per-schedulable in-memory state (`scheduleState` in `internal/ops/queue/queue.go`)
recording the timing the last run anchored under: an edited interval or
preferred time re-phases the next run to the closest upcoming occurrence of
the new timing (in-memory only — a daemon restart between the edit and the
re-phased run falls back to the steady rule), and a reverted edit before the
run restores the steady rule. The handler
(`internal/tags/reconcile/tagreconcile.go`) owns the batch lifecycle
(`MarkBatchProcessing`/`FinalizeBatchStatus`), dedups on
`tagreconcile:<UTC date>`, and reports a JSON `Report` — worklist counts per
category, judged/confirmed, flips by direction, merges with reassignment
counts, re-tags, invalidations, skip reasons — as the task result. The report
also carries pair-level entries (`planned_merges`, `planned_keep_flips`,
`retag_entries`, `invalidated_entries`) plus `capped`, `no_documents`,
`swept` (orphan tags deleted, or the would-be count in report-only runs),
`provisional` (the provisional sweep's `worklist`, `zero_assignment`,
`promoted`, `rejudged`, `deferred`, `no_judge`, `skipped`, `failed`),
`identity` (the identity sweep's `pending`, `resolved`, `folded`, `aliased`,
`deferred`, `failed`), and `adjudicator_disabled`; report-only keep-flip
document counts come from a
single batched `TagPairEvidenceStats` call (a stats failure keeps every
planned entry with zero documents instead of dropping it), and no-document
keep flips are counted in `no_documents` so the judged/skips/capped partition
stays exact. Each pair entry carries `reason` — `adjudicated` for LLM-judged
pairs — and `creates_tag` (true when a keep
flip creates the query tag; always present in new reports, absent only in
reports persisted before the field existed).
The report is served by `GET /api/v1/tags/reconciliation/report` (latest
completed run, viewer-gated, Postgres-only) and the `result` field on
`GET /api/v1/tasks/{id}` for `tagreconcile` tasks (list endpoints omit it);
the web UI renders it on the task detail page and as a status strip on the
tags page.

**Worklist** (from what the table persists, plus logs):

- **Dead-name pass** — `TagVerdictPairsWithMissingTags` classifies pairs with
  a missing side: dead-target pairs have their fresh verdict rows deleted
  (`DeleteFreshTagVerdictsForPairs`, so a cached `same` can never re-create a
  deleted target); dead-query pairs whose latest verdict is not `same` are
  re-adjudicated (a `same` latest means stable absorption — skipped).
- **Stale/unjudged** — `TagVerdictPairsWithoutFreshVerdict`: no fresh row at
  the current `policy.Version` (older version → stale; current version with
  NULL `verdict_model` → unjudged).
- **Aged** — `TagVerdictPairsOlderThan`, fresh rows older than 90 days.
- **Drifted** — fresh `variant`/`related` pairs whose document-population
  overlap grew (≥ 5 documents and ≥ 0.5 × min(docCounts)), via
  `TagVerdictPairsByVerdict` + `TagPairEvidenceStats`.
- **Log bootstrap** (`bootstrap.go`) — pairs absent from the table entirely,
  parsed from `hugot.log*`/`kushim.log*` rotations (timestamped siblings,
  `.gz` decompressed, chronological). Four line shapes are recognized: the
  pre-rewrite `hugot: consolidate "q" → t (sim)`, the unversioned
  `consolidate "q" → t (sim)`, `consolidation[vN]: keep "q" (sim=…
  reason=ambiguous_band …)` — any version prefix — and the band-less
  `consolidation[vN]: keep "q" (reason=rank_unavailable)` emitted when the
  post-LLM rank call fails (  parsed with an empty target and zero similarity,
  so the entry flows through the normal re-adjudication path). Legacy
  guard-reason keeps and `auto_replace` replaces from pre-identity logs are not
  parsed — they were decided by rules that no longer exist; REQIDs resolve to
  document ids via
  `GetDocumentIDsByDocumentIDs`, unresolvable ones are dropped but the pair is
  kept; table rows supersede log-derived entries.

Entries are ordered stale → unjudged → drifted → aged, then oldest verdict,
and capped by `max_rejudge`.

**Re-adjudication is policy replay**: each entry gets a fresh batched
`RankTags` (256 names per batch, synthetic docId `tagreconcile`) plus one
target-as-query rank pass for reciprocal evidence. Entries whose query is a
store tag again (born since) or whose stored target fell below the
consideration floor are dropped — future emissions will resolve them. The rest
are evaluated through `policy.Evaluate` on the current store with the
stored target at its fresh similarity: `identity` where the query equals the
stored target, otherwise `ambiguous_band` → LLM adjudication (batches of 50).
A fresh verdict that differs from the stored one is confirmed by a second,
**orientation-reversed** adjudication pass — the flip applies only when both
orientations agree; ambiguity or disagreement keeps the stored verdict.

**Flips and caps**: a merge flip runs the full `TagMerge.Merge` (query tag
deleted, all assignments re-pointed). A keep flip resolves the pair's affected
documents (`DocumentIDsForPairs`, event-derived or REQID-derived doc ids)
*before* touching the store: when the pair has no resolvable documents the
entry is only marked judged (`upsertVerdict`) and no tag is created, so an
unattributed pair cannot add a name to the vocabulary. Otherwise the keep flip
`Tag.Create`s the query tag (Created or Conflict both fine), re-points only
those documents (`RepointDocumentTagsForDocs`, with the `NOT EXISTS` guard),
clears their orphan marks, and upserts the verdict — all in one transaction.
Every adjudicated pair (flip or agreement) upserts a fresh verdict row at the
current version (`UpsertFreshTagVerdict`, `verdict_model` = the adjudicator
model; empty when a cached verdict was applied), refreshing `verdict_at` so the aged scan does
not re-judge it. `max_retag` bounds re-pointed documents per run, with a
first-pair carve-out (the budget is soft by at most one pair).

**Blocklist enforcement**: a query whose name is in the `deleted_tag`
blocklist is skipped before the create — the flip is not applied and the skip
is logged, but the pair still gets its fresh verdict so it is not re-judged.
Blocked names are dropped by enrichment; merged names are rewritten to their
merge target there. Only the operator restore path (and re-creating the tag
through the operator create endpoint) clears a blocklist row.

**Orphan-tag sweep**: each run maintains `tag.orphaned_since` — marks are
cleared for tags that currently hold documents (`ClearOrphanTagMarks`), then
set for document-less tags without one (`MarkOrphanTags`) — and deletes (plus
removes from the embedding store) tags whose mark is older than
`enricher.tag_reconcile.dead_tag_grace_days` (default 7). Every assignment
path clears the assigned tags' marks (`ClearOrphanTagMarksByTagIDs`) so the
grace window reflects real last usage rather than the last reconcile
observation. The list/delete queries re-check `NOT EXISTS (document_tag)`, so
a tag re-assigned mid-run is never deleted. Report-only runs refresh the marks
inside a transaction that is always rolled back, so the reported `swept` count
predicts what the next apply would delete.

**Provisional sweep**: after the orphan sweep, the same run walks provisional
tags older than `enricher.tag_reconcile.provisional_grace_days` (default 30),
oldest first. Zero-assignment rows are only counted — the orphan sweep owns
their deletion. Rows whose latest verdict at `TagQualityVersion` passes and
that hold ≥2 live documents are promoted through `curation.Tag.Promote` (the
same method behind `POST /api/v1/tags/{id}/promote`, so the log line and the
store add match the operator path). Verdict-less rows are re-judged under the
shared `max_rejudge` budget: `FirstDocumentIDForTag` picks the first live
carrier, `GetDocumentTextForTagQuality` fetches its text and word count, and
`Runner.ReduceContent` reduces it with `matcher.TargetWordCount` (the
tagreconcile runner loads the `textreducer` tool for this path). One
`JudgeTagQuality` call judges the single candidate — category `other` and the
whitelisted-place flag recomputed from the name — and a failure retries once
unless the error carries `context.Canceled`/`DeadlineExceeded`. The verdict is
written with `InsertTagQualityEvent`, the enricher's `DO NOTHING` conflict
rule: a concurrent fresh verdict is kept and its `pass`/model drive the
decision instead of being overwritten. A fresh pass promotes at ≥2 live
documents, a reject is deferred to the review queue, a tag with no live
document text is skipped, a missing judge is reported as `no_judge`, and
lookup or write failures as `failed`; the run never deletes a tag. Report-only
runs count would-promote/would-re-judge and touch nothing. The counts land in
`Report.Provisional` (`worklist`, `zero_assignment`, `promoted`, `rejudged`,
`deferred`, `no_judge`, `skipped`, `failed`).

**Identity-evidence sweep**: after the provisional sweep, the same run resolves
pending identity evidence up to `max_rejudge` rows through the shared resolver
(`internal/tags/admission/identity_resolve.go`). Candidate criteria are judged in
batches, same-intent pairs adjudicated, and the dispositions applied: a
confirmed fold renames the tag (or aliases it when the folded name already
exists), a same-intent loser becomes an alias of its winner, a deleted fold
target is skipped, and a deferral stays for review. The per-pair cache settles
each claim once, so the triggered run and this sweep converge on whichever
runs first. Report-only runs skip the sweep. The counts land in
`Report.Identity` (`pending`, `resolved`, `folded`, `aliased`, `deferred`,
`failed`); with the adjudicator disabled the sweep only counts the backlog.

**Report-only**: the first run is always report-only
(`GetLastCompletedTagReconcileTask` finds no completed prior run), regardless
of the flag — it executes the full evidence/evaluation path and logs every
would-be action without applying anything. With the adjudicator disabled,
runs log the worklist snapshot and complete without LLM calls or writes; the
daemon warns when reconciliation is enabled without the adjudicator.

---

## 9. Tag extension measurement

The measurement pass (`internal/tags`) is the substrate behind every "how much
do these two names overlap" question in this subsystem. It also retired the
duplicated SQL those questions used to run on: the enricher's promotion gate,
the reconciler's live counts, and the merge-review evidence all read through
`tags.Store` now (`LiveMass` for live per-tag document counts,
`PairVerdictStats` for the adjudication aggregates).

- **The algebra** (`internal/tags/algebra.go`) is pure: mass, pairwise overlap
  (`|S(a) ∩ S(b)|` with Jaccard, overlap-coefficient, and both containment
  ratios), the `≈` set-similarity number, reconstructibility with excess
  `E = |S(c)| − |S(c) ∩ ⋂S(p)|` and deficit `D = |⋂S(p)| − |S(c) ∩ ⋂S(p)|`,
  part coverage and productivity, and complement views. No thresholds live in
  the operators; cuts belong to consumers.
- **The extension reader** takes a pluggable alias resolver (identity today),
  so canonical-label folding can land without touching the operators.
- **The pass** (`internal/tags/measure.go`) reads one REPEATABLE READ snapshot —
  every tag's live mass, every co-occurring pair's intersection, and each
  stored two-to-three-word compound's counts against its mechanically derived
  parts (articles, conjunctions, and prepositions stripped; content modifiers
  kept) — and appends the readings to the append-only `tag_measurement` table
  in the same transaction. Records carry the run id (one per pass), the
  snapshot instant, the rule-set version, the operator, the operands, the raw
  numbers, an evidence state (`sufficient`/`insufficient` — empty extensions
  and empty intersections support no reading), and caveats.
- **Rule-set v0** derives the composition-review worklists from the persisted
  records: `≈` twin candidates (Jaccard ≥ 0.9 with both masses ≥ 2, gray band
  0.7–0.9), compound split proposals (both leftovers ≤ 0.1, gray to 0.25), and
  near-contained siblings (containment ≥ 0.8, gray 0.6–0.8). The worklists are
  evidence for review; nothing acts on them.
- **Running it**: `enricher.tag_measure.{enabled, interval, time}` schedules a
  `tagmeasure` task (disabled by default), and `kushim tagmeasure` runs the
  pass in the foreground under the maintenance lock, while
  `kushim tagmeasure --run <batch-id>` is the scheduled fork's execution mode.
  The pass holds the lock for its whole transaction, so runs are
  minutes-scale and serialize with every other blocking task.

The pass measures; it never judges. Dispositions — promote, merge, split,
delete — stay with the reconciler's worklists and the operator surfaces.

---

## 10. Safety properties

- **Keep is the failure direction everywhere**: rank error, adjudicator
  disabled/unavailable/ambiguous, reconciliation disagreement — the emitted or
  stored state is kept, unresolved claims stay pending, and enrichment never
  blocks or fails on this subsystem.
- **Wrong keeps are visible and mergeable; wrong replacements are silent and
  permanent** — hence the asymmetric strictness: similarity never decides. The
  only paths that change a name are M1 style rewrites, M3 key equality, a fold
  confirmed by all three criteria, an explicit `same` verdict (with direction),
  and an operator's merge.
- **Merges are explicit operator actions**; the automatic path uses the alias
  disposition instead, so a losing name stays resolvable through `tag_alias`
  rather than being deleted outright.
- **Bounded automation** — policy-version and rule-set-version pinning, the
  double-orientation confirmation, `max_rejudge`/`max_retag`, and the
  report-only first run.
- **Dead names cannot resurrect** — the registration path re-checks the
  `deleted_tag` blocklist (a blocked name is never registered and never
  aliased), fold application skips deleted targets, dead-target verdict rows
  are deleted, and every operator delete/merge is recorded in the blocklist:
  enrichment drops blocked names (and rewrites merged names to their merge
  target), and the reconciler skips their restoration.

---

## 11. Gotchas

- **The adjudicator, quality judge, and reconciliation are disabled by
  default** — novel tags are born unfiltered, folds are not applied, and
  same-intent claims wait as pending evidence (their candidates register
  provisionally) until `enricher.tag_adjudicator.enabled` is set or the
  triggered run/sweep resolves them; both event tables stay empty until their
  layers run. Merge suggestions then run similarity-only (`similarity_only`
  confidence).
- **Quarantined tags are invisible to the vocabulary** — with `tag_quality`
  on, a newborn is attached and searchable from birth but stays out of the
  embedding store — and therefore out of suggestions, consolidation targets,
  and the all-names fallback — until it has a cached passing verdict and
  assignments on ≥2 distinct live documents. Verdict-less births (judge
  outages) and re-emitted legacy provisionals never get a verdict row in
  flight — a name in the tag table is not novel, so the judge never sees it
  again — and stay provisional until the reconciliation sweep's provisional
  phase or an operator in the review queue resolves them.
- **The first reconciliation run is always report-only**, even with
  `report_only: false` — flip behavior only starts with the second run.
- **`GET /rpc/v1/similar-pairs` has no legacy fallback** — after upgrading
  edub, restart `kushim hugot` or merge suggestions 503. `Rank` does fall back
  (legacy consolidate endpoint), so enrichment itself is skew-safe.
- **`consolidation_similarity: 0` disables the whole subsystem** — `Rank`
  returns no candidates, the pair cache stays empty, everything passes
  through. That is the documented knob, not a bug.
- **Verdicts are cache-scoped to `policy.Version`** — bumping the version
  invalidates every cached verdict at once (old rows stay in the table for
  analysis; they are simply never read for the new version). Until the
  reconciler runs, every band pair is re-judged fresh — the caps are the bound.
  Identity evidence carries an independent pin, `IdentityRuleSetVersion`, and
  quality verdicts another, `TagQualityVersion`; bumping either retires its
  cached rows at once.
- **The pair cache rebuild is N²/2 at daemon boot** — sub-second to a few
  seconds at hundreds-to-low-thousands of tags, but it is part of the
  `TimeoutStartSec` budget alongside model load and tag-cache build.
- **Consolidation and identity logs live in the kushim (enrichment) log, not
  `hugot.log`** — since the policy moved enricher-side (`consolidation[v1]`
  decisions, `identity[v0]` dispositions). `hugot.log` only holds the
  pre-rewrite history (which is exactly what the bootstrap parser reads).
- **Interface changes ripple** — `Matcher`/`Embedder` implementations include
  `*Hugot`, `*matcher.MatcherClient`, and the test doubles in
  `internal/testutil` and `internal/tools/runner_test.go`; keep them all
  consistent.
- **Similarity never decides** — a high-similarity neighbour is only a gate
  subject; a name changes only through M1/M3, a fully confirmed fold, an
  explicit `same` verdict, or an operator merge. Bands that keep without a
  verdict stay provisional with pending evidence.

---

*Last verified against the tree: 2026-09-22. If code and doc disagree, code wins.*
