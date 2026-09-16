# Developer Guide — Tag Consolidation Policy

This guide explains how the post-LLM tag-consolidation subsystem works
internally: the pure policy layer, the enricher flow that applies it, LLM
adjudication of the ambiguous band, the verdict event table, the matcher RPC
surface and pair cache, the merge-review service, and the scheduled
reconciler.

It is the internal-mechanics companion to `docs/tag-consolidation.md`
(operator-facing). Audience: developers who want to understand, tune, or change
how emitted tags are mapped onto the canonical vocabulary.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [The policy layer](#2-the-policy-layer)
3. [The enricher flow](#3-the-enricher-flow)
4. [LLM adjudication](#4-llm-adjudication)
5. [The verdict event table](#5-the-verdict-event-table)
6. [Matcher RPC surface and the pair cache](#6-matcher-rpc-surface-and-the-pair-cache)
7. [Merge review](#7-merge-review)
8. [Scheduled reconciliation](#8-scheduled-reconciliation)
9. [Safety properties](#9-safety-properties)
10. [Gotchas](#10-gotchas)

---

## 1. Orientation

Consolidation runs at the end of enrichment, after the LLM emits tags
(architecture pipeline step 5). The seam is deliberate: **the matcher daemon
only ranks** — it embeds the emitted names and returns candidates with scores —
and **the enricher owns the replacement decision**. That split lets the policy
(and everything built on it: adjudication, reconciliation) live in non-cgo
packages that run in both binaries, while the cgo-gated model stays in the
daemon.

The subsystem spans these packages:

| Package | Role |
| ------- | ---- |
| `internal/enrichment/tagpolicy` | pure decision layer: gates + bands, no logging, no config |
| `internal/enrichment` (`enricher.go`) | adapter: store set, decision logging, verdict writes, adjudication orchestration |
| `internal/enrichment/tagreconcile` | scheduled re-adjudication + log bootstrap parser |
| `internal/service/tagmerge.go` | merge operation + merge suggestions |
| `internal/tools/adapters/tagmatcher` | the daemon: `Rank`, pair cache, embedding store |
| `internal/tagmatch` | the RPC client (`edub`'s deployment) |
| `internal/database` | migrations 00017/00018, event-table queries |

Two deployments share the `tagmatcher.Matcher` interface: the in-process
`*Hugot` (kushim, cgo) and `*tagmatch.MatcherClient` (edub, Unix-socket RPC to
`kushim hugot`). Any interface change must update both plus the test doubles
(`internal/testutil`, `internal/tools/runner_test.go`).

Thresholds involved (both model-derived, `internal/config/config.go`):
`consolidation_similarity` is the **consideration floor** — the daemon's `Rank`
candidate filter (bge-m3 → 0.80); `auto_replace_similarity` is the
**auto-replace cutoff** consumed by the policy (bge-m3 → 0.95 = the
consolidation default + 0.15). Per-model values and the derivation switches are
documented in the [semantic matching guide](semantic-matching.md#10-thresholds-and-configuration).

---

## 2. The policy layer

`tagpolicy` (`internal/enrichment/tagpolicy/policy.go`) is a pure function of
its inputs — no logging, no config, no I/O, no cgo:

```go
func Evaluate(queries []string, ranked []tagmatcher.RankResult,
	storeTags map[string]struct{}, autoReplaceSim float64) []Decision
```

Each `Decision` records `Query`, `Output`, `Target`, `Sim`, `RunnerUpSim`,
`Action` (`Passthrough` / `Replace` / `Keep`) and `Reason` (`identity`,
`auto_replace`, `negation_guard`, `shared_token_guard`, `direction_guard`,
`ambiguous_band`, `no_candidates`, `short_slice`). `Version = "v1"`
(`policy.go:12`) is the policy version stamped on every decision log and stored
verdict — bumping it is what marks all stored verdicts stale to the reconciler.

Per query, `Evaluate` runs in order:

1. **Passthrough** — short slices (ranked shorter than queries) and results
   with no candidates (legacy-daemon fallback, disabled consolidation) pass
   through untouched; they are already decided.
2. **Identity fast path** — exact equality with a store tag (names are
   canonical on both sides, so this is string equality) → replace. This is
   ~87% of real traffic: casing, hyphenation, and symbol-form fixes.
3. **Negation guard** — exactly one of {query, target} carries a negation
   prefix: a standalone `anti`/`contra`/`non` token of the normalized name, or
   a closed compound (`antifascism`) whose stripped remainder exists as a store
   tag → keep. Two-sided detection blocks both flip directions without
   touching true synonyms (`birth control` → `contraception`).
4. **Shared-token guard** — *proper partial* token overlap (neither side a
   strict subset of the other) → keep. Deliberately blunt; blocked genuine
   synonyms become merge-review candidates.
5. **Direction guard** — query tokens a strict subset of the target's
   (`democracy` ⊂ `direct democracy`) → keep, so the broader term is born. The
   reverse direction (target ⊂ query, e.g. `social revolution` vs `revolution`)
   deliberately falls through to the bands — it is adjudication material, not a
   deterministic keep.
6. **Bands** — a guard-free candidate at or above `autoReplaceSim` → replace;
   otherwise `ambiguous_band` → keep (the enricher may adjudicate, §4).

The runner-up similarity is recorded on every decision and never affects it —
it exists as adjudication evidence.

---

## 3. The enricher flow

`internal/enrichment/enricher.go`:

1. `e.runner.RankTags(ctx, docId, analysis.Tags)` (`enricher.go:244`) — timeout
   wrapper in `internal/tools/runner.go:454`; on error the enricher logs and
   keeps the unconsolidated tags (enrichment never fails on consolidation).
2. `applyConsolidationPolicy` (`enricher.go:433`) — builds the canonical
   `storeTags` set from `services.Tag.ListAll`, evaluates via
   `tagpolicy.Evaluate`, and logs every decision as
   `consolidation[v1]: replace|keep|passthrough … reason=…` (replace/keep at
   info, passthrough at debug).
3. Band pairs (`Reason == ambiguous_band`) route to `adjudicateBandPairs`
   (`enricher.go:548`) when the adjudicator is enabled; `writeVerdictEvent`
   (`enricher.go:496`) records one row per band-pair occurrence that has a
   verdict.
4. Adjudication evidence uses a second, target-as-query `RankTags`
   (`enricher.go:640`) over the distinct unresolved targets.

Kept tags are then created via `services.Tag.Create` → `AddToStore`, so a born
tag is immediately part of the store and later emissions of the same name hit
the identity fast path — the self-healing loop.

When `enricher.tag_quality.enabled` is true, `applyTagQuality`
(`enricher.go`) filters that kept set once more before persist: novel names
(absent from the store) pass the deterministic weak band, resolve from the
`tag_quality_event` cache, or go into one rubric call per document; rejected
names are dropped for that document only. The judge's prompt, criteria, and
cache semantics are documented in the
[semantic matching guide](semantic-matching.md#rubric-judge-for-novel-tags).

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

The call chain: `Runner.AdjudicateTags` (bounded by the adjudicator timeout) →
`ContentAnalyzer.Adjudicate` on both LLM adapters (openai-compatible and
anthropic) — temperature 0, structured output where the provider supports it.
The prompt is built by `BuildAdjudicationPrompt` (pair terms, the pair's
embedding similarity, the query-side runner-up, the target's nearest other
store tag); `ParseAdjudicationResponse` maps unknown verdicts, out-of-range
indices, and short slices to ambiguous entries. See the
[LLM guide](llm.md) for the adapter contract.

Resolution order per document:

1. **Verdict cache** — `LatestTagVerdictsForPairs` reads the latest verdict per
   `(query, target)` at the current `tagpolicy.Version`. Cached pairs resolve
   with no LLM call: `same` replaces with the target, `variant`/`related` keep.
2. **Evidence** — one batched target-as-query rank (a rank failure drops the
   evidence, not the pair).
3. **Verdict** — one `AdjudicateTags` call for all of the document's
   unresolved pairs. Any error, empty result, or ambiguous entry keeps every
   unresolved pair and logs one error line. A disabled or failed-to-construct
   adjudicator skips the step entirely — Stage-1 behavior.

---

## 5. The verdict event table

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

---

## 6. Matcher RPC surface and the pair cache

The daemon (`internal/commands/hugot.go`) serves:

| Endpoint | Purpose |
| -------- | ------- |
| `POST /rpc/v1/rank` | Per query: `{kept_name, candidates: [{tag, similarity}]}` — top-2 candidates (`rankCandidates`, `hugot.go:115`), all ≥ the consideration floor. The daemon makes no replacement decision. |
| `GET /rpc/v1/similar-pairs` | The full pair cache — every unordered pair of store entries ≥ the floor. No legacy fallback: an old daemon 404s → matcher-unavailable → suggestions 503 until the daemon restarts. |
| `POST /rpc/v1/consolidate` | Legacy endpoint (`Hugot.Consolidate`, `hugot.go:299`) kept for pre-upgrade clients only. |

`MatcherClient.Rank` (`internal/tagmatch/client.go:165`) prefers `/rpc/v1/rank`
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

`TagMerge` (`internal/service/tagmerge.go`, registered on `CrudServices`) —
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

**`Suggestions(ctx, limit)`**: one `SimilarPairs` RPC →
`ListAllTagsWithDocumentCount` (one query for the canonical tag set and
document counts) → drop pairs whose names are no longer in the tag table →
`TagPairEvidenceStats` over the survivors → shape, canonical two-tag ordering
(higher document count first, tie lexicographic — a display default, not a
merge decision), confidence `"population"` when the pair has event rows else
`"similarity_only"`, sort population-first, limit. The endpoint 503s when the
matcher is down (`isMatcherUnavailable`), surfaced as a distinct state in the
UI ("Review merges" on the tags page).

---

## 8. Scheduled reconciliation

`internal/enrichment/tagreconcile` + the task plumbing. Migration
`00018_tag_reconcile_vocabulary.sql` adds `tagreconcile` to the `task_type` and
`batch_source` enums; the `internal/types` registry declares it **blocking**
(acquires the maintenance lock at claim time, drains in-flight tasks, and
heartbeats the lock during the run), and it joins `nonConsumeBatchSources` so
its batches never occupy a consume slot.

**Scheduling** — `maybeScheduleTagReconcile` (`internal/commands/queue.go:658`)
runs on the queue daemon's housekeeping ticker (thumbnail-backfill precedent):
the daemon starts a 1-worker/60s pool for the source; `lastRun` is anchored on
the most recent `tagreconcile` batch's `created_at`; a run is skipped while one
is queued/processing or the maintenance lock is held. With no prior batch,
`FirstRunAnchor` (`internal/backup/scheduler.go`) anchors to the *next*
occurrence of the preferred time minus the interval, so a freshly-enabled
schedule lands its first run at the next preferred time (not a full interval
out); later runs count the interval from the previous run. The daemon keeps
per-schedulable in-memory state (`scheduleState` in `internal/commands/queue.go`)
recording the timing the last run anchored under: an edited interval or
preferred time re-phases the next run to the closest upcoming occurrence of
the new timing (in-memory only — a daemon restart between the edit and the
re-phased run falls back to the steady rule), and a reverted edit before the
run restores the steady rule. The handler
(`internal/task/handlers/tagreconcile.go`) owns the batch lifecycle
(`MarkBatchProcessing`/`FinalizeBatchStatus`), dedups on
`tagreconcile:<UTC date>`, and reports a JSON `Report` — worklist counts per
category, judged/confirmed, flips by direction, merges with reassignment
counts, re-tags, invalidations, skip reasons — as the task result. The report
also carries pair-level entries (`planned_merges`, `planned_keep_flips`,
`retag_entries`, `invalidated_entries`) plus `capped`, `no_documents`,
`swept` (orphan tags deleted, or the would-be count in report-only runs), and
`adjudicator_disabled`; report-only keep-flip document counts come from a
single batched `TagPairEvidenceStats` call (a stats failure keeps every
planned entry with zero documents instead of dropping it), and no-document
keep flips are counted in `no_documents` so the judged/skips/capped partition
stays exact. Each pair entry carries `reason` — the policy guard that decided
it (`auto_replace`, `negation_guard`, `shared_token_guard`, `direction_guard`)
or `adjudicated` for LLM-judged pairs — and `creates_tag` (true when a keep
flip creates the query tag; always present in new reports, absent only in
reports persisted before the field existed).
The report is served by `GET /api/v1/tags/reconciliation/report` (latest
completed run, viewer-gated, Postgres-only) and the `result` field on
`GET /api/v1/tasks/{id}` for `tagreconcile` tasks (list endpoints omit it);
the web UI renders it on the task detail page and as a summary card on the
tags page.

**Worklist** (from what the table persists, plus logs):

- **Dead-name pass** — `TagVerdictPairsWithMissingTags` classifies pairs with
  a missing side: dead-target pairs have their fresh verdict rows deleted
  (`DeleteFreshTagVerdictsForPairs`, so a cached `same` can never re-create a
  deleted target); dead-query pairs whose latest verdict is not `same` are
  re-adjudicated (a `same` latest means stable absorption — skipped).
- **Stale/unjudged** — `TagVerdictPairsWithoutFreshVerdict`: no fresh row at
  the current `tagpolicy.Version` (older version → stale; current version with
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
  post-LLM rank call fails (parsed with an empty target and zero similarity,
  so the entry flows through the normal re-adjudication path). Guard-reason
  keeps and `auto_replace` replaces are not parsed (they were decided by rules
  that replay deterministically); REQIDs resolve to document ids via
  `GetDocumentIDsByDocumentIDs`, unresolvable ones are dropped but the pair is
  kept; table rows supersede log-derived entries.

Entries are ordered stale → unjudged → drifted → aged, then oldest verdict,
and capped by `max_rejudge`.

**Re-adjudication is policy replay**: each entry gets a fresh batched
`RankTags` (256 names per batch, synthetic docId `tagreconcile`) plus one
target-as-query rank pass for reciprocal evidence. Entries whose query is a
store tag again (born since) or whose stored target fell below the
consideration floor are dropped — future emissions will resolve them. The rest
are evaluated through `tagpolicy.Evaluate` on the current store with the
stored target at its fresh similarity: `auto_replace` → merge flip, guard
reasons → keep flip, `ambiguous_band` → LLM adjudication (batches of 50). A
fresh verdict that differs from the stored one is confirmed by a second,
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
model or `policy:<version>`), refreshing `verdict_at` so the aged scan does
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

**Report-only**: the first run is always report-only
(`GetLastCompletedTagReconcileTask` finds no completed prior run), regardless
of the flag — it executes the full evidence/evaluation path and logs every
would-be action without applying anything. With the adjudicator disabled,
runs log the worklist snapshot and complete without LLM calls or writes; the
daemon warns when reconciliation is enabled without the adjudicator.

---

## 9. Safety properties

- **Keep is the failure direction everywhere**: rank error, adjudicator
  disabled/unavailable/ambiguous, reconciliation disagreement — the emitted or
  stored state is kept, and enrichment never blocks or fails on this
  subsystem.
- **Wrong keeps are visible and mergeable; wrong replacements are silent and
  permanent** — hence the asymmetric strictness: deterministic guards only
  *block*; the only paths to replacement are identity, the auto-replace
  cutoff, an explicit `same` verdict, or an operator's merge.
- **Nothing merges automatically** — merges are explicit operator actions;
  automatic flips only re-tag documents.
- **Bounded automation** — policy-version pinning, the double-orientation
  confirmation, `max_rejudge`/`max_retag`, and the report-only first run.
- **Dead names cannot resurrect** — dead-target verdict rows are deleted and
  every operator delete/merge is recorded in the `deleted_tag` blocklist:
  enrichment drops blocked names (and rewrites merged names to their merge
  target), and the reconciler skips their restoration, so no cached `same`
  verdict or later LLM emission can re-create a merged/deleted tag.

---

## 10. Gotchas

- **The adjudicator, quality judge, and reconciliation are disabled by
  default** — the ambiguous band keeps, novel tags are born unfiltered, and
  both event tables stay empty until `enricher.tag_adjudicator.enabled`,
  `enricher.tag_quality.enabled`, and `enricher.tag_reconcile.enabled` are
  set. Merge suggestions then run similarity-only (`similarity_only`
  confidence).
- **The first reconciliation run is always report-only**, even with
  `report_only: false` — flip behavior only starts with the second run.
- **`GET /rpc/v1/similar-pairs` has no legacy fallback** — after upgrading
  edub, restart `kushim hugot` or merge suggestions 503. `Rank` does fall back
  (legacy consolidate endpoint), so enrichment itself is skew-safe.
- **`consolidation_similarity: 0` disables the whole subsystem** — `Rank`
  returns no candidates, the pair cache stays empty, everything passes
  through. That is the documented knob, not a bug.
- **Verdicts are cache-scoped to `tagpolicy.Version`** — bumping the version
  invalidates every cached verdict at once (old rows stay in the table for
  analysis; they are simply never read for the new version). Until the
  reconciler runs, every band pair is re-judged fresh — the caps are the bound.
  Quality verdicts carry an independent pin, `TagQualityVersion` — bumping it
  retires every cached quality verdict at once.
- **The pair cache rebuild is N²/2 at daemon boot** — sub-second to a few
  seconds at hundreds-to-low-thousands of tags, but it is part of the
  `TimeoutStartSec` budget alongside model load and tag-cache build.
- **Consolidation decision logs live in the kushim (enrichment) log, not
  `hugot.log`** — since the policy moved enricher-side. `hugot.log` only holds
  the pre-rewrite history (which is exactly what the bootstrap parser reads).
- **Interface changes ripple** — `Matcher`/`Embedder` implementations include
  `*Hugot`, `*tagmatch.MatcherClient`, and the test doubles in
  `internal/testutil` and `internal/tools/runner_test.go`; keep them all
  consistent.
- **The shared-token guard is deliberately blunt** — genuine synonyms that
  share a token (`software development` / `software engineering`) are kept and
  become merge-review candidates, not replacements. That is the design, not a
  miss.

---

*Last verified against the tree: 2026-09-12. If code and doc disagree, code wins.*
