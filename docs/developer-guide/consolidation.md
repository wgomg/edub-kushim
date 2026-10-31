# Tag consolidation

Consolidation maps every emitted tag name onto the existing vocabulary before anything is
saved: spelling and casing variants collapse onto their canonical form, synonyms are
recognized, and genuinely new concepts are born as new tags. This guide documents the
subsystem as it operates — the identity machinery, the opt-in LLM adjudication, the
quality gate, quarantine and promotion, aliases, and the scheduled reconciliation that
heals older damage. The normative rules these mechanics enforce live in the specs:
[canonical form](../specs/tags/canonical-form.md) and the
[definition](../specs/tags/definition.md).

The principle throughout: **creation is the default; replacement requires positive
evidence of concept identity.** A wrongly kept tag is a visible duplicate; a wrongly
replaced tag silently and permanently erases a concept from the vocabulary. Every failure,
ambiguity, or outage defaults to *keep*.

## 1. Orientation

Consolidation runs at the end of enrichment, after the LLM emits tags. The seam is
deliberate: **the matcher daemon only ranks** — it embeds the emitted names and returns
candidates with scores — and **the enricher owns the identity decision**. That split lets
the pipeline (and everything built on it: adjudication, reconciliation) live in non-CGo
packages that run in both binaries, while the CGo-gated model stays in the daemon (see
[ADR 0004](../../adr/0004-matcher-is-operator-owned.md)).

| Package | Role |
| --- | ---- |
| `internal/tags` | canonical-form identity mechanics: house form (M1), lemma/stem signatures (M2, evidence only), key equality (M3), tie-break (M4), variant classes (M8), alias/evidence types, deterministic planning |
| `internal/tags/policy` | pure candidate generation: identity + band subjects, no logging, no config |
| `internal/tags/admission` | admission seams: alias lookup, identity planning, judge/adjudicator orchestration, tag-quality gating, plan application |
| `internal/enrichment` | coordinator: runs the pipeline and calls the admission seams |
| `internal/tags/reconcile` | scheduled re-adjudication + log bootstrap parser |
| `internal/tags/curation` | tag vocabulary services: tag CRUD, alias disposition, identity-run enqueue |
| `internal/matcher` | the daemon (`Rank`, embedding store) and the RPC client |

Two deployments share the `matcher.Matcher` interface: the in-process `*Hugot` (kushim,
CGo) and `*matcher.MatcherClient` (edub, Unix-socket RPC to `kushim hugot`). Any
interface change must update both plus the test doubles.

## 2. Identity resolution

Per enrichment, after the LLM emits its tags:

1. Emitted names are canonicalized (`textnorm.NormalizeTag`: NFKC, lowercase,
   accent-folded; `+`, `#`, `.`, digits, and non-Latin scripts preserved — `c++` is one
   token). The same normalizer runs at the tag-service save chokepoints, so store names
   are always canonical and identity is exact string equality.
2. Typed tags are gated by the model's own `category`: `form_genre`, `person`, and `work`
   emissions are dropped, and `place` emissions survive only as countries or continents —
   every drop is logged as `tags[facet]: drop …`. `topic`/`other` and untyped emissions
   pass through. `FilterTags` then drops deterministic garbage, and a decomposition pass
   replaces compound tags by their parts (§3).
3. The matcher daemon ranks each name against the tag store and returns up to two
   candidate neighbours with similarity scores, all at or above the **identity-candidate
   floor** (`identity_candidate_similarity`, derived per model — bge-m3 → 0.80). The
   floor selects which stored neighbours reach the identity gate; it is a model-derived
   internal, not a YAML key, and no similarity cutoff ever replaces or keeps a name on
   its own.
4. The enricher resolves identity before any judging. The rules, in order:

| Rule | Outcome | Catches |
| ---- | ------- | ------- |
| Alias lookup — the name is a `tag_alias` row | apply the canonical value | names that already lost a same-intent decision |
| Key equality (M3) — house form matches a store tag | apply the store label | style variants (`nineteenth century` / `19th century`) |
| Exact identity with a store tag | apply the store label | casing, hyphenation, and symbol-form fixes after normalization |
| Flagged fold — a mechanically proposed reduction (M2) | judged: base form (J1), fold gate (J2), conventional form (J5); the fold applies only when all three confirm | inflections, relational adjectives (`chilean` → `chile`), bounded morphographic variants (`mexican` → `mexico`), conventional forms (`c plus plus` → `c++`) |
| Pinned variant class — an orthographic spelling variant or a literal token permutation | mechanical `same`, no judge call: the deterministic tie-break (M4) picks the winner, the other name aliases | `decolonisation`/`decolonization`, `military history`/`history military` |
| Any other candidate neighbour — the same-intent gate | judged (J3) with a direction (J4): `same` aliases the loser onto the winner (a store label that loses becomes the alias), `variant`/`related` register both | the residual band and shape-related pairs |
| No judge available | the claim waits: the candidate registers provisionally with a pending identity-evidence row | adjudicator disabled or unreachable |

Every identity disposition is logged in the kushim (enrichment) log
(`identity[v10]: apply|alias|defer|separate <name> …`; the prefix carries the rule-set
version), so the pipeline's behavior is auditable after the fact and the reconciliation
task can recover history the database never captured.

### 2.1 The mechanical body

The mechanics (`internal/tags/identity.go`) are pure and shared by the admin and emission
registration paths ([spec](../specs/tags/canonical-form.md) §3.2):

- **M1 — house form**: word ordinals (`nineteenth century` → `19th century`) and roman
  numerals (`world war ii` → `world war 2`) convert to numeric house style; English
  dictionary words that happen to parse as roman numerals (`mix`, `mid`) are left alone.
  A style rewrite is written directly at registration.
- **M2 — shape signatures and fold proposals**: per-token lemma (golem) and stem
  (snowball) signatures, the plural/lemma fold proposal, the store-anchored derivational
  transforms that reduce relational adjectives onto a noun the store already holds, and
  the bounded morphographic proposal that reaches a single-token store value within two
  edits (two distinct store values in reach make the candidate stand). Evidence only —
  the stemmer's output is never written, and a proposed fold is never applied
  unconfirmed.
- **M3 — key equality**: the identity key is the house form, so style variants collapse
  without judging.
- **M4 — tie-break**: fixed lexical order decides when the direction criteria do not
  separate two labels.
- **M8 — deterministic variant classes**: pinned orthographic spelling variants
  (`-isation`/`-ization`, `-yse`/`-yze`, `-ogue`/`-og`, `-our`/`-or`, per token) and
  literal token permutations resolve `same` with no judge call. Plurals, mixed-language
  spellings, and morphological or function-word inversions stay with the pair judge.

`IdentityRuleSetVersion` is stamped on every alias row and identity-evidence row; it pins
the mechanical fold proposals, the pair judge's criteria text, the variant-class table,
the birth-time canonical direction, and the split-seam fold source. Bumping it retires
every cached identity verdict at once.

- **`PlanDeterministic`** — the admin path's deterministic phase: normalization, M1
  rewrites, alias lookup, key equality, and M2 flagging. It never applies a fold, never
  adjudicates, and never makes an alias; flagged relations register provisionally with
  pending evidence.
- **`ResolveAliases`** — the single alias resolver: normalizes and dedupes names and
  resolves them to canonical names in one query. It backs registration, suggestions,
  structured search, and the enricher.

### 2.2 Candidate generation and the gate

`internal/tags/policy.Evaluate(queries, ranked) []Decision` is a pure function — no
logging, no config, no I/O. Per query it runs: **passthrough** (short slices, no
candidates), **identity** (exact equality with a store tag → replace; the bulk of real
traffic), **band subjects** (everything else: the query keeps its name and the top
candidate is recorded as a gate subject). `policy.Version` is the policy version stamped
on stored verdicts — bumping it marks all stored verdicts stale to the reconciler. The
judge sees only the two terms of a pair; the similarity that selected the pair is never
part of the prompt.

The gate resolves flagged candidates and band subjects before the persist transaction —
adjudication stays outside it:

- **Folds (J1/J2/J5)** — one identity-judge call per flagged candidate answers three
  binary criteria: fold to base form, fold preserves denotation, conventional written
  form. A fold applies only when all three confirm. A refused fold whose proposal
  targeted a live store value is the disjoint-twin risk: the birth is held for review —
  provisional plus an evidence-derived promotion hold that blocks inline and sweep
  promotion until an operator resolves it.
- **Same-intent pairs (J3/J4)** — pinned variant classes resolve `same` mechanically;
  the rest go to the pair adjudicator (`same` / `variant` / `related`, plus a direction
  for `same`). `same` yields the alias disposition.
- **Dispositions** — `apply` (alias or key equality), `register` (new value), `alias`
  (the losing name becomes a `tag_alias` row; when the loser already exists as a tag its
  assignments are re-pointed, its row is removed, and its own aliases are retargeted — no
  chains), `defer` (gray: registers provisionally for review). A candidate that wins a
  pair against a store label makes the store label the alias (candidate-wins).
- **Pending evidence** — with the adjudicator disabled or unavailable, flagged candidates
  stay provisional and pending rows accumulate in `tag_identity_event`. The identity pass
  (`kushim tagidentity`, enqueued by `POST /api/v1/tags/identity/reevaluate`, a manual
  run, or its own daily schedule) resolves them through the cache.

## 3. Compound decomposition

A compound tag whose parts each name a subject — `russian history`, `philosophy of mind`,
`air fryer repair` — is replaced by its parts before consolidation. Fixed names and set
phrases that name one subject their parts cannot compose (`cold war`, `internet of
things`, `black bloc`) stay whole, and a compound whose key already exists in the store
(names and aliases) is an application: it is skipped before judgment and never split.

- **Detection**: every canonical tag of two to three words in Latin script with at least
  one content part is a candidate (`tags.ClassifyShape` = `ShapeCompound`).
- **Decision**: with the tag adjudicator enabled, the split decision runs two context-free
  judged passes (same adjudicator as tag pairs) — a **parts pass**
  (`analyzer.TagPartDimensionVersion`) judges each fold-resolved content word on its own,
  and a **compound pass** (`analyzer.TagSplitVersion`) reads the phrase as a named unit,
  a plain composition, or a phrase with no retrievable dimension — and the two answer sets
  fold into one decision (`tags.DecideComposition`). The parts verdict selects the branch;
  the compound reading disposes within it; a `named_unit` reading over all-dimensional
  parts is the held gray keep (`named_unit_reconstructible`, or `relational_keep` when
  the parts are not all promoted) whose parts apply alongside. With the adjudicator
  disabled, only conjunction joints (`and`, `or`, `vs`, `versus`; after normalization `&`
  is stripped) split deterministically; every other compound passes whole.
- **Store-keyed compounds**: a compound already in the store skips judgment only when a
  current-version verdict decides it; one with no verdict, or only a degraded
  `judge_unavailable` keep, re-enters judgment until a verdict exists, and a decided keep
  applies its judged-dimensional parts alongside from the persisted cache on every
  emission, making no model call.
- **Evidence**: every judged compound appends one `tag_composition_event` verdict row —
  candidate, validated segments, disposition, reason, criteria version, judge model
  (NULL for cached verdicts and the conjunction fallback) — with a matching `tag
  split[…]` log line per candidate. The stored-compound sweep journals the same rows with
  a NULL document, so a decided hold binds promotion and a decided keep is skipped at
  emission.
- **Validation**: segments are first fold-resolved onto **promoted** store values through
  the attested seam fold (the generated WordNet pertainym table's adjective→noun pairs in
  sense order; a multi-base part folds only onto its primary-sense base and only while
  that base is live, with the residue routed to the phrase-context fold judge; provisional
  births are not seam anchors) and then pass the hard per-document rules — normalized,
  deduplicated, ≤3 words, ≤64 characters, no person or doc-type token matches, no
  unseeded time designation — and a split must yield between 2 and `MaxCompoundWords` (3)
  valid **segments**; anything else keeps the compound as-is. Emissions are not
  count-capped.
- **Retroactive cleanup**: the scheduled reconciliation task scans the store for every
  stored compound and disposes it through the same composition decision (§8).

The mechanism's normative rules — part derivation, branch selection, dispositions — are
specified in [atomicity](../specs/tags/atomicity.md).

## 4. LLM adjudication (opt-in)

Identity is where similarity alone cannot decide. With
`enricher.tag_adjudicator.enabled: true` (default `false`), the gate resolves proposed
folds and candidate neighbours with LLM calls. Inheritance is resolved at use time:
`Config.ResolvedAdjudicatorLlm()` and `ResolvedAdjudicatorTimeout()` fall back to the
content analyzer's `llm`/`timeout` when the block's own values are empty, so analyzer
model changes keep propagating.

- **Folds**: one identity call per flagged candidate answers the three binary
  canonical-form criteria; the fold applies only when all three confirm.
- **Verdicts**: `same` (one value → the losing name becomes an alias), `variant`
  (broader/narrower → keep), `related` (different concepts → keep); for `same` the call
  also names the precise term (`query`, `target`, or `tie`, with M4 breaking ties).
- **Resolution order per document**: identity criteria cache (`tag_identity_event` at the
  current rule-set version) → pair cache → deterministic variant class → one batched
  evidence rank → batched verdict calls. A pair with no parseable verdict — a missing
  entry, an unknown verdict string, an out-of-range index — is treated as missing, never
  as a rejection; a truncated reply returns the verdicts it did read, and only the
  omitted pairs stay pending.
- **Cache**: every verdict is stored keyed by `(query, target, policy_version)` in
  `tag_verdict_event` (fresh rows carry the judging model; cache-applied rows store NULL).
  Decided pairs are also recorded as document-anchored rows so the reconciler's pair view
  stays complete. A partial unique index deduplicates fresh verdicts per pair/version
  across concurrent workers with no in-flight coordination.
- **Terms only**: similarity selects which pairs are judged; it is never an input to the
  verdict.
- **Failure safety**: a transient failure (transport, generic provider error, empty
  response) retries once; parse failures, incomplete replies, output-cap hits, credit
  exhaustion, token-limit, content-too-large and context errors are never retried. Every
  non-credit failure keeps the emitted tag — enrichment never blocks on adjudication —
  and a credit-exhaustion failure aborts the document so the batch pauses. With the
  adjudicator disabled, the band behaves as pure keep.

## 5. Tag quality gate (opt-in)

Adjudication decides what is a duplicate; the quality gate decides what deserves to be
born. With `enricher.tag_quality.enabled: true` (default `false`), the novel tags that
survive consolidation — names absent from the vocabulary — are decided before they are
created (`Adjudicator.ApplyTagQuality`):

- **Features**: the judge's rubric answers — `subject_matter`, `not_person`, `atomic`,
  `not_form_genre` — plus the `dimension` verdict the parts pass owns (read from the
  shared parts cache, judged on a miss), the judge's optional `defer` abstention, and the
  emission's deterministic signals: a typed detail annotated `other`, and a best store
  similarity below the internal no-anchor floor (0.60).
- **Decision**: the pinned rule set (`tags.DecideAdmission`) defers on the judge's
  abstention, rejects on two or more failing signals, defers on exactly one (the gray
  band — no single signal decides), and admits when nothing fails. Rejected names are
  dropped for that document and never stored — their plan registrations and pending
  identity evidence are removed before persist. A missing detail or similarity is absence
  of evidence, not a failing signal.
- **Judge call**: uncached candidates are judged in temperature-0 LLM calls over batches
  of up to 40 candidates per document; the reduced document text serves the
  `subject_matter` question alone, the other criteria answering on the candidate.
- **Cache**: verdicts are stored in `tag_quality_event` keyed by
  `(tag_name, policy_version)` with the recorded outcome (`reject`/`defer`/`admit`). A
  re-emitted name resolves from the cache — the decision re-runs over the stored rubric
  with the current emission's deterministic signals.
- **Failure safety**: same retry taxonomy as adjudication; a reply that omits verdicts
  for some candidates salvages the entries it returned and defers only the missing ones;
  every other non-credit failure keeps every novel tag with a defer decision.
- **Logging**: rejections and defers are logged as `tagquality[v6]:` lines carrying the
  outcome and reason, siblings of the `identity[v10]` and `policy[v1]` lines.

The decision thresholds are pinned constants, not configuration keys; the judge LLM and
timeout inherit the content analyzer's unless the `llm` block overrides them, and a
warning is logged when the resolved judge equals the generator.

## 6. Judge criteria versions

Every LLM judge pins its criteria with a version constant, and a cached verdict applies
only under the version that produced it — a criteria or prompt change bumps the constant
so older rows never apply (the version also appears in the log prefix). The constants and
what they pin:

- `analyzer.TagQualityVersion` — the quality judge's rubric and prompt, and the language
  guard's policy version.
- `analyzer.TagPartDimensionVersion` — the split gate's parts pass and the value-keyed
  dimension verdict the quality gate reads.
- `analyzer.TagSplitVersion` — the split gate's compound pass trichotomy, and everything
  a composition row means: the branch-first decision order, the attested seam folds, the
  parts-alongside keep rows, the all-parts-rejected degradation, the seed-bounded time
  policy, the in-order segment fan, and the retrospective reconstructibility reading.
- `analyzer.TagTranslationVersion` — the language guard's translation pass.
- `analyzer.TagPhraseFoldVersion` — the seam's residual phrase-context fold.
- `analyzer.TagAuditVersion` — the completeness audit's applicability question; the
  coverage verdict cache is keyed by the document text hash and this version.
- `tags.IdentityRuleSetVersion` — the identity judge's criteria ride this constant
  alongside the birth-time rules, which it pins together.

## 7. Quarantine and promotion

With the quality gate on, the novel tags the decision keeps are **provisional**: they are
attached to the document and searchable from birth, but `tag.promoted_at` is NULL and the
name is excluded from the embedding store — never suggested, never a consolidation
target, never part of the all-names fallback vocabulary (`BuildTagCache` embeds promoted
tags only). With the gate off, newborns are born trusted.

Promotion is inline and runs inside the persist transaction: a provisional name the
admission decision admits with assignments on at least ten distinct **live** documents
(trashed carriers are not evidence, here and in the reconciliation sweep alike) gets
`promoted_at = now()` and enters the store after the commit. The `document_tag` primary
key makes the count distinct-document, so a single document re-enriching itself cannot
promote a name. An identity-held name (a refused fold beside a live store value) and a
composition-held name (a decided drop, defer, conflict, disagreement, reconstructible
named unit, relational keep, or degraded `judge_unavailable` keep) are skipped by both the
inline check and the sweep until an operator resolves them or a later verdict decides
them.

Influence is revocable (definition §6.4): a promoted value that stops qualifying is
demoted, which clears `promoted_at`, records the reason, and removes the name from the
store while the row and its assignments stay.

Verdict-less provisionals — judge outages and re-emitted legacy rows — are not re-judged
in flight: a name already present in the tag table is not novel, so the judge never sees
it again. They are re-judged by the reconciliation sweep once they pass
`provisional_grace_days` (§8).

Operators resolve what remains from the tags page's "New tags" tab or the API:
`GET /api/v1/tags/provisional` lists provisional tags with a live `document_count` and
the latest verdict; `POST /api/v1/tags/{id}/promote` promotes one through the same path
the sweep uses. Rejecting a provisional tag is a normal tag delete, so the name lands in
the `deleted_tag` blocklist and enrichment cannot resurrect it.

## 8. Aliases

Nothing is deleted to resolve a duplicate: a same-intent pair collapses by **aliasing**
the losing name onto the winner. The loser becomes a `tag_alias` row pointing at the
canonical — its `document_tag` assignments re-point to the canonical, its own aliases
re-target (no alias chains), its tag row is removed, and the name leaves the embedding
store. The canonical-only rule is structural: alias rows never enter the embedding store,
so a losing name can never be suggested or become a consolidation target.

`Tag.ApplyAliasPlan` applies a batch in one transaction: a blocklisted name is skipped;
a missing canonical/loser row or an already-alias name is skipped; with
`RemoveExistingTag` the loser's assignments re-point (skipping documents that already
carry the canonical), its own aliases re-target, and its tag row is deleted; then the
`tag_alias` row records the collapse. Post-commit the removed names leave the embedding
store (log-only failure, the same contract as tag delete).

Aliasing happens at birth (§2) and in the scheduled reconciliation (§9); a stored pair
whose fresh verdict flips back to `keep` **withdraws** the alias: the alias row is
removed, the name is re-created, and the affected documents return to it. An alias onto a
*different* canonical is left alone — the name still resolves.

The tags page exposes vocabulary CRUD (rename, delete) and the provisional review; there
is no merge queue. An operator delete records the name in `deleted_tag` as `blocked`, and
the Deleted tab restores a name by clearing the row and re-creating the tag.

## 9. Scheduled reconciliation

`enricher.tag_reconcile` schedules a recurring `tagreconcile` task
(`internal/tags/reconcile`) that re-judges stored verdicts and repairs old damage —
bounded and anti-oscillating.

**Scheduling** — one declarative schedule on the queue daemon's housekeeping ticker; the
daemon enqueues a `tagreconcile` batch when the interval is due (the enabled flag gates
only automatic enqueueing; a manual `kushim batch create tagreconcile` batch dispatches
the same way). Due-ness anchors on the last terminal batch of the schedule name; a run is
skipped while one is queued or processing; the fixed dedup key blocks overlap the active
check misses. The batch claims with the maintenance lock held, and its `kushim
tagreconcile --from-batch` child adopts the claim lease, running the whole
lock-drain-heartbeat discipline for the pass.

**Worklist** — what it re-examines, from what the table persists plus logs:

- **Dead-name pass** — pairs with a missing side: dead-target pairs have their fresh
  verdict rows deleted (a cached `same` can never re-create a deleted target); dead-query
  pairs whose latest verdict is not `same` are re-adjudicated.
- **Stale/unjudged** — no fresh row at the current policy version.
- **Aged** — fresh rows older than 90 days.
- **Drifted** — fresh `variant`/`related` pairs whose document-population overlap grew.
- **Log bootstrap** — pairs absent from the table entirely, parsed from rotated
  `hugot.log*`/`kushim.log*` archives (historical formats only; table rows supersede
  log-derived entries).
- **Stored-compound scan** — every stored tag that classifies as a compound (excluding
  seeded periods, identity-held and composition-held names, and compounds a
  current-version verdict already decided) is judged through the same composition
  decision the emission gate runs, reading the substrate reconstructibility of every
  judged cover so a far reading keeps whole as a relational keep. Only a split touches
  the store immediately; a decided drop demotes the standing tag.
- **Twin arm** — the latest `tagmeasure` run's `≈` twin worklist: a pinned variant class
  aliases mechanically onto the `PreferCanonical` winner with no judge call; every other
  pair becomes pending same-intent evidence the identity pass settles.

**Re-adjudication is policy replay**: each pair gets a fresh rank, is evaluated through
`policy.Evaluate` on the current store, and band survivors are re-adjudicated; a verdict
flip must be confirmed by a second, **orientation-reversed** adjudication before it
applies (a deterministic variant pair confirms through its own class). A `same` flip
aliases the loser onto the canonical; a `keep` flip resolves the pair's affected documents
*before* touching the store (an unattributed pair cannot add a name to the vocabulary),
then withdraws the alias, re-creates the query tag, and re-points only those documents.

**Flips and caps**: `max_rejudge` pairs and `max_retag` documents per run; the provisional
re-judge and the twin arm share `max_rejudge`, the compound scan and the twin aliases
share `max_retag`, and the demotion arm's revocations draw on its re-judgment bound. The
first run is always **report-only** — it takes no store action regardless of the
`report_only` flag (the sweep's own judgment evidence still persists, so the compound
scan can resume from cache). A failed hold, alias, settled, or mass lookup aborts the
twin arm for the run rather than degrading its safety gates.

**Provisional hygiene** — the same run re-checks provisional tags older than
`provisional_grace_days` (default 30), oldest first. A stored verdict the decision
re-derives as admit plus ≥10 live documents promotes the name with no judge call; defer
and reject verdicts stay queued for review; verdict-less names are re-judged one at a
time (the tag's first live document, reduced with the tag-matcher arithmetic, one retry
on failure) and promoted when admitted. Zero-assignment rows are only counted — the
orphan sweep owns them.

**Demotion** — the same run withdraws the standing of promoted values the current rules
no longer back: live mass below the promotion floor (`mass_decay`), or a stale verdict
that re-judges to reject or defer. A value carrying a current-version verdict is
respected as it stands; seeded values never enter the worklist; identity- or
composition-held names, a missing judge, and a failed lookup leave the value promoted.
Each demotion goes through `Tag.Demote`, which clears `promoted_at`, records the reason,
and removes the name from the embedding store.

**Dead-tag hygiene** — the same run sweeps zero-assignment tags: a tag whose
`orphaned_since` mark is older than `dead_tag_grace_days` (default 7) is deleted and
removed from the embedding store, and every assignment path clears the mark so the window
tracks real last usage. Seeded values are exempt. Names blocked by an operator are
skipped by the keep-flip path (and dropped by enrichment), so they cannot be resurrected
outside the restore flow.

**Identity evidence** — the reconcile does not resolve identity claims itself: the twin
arm inserts pending `same_intent` evidence and the separate scheduled `tagidentity` pass
resolves them through the shared resolver — a confirmed fold renames the tag (or aliases
it when the folded name already exists), a same-intent loser becomes an alias, a deleted
fold target is skipped, a deferral stays for review. The per-pair verdict cache settles
each claim once, so the twin arm and the identity pass converge on whichever runs first.

**Report** — each run persists a full JSON report as its task result: worklist counts per
category, judged/confirmed, flips by direction, aliases with reassignment counts, compound
splits, twin counts and the consumed measurement run, skip reasons, the provisional-sweep
counts, and the demotion block with per-trigger counts and named entries. Served by
`GET /api/v1/tags/reconciliation/report` and the task's `result` field; the web UI
renders it on the task detail page and as a status strip on the tags page. With the
adjudicator disabled, runs log the worklist snapshot and complete without LLM calls; the
only store action is the twin arm's mechanical variant aliases. The daemon warns when
reconciliation is enabled without the adjudicator.

## 10. Tag extension measurement

The measurement pass (`internal/tags/measure.go`, [algebra](../specs/tags/extension-algebra.md))
is the substrate behind every overlap question in this subsystem. The algebra
(`internal/tags/algebra.go`) is pure: mass, pairwise overlap with Jaccard/overlap/
containment ratios, the `≈` set-similarity number, reconstructibility with excess `E` and
deficit `D`, part coverage and productivity, complement views — no thresholds in the
operators. The pass reads one REPEATABLE READ snapshot and appends the readings to
`tag_measurement` in the same transaction; records carry the run id, snapshot instant,
rule-set version, operator, operands, raw numbers, evidence state, and caveats. The
live reads (`Store.ReconstructibilityBatch`, `LatestWorklists`, `SiblingWorklist`) mean
the decision never waits for a run.

Rule-set v1 derives the composition-review worklists: `≈` twin candidates (Jaccard ≥ 0.9
with both masses ≥ 3, gray band 0.7–0.9), compound split proposals (both leftovers ≤ 0.1,
gray to 0.25), and near-contained siblings (containment ≥ 0.8, gray 0.6–0.8). The
worklists are evidence: the reconciler's twin arm consumes the `≈` pairs, the sibling
surfaces read-only on the tags page's Overlap tab (`GET /api/v1/tags/siblings`) — nothing
is acted on at measurement time.

`enricher.tag_measure.{enabled, interval, time}` schedules a `tagmeasure` batch; a bare
`kushim tagmeasure` runs the pass under the manual maintenance hold. The pass measures;
it never judges — dispositions stay with the reconciler's arms and the operator surfaces.

## 11. Configuration

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
  #   report_only: true          # plans instead of acting (judgment evidence still persists); the first run is always report-only
  #   interval: 7                # days between runs
  #   time: "02:00"              # preferred time of day (HH:MM)
  #   max_rejudge: 200           # max pairs re-evaluated per run
  #   max_retag: 1000            # max documents re-pointed per run
  #   dead_tag_grace_days: 7     # days a tag must stay without documents before the sweep removes it
  #   provisional_grace_days: 30 # days a provisional tag may stay unreviewed before the sweep re-checks it
```

All three blocks are also editable on the Settings page ("Tag adjudicator", "Tag quality",
and "Tag reconciliation" cards), including an "Inherit from content analyzer" option for
the judge LLMs — leaving the LLM fields empty follows the content analyzer's model even
after it changes. The identity-candidate floor, the identity rule-set version, and the
quality band constants are internals; see the
[semantic matching guide](semantic-matching.md) for the per-model floor values.

## 12. Safety properties

- **Keep is the failure direction everywhere**: rank error, adjudicator
  disabled/unavailable/ambiguous, reconciliation disagreement — the emitted or stored
  state is kept, unresolved claims stay pending, and enrichment never blocks or fails on
  this subsystem.
- **Wrong keeps are visible and correctable; wrong replacements are silent and
  permanent** — hence the asymmetric strictness: similarity never decides. The only paths
  that change a name are M1 style rewrites, M3 key equality, the deterministic variant
  classes, a fold confirmed by all three criteria, and an explicit `same` verdict (with
  direction).
- **Same-intent resolution is an alias, never a deletion**.
- **Bounded automation** — policy-version and rule-set-version pinning, the
  double-orientation confirmation, `max_rejudge`/`max_retag`, and the report-only first
  run.
- **Dead names cannot resurrect** — the registration path re-checks the `deleted_tag`
  blocklist, fold application skips deleted targets, dead-target verdict rows are
  deleted, and every operator delete is recorded in the blocklist.

## 13. Gotchas

- **The adjudicator, quality judge, and reconciliation are disabled by default** — novel
  tags are born unfiltered, folds are not applied, and same-intent claims wait as pending
  evidence until the layers are enabled or the triggered run/sweep resolves them; both
  event tables stay empty until their layers run.
- **Quarantined tags are invisible to the vocabulary** — a newborn is attached and
  searchable from birth but stays out of the embedding store — and therefore out of
  suggestions, consolidation targets, and the all-names fallback — until the admission
  decision admits it and its assignments clear the promotion floor (10 live documents).
- **The first reconciliation run is always report-only**, even with `report_only: false`
  — flip behavior only starts with the second run.
- **`identity_candidate_similarity: 0` disables candidate generation** — `Rank` returns
  no candidates, so every name passes through. That is the documented knob, not a bug.
- **Verdicts are cache-scoped to their version constant** — bumping `policy.Version`,
  `IdentityRuleSetVersion`, `TagQualityVersion`, `TagPartDimensionVersion`, or
  `TagSplitVersion` retires that layer's cached rows at once; until the reconciler runs,
  every affected pair is re-judged fresh, bounded by the caps.
- **Identity and policy logs live in the kushim (enrichment) log, not `hugot.log`** —
  `hugot.log` only holds the older history the bootstrap parser reads.
- **Matcher dependency**: with the daemon down, tag CRUD and identity planning return 503
  and enrichment degrades to LLM-only tags (no identity resolution) — the pipeline
  degrades, it does not fail.
- **Rollout**: `kushim` and `edub` ship as one version over the matcher socket, so an
  upgrade needs no restart ordering. The three opt-in layers default to off, so an
  upgrade never silently changes tag behavior or adds LLM cost.

## See also

- [Tag specs](../specs/tags/overview.md) — the normative definition, pipeline, and
  mechanism specs these mechanics enforce
- [Semantic matching](semantic-matching.md) — the matcher daemon, embedding store, and
  thresholds
- [API guide](../manual/api.md) — tag review workflows;
  [configuration reference](../manual/configuration.md); [CLI reference](../manual/cli.md)
  — task types
- [Architecture](../architecture.md) — pipeline placement and process model
