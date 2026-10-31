# Tag canonical form and identity

Operationalizes the definition's canonicality predicate (P3,
[definition](definition.md) §4.3) and specifies the canonical-form policy the definition
records as decided (§8 item 3). It covers labels at birth — emitted candidates and the
atomic parts [atomicity](atomicity.md) derives — and is the birth-time half of duty D1 in
[pipeline](pipeline.md) §5; the retrospective store sweep stays with D1's canonicalization
work. The rules are organized by enforcement: a mechanical body (code-enforceable) and a
judging body (LLM-adjudicated), with the invariants that bind both and the seam between
them. No rule requires prior document-tag data (definition §4.5), and names arrive in the
store's established shape (§2).

## 1. Scope

- The **definition** is the authority: P3 ("one label per intent," §4.3) is the predicate
  being operationalized; §8 item 3 records the policy decided here.
- The **pipeline** places this mechanism in S3 (canonicality and registration); it
  supplies D1's birth-time half, and the direction decisions also apply when a candidate
  meets an existing store label.
- **[atomicity](atomicity.md)** is the sibling: it produces the atomic parts; this spec
  decides the form those parts take and when two of them are one value. It assumes the
  adjective folding specified here runs before its part lookup (§3.1 there).
- **Admission** is separate: whether a label is a dimension at all (facet, breadth,
  non-decomposability) belongs to the admission machinery; these rules only shape and
  identify labels passing through registration.

## 2. Givens

Already enforced elsewhere; listed so this spec does not restate them:

- English only; one to three words; facet ownership, place rule, and typed contract
  from the generation pipeline.
- Names arrive in the store's established shape: presentational normalization (case,
  accents, spacing, permitted marks) is a given, and this spec starts from the
  established name.
- The atom is a single label — an emitted candidate or a part derived by
  [atomicity](atomicity.md).
- Single-token labels carry no decomposition question; multi-word candidates arrive as
  their parts.
- Rows in the store that violate the givens (another language, longer than three words)
  are handled by separate cleanup work.

## 3. The mechanism

### 3.1 Invariants (bind both bodies)

**V1 — Lemmatize, never stem.** Stored labels come from inflection-aware reduction; a
stemmer's output is never written. Stem signatures may be computed for detection only.

**V2 — No denotation crossing.** No fold, rewrite, or selection may generalize,
specialize, or merge sibling concepts; where it would, the unreduced form stands.

### 3.2 Mechanical body (code-enforceable)

Everything in this body runs with code and pinned configuration, with no semantic
judgment; package dependencies are allowed. The target of the shape rules is the lemma
(singular countable nouns, uninflected verbs); the mechanical body detects shape
relations, proposes folds, and resolves the pinned variant classes (M8), and the judging
body confirms everything else (§3.4).

**M1 — Number and ordinal forms.** Word↔digit ordinals and roman numerals (`nineteenth
century` ↔ `19th century`, `world war ii` ↔ `world war 2`) via a rule-based converter.
The house style is **numeric**; the converter writes directly — these are style rewrites,
not semantic folds — and the other form is recorded as a matching variant.

**M2 — Shape signatures and fold proposals.** A dictionary-based lemmatizer and a
stemmer compute lemma/stem signatures used only to detect shape relations and to propose
folds. No proposal is written from here: folds that change the lexical form pass the
seam's gate (J2). Lemmatizer output is evidence, not verdict — its dictionaries will map
`data` → `datum` and similar. Package selection is a tooling concern; the roles here are
fixed.

**M3 — Key equality.** Identical key → identical label; resolved here without judging.
This covers everything M1–M2 collapse to the same key.

**M4 — Deterministic tie-break.** When the judging body's direction criteria do not
separate two labels, a fixed, versioned lexical order decides.

**M5 — Alias mechanics.** The loser of a same-intent pair becomes an alias: it resolves
to the canonical label in matching, suggestions, emitted names, and query input; it
never surfaces as a value itself; its assignments are re-pointed and its counts merge.
An alias is never deleted or blocklisted; its representation is schema work while these
semantics are fixed here.

**M6 — Measurement.** Extension comparison, near-containment, recurrence — raw counts
computed for later confirmation of birth calls; no disposition is taken here. A pair
judged same-intent stays merged unless measurement later shows materially different
extensions with independent recurrence (a later observation, not a birth-time
assumption).

**M7 — Plumbing.** Idempotence checks; the rule-set version is stamped on every
registration.

**M8 — Deterministic variant classes.** Two names are one value without judging when
they are pinned orthographic spelling variants (`-isation`/`-ization`, `-yse`/`-yze`,
`-ogue`/`-og`, `-our`/`-or`, applied per token) or literal token permutations (the same
tokens in a different order). The pair resolves as same-intent with no direction, so M4
picks the canonical label. Token permutation is whole-name canonicalization and is
explicitly **not** the decomposition fan: it does not split a name into value spans the
way atomicity §3.1 does. Plural and other inflections, mixed-language spellings, and
morphological or function-word inversions stay with the judging body (J3), as does any
pair the pinned table does not reach. The class is consulted at birth for every novel
name, so a store-anchored orthographic or permutation twin resolves mechanically without
a judge.

### 3.3 Judging body (LLM-adjudicated)

Every call here is model-adjudicated with pinned binary criteria, cached per candidate
or pair, and versioned with the rule set; split criterion answers are gray-band review,
never a forced call.

**J1 — Base-form decisions.**

- Relational adjectives fall to their noun when it denotes the same thing (`chilean` →
  `chile`).
- Inflections stand when they are the established name of the sense (`data`, `human
  rights`) or denote a different sense than the base (`arms` ≠ `arm`).
- Lexicalized derivations and gerunds stand (`programming`, `learning`, `running`); a
  derived word that names its own concept is never reduced to a root (`colonization` is
  not `colony`, and it is not reduced to `colonialism` either — that is J3/J4's decision,
  not a fold).

**J2 — Fold gate.** Confirms that a mechanically proposed fold preserves denotation
(V2) before registration. The gate covers every fold that changes the lexical form —
plural→singular, verb→lemma, adjective→noun. Style rewrites (M1) do not pass the gate.
When the gate fails, the proposal is rejected and the unreduced form stands.

**J3 — Same-intent test.** Two labels denote one value when faithful replacement holds.
Runs on shape-related pairs flagged by the mechanical body, on candidates within one
emission, and against store labels (§3.4, scenario 4.2). Subject: morphographic variants
and intent-equivalent derivations (`colonialism` / `colonization`).

**J4 — Canonical direction.** When J3 finds one intent, the canonical label is chosen
by, in order:

(a) **sense-fit** — the label whose standard sense is exactly the dimension wins; a
polysemous label loses to the precise one (`colonialism` over `colonization`, which also
denotes space and biological colonization);
(b) **house form / language** — where sense-fit does not separate the pair, an English
(house-form) label wins over a non-English one; a pinned deterministic variant class
carries no direction and keeps resolving through M8/M4;
(c) the deterministic tie-break (M4) where the steps above do not separate the pair.

Neither arrival order nor observed usage decides at birth; usage informs later
canonicalization only. A kindred-form criterion is rejected: with no axis model
(definition §8 item 7), there is no kindred membership to consult.

**J5 — Conventional forms beyond the converter.** Spelled-out symbols follow the form
the domain writes (`c plus plus` → `c++`), judged here with pinned criteria; no pinned
map exists in the mechanism (an operator override, if any, is configuration).

### 3.4 The seam

- **Code decides the mechanical cases; the judging body concludes the semantic ones.**
  M2 computes shape signatures; M3 resolves identity on equal keys; M8 resolves the pinned
  variant classes mechanically; every other semantic fold and shape-related pair goes to
  the judging body (J2, J3). A proposed semantic fold is never written unconfirmed.
- **Emission-first.** House form is primarily produced at emission: the typed contract
  asks the generator for labels already in house form, so J1/J5 decisions happen where
  the document is read. The mechanical body converts and validates what arrives; the gate
  exists for repairs.
- **Unrelated keys** never trigger a judging call on their own; they meet only through
  same-emission comparison or the store candidate list.
- **Placement.** The rules run at the registration of every label — emitted candidates
  and derived parts — and alias resolution runs whenever names are matched: suggestions,
  consolidation, and query input. A split-derived part may first resolve mechanically onto
  a **promoted** store label through the attested seam fold (the generated WordNet
  pertainym table's adjective→noun pairs, in WordNet sense order, or a pinned variant class
  the table does not cover; a part with more than one attested base folds mechanically only
  onto its **primary-sense** base, the table's first entry, and only while that base is
  live — a live sibling sense neither attracts nor blocks the fold); provisional births are
  never seam anchors, so an unattested near-spelling of an unjudged birth flows to the
  judging body instead of being rewritten. A multi-base part whose primary-sense base is
  not live while another attested base is is the residue the seam does not fold on a
  guess: it is routed to a **phrase-context fold** judged call ("in this phrase, does this
  part denote anchor X?"), cached per (phrase, part, anchor), which folds the part on a
  confirmed answer and holds the compound for review on an abstention. Shape-based
  proposals reach only the judged path, where the gate confirms them. The retrospective
  store sweep is D1's work, outside this spec.
- **Shared discipline.** Pinned criteria, per-candidate/pair caching, rule-set versioning,
  and gray-band deferral follow the existing adjudicator patterns; the mechanical body
  stays deterministic and idempotent.

### 3.5 Dispositions

**apply** (key equality → the existing value) · **register** (new value) · **alias**
(the loser of a same-intent pair) · **defer** (gray band). Two labels for which J3 finds
no same-intent register separately; a pair may be re-split or merged later through
measurement (M6) and canonicalization. A same-intent label is never deleted or
blocklisted.

### 3.6 Evidence record

Per candidate or pair: the key; the proposed fold and its gate result; the J3 verdict
with criteria and model versions (the M8 class, with no model, for a mechanically resolved
pair); the J4 direction and reason; any alias made; the rule-set version; the review
outcome when deferred. Consumers are D1's canonicalization work, the review surfaces, and
later calibration — it is evidence, not a verdict machine.

## 4. Scenarios and worked examples

**4.1 — No similar label in the store.** The candidate registers under the mechanical body
plus whatever J1/J5 decisions its shape requires; duplicates within one emission resolve by
J3/J4, never by arrival order.

**4.2 — The store holds a similar label.**

- Key equality (M3) → application of that value; no new registration.
- Same intent, different key (J3) → J4 decides; an existing store label is not exempt: if
  it is the loser it becomes the alias (M5), the winner is registered or promoted, and
  reconciling existing assignments is canonicalization work (D1) — aliasing, never
  deletion.
- Different intent → a new value through the same rules.

Behavior references; the gray rows are where review lands.

| Candidate(s) | Outcome | Rule |
|---|---|---|
| `repairing` | `repair` — fold proposed and gated | M2 + J2 |
| `chilean` | `chile` | J1 relational adjective |
| `programming` | stands | J1 lexicalized gerund |
| `data` | stands | J1 established plural |
| `arms` | stands (≠ `arm`) | J1 distinct sense |
| `nineteenth century` | `19th century`; variant kept | M1, numeric house style |
| `c plus plus` | `c++` | J5 |
| `decolonisation` / `decolonization` | one intent; the label the deterministic tie-break picks wins, the other aliases | M8 + M4 + M5 |
| `military history` / `history military` | one intent; the label the deterministic tie-break picks wins, the other aliases | M8 + M4 + M5 |
| `colonization` / `colonialism` | one intent; `colonialism` canonical, `colonization` aliases | J3 + J4(a) + M5 |
| any pair with split criterion answers | keep as written; review | §3.5 defer |
