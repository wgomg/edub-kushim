# Developer Guide — Tag Consolidation and Identity

This guide explains how the post-LLM tag-consolidation subsystem works
internally: the birth-time identity machinery, the pure candidate-generation
policy, the enricher flow that applies it, LLM adjudication of the same-intent
gate, the verdict and identity-evidence tables, the matcher RPC surface, the
alias disposition, and the scheduled reconciler.

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
6. [Matcher RPC surface](#6-matcher-rpc-surface)
7. [Alias disposition](#7-alias-disposition)
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
| `internal/tags/reconcile` | scheduled re-adjudication + log bootstrap parser |
| `internal/tags/curation` | tag vocabulary services: tag CRUD, alias disposition, identity-run enqueue |
| `internal/matcher` | the daemon (`Rank`, embedding store) and the RPC client (`edub`'s deployment) |
| `internal/database` | migrations 00017/00018/00029, verdict + evidence queries |

Two deployments share the `matcher.Matcher` interface: the in-process
`*Hugot` (kushim, cgo) and `*matcher.MatcherClient` (edub, Unix-socket RPC to
`kushim hugot`). Any interface change must update both plus the test doubles
(`internal/testutil`, `internal/tools/runner_test.go`).

Thresholds involved (`internal/config/config.go`): `identity_candidate_similarity`
is the **identity-candidate floor** — the daemon's `Rank` candidate filter
(bge-m3 → 0.80), and the only stored neighbours the identity gate considers. No
similarity cutoff replaces or keeps a name on its own: identity resolution and
the same-intent gate do. The field is derived per model and serialized in config
responses. Per-model values and the derivation switches are documented in the
[semantic matching guide](semantic-matching.md#10-thresholds-and-configuration).

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
- **M2 — shape signatures and fold proposals** (`ComputeSignature`,
  `ProposedFold`, `ProposedDerivationalFold`, `ProposedMorphologicalFold`):
  per-token lemma (golem) and stem (snowball) signatures, the plural/lemma fold
  proposal, the store-anchored derivational transforms that reduce relational
  adjectives onto a noun the store already holds (`chilean` → `chile`,
  `political` → `politics`), and the bounded morphographic proposal that
  reaches a single-token store value within two edits (`mexican` → `mexico`;
  two distinct store values in reach make the candidate stand). They are
  evidence only — the stemmer's output is never written, and a proposed fold is
  never applied unconfirmed.
- **M3 — key equality** (`Key`): the identity key is the house form, so style
  variants collapse without judging.
- **M4 — tie-break** (`TieBreak`): fixed lexical order decides when the
  direction criteria do not separate two labels.
- **M8 — deterministic variant classes** (`VariantClass`): two names are one
  value without judging when they are pinned orthographic spelling variants
  (`-isation`/`-ization`, `-yse`/`-yze`, `-ogue`/`-og`, applied per token) or
  literal token permutations (the same tokens in a different order). The pair
  resolves `same` with no direction, so M4 picks the canonical label. Plural and
  other inflections, mixed-language spellings, and morphological or function-word
  inversions stay with the pair judge.
- **Version**: `IdentityRuleSetVersion = "v10"` is stamped on every alias row and
  identity-evidence row. It pins the mechanical fold proposals — the
  plural/lemma fold, the store-anchored derivational adjective transforms, and the
  bounded morphographic proposal — the store-anchored rejection hold, the
  pair judge's criteria text (with the similarity context dropped, so the two terms are
  the gate's only evidence), the deterministic variant-class table (including the
  `-our`/`-or` spelling pair), the birth-time canonical direction (sense-fit, then
  the house form/language, then the M4 tie-break), and the
  split-seam fold source (promoted-only anchors over the generated attested
  pertainym table in sense order, folding a multi-base part only onto its
  primary-sense base and only while that base is live — the residue goes to the
  phrase-context fold judge — and also folding a pinned variant class onto a
  promoted label; the shape-based legs
  are not the seam's rule, where the judged path's proposals remain for the
  gate to confirm).
- **`PlanDeterministic`** — the admin path's deterministic phase: normalization,
  M1 rewrites, alias lookup, key equality, and M2 flagging, including the
  plural/lemma, derivational, and morphographic fold proposals. It never applies
  a fold, never adjudicates, and never makes an alias; flagged shape relations
  and proposed folds register provisionally with pending evidence. `NameIndex`
  memoizes keys and signatures per name so the flagging loops compare cached
  values.
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
   (`ambiguous_band`): the query keeps its name and the top candidate is
   recorded with its similarity. Similarity ranks and filters candidates; it
   never replaces or keeps on its own.

The judge sees only the two terms of a pair; the similarity that selected the
pair is never part of the prompt.

### 2.3 The gate and its dispositions

Flagged candidates and band subjects are resolved before the persist
transaction — adjudication stays outside it:

- **Folds (J1/J2/J5)** — one identity-judge call per flagged candidate answers
  three binary criteria: fold to base form, fold preserves denotation,
  conventional written form. Proposals come from the lemmatizer, from a
  conservative plural strip for tokens the dictionary does not know (a plural
  whose base form exists folds unless it is the established name of the sense,
  `data` stands), from the store-anchored derivational transforms for
  relational adjectives (`chilean` → `chile`, `political` → `politics`), and
  from the bounded morphographic proposal for single-token names (`mexican` →
  `mexico`, at most two edits): a proposal only stands when its target already
  exists in the store, and two distinct store hits make the candidate stand. A
  fold applies only when all three confirm; a candidate whose criteria have no
  judge answer stays provisional with the three criteria recorded as pending
  evidence. A refused fold whose proposal targeted a live store value is the
  disjoint-twin risk: the birth is held for review — provisional plus an
  evidence-derived promotion hold that blocks inline and sweep promotion until
  an operator resolves it (INFO `fold rejected beside store anchor …`).
- **Same-intent pairs (J3/J4)** — a pair whose names are pinned variant classes
  (orthographic spelling variants, literal token permutations) resolves `same`
  mechanically with no judge call and no direction, so M4 picks the canonical
  label; the rest go to the pair adjudicator, which answers `same` / `variant` /
  `related`, plus a direction for `same` (`query`, `target`, or `tie`; a tie
  falls back to M4). `same` yields the alias disposition; `variant`/`related` keep
  both names.
- **Dispositions** — `apply` (the name resolves to an existing value by alias
  or key equality), `register` (new value), `alias` (the losing name becomes a
  `tag_alias` row; when the loser already exists as a tag, its assignments are
  re-pointed, its row is removed, and its own aliases are retargeted onto the
  canonical — no chains), `defer` (gray: the candidate registers provisionally
  for review). A candidate that wins a pair against a store label makes the
  store label the alias (candidate-wins).
- **Pending evidence** — with the adjudicator disabled or unavailable, flagged
  candidates stay provisional and pending rows accumulate in
  `tag_identity_event`. The identity pass
  (`kushim tagidentity`, enqueued by
  `POST /api/v1/tags/identity/reevaluate`, a manual run, or its own daily
  schedule) resolves them through the cache.

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
   query, M1 house form, M3 key-equality application, M2 shape flagging and fold
   proposals (plural/lemma and derivational) through a `NameIndex`, band
   subjects from the ranked decisions, and same-emission
   shape pairs. Flagged candidates go into one batched identity-judge call
   (cache first: `tag_identity_event` at `IdentityRuleSetVersion`); flagged
   pairs in a pinned variant class resolve mechanically with no judge call, and
   the rest go through the pair adjudicator. The result is a `tags.Plan` carrying
   registrations, alias actions, evidence rows, and the composition rows the
   decomposition gate judged, and per-disposition log
   lines (`identity[v10]: apply|alias|defer|separate …`), with the applied-fold
   input→output mapping and the judge-unavailable mint at DEBUG
   (`identity[v10]: fold applied …`, `identity[v10]: pending evidence minted …`).
4. **Application** (`CreatePlannedTx`) runs inside the caller's persist
   transaction: registrations (write-boundary normalization, blocklist
   re-check, provisional births, promotion when not quarantined), scheduled
   alias actions (re-point, delete, flatten, alias row), identity-evidence
   rows, and the plan's composition evidence. `RenameTagDetails` keeps the
   analyzer's typed metadata attached to
   folded or applied outputs.
5. **Store hygiene** after the commit: promoted/new names enter the embedding
   store and removed alias names leave it.

A trusted birth is immediately part of the store, and later emissions of the
same name hit M3 or the alias resolver — the self-healing loop.

When `enricher.tag_quality.enabled` is true, `Adjudicator.ApplyTagQuality`
(`enricher.go`) filters that kept set once more before persist: novel names
(absent from the tag table) resolve from the `tag_quality_event` cache or go
into rubric calls over batches of up to 40 candidates, and the pinned
admission decision over the
rubric answers, the parts pass's dimension verdict, plus the emission's deterministic signals drops rejected names
for that document only — their plan registrations and pending evidence never
reach the store. Survivors are born **provisional**
and are promoted inline once the admission decision admits them and their
assignments clear the promotion floor (`tags.RecurrenceFloor`, 10 live
documents); only promoted names enter the store. The judge's
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
  `ParseTagIdentityResponse`, which treats a candidate without a complete
  verdict as missing — an omitted criterion is never read as an all-false
  rejection, and a truncated reply returns `*IncompleteReplyError` alongside
  the answers it did read, so the present candidates are decided and only the
  missing ones stay provisional. The evidence payload (`CriteriaRecord`)
  carries the criteria plus the fold the mechanism proposed.
- **Pair adjudication** — `Runner.AdjudicateTags` →
  `ContentAnalyzer.Adjudicate`. The prompt (`BuildAdjudicationPrompt`) carries
  the same-intent test and the pair's two terms and nothing else — similarity
  selected the pair and is never an input to the verdict. For a `same`
  verdict it also asks which term is the precise one (`direction`:
  `query`/`target`/`tie`). `ParseAdjudicationResponse` treats a pair with no
  parseable verdict — a missing entry, an unknown verdict string, or an
  out-of-range index — as missing, and a truncated reply returns
  `*IncompleteReplyError` alongside the pairs it did read, so the present pairs
  are decided and only the missing ones stay pending. See the [LLM
  guide](llm.md) for the adapter contract.

Resolution order per document:

1. **Identity criteria cache** — the latest verdict per
   `(kind, candidate, partner)` in `tag_identity_event` at
   `IdentityRuleSetVersion` resolves the three criteria without a call; only
   candidates without a complete cached set enter the judge batch.
2. **Pair cache** — the latest `same_intent` row per `(candidate, partner)`
   resolves the pair, with the direction carried in its criteria payload.
3. **Deterministic variant class** — a remaining pair whose names are pinned
   orthographic spelling variants or literal token permutations resolves `same`
   mechanically, recorded with the class in the criteria payload and no model;
   only the pairs left after this enter the adjudicator batch.
4. **Evidence** — one batched target-as-query rank (a rank failure drops the
   evidence, not the pair).
5. **Verdict** — batched `AdjudicateTags` calls for all unresolved pairs. The
   pairs a reply returns are decided; a non-retryable error, an empty result,
   or a pair left missing by a partial reply keeps only those pairs pending:
   the candidates register provisionally with a pending `same_intent` row,
   resolved later by the identity pass. A
   disabled or failed-to-construct adjudicator skips both calls entirely —
   candidates register provisionally, folds are not applied, and every claim
   waits as pending evidence.

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
counts across both orientations and all policy versions (the reconciler's drift
scan reads it), and the reconciler's worklist
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
  `same_intent` (pair rows; `direction`, or the variant class for a mechanically
  resolved pair, lives in the criteria payload). The
  table serves both the identity verdict cache (latest row per
  `(kind, candidate, partner)` at the current rule-set version with a non-NULL
  verdict) and the pending-evidence queue (rows with NULL verdict, deduplicated
  by a partial unique index over `(kind, candidate, COALESCE(partner,''),
  rule_set_version)`; a second partial index on `created_at WHERE verdict IS
  NULL` serves the pending backlog scan and count). Verdicts settle in place
  through `ResolveTagIdentityEvent`, which only touches rows that are still
  pending, so the triggered run and the sweep cannot overwrite each other.

---

## 6. Matcher RPC surface

The daemon (`internal/ops/cli/hugot.go`) serves:

| Endpoint | Purpose |
| -------- | ------- |
| `POST /rpc/v1/rank` | Per query: `{candidates: [{tag, similarity}]}` — top-2 candidates (`rankCandidates`), all ≥ the identity-candidate floor. The daemon makes no replacement decision. |
| `POST /rpc/v1/encode`, `match`, `add-to-store`, `remove-from-store` | The embedding, document-matching and store-maintenance calls. |

`MatcherClient.Rank` (`internal/matcher/client.go`) POSTs `/rpc/v1/rank` and
returns its `RankResult`s verbatim; the policy turns the candidates into gate
subjects, and the identity plan owns the outputs it sets. The store calls are
what tag create, rename and delete flow through, keeping the daemon's view of
the vocabulary current. A daemon that is down surfaces as matcher-unavailable
(503), the same contract as tag CRUD.

---

## 7. Alias disposition

Same-intent labels collapse by **aliasing**, never by deletion. The mechanics
live in `curation` and run from three places: the birth-time plan
(`Tag.CreatePlannedTx` applies the plan's `AliasAction`s inside the persist
transaction), the identity resolver's fold and alias application, and the
reconciler's confirmed `same` flips.

**`Tag.ApplyAliasPlan(ctx, actions []tags.AliasAction) ([]tags.AliasOutcome, error)`**
(`internal/tags/curation/alias.go`) applies a batch in one transaction and
returns one outcome per action in order:

1. A blocklisted name is skipped (`Applied:false`).
2. The canonical is resolved by name; an action whose canonical or loser row is
   missing, or whose name is already an alias, is skipped.
3. With `RemoveExistingTag`, `GetTagForUpdateByName` locks the loser,
   `ReassignDocumentTags` re-points its `document_tag` rows to the canonical
   (skipping documents that already carry it), leftovers are cleared, the
   loser's own aliases re-target the canonical (`RetargetTagAliases` — no alias
   chains), and the loser's tag row is deleted.
4. A `tag_alias` row (`alias_name`, `canonical_tag_id`, `rule_set_version` =
   `tags.IdentityRuleSetVersion`) records the collapse.

The outcome reports `Applied` and how many documents the action re-pointed
(zero for an applied action whose loser carried none). Post-commit, the removed
names leave the embedding store (log-only failure, the same contract as tag
delete).

**Withdrawal.** A stored pair whose fresh verdict flips back to `keep` undoes a
collapse: `withdrawAlias` deletes the alias row that pointed the query at the
target, `Tag.Create` re-creates the name, and `RepointDocumentTagsForDocs`
returns the pair's affected documents to it. An alias onto a *different*
canonical is left alone — the name still resolves, and the flip settles without
re-creating it.

**Operator surfaces.** The tags page exposes vocabulary CRUD (rename, delete)
and the provisional review; there is no merge queue and no dismissal table.
Deleting a tag records it in the `deleted_tag` blocklist (`kind` `blocked`), and
the Deleted tab restores a name by clearing the row and re-creating the tag.
The reconciler reports its applied collapses under `aliases` (with the
re-pointed count), skipped ones under the `alias_skip` skip reason.

---

## 8. Scheduled reconciliation

`internal/tags/reconcile` + the task plumbing. Migration
`00018_tag_reconcile_vocabulary.sql` adds `tagreconcile` to the `task_type`
enum; the `internal/types` registry declares the full blocking stack for the
task type — the batch claim acquires the maintenance lock (verifying the
waits are free) and the reconciling child keeps it fresh (heartbeat + drain)
for the whole run — and `tagreconcile` joins `NonConsumeTaskTypes`, the list
the batch overviews use to classify the maintenance batches.

**Scheduling** — one declarative schedule (`tagreconcile`,
`internal/ops/queue/scheduler.go`) runs on the queue daemon's housekeeping
ticker: the daemon enqueues a `tagreconcile` batch whenever the interval is
due — the enabled flag gates only automatic enqueueing, and a manual
`kushim batch create tagreconcile` batch is dispatched the same way. Due-ness
comes from the last terminal batch of the schedule name (a `completed`,
`failed` or `cancelled` run anchors `NextBackupTime` over its `created_at`,
so a failed run waits for the next occurrence), and a run is skipped while
one is queued or processing; the fixed `tagreconcile` dedup key blocks any
overlap the active check misses, and later runs count the interval from the
previous run. With no terminal batch yet, the first run waits for the next
occurrence of the preferred time-of-day. The scheduling side does not consult
resource locks: a `tagreconcile` batch enqueued while a maintenance run holds
`maintenance` simply queues, and the dispatcher claims it when the lock frees.
The handler
(`internal/tags/reconcile/tagreconcile.go`) runs the shared blocking
lifecycle through the queued batch's own task CLI — a `tagreconcile` batch is
claimed with the maintenance lock held and its `kushim tagreconcile
--from-batch` child adopts the only claim lease, running the whole lock-drain-heartbeat
discipline while the pass does its store work — and the child stores its `Report`
JSON as the task result; it dedups on the fixed `tagreconcile` key, so any
active run blocks further enqueues of the type. The report
carries worklist counts per category (the four pair categories plus `compounds` for the stored-compound
scan and `twins` for the canonicalization arm), judged/confirmed, flips by
direction, aliases with reassignment
counts, re-tags, invalidations, skip reasons — as the task result. The report
also carries pair-level entries (`aliases`, `planned_aliases`,
`planned_keep_flips`, `retag_entries`, `invalidated_entries`), compound splits
(`compound_splits` / `planned_compound_splits`), the `twins` counts and the
consumed `measurement` run (`run_id`, `snapshot_at`), plus `capped`,
`no_documents`,
`swept` (orphan tags deleted, or the would-be count in report-only runs),
`provisional` (the provisional sweep's `worklist`, `zero_assignment`,
`promoted`, `rejudged`, `deferred`, `no_judge`, `skipped`, `failed`),
`demotion` (the demotion arm's `worklist`, `mass_decay`, `no_dimension`,
`rejudged`, `demoted`, `held`, `no_judge`, `skipped`, `failed`, plus the
demoted/planned entries naming each value and its reason),
`siblings` (the containment worklist's `worklist`,
`gray`, `firm` counts — evidence only, nothing acts on a containment reading),
and `adjudicator_disabled`; report-only keep-flip
document counts come from a
single batched `TagPairEvidenceStats` call (a stats failure keeps every
planned entry with zero documents instead of dropping it), and no-document
keep flips are counted in `no_documents` so the judged/skips/capped partition
stays exact. Each pair entry carries `reason` — `adjudicated` for LLM-judged
pairs, `deterministic` for a mechanically resolved variant pair — and
`creates_tag` (true when a keep
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
  deleted target — apply runs only; a report-only run skips the delete, and the
  pair is re-adjudicated and invalidated again on every run); dead-query pairs
  whose latest verdict is not `same` are re-adjudicated (a `same` latest means
  stable absorption — skipped). The dead-query collection ignores verdict
  freshness — a pair whose query is gone and whose latest verdict is `variant`
  or `related` re-enters the worklist on every run, in report-only and apply
  runs alike, and never settles: the re-confirmed verdict is upserted, but the
  pair is collected by its missing query, not by freshness, so the next run
  re-adjudicates it again.
- **Stale/unjudged** — `TagVerdictPairsWithoutFreshVerdict`: no fresh row at
  the current `policy.Version` (older version → stale; current version with
  NULL `verdict_model` → unjudged).
- **Aged** — `TagVerdictPairsOlderThan`, fresh rows older than 90 days.
- **Drifted** — fresh `variant`/`related` pairs whose document-population
  overlap grew (≥ 5 documents and ≥ 0.5 × min(docCounts)), via
  `TagVerdictPairsByVerdict` + `TagPairEvidenceStats`.
- **Log bootstrap** (`bootstrap.go`) — pairs absent from the table entirely,
  parsed from `hugot.log*`/`kushim.log*` rotations (timestamped siblings,
  `.gz` decompressed, chronological). The parser reads archived formats only:
  the enricher no longer emits them. Four line shapes are recognized: the
  pre-rewrite `hugot: consolidate "q" → t (sim)`, the unversioned
  `consolidate "q" → t (sim)`, `consolidation[vN]: keep "q" (sim=…
  reason=ambiguous_band …)` — any version prefix — and the band-less
  `consolidation[vN]: keep "q" (reason=rank_unavailable)` emitted when the
  post-LLM rank call fails (  parsed with an empty target and zero similarity,
  so the entry flows through the normal re-adjudication path; the live
  equivalent is `policy[vN]: keep "q" (reason=rank_unavailable)`, which the
  parser does not read). Legacy
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
identity-candidate floor are dropped — future emissions will resolve them. The rest
are evaluated through `policy.Evaluate` on the current store with the
stored target at its fresh similarity: `identity` where the query equals the
stored target, otherwise `ambiguous_band` → LLM adjudication (batches of 50). A
pair whose names are pinned variant classes (orthographic spelling variants,
literal token permutations) resolves `same` mechanically with no judge call. A
fresh verdict that differs from the stored one is confirmed by a second,
**orientation-reversed** adjudication pass — the flip applies only when both
orientations agree; ambiguity or disagreement keeps the stored verdict — except a
deterministic variant pair, which its own class confirms.

**Flips and caps**: a `same` flip aliases the loser onto the canonical in one
batched `ApplyAliasPlan` call per run — the loser's assignments re-point, its
own aliases re-target, its tag row is removed, and the name survives as an
alias row. A keep flip resolves the pair's affected
documents (`DocumentIDsForPairs`, event-derived or REQID-derived doc ids)
*before* touching the store: when the pair has no resolvable documents the
entry is only marked judged (`upsertVerdict`) and no tag is created, so an
unattributed pair cannot add a name to the vocabulary. Otherwise the keep flip
first withdraws the alias row that collapsed the query onto the target
(`GetTagAliasByName` → `DeleteTagAliasByName`), then
`Tag.Create`s the query tag (Created or Conflict both fine), re-points only
those documents (`RepointDocumentTagsForDocs`, with the `NOT EXISTS` guard),
clears their orphan marks, and upserts the verdict — all in one transaction.
Every adjudicated pair (flip or agreement) upserts a fresh verdict row at the
current version (`UpsertFreshTagVerdict`), refreshing `verdict_at` so the aged
scan does not re-judge it. `verdict_model` carries the adjudicator model, the
`deterministic` marker for a mechanically resolved pair, or is empty when a
cached verdict was applied — the non-null marker is what the fresh-verdict scans
require to treat the pair as settled. `max_retag` bounds re-pointed documents per
run, with a first-pair carve-out (the budget is soft by at most one pair).

**Stored-compound scan**: after the pair scans, the same run takes every stored
tag that classifies as a compound (`tags.ClassifyShape` = `ShapeCompound`),
excluding seeded periods, identity-held and composition-held names (both hold
checks run before any verdict lookup, so a held name never spends a judge call),
and compounds a current-version verdict already decided as a keep or a drop.
The rest are judged through `Adjudicator.JudgeCompounds` — the same reading the
emission gate runs, so a compound disposes identically on either path. The
judgment reads the substrate reconstructibility of every judged cover (the
compound's live mass and its parts' live intersection, resolved through the
store's own name recipe) and feeds it to the composition decision, so a judged
composition the reading places far from its intersection keeps whole as a
relational keep instead of splitting, while a firm or gray reading lets the
promoted-parts proxy stand down. Each
decided compound journals one `tag_composition_event` row with a NULL document
(`00036`) whose `reading` column records that evidence (which source answered —
substrate, proxy, or none — with the band and the numbers), which is what makes a decided hold bind promotion and a decided keep
the thing the emission path later skips. A decided split is applied from the
persisted caches with no new judgment (crash/cap resume); a degraded
`judge_unavailable` keep is not journalled, so an outage leaves the entry
undecided for the next run. Only a split touches the store immediately; a decided
drop demotes the standing tag through the demotion arm, which withdraws its
influence while the row and its assignments stay. Pending-apply splits sort first, then the heaviest
compounds, and the scan shares `max_rejudge`/`max_retag` with the other arms.

**Twin canonicalization**: the same run reads the latest `tagmeasure` run's `≈`
twin worklist (`tags.Store.LatestWorklists`, the newest run only) and resolves
each pair: a pinned variant class aliases mechanically onto the
`PreferCanonical` winner with no judge call, and every other pair becomes
pending same-intent evidence the identity pass settles. Pairs are skipped
when a side is no longer a live tag, either side is identity-held, either is a
conjunction the compound scan owns, the pair is already alias-linked or settled
at the current rule-set version, or the pair is already in this run's band
worklist; a failed hold, alias, settled, or mass lookup aborts the arm for the
run rather than degrading its gates. The arm is bounded by `max_rejudge` (pairs)
and `max_retag` (re-pointed documents) and applies its mechanical aliases in one
batched plan. Report-only runs plan without writing.

**Demotion**: the same run withdraws the standing of promoted values the current
rules no longer back. The worklist is every promoted row with `NOT seeded`;
identity-held and composition-held names are left for review, a value whose live
mass fell below the promotion floor is demoted with reason `mass_decay`, and a
value with no current-version quality verdict is re-judged through the same step
the provisional sweep uses (`rejudgeStoredName`) — a fresh reject or defer demotes
with the decision's reason, an admit leaves it promoted. A value that already
carries a current-version verdict is respected as it stands, so an operator
promotion survives until the next criteria bump. Revocations and re-judgments draw
on one per-run bound (`max_rejudge`), the deferral counted under `capped`; a missing
judge or a failed lookup leaves the value promoted. Each demotion goes through
`Tag.Demote`, which clears `promoted_at`, records `demoted_at`/`demoted_reason`, and
removes the name from the embedding store. Report-only runs plan the would-be
demotions and touch nothing.

**Blocklist enforcement**: a query whose name is in the `deleted_tag`
blocklist is skipped before the create — the flip is not applied and the skip
is logged, but the pair still gets its fresh verdict so it is not re-judged.
Blocked names are dropped by enrichment. Only the operator restore path (and
re-creating the tag through the operator create endpoint) clears a blocklist row.

**Orphan-tag sweep**: each run maintains `tag.orphaned_since` — marks are
cleared for tags that currently hold documents (`ClearOrphanTagMarks`), then
set for document-less tags without one (`MarkOrphanTags`) — and deletes (plus
removes from the embedding store) tags whose mark is older than
`enricher.tag_reconcile.dead_tag_grace_days` (default 7). Every assignment
path clears the assigned tags' marks (`ClearOrphanTagMarksByTagIDs`) so the
grace window reflects real last usage rather than the last reconcile
observation. Seeded values are exempt from the marks and the delete, so the shipped inventory never ages out. The list/delete queries re-check `NOT EXISTS (document_tag)`, so
a tag re-assigned mid-run is never deleted. Report-only runs refresh the marks
inside a transaction that is always rolled back, so the reported `swept` count
predicts what the next apply would delete.

**Provisional sweep**: after the orphan sweep, the same run walks provisional
tags older than `enricher.tag_reconcile.provisional_grace_days` (default 30),
oldest first. Zero-assignment rows are only counted — the orphan sweep owns
their deletion. Identity-held rows — a refused fold beside a live store value,
derived from the latest `base_form`/`fold_gate` evidence — and composition-held
rows — the latest composition verdict at the current split criteria version
defers, conflicts, disagrees, is a reconstructible named unit, is a relational
keep, or is a degraded judge_unavailable keep — are counted
as skipped and left for review before any verdict check, so the sweep never
promotes them. Rows whose latest verdict at `TagQualityVersion` passes and
that hold ≥10 live documents are promoted through `curation.Tag.Promote` (the
same method behind `POST /api/v1/tags/{id}/promote`, so the log line and the
store add match the operator path). Verdict-less rows are re-judged under the
shared `max_rejudge` budget: `FirstDocumentIDForTag` picks the first live
carrier, `GetDocumentTextForTagQuality` fetches its text and word count, and
`Runner.ReduceContent` reduces it with `matcher.TargetWordCount` (the
tagreconcile runner loads the `textreducer` tool for this path). One
`JudgeTagQuality` call judges the single candidate — category `other` — and a failure retries once
unless the error carries `context.Canceled`/`DeadlineExceeded`. The verdict is
written with `InsertTagQualityEvent`, the enricher's `DO NOTHING` conflict
rule: a concurrent fresh verdict is kept and its `pass`/model drive the
decision instead of being overwritten. A fresh pass promotes at ≥10 live
documents, a reject is deferred to the review queue, a tag with no live
document text is skipped, a missing judge is reported as `no_judge`, and
lookup or write failures as `failed`; the run never deletes a tag. Report-only
runs count would-promote/would-re-judge and touch nothing. The counts land in
`Report.Provisional` (`worklist`, `zero_assignment`, `promoted`, `rejudged`,
`deferred`, `no_judge`, `skipped`, `failed`).

**Identity evidence**: the reconcile no longer resolves identity claims
itself — Stage I retired the sweep (D49). The twin arm inserts pending
`same_intent` evidence (write runs; report-only runs count the would-insert
rows without writing), and the separate scheduled `tagidentity` pass resolves
them through the shared resolver
(`internal/tags/admission/identity_resolve.go`): candidate criteria are judged
in batches, same-intent pairs adjudicated, and the dispositions applied — a
confirmed fold renames the tag (or aliases it when the folded name already
exists), a same-intent loser becomes an alias of its winner, a deleted fold
target is skipped, and a deferral stays for review. The per-pair verdict cache
settles each claim once, so the twin arm's insert and the identity pass
converge on whichever runs first.

**Report-only**: the first run is always report-only
(`GetLastCompletedTagReconcileTask` finds no completed prior run), regardless
of the flag — it executes the full evidence/evaluation path and logs every
would-be action without applying anything. What it withholds is the store
action set: alias application, compound splits, keep-flip writes, twin
pending-row inserts, promotions, demotions, orphan deletions, and the
dead-target verdict deletion. The sweep's own judgment evidence still persists
(the compound scan's `tag_composition_event`, phrase-fold, and part-dimension
rows, plus a confirmed pair verdict that flips nothing), which is what lets
the compound scan resume from cache instead of re-judging on a later run —
the pair worklist's dead-query entries are the exception, since they re-enter
the worklist by their missing query rather than by freshness. With the
adjudicator disabled, runs log the worklist snapshot and complete without LLM
calls; the only store action is the twin arm's mechanical variant aliases
(planned, not applied, in report-only). The daemon warns when reconciliation
is enabled without the adjudicator.

---

## 9. Tag extension measurement

The measurement pass (`internal/tags`) is the substrate behind every "how much
do these two names overlap" question in this subsystem. It also retired the
duplicated SQL those questions used to run on: the enricher's promotion gate,
the reconciler's live counts, and the pair-verdict aggregates all read through
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
- **The live reads** — the decision does not wait for a run: `Store.ReconstructibilityBatch` resolves a batch of judged covers against the live store (names through the store's own recipe, one live-mass and one intersection query per batch) and returns a reading per cover, or nothing where the store cannot measure it. `Store.LatestWorklists` and `Store.SiblingWorklist` derive the persisted worklists for the reconciler and the review surface (the latter reading only the run's overlap records).
- **Rule-set v1** derives the composition-review worklists from the persisted
  records: `≈` twin candidates (Jaccard ≥ 0.9 with both masses ≥ 3, gray band
  0.7–0.9), compound split proposals (both leftovers ≤ 0.1, gray to 0.25), and
  near-contained siblings (containment ≥ 0.8 with both masses ≥ 3, gray
  0.6–0.8), each sibling pair ordered broader tag first. The worklists are
  evidence: the reconciler's twin arm consumes the `≈` pairs, the stored-compound
  scan covers stored compounds directly, and the sibling readings surface
  read-only on the tags page's Overlap tab (`GET /api/v1/tags/siblings`) —
  nothing is acted on at measurement time.
- **Running it**: `enricher.tag_measure.{enabled, interval, time}` schedules a
  `tagmeasure` batch (disabled by default), and `kushim batch create tagmeasure`
  enqueues the same queued task — the flag gates only the
  schedule, while a bare `kushim tagmeasure` runs the pass under the manual
  maintenance hold. The child holds the lock for its whole transaction, so runs are
  minutes-scale and serialize with every other maintenance hold.

The pass measures; it never judges. Dispositions — promote, demote, alias, split,
delete — stay with the reconciler's arms (the stored-compound scan, the twin
canonicalization arm, and the demotion arm) and the operator surfaces.

---

## 10. Safety properties

- **Keep is the failure direction everywhere**: rank error, adjudicator
  disabled/unavailable/ambiguous, reconciliation disagreement — the emitted or
  stored state is kept, unresolved claims stay pending, and enrichment never
  blocks or fails on this subsystem.
- **Wrong keeps are visible and correctable; wrong replacements are silent and
  permanent** — hence the asymmetric strictness: similarity never decides. The
  only paths that change a name are M1 style rewrites, M3 key equality, the
  deterministic variant classes (M8), a fold confirmed by all three criteria, and
  an explicit `same` verdict (with direction).
- **Same-intent resolution is an alias, never a deletion**: a losing name stays
  resolvable through `tag_alias` instead of being deleted outright, and a later
  `keep` re-judgment withdraws the alias.
- **Bounded automation** — policy-version and rule-set-version pinning, the
  double-orientation confirmation, `max_rejudge`/`max_retag`, and the
  report-only first run.
- **Dead names cannot resurrect** — the registration path re-checks the
  `deleted_tag` blocklist (a blocked name is never registered and never
  aliased), fold application skips deleted targets, dead-target verdict rows
  are deleted, and every operator delete is recorded in the blocklist:
  enrichment drops blocked names, and the reconciler skips their restoration.

---

## 11. Gotchas

- **The adjudicator, quality judge, and reconciliation are disabled by
  default** — novel tags are born unfiltered, folds are not applied, and
  same-intent claims wait as pending evidence (their candidates register
  provisionally) until `enricher.tag_adjudicator.enabled` is set or the
  triggered run/sweep resolves them; both event tables stay empty until their
  layers run.
- **Quarantined tags are invisible to the vocabulary** — with `tag_quality`
  on, a newborn is attached and searchable from birth but stays out of the
  embedding store — and therefore out of suggestions, consolidation targets,
  and the all-names fallback — until the admission decision admits it and
  its assignments clear the promotion floor (`tags.RecurrenceFloor`, 10 live
  documents). Verdict-less births (judge
  outages) and re-emitted legacy provisionals never get a verdict row in
  flight — a name in the tag table is not novel, so the judge never sees it
  again — and stay provisional until the reconciliation sweep's provisional
  phase or an operator in the review queue resolves them.
- **The first reconciliation run is always report-only**, even with
  `report_only: false` — flip behavior only starts with the second run.
- **`identity_candidate_similarity: 0` disables candidate generation** — `Rank`
  returns no candidates, so nothing becomes a gate subject, and every name
  passes through. That is the documented knob, not a bug.
- **Verdicts are cache-scoped to `policy.Version`** — bumping the version
  invalidates every cached verdict at once (old rows stay in the table for
  analysis; they are simply never read for the new version). Until the
  reconciler runs, every band pair is re-judged fresh — the caps are the bound.
  Identity evidence carries an independent pin, `IdentityRuleSetVersion`,
  quality verdicts another, `TagQualityVersion`, and the split verdicts two
  more, `TagPartDimensionVersion` (parts) and `TagSplitVersion` (compounds);
  bumping any of them retires its cached rows at once.
- **Identity and policy logs live in the kushim (enrichment) log, not
  `hugot.log`** — the identity dispositions (`identity[v10]`) and the pinned
  policy's passthroughs (`policy[v1]`) are emitted enricher-side. `hugot.log`
  only holds the older history (which is exactly what the bootstrap parser
  reads).
- **Interface changes ripple** — `Matcher`/`Embedder` implementations include
  `*Hugot`, `*matcher.MatcherClient`, and the test doubles in
  `internal/testutil` and `internal/tools/runner_test.go`; keep them all
  consistent.
- **Similarity never decides** — a high-similarity neighbour is only a gate
  subject; a name changes only through M1/M3, the deterministic variant classes
  (M8), a fully confirmed fold, an explicit `same` verdict, or an operator
  rename/delete. Bands that keep without a verdict stay provisional with
  pending evidence.

---

*Last verified against the tree: 2026-10-13. If code and doc disagree, code wins.*
