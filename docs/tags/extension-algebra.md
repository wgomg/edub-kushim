# Tag extension algebra — working specification

**Status:** Working spec. It defines the measurement substrate — the extension algebra
the pipeline model calls the keystone (`pipeline-model.md` §6.3 item 1): what an
extension is as a computed object, which measurements exist over it, how they are
scoped, versioned, and trusted, and what each consumer receives. It defines
measurements, never dispositions. Measurements do not require prior document-tag data
to be defined, and no consumer may gate on measurements existing (prospective-first,
`pipeline-model.md` §7 item 11). Unqualified § references point into the
definition (`definition-and-faceted-retrieval.md`).

**Non-goals:** thresholds and calibration (every consumer's cut is calibration's);
schema or endpoint design (where measurements live); per-consumer policies and
dispositions (atomicity, canonical form and identity, and completeness own them); the
zero-data ladders (atomicity and completeness own them); axis grouping (rejected,
definition §8.7).

## 1. Relationship to the corpus

- **Definition** — the authority. §4.1–§4.2 define D, `S(t)`, and the intersection; §4.3
  the predicates P1–P5; §4.5 the limits: observed extensions, gray bands, lower bounds.
  The global scope is decided (§8.8).
- **`pipeline-model.md`** — the map. D4 is the keystone (§6.3 item 1); the
  prospective-first requirement (§7 item 11) binds every rule here; the conflicts it
  resolves read extension equality over similarity (§6.4 item 4) and measurement
  versioning (§6.4 item 10).
- **`atomicity.md`** — the sibling. Its per-compound measurements (existence,
  mass, intersection, `E`/`D`) are the compound case of the algebra; its evidence record
  generalizes into §3.5 here.
- **`canonical-form-and-identity.md`** — the sibling. Aliases resolve into
  canonical extensions; the store-side detector (D1) consumes `≈` equality, and the
  canonical-form rules decide the disposition.
- **`completeness.md`** — the sibling. Coverage counts and their classes are a
  consumer's view; audit triggers are worklists built from complement views.
- **S5** — the eventual consumer: its features come from here, and the staged operating
  mode means the algebra must serve pinned rules as well as any fitted refinement.

## 2. Givens

- **Global scope**: one universe, store-wide counts (definition §8.8, decided);
  grouping is analysis-time and outside this mechanism.
- **Canonical labels, aliases resolved** (14); provisional anchors are counted
  separately and never inflate promoted measures.
- **Live documents only**; the store version a measurement was computed against is part
  of the measurement.
- **Evidence, not verdicts**: raw numbers; no thresholds live here (§4 explains what
  each consumer does with them).
- **Zero-data mode**: a novel candidate or a fresh store yields an explicit
  *insufficient* state, not zeros posing as knowledge (§3.3); no consumer may require
  measurements to operate.

## 3. The algebra

### 3.1 Extension and snapshot

- `S(t)` is the set of live documents that carry `t`, with aliases resolved and
  provisional anchors tracked separately.
- Every measurement is relative to a **snapshot**: the store version and the rule-set
  version it was computed under. A measurement computed under an older version is
  stale, not wrong (§3.4).
- Measurements are **re-derivable**: the record carries both versions, and consumers
  pin the versions they use (measurement versioning, `12` §6.4 item 10).
- Extensions are **observed, not true** (definition §4.5): every measure is a lower
  bound under incomplete tagging; the completeness duty narrows the gap.

### 3.2 The operators

- **Mass** — `|S(t)|`; the count every consumer's thresholds are expressed against.
- **Overlap** — `|S(a) ∩ S(b)|` and coefficients (Jaccard, overlap) between values.
- **`≈` equality** — set similarity above the equality threshold (P3): the store-side
  identity signal for D1's detector.
- **Containment / redundancy** — `|S(a) ∩ S(b)| / |S(a)|` (P5): how much of `a`'s
  extension lives inside `b`'s; near-containment between siblings marks redundancy for
  the composition review.
- **Reconstructibility** — the compound case (P4): `S(c)` against `⋂ S(pᵢ)`, with
  excess `E` and deficit `D` exactly as `atomicity.md` defines them, generalized
  to stored compounds in the retrospective pass.
- **Part coverage and productivity** — for each part, the share of the compound's
  extension that carries it, and the part's reach across the store.
- **Complement views** — `S(a) \ S(b)` and the documents behind a reading; worklists
  are built from these, not from scores.

All raw numbers. Every cut is a consumer's, and every threshold is calibration's.

### 3.3 Evidence states

- **Sufficient** — observations exist in the quantity the consumer's pinned rule
  requires.
- **Insufficient** — the extension is thin or empty (a novel candidate, a fresh store):
  the measurement exists but supports no reading; consumers fall back to their
  zero-data ladders (`atomicity.md` §3.4; `completeness.md` §3.3).
- **Gray** — the value sits near a consumer's cut; the disposition defers to review,
  never forced.
- "Zero documents" and "no data yet" are different states; conflating them is the
  failure mode this section exists to prevent.

### 3.4 Computation mode

- **Retrospective pass** — batch computation over a store export (the tooling
  register's DuckDB entry), producing measurements for the existing corpus.
- **Refresh triggers** — store changes and rule-set or measurement version bumps; a
  refresh may be full or incremental, but the version always advances with the rules.
- **Cost policy** — which operators are computed eagerly and which on demand; the pass
  is offline, the runtime consumes its output.
- **Storage shape** is schema work (non-goal); the record's fields are the evidence
  contract (§3.5).

### 3.5 Evidence record

Per measurement: the operand values; the operator and its inputs; the raw number(s);
the snapshot version and the rule-set version; the coverage classes involved where
applicable (`15` §3.4); and the evidence state (§3.3). Consumers pin versions;
re-computation is always possible from the record.

## 4. Consumers

Thin, policy-light contracts. Each consumer receives measurements and produces
proposals or evidence — never automatic actions.

- **D1 · canonicalization detector** — `≈` pairs over stored values become alias/merge
  candidates; the disposition runs the canonical-form rules and alias semantics. Similarity alone
  never merges.
- **D2 · composition review** — reconstructibility and containment readings become
  worklists: stored compounds to decompose, near-contained siblings to review. Outputs
  act through 13's decomposition.
- **D3 · completeness** — coverage counts by class; the audit's triggers are worklists
  built from complement views, and its verdicts feed the calibration pool.
- **S5 · admission** — the feature vector (mass, overlap, part coverage,
  reconstructibility, redundancy) feeds the pinned rule set, and the fitted refinement
  consumes the same vector. Features are measurements of their extractors
  (definition §6.6).

## 5. Worked examples

| Reading | Measurement | Consumer and outcome |
|---|---|---|
| Twin labels (spelling variants) | `≈` above threshold | D1 alias worklist; 14 decides direction |
| Stored compound (`X repair`) | reconstructibility | D2 decompose worklist; 13 splits |
| Overlapping siblings | containment | D2 review reading; no automatic merge |
| `device disassembly` | part coverage / reconstructibility | D2: value or parts? — review decides |
| Novel candidate, empty store | insufficient | consumers fall back to zero-data ladders (13/15) |
| Thin sibling extension | insufficient / gray | review; no forced reading |

## 6. Open items

1. **Snapshot and versioning mechanics** — how store versions are named and pinned;
   when a measurement is recomputed versus invalidated on a version bump.
2. **Incremental versus full pass** — refresh cadence and scope.
3. **Storage shape** — schema work (non-goal here).
4. **Eager versus on-demand operators** — the cost policy.
5. **Consumer interface details** — what each consumer pins, how worklists are
   sourced; thresholds stay calibration's.
6. **Legacy accounting** — pre-13/14 artifacts in extensions (the pending-split and
   pending-canonicalization classes of `15` §3.4).
