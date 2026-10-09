# Tag definition and faceted retrieval

**Status:** Normative. Defines what a *tag* is in this system, derives the admission and
decomposition criteria from that definition, and specifies the governance model that
materializes it. This document is the source of truth for tag semantics; implementation
plans must cite it and must not contradict it implicitly.

**Component:** tag vocabulary and retrieval semantics — enrichment pipeline, tag store,
structured search, curation surfaces.

**How to read this document:**

- Want the definition? → §3.
- Want the mathematical form? → §4.
- Want to know how it judges the observed vocabulary? → §5.
- Want the curatorial/governance model? → §6.
- Want to know what it implies for existing machinery? → §7.
- Want the policies this definition settles? → §8.

---

## 1. Purpose and scope

### 1.1 The question this document answers

What is a tag? Not "what makes a good tag" — that question presupposes the answer — but
what *kind of object* a tag is: what it denotes, what its extension is, what makes one
string a tag and another not, and under what conditions a multi-word phrase may be a
single tag or must decompose.

The system has now built several layers of tag machinery — a typed generation contract,
facet hard gates, a rubric judge, quarantine and promotion, a human review queue, and
scheduled hygiene sweeps — and every one of them enforces a *side constraint* (subject
matter, standardness, breadth, recurrence) without any of them stating what the object is
supposed to be. That is why the vocabulary's drift (chronicled in §2) is
incoherent rather than merely noisy: the generator optimizes "faithful to the document,"
the judge optimizes "looks like a real concept," the promotion gate optimizes "recurs,"
and the vocabulary satisfies all three while being none of them.

This document supplies the missing object definition and derives every criteria question
from it.

### 1.2 What this document is not

- **Not an implementation plan.** No schema, endpoint, configuration key, query, or code
  change is specified. Where the definition has consequences for existing machinery (§7),
  those consequences are stated conceptually so a later, separate plan can implement
  them.
- **Not a facet-boundary decision.** Persons, works, and form/genre are owned by other
  facets (`People`, title/works, `document_type`), and places follow the existing
  country/continent policy with its whitelist. This document does not reopen those
  decisions; it constrains only what the **tag facet** may hold.
- **Not a hierarchy or typed-relations design.** Tag-to-tag relationship architectures
  are a separate concern. The definition here is intentionally flat: it says what a tag
  *is*, not what relations tags may have.

### 1.3 Why the definition must precede further machinery

Every mechanism downstream — the prompt, the gates, the judge, the splitter, promotion,
the review queue, the consolidation policy — makes a judgment about tag-hood. Without a
shared definition, each mechanism imports its own implicit one (the generator's is
"document relevance," the judge's is "dictionary standardness"), and their disagreements
surface as vocabulary defects rather than as policy ambiguities. Defining the object makes
each mechanism's job derivable, auditable, and mutually consistent.

---

## 2. Context

### 2.1 What tags are in this system

Documents are enriched by an LLM that emits a set of tags alongside title, type, and
people. Tags are stored in a shared vocabulary and then used for several consumers:

- **Filtering and structured search.** Documents can be filtered by one or more tags;
  multiple tags in a query are composed with AND (intersection).
- **Consolidation.** Every emitted name is mapped onto the existing vocabulary before
  persistence (identity, similarity bands, adjudication); genuinely new names are born.
- **Suggestion.** The vocabulary is embedded in a matcher store and injected into the
  generator prompt as candidate suggestions.
- **Curation.** The tags page exposes the vocabulary, merge review, provisional-tag
  review, and the blocklist; scheduled reconciliation re-examines decisions and sweeps
  stale rows.

A tag is therefore not a display label only: it is the unit of the shared index and of the
consolidation machinery. Its granularity determines what queries the corpus can answer.

### 2.2 The observed state

The measured state of the vocabulary (live-database snapshot, 2026-09-17):

| Measurement | Value |
|---|---|
| tags | 13,689 (4,381 promoted / 9,308 provisional) |
| words per tag | 2,152 one-word · 9,801 two-word · 1,736 three-word · 0 four-word |
| assignments | 239,358 rows over 53,889 documents; average 4.4 tags/document, maximum 9 |
| usage tail | 6,546 zero-document · 2,735 one-document · 1,859 at 2–4 · 1,474 at 5–19 · 1,074 at ≥ 20 |
| blocklist | 258 deleted names |
| promotions | none on 2026-09-16 and 2026-09-17 despite 44 births |

Two corpora share one store: a repair/teardown corpus (`guide` documents; `device
disassembly` 15,578 documents, `smartphone repair` 6,479) and a political-philosophy
corpus (`anarchism` 12,695, `philosophy` 2,460, `capitalism` 2,265).

### 2.3 The observed failure modes

The vocabulary is drifting in **two directions at once**, and both are dictionary-shaped:

1. **Toward pre-coordinated phrases** — compounds that name a combination of concepts as
   if it were one concept:
   - connector families: 84 tags containing `of` (`philosophy of mind`, `history of X`,
     `critique of X`), 16 containing `in`;
   - country-adjective compounds: 322 tags (`chilean history`, `russian history`),
     alongside their unused noun-head twins (`chile history`, 0 documents);
   - generic-head families: 190 tags ending in `repair`, 163 in `maintenance`, 159 in
     `theory`, 138 in `history`, 322 country-adjective combinations, and 930
     device-plus-operation tags (519 provisional);
   - 291 two-word tags whose two components each exist as promoted standalone tags
     (`sql programming`, `ritual violence`, `palestine solidarity`).
2. **Toward dictionary-generic single words** — broad concepts with no retrieval
   function: `use` (promoted, 54 documents), `art`, `law` (44), and the abstract nouns in
   the zero-document tail (`thought`, `knowledge`, `irony`); normalization debris such as
   `s` (promoted, 6 documents), `g`, `may`.

A pre-existing failure taxonomy maps onto the same split:

- **F1 — thesis compression:** the document's argument compressed into a phrase no other
  document can emit (`racial scapegoating` for an analysis of the opioid crisis). Faithful
  to the document; useless as a shared key.
- **F2 — form/genre as topic:** `literary anthology`, `personal letters` — describing
  what the document *is*, which the `document_type` facet already owns.
- **F3 — place leaks:** `newfoundland`, `libya` — facet violations.
- **F4 — wrong-granularity device tags:** `bail spring replacement`, `calculator
  teardown` — legitimate repair-corpus topics born at a granularity that never recurs
  while broad covering tags already exist.

Plus a class the earlier taxonomy did not name: **equivalence fragmentation** — the same
retrieval intent held by several labels that cannot merge: 123 singular/plural pairs
(`programming language` 4 docs / `programming languages` 239; `factory occupation` 89 /
`factory occupations` 0), orthographic variants (`decolonization` / `decolonisation`),
inverted twins (`history of anarchism` / `anarchist history` / `anarchism history`), and
mixed-language compounds (`marxism anarquismo` / `marxism anarquism`).

The unifying observation: the vocabulary behaves as if it were an encyclopedia of subject
headings while the system's search behaves as a facet filter. Two different objects; one
store.

### 2.4 Why the inherited admission tests produce the drift

No layer in the current pipeline tests for "reusable dimension of retrieval." Each test
that exists covers a proxy:

- **"Relevant to the document"** (the generator's implicit standard) produces F1: a
  faithful compression of a unique argument is the *most* relevant string for its
  document and the *least* reusable one.
- **"Standard, widely-recognized terminology" and "appropriately broad"** (the judge's
  criteria) are dictionary criteria. They admit pre-coordinated named concepts (`russian
  history` is a standard term; `calculus of variations` is a recognized field) and generic
  dictionary words, while rejecting corpus-valid narrow values: six or more of the
  fourteen observed judge rejects are repair-corpus terms (`nut driver`, `jack stands`,
  `cotton swab`) rejected as `subject_matter: false` — in a corpus where those are
  perfectly usable filters. The rejection pattern is not arbitrary: it is the judge
  model's **population prior** — its memory of what counts as standard terminology in
  general text — applied to a specialized corpus. A corpus-specific standard (which the
  definition supplies) is being requested from a general prior; §6.6 treats the resulting
  verdict as one feature among several rather than as the decision.
- **"Recurs"** (the promotion gate) is a regularity signal, not a quality signal: it
  admits systematic model habits (`chilean history`) and cannot, alone, distinguish a
  dimension from a recurring quirk.

The drift is what a vocabulary looks like when every layer has a proxy and none has the
object.

### 2.5 The definition that was missing

The rest of this document states the object, formalizes its criteria, applies them to the
observed classes, and describes the governance model that materializes them.

---

## 3. The definition

### 3.1 Core statement

> **A tag is a reusable dimension of content-meaning: a broad, stable, canonical,
> non-decomposable unit of faceted retrieval, whose retrieval meaning is its intersection
> with other tags.**

Three consequences are packed into that sentence and are each developed below:

1. **Specificity belongs to the query, not the tag.** A tag is broad by design; precision
   emerges from intersecting several tags. `cheating` and `uncle` are each vague; their
   conjunction is a precise retrieval.
2. **Meaning is extension.** A tag denotes the set of documents it selects, not a
   dictionary definition. Two labels that select the same set are the same tag.
3. **The unit is the smallest retrievable dimension.** A phrase that conjoins two
   dimensions is not one tag; a phrase that names unique content (an argument, a plot, a
   one-off observation) is not a tag at all.

### 3.2 The primitive: the retrieval act

The system already has free-text search for unique content. The marginal value of a tag is
not that it is searchable — everything is searchable — but that it is **shared,
canonical, and composable**: the same string across many documents, forming a filter that
intersects with other filters, autocompletes, feeds the consolidation machinery, and
anchors the embedding store. A tag is what a search string becomes when it is worth
naming, sharing, and intersecting.

This is why "is this a real concept?" is the wrong admission test. Dictionary status is
about *concept-hood*; tag status is about *retrieval behavior*. Many dictionary concepts
make useless tags (`irony`, `agency` — present in the zero-document tail); many
non-dictionary strings make useful ones (repair jargon, product names, artifact classes).
The document's uniqueness — its thesis, its plot, its specific argument — lives in the
title, the summary, and free text, and never in its tags. "Content-meaning" here means
**recurring categories of use**: what the document shares with others, not what
distinguishes it.

### 3.3 The reference model

A useful external reference (the operator's example) is the axis-style tagging used by
booru-family content sites, such as nhentai: a moderator-curated vocabulary, plus
user-suggested tags accepted through a moderation/voting process. Its tags are short
content-meaning keywords (`cheating`, `uncle`, `milf`); a typical search is the
aggregation of scoped terms (`tag:cheating tag:uncle tag:milf`), where each term alone is
broad and unspecific and the conjunction is a precise scenario. Nothing in the system
stores "the specific scenario" as a tag; the scenario is composed at query time.

The same model has been converged on independently by tag-recommendation systems (the
Stack Overflow literature formulates tagging as closed-set prediction over an existing
tag set precisely to avoid "tag ambiguity and tag explosion"), and by library science's
distinction between subject headings and post-coordination:

- **Pre-coordination** bakes a combination into one label (`calculus of variations`,
  `russian history`). It is the LCSH style: catalog once for browsing, enumerate the
  combinations as headings.
- **Post-coordination** keeps the dimensions separate and composes them in the query
  (`russia` AND `history`). It is the facet-filter style: the vocabulary stores dimensions,
  the query composes specificity.

This system's search layer is already post-coordinated (multi-tag filters are AND). The
vocabulary is what does not obey the model.

The reference model's preconditions are worth naming, because they are the adoption
preconditions here too:

1. **A bounded domain with a learnable axis set.** The content space is narrow enough
   that a few hundred values span it; the tag set behaves like a vocabulary, not like a
   topic census.
2. **Near-orthogonal dimensions.** The tags come from different dimensions (dynamic, role,
   participant archetype), so intersections add information rather than redundancy.
3. **A completeness convention.** Every work carries every applicable tag. Intersection
   search presumes this: a partially tagged dimension silently loses documents, and the
   searcher concludes the content does not exist.

### 3.4 The five properties of a tag

A string is a tag in this system iff it satisfies all five:

1. **Facet ownership.** It classifies subject matter — what the document is *about*. It
   is not a person, not a work, not a form/genre (those have their own facets), and not a
   place outside the existing country/continent policy.
2. **Dimensionality.** It is a value on a dimension of content-meaning: the kind of thing
   a searcher would use as a filter, alone or in combination, expecting other documents
   to share it. This is the "broad by design" property: a value too specific to recur is
   not a dimension.
3. **Recurrence.** It partitions a recurring subset of the corpus. A one-document phrase
   is a candidate, not yet a tag; a zero-document phrase is not a tag at all.
   Prospectively, every label is born below the floor and accrues recurrence: recurrence
   gates standing (promotion), not existence (§6.5).
4. **Canonicality.** One label per retrieval intent. Variant labels for the same intent
   (singular/plural, orthographic, inverted, digit/word) are the same tag and must
   collapse to one.
5. **Atomicity.** It is not faithfully replaceable by the intersection of other tags. If
   the intent it represents is reconstructible by combining existing tags, it is a
   pre-coordinated artifact and must decompose.

Properties 1–4 are hygiene; property 5 is the answer to the multi-word question.

### 3.5 Meaning is extension

For retrieval purposes, a tag's meaning **is** the set of documents it selects. This
resolves the recurring "words vs concepts" debates mechanically:

- `bike repair` and `bicycle repair` select the same documents (under complete tagging):
  same meaning, two labels — a canonicality defect, not a synonym judgment call.
- `russian history` selects (approximately) what `russia` AND `history` select: same
  meaning as an intersection, so it is redundant as a tag.
- `black bloc` selects a tactical/grouping phenomenon that `black` AND `bloc` could never
  select: distinct meaning, legitimately one tag.

### 3.6 The admission trichotomy

For any candidate string (or compound), the definition yields one of three dispositions:

| Candidate names… | Disposition | Example |
|---|---|---|
| one dimension | **admit** (subject to recurrence and canonicality) | `repair`, `russia`, `philosophy`, `black bloc`, `internet of things` |
| a conjunction of dimensions | **split** | `russian history` → `russia` + `history`; `air fryer repair` → `air fryer` + `repair`; `philosophy of mind` → `philosophy` + `mind` |
| no dimension | **drop** | `racial scapegoating` (a thesis); `use`, `thought` (containers with no retrieval function) |

And one special class that the trichotomy must not mis-handle: **lexical units, idioms,
and recurring named units whose parts are not dimensions**. `internet of things`,
`black bloc`, `cold war`, `machine learning` name one thing; decomposing them produces
noise (`things`, `bloc`, `cold`). These are admitted whole **iff they pass recurrence** —
they are the axis value "named thing/unit," not a pre-coordinated combination. The
distinction between this class and the split class is not "is it a name or an idiom" but
**"do the parts function as retrievable dimensions"** — which §4 formalizes.

---

## 4. Formalization

This section states the definition in set terms. The formalization is what makes the
criteria audit-able and what implementation details will rest on. It is an idealization,
and §4.5 states exactly where it stops.

### 4.1 Corpus and extensions

Let `D` be the corpus: the finite set of live documents.

Each tag `t` denotes a predicate over `D`; its **extension** is the set of documents it
selects:

    S(t) ⊆ D

Two notes on readings:

- Operationally, `S(t)` is observed as the set of documents currently carrying `t`
  (live assignments). Under complete and consistent tagging, the observed set equals the
  predicate's true extension; §4.5 addresses incompleteness.
- The extension is the tag's **meaning for retrieval**. Everything below is phrased over
  extensions, never over definitions, etymology, or "concept-hood."

### 4.2 Search as intersection

A query is a finite set of tags:

    Q = {t₁, …, t_k}

and its result is the intersection of the extensions:

    result(Q) = S(t₁) ∩ S(t₂) ∩ … ∩ S(t_k)

This is exactly what the system's multi-tag filter does (multiple tags are ANDed). Three
properties follow:

- **Broad tags, precise queries.** Each `|S(tᵢ)|` may be large; the intersection may be
  small. Precision is a property of the query, not of any single tag. The reference
  model's `tag:cheating tag:uncle tag:milf` is `S(cheating) ∩ S(uncle) ∩ S(milf)` — a
  small, coherent set assembled from broad operands.
- **Intersections are informative across dimensions.** An intersection adds information
  when the operands are not redundant. If `S(a) ⊆ S(b)` (or nearly), `a` AND `b` adds
  nothing beyond `a` — a redundancy signal (canonicalization/merge territory, not
  necessarily a defect if the narrower value is itself a recurring dimension).
- **Every tag is a coordinate; every query is a point.** The vocabulary stores dimensions;
  the query space is what gets composed.

### 4.3 The predicates

Written as properties of extensions. Let `m` be a recurrence floor (the current system
uses 10 as its promotion minimum; axis health in a mature vocabulary wants higher floors
for the core).

**(P1) Facet ownership** — precondition, not a set test: `t` belongs to the subject
facet; persons, works, form/genre, and out-of-policy places are excluded by the existing
facet machinery. The formalization below applies only within the subject facet.

**(P2) Mass (recurrence)** —

    |S(t)| ≥ m

A value below the floor is a pending candidate; a value with `|S(t)| = 0` is not a value
at all.

**(P3) Canonicality** (one label per intent) — for labels `t`, `u`:

    if S(t) ≈ S(u) then t and u denote the same value
    ⇒ keep one label as canonical; alias/merge the other

"≈" is approximate equality of sets (mutual containment above a threshold; §4.5). All the
variant classes observed in the vocabulary — singular/plural, `-isation`/`-ization`, inverted
order, digit/word, mixed-language forms — are expected to satisfy `S(t) ≈ S(u)` under
complete tagging and are therefore canonicality candidates, not separate values.

**(P4) Atomicity / reconstructibility** — for a candidate `c` with parts
`p₁, …, p_k`:

    reconstructible(c)  ⟺  S(c) ≈ S(p₁) ∩ … ∩ S(p_k)

The disposition depends on whether the parts are themselves values:

- **Parts are values (dimensions):**
  - `S(c) ≈ ⋂ S(pᵢ)` → `c` is **redundant**: it adds nothing to the algebra. **Split**
    into the parts (if the whole survives after splitting as its parts), or drop if the
    parts are below mass.
  - `S(c) ⊊ ⋂ S(pᵢ)` (materially smaller than the parts' intersection) → `c` selects a
    relation or narrowing the conjunction does not express. This is the *relational*
    case: `ritual violence` is not `ritual` ∩ `violence`; `palestine solidarity` is not
    `palestine` ∩ `solidarity`. `c` **may** be a distinct value; assess mass, and whether
    the relation is itself a recurring subject of the corpus. If not, split and let the
    relation fall back to free text.
- **Parts are not values (dead parts):** the split test cannot apply because
  `S(pᵢ)` is not a recurring set at all (`air` from `air fryer`, `things` from
  `internet of things`). `c` is then either a **lexical/named unit** or a **compression**,
  and mass decides:
  - `|S(c)| ≥ m` → admit whole (a named unit: `internet of things`, `black bloc`,
    `cold war`, `machine learning`);
  - `|S(c)| < m` → drop (`racial scapegoating`).

**(P5) Relation to other values (redundancy)** — for any two values `t`, `u`:

    S(t) ⊆ S(u) with high containment ⇒ t filters a subset of u

One-way containment is not automatically a defect: a narrower recurring value can be a
legitimate dimension of its own. Mutual containment is P3 (same value, two labels). The
signal matters for canonicalization and for evaluating whether a candidate is a distinct
dimension or a duplicate.

### 4.4 The composition law: why pre-coordination explodes

The vocabulary should be sized by the number of dimensions, not by the number of
expressible queries. This is the formal content of the "encyclopedia drift" observation.

- **Post-coordinated vocabulary.** With `k` dimensions carrying `v` values each, the
  vocabulary needs on the order of `k · v` symbols, and the query algebra can express any
  conjunction of the values — on the order of `2^(k·v)` distinct queries.
- **Pre-coordinated vocabulary.** Naming conjunctions as individual symbols means the
  vocabulary grows with the *number of conjunctions one wants to express*: enumerating
  all combinations of up to `r` values requires on the order of `Σ_{i≤r} C(k,i)·v^i`
  symbols. The observed families — 190 `repair`, 163 `maintenance`, 159 `theory`, 322
  country-adjective tags — are this enumeration in progress.

Two costs come with every pre-coordinated tag:

1. **Deleted queries.** `russian history` cannot answer "all documents about Russia"
   (unless `russia` also exists) and cannot answer "all documents about history"; and it
   cannot be intersected with a new modifier (`19th century`) without birthing yet
   another compound. Pre-coordination compresses the vocabulary by discarding the
   algebra that is the tag's entire reason to exist.
2. **Fragmented coverage.** Each compound is one more label whose documents are not
   reachable through the dimension it embeds — the inverse of the completeness
   precondition (§4.5, §6.4).

The economical arrangement is: **dimensions are tags; combinations are queries.**

### 4.5 Idealization and its limits

The set tests above hold exactly only under **complete and consistent tagging** — the
completeness convention of §3.3. The system's tagging is produced by an LLM with
variable recall, so:

- `S(t)` is an **observed** extension, not the true one. Approximate equality and
  containment must be computed as set-similarity measures (e.g., Jaccard or overlap
  coefficients) with thresholds, not as exact equality.
- The predicates are therefore **evidence-based tests with a gray band**, not decidable
  properties. The gray band is not a defect of the formalization; it is where evidence
  and human arbitration live.
- The math does not fix its own thresholds. "Faithful replacement," "recurring," and
  "worth filtering by" are thresholds over evidence, and the definition fixes the
  *direction* (which comparison to make) while evidence and curation fix the *cases*.
- The **preference half is not in the math at all**: extensions measure what documents
  *contain*, not what searchers *want*. §6.3 states the consequence.

One further limitation: where tagging is incomplete, an intersection result is a
*lower bound* — the query is correct, the corpus coverage is not. This is why
completeness (§6.4) is a first-class duty and not an optimization.

One more limitation is the **prospective case**: the set tests state the target
semantics, not a data prerequisite. A novel candidate has no observed extension, and a
fresh corpus has no document-tag relationships at all; the evidence is then composed
from what exists at emission — the candidate's parts and shape, the values already
present, and the model's reading — with observed extensions confirming or revising the
judgment as they accrue. No mechanism may require prior document-tag relationships to
operate.

---

## 5. How the definition judges the observed vocabulary

This section states the verdicts the definition yields for the observed classes.

### 5.1 Class-by-class verdicts

| Observed class | Predicate analysis | Disposition |
|---|---|---|
| Connector phrases: `philosophy of mind`, `history of X`, `critique of X` | parts are values; `S(c) ≈ S(philosophy) ∩ S(mind)` | **split** |
| Lexical/idiomatic connectors: `internet of things`, `black bloc`, `cold war`, `state of emergency` | parts are not values | **keep whole** iff recurring; otherwise drop |
| Relational compounds: `ritual violence`, `palestine solidarity` | `S(c) ⊊` the parts' intersection | **keep** iff the relation is a recurring subject; otherwise split, relation falls back to free text |
| Country-adjective compounds: `chilean history`, `russian history` | adjective is a surface inflection of the entity value; `S(c) ≈ S(chile) ∩ S(history)` | **split**; canonical entity form is the noun (`chile`, not `chilean`) |
| Generic-head compounds: `X movement`, `X theory`, `X repair` | parts are values (heads are dimensions when they pass mass) | **split** into modifier + head |
| Device-plus-operation tags: `air fryer repair`, `bail spring replacement` | parts are values (device class × operation) | **split**; the operation dimension (`repair`, `maintenance`, `disassembly`, `replacement`, `teardown`) must exist as values |
| Both-parts-exist compounds: `sql programming`, `chemistry education` | `S(c) ≈` the parts' intersection | **split** |
| Thesis compressions (F1): `racial scapegoating` | names no dimension; extension ≈ singleton | **drop** |
| Form/genre (F2): `literary anthology`, `personal letters` | facet violation (`document_type` owns it) | **drop from tags** (facet machinery) |
| Places (F3): `newfoundland`, `libya` | facet policy | **drop** per existing place policy |
| Singular/plural, orthographic, inverted twins | same extension, different labels | **canonicalize** (alias/merge) |
| Normalization artifacts: `st century`, `argentina s`, `s`, `g` | malformed labels; no dimension | **repair or drop** |
| Generic containers: `use`, `thought`, `knowledge`, `irony` | no dimension; `|S(t)|` small or semantically empty | **drop** (or leave below mass) |
| Mixed-language compounds: `marxism anarquismo` | same intent as English form; canonicality | **canonicalize** (canonical-form policy, §8) |
| Judge's repair-corpus rejects: `nut driver`, `jack stands` | values on a tool/part dimension of the repair corpus | **admissible** under this definition (rejected only by the inherited dictionary standard) |
| `freedom of speech` (902 docs) | parts are values; the AND approximates the intent closely | **gray**: canonicality/atomicity test on observed overlap; evidence decides |
| `calculus of variations` (6 docs) | parts are weak values (`variations` classifies nothing) | **gray**: decide by mass and whether the field is a recurring dimension of the corpus |

### 5.2 The definition judges the existing vocabulary, not only new emissions

Two consequences that a pure admission policy would miss:

- **The promoted vocabulary contains violations.** `device disassembly` (15,578
  documents) raises the question whether `disassembly` is the value with `device` the
  unmarked case; `smartphone repair` (6,479) is `smartphone` × `repair`; the country-
  adjective and generic-head families are largely promoted. The definition is retroactive:
  it defines an **audit worklist**, not just an admission gate.
- **The decomposition targets often do not exist.** The broad anchors are exactly what is
  missing: `repair`, `maintenance`, `theory`, `critique`, `analysis`, `movement`,
  `culture` exist at zero or one documents (or only as heads inside compounds). A
  split of `X repair` lands on a provisional empty anchor today. The definition therefore
  implies vocabulary *repair* — creating and curating the missing dimension values — as
  an operation of the same rank as preventing new compounds.

### 5.3 What this section does not re-open

Persons, works, form/genre, and the place whitelist remain governed by their existing
facet decisions. The definition constrains the subject-tag facet only; the fact that
`foucault` (a person) lives provisionally as a tag is a facet-enforcement gap under
existing policy, not a new question here.

---

## 6. Governance: who plays which role

### 6.1 The reference model's social structure

In the reference model, the tag vocabulary is maintained by a division of labor:
users suggest and apply tags; a moderation process (partly votes, partly appointed
moderators) accepts, rejects, renames, aliases, merges, and retires them; a tagging
convention keeps application complete. The system's job here is to occupy the offices
that the reference model fills with people, with machinery and policy — not because
automation is preferable, but because the generator produces tags at machine rate.

### 6.2 The role mapping

| Reference model | This system | Notes |
|---|---|---|
| Users suggest tags | the LLM proposes (emission) | In the reference model, most user activity is *applying* an existing vocabulary; invention is rare and deliberate. The LLM applies and invents in one gesture, so the pipeline must separate the two structurally: consolidation resolves emissions onto existing values (application); only the residue is an invention proposal. |
| User votes | recurrence across documents | A regularity signal, not a preference signal; human use is not a vote (see §6.3). |
| Moderator admission | judge verdict + promotion gate + review queue | The standard must be the definition's (dimension status), not the inherited dictionary standard (§2.4). |
| Moderator aliasing/merge | consolidation + merge review + persisted dismissals | Already exists; under the definition it becomes standing work, not incident response (§6.4). |
| Moderator bans | deleted-tag blocklist | A rejected name cannot be reborn. |
| Tagging guidelines | the generation prompt and typed contract | The definition's language ("dimension", "decompose compounds") belongs in the guidelines the proposer reads. |
| Approved vocabulary | the promoted tag store | Store membership is the system's notion of "accepted." |
| Vocabulary fluidity (rename, alias, retire) | one-way promotion today | The definition argues for revisable influence; see §6.5. |

### 6.3 Two limits of the voting analogy

**The electorate is correlated.** In the reference model, votes come from independent
users; agreement is evidence. Here, recurrence is repeated draws from one model with one
prompt: a systematic quirk (`chilean history` compounds, a preference for `X movements`)
recurs exactly like a genuine dimension does. Formally, agreement among correlated
samples carries much less evidence than agreement among independent ones. Consequence:
**recurrence is a weak prior on regularity — it distinguishes an idiosyncratic
compression from a systematic proposal, not a good value from a bad one.** Admission
must rest on the dimension test (P2–P5); recurrence supports it and cannot replace it.

**Usage does not signal.** The reference model's votes express what users *want to
find*; extensions measure what documents *turn out to contain*. This system reads no
preference half from behavior: recurrence is a regularity signal, and usage — manual
assignments, review actions, and any hypothetical search or filter logs — carries no
weight in admission, ranking, or demotion (§8.6). Dimensionality is judged from the
value itself (the kind of thing a searcher would filter by), never from observed use,
and users remain free to use the store for anything, including pure archival, where no
usage signal could exist. The review surfaces serve error correction and gray-zone
arbitration only; preference-like behavior, if ever wanted, is an explicit opt-in
feature outside this mechanism.

### 6.4 The duties with no owner today

Three curatorial duties follow from the definition and have no assigned machinery:

1. **Canonicalization** (one label per intent). The variant classes — singular/plural,
   orthographic, inverted, digit/word, mixed-language — are not incident-level merges;
   they are a standing hygiene operation over the vocabulary. The merge-review and
   consolidation machinery already provide the mechanics; what is missing is the standing
   mandate.
2. **Composition review.** The compound classes — connector phrases, country
   adjectives, generic heads, device-plus-operation tags — are the inverse of admission:
   the worklist of existing tags whose extension analysis says "pre-coordinated
   artifact." The observed catalogs (§2.3) are the first worklist. The review also
   covers the inverse question for existing broad tags (`device disassembly`: value
   `disassembly`, or value `device disassembly`?).
3. **Completeness** (application recall). Intersection search is only as true as its
   tagging coverage. The generator is recall-poor on exactly the broad values the model
   needs — `repair` does not exist as a tag while 930 compounds carry its information.
   A half-applied dimension is worse than a missing one: the search engine will trust it
   and silently under-return. Completeness is therefore a quality dimension of the same
   rank as admission: for each document, are the applicable dimensions present?

### 6.5 Revocable influence

The definition's model of a vocabulary is a living one: dimensions are discovered,
labels are canonicalized, combinations are retired as they decompose into their values.
The reference model reflects this — its vocabulary is fluid by design; moderators rename,
alias, merge, and retire continuously.

The current system's conservatism — quarantine gates influence, promotion is one-way —
was justified by the premise that a wrongly born tag is *permanent pollution*. Under the
definition, that premise splits:

- **Existence** of a label on documents stays (a document's labels remain coherent; the
  blocklist handles names that must never return).
- **Influence** — store membership, suggestion injection, consolidation targeting — is
  what a wrong value pollutes, and influence is revocable.

The existing principle "quarantine gates influence, not existence" therefore extends
naturally from birth to lifecycle: **demotion** is quarantine applied later, and the
one-way-promotion doctrine is retired. The demotion policy (§8): supersession and the
scheduled rule-reapplication sweep revoke influence only, with assignments and history
intact.

### 6.6 The decision layer: features, not verdicts

A structural principle follows from the limits above. No single signal — not the LLM
judge's verdict, not recurrence, not one set predicate — should *be* the decision. Each
is a **feature**: the judge's per-criterion booleans, the definition's set predicates
(P2–P5), embedding similarity, stability across repeated samples, and human actions are
all measurements of the same latent question ("is this a reusable dimension?"). The
decision — admit, keep provisional, split, drop, demote — belongs to a **decision over
those features**: a pinned, versioned rule set as the operating mode, which runs with
zero observations, and a fitted, label-grounded layer as an optional refinement where
labels accrue.

The reasons are the classical ones for preferring a model over a hard verdict:

- **Calibration and threshold control.** A hard pass/fail carries no error control: its
  threshold cannot be traded against a tolerated bad-birth rate, and its confidence is
  not usable. A decision over features yields a score whose threshold can be set
  deliberately — pinned by policy at zero labels, calibrated against a tolerated
  bad-birth rate once labels exist.
- **Structured evidence.** Mass, canonicality overlap, reconstructibility, redundancy —
  the definition's predicates — are computable facts the judge never sees and cannot be
  trusted to incorporate from prose.
- **Auditability.** The layer's weights expose which evidence drives decisions; the
  judge's verdict remains one input whose marginal contribution over the deterministic
  predicates is itself a measurable quantity (the nested comparison: predicates alone,
  plus judge features, plus labels). A fitted tree's split structure is an even more
  legible audit artifact — which evidence decides first, and on what it depends.
- **Abstention and deferral.** A hard verdict has no way to say "insufficient
  information": the model either picks a label or errors. Empirical work on LLM judges
  used as features quantifies the alternative — forcing a decision on every case buys
  full coverage at visibly lower precision, while an explicit "neither / defer" output
  raises precision to roughly 85–91% while leaving most cases undecided. Abstention is
  the mechanism that turns uncertainty into deferral instead of a coin flip; deferred
  cases land in the review queue (the repeat-disagreement rule covers instability, not
  admitted uncertainty).

Three consequences must be inherited with the principle:

- **Labels are the scarce asset.** A fitted layer is only as good as its labels: the
  review queue is not merely an error-correction surface but the label source, and label
  hygiene applies — binary labels, input parity with the generator, and no training on
  biased or mismatched labels. Scarcity degrades the fitted refinement but never gates
  the operating mode: pinned rules and criteria run with zero labels, and fitting
  upgrades only if labels accrue — a deployment may start with no document-tag data and
  never produce any.
- **The target is non-stationary and the loop is closed.** Admission decisions change
  the store, the suggestions, and therefore the emissions that produce future labels.
  Where a fitted refinement is used it must be monitored for drift and refitted
  periodically; a one-shot fit is a snapshot, not a policy. The pinned rule set is
  versioned, and its changes are deliberate.
- **Features are measurements of their extractor.** A judge-model or judge-prompt change
  shifts the feature distribution even when the inputs are unchanged — a pinned judge
  model is a measurement version, and a fitted refinement must be re-validated (or
  refitted) when its feature extractors change.

This is compatible with — and generalizes — the label-gated directions the quality
machinery already anticipates (calibration of the accept cut; distillation of the judge
into a small runtime model). The point is not merely to calibrate the judge: the whole
admission decision is a decision over features. The decision is the staged form (§8):
the pinned rule set is the operating mode and fitting is an optional refinement, never
a precondition. That its decisions be feature-combined and auditable is the normative
statement; label-grounding belongs to the refinement.

---

## 7. Consequences for the existing machinery

Conceptual deltas only. No implementation decisions are made here.

### 7.1 Criteria re-derived

| Layer | Inherited standard | Definition-derived standard |
|---|---|---|
| Generation prompt | "thematic tags" relevant to the document | emit values on dimensions; decompose compounds into their values; unique content stays in title/summary |
| Typed category gate | facet ownership (unchanged in mechanics) | confirmed by the definition; the gate is this document's P1 |
| Weak score / judge | "standard, recognized terminology"; "appropriately broad" | "is this a reusable dimension?" — queryable, recurring, canonical, non-decomposable. The expected reject class changes: pre-coordinated compounds become rejects; repair-corpus values like `nut driver` become admissible. |
| Joint splitter | conjunction detection (`and`, `or`, `vs`) | reconstructibility/atomicity test (P4) — which extends beyond conjunctions to `X of Y`, adjective+head, device+operation, and both-parts-exist compounds. Splitting is the mechanical form of "the parts are values and the compound is their intersection." |
| Promotion | recurrence as evidence | recurrence as a weak prior on regularity; admission still requires the dimension test (§6.3). |
| Review queue | error correction | error correction and the gray-zone arbiter; no preference channel (§6.3). |
| Canonicalization | incident merges | standing duty (§6.4). |
| Completeness | not represented | new duty class (§6.4). |

### 7.2 What stays locked

The definition is compatible with the system's locked quality-control principles, and
sharpens them:

- **Mirror-image asymmetry** (wrongly rejected good tags self-heal; wrongly born trash
  pollutes): still true for dimensions; §6.5 relaxes only the permanence of *influence*.
- **No curated lists**: the definition adds no lists; `chile` vs `chilean` is decided by
  extension analysis, not by a gazetteer.
- **Filter over samples, never self-rewrite**: unchanged.
- **Hard gates only where classification is unambiguous**: the definition keeps hard
  gates for facet ownership and canonicality, and keeps the gray band for the semantic
  judgments (relational compounds, named units, near-faithful intersections) as
  evidence-and-curation territory.
- **The long tail is partly legitimate**: unchanged, now expressed as "a value below the
  recurrence floor is a pending candidate, not a reject."

### 7.3 The completion precondition is the load-bearing adoption risk

Everything above assumes the corpus is actually tagged with its dimensions. The current
pipeline produces tags in the generator's own framing at the generator's own recall;
nothing audits whether each document carries the broad values that make intersections
work. Without completeness work, adopting the definition yields a vocabulary that
*permits* the right queries while the corpus *fails* them — the reference model's
completeness convention is not optional. This is the largest operational gap and the
reason §6.4 ranks completeness with admission.

---

## 8. Resolved questions

The questions the definition leaves to policy, and their decided answers:

1. **Relational compounds.** No separate rule: a relational phrase is an ordinary
   candidate, judged by the five properties and P4 (`S(c) ⊊ ⋂ S(pᵢ)` marks the
   relational case). It is a tag only if it names one dimension — the relation as
   content, recurring as a subject; otherwise it decomposes and the relation falls to
   free text.
2. **Recurring named units.** Admitted whole above a recurrence threshold (item 9) and
   tracked as candidates while their count accumulates — existence without influence,
   like any provisional value. Their parts remain valid values applied alongside, so a
   unit like `spanish civil war` is still found through `spain` + `civil war` or broader
   intersections; the overlap is accepted. The utility threshold, not a parts-failure
   test, separates a named unit from pre-coordination.
3. **Canonical form.** Labels are shaped and identified at birth: the lemma form, the
   conventional written form, same-intent resolution with the alias disposition, and
   direction by sense-fit, then the house form/language, then a deterministic tie-break. Frequency is not a birth-time
   criterion; it refines later through canonicalization. A kindred-form criterion is
   rejected — there is no axis model to consult (item 7).
4. **Demotion.** Two triggers: supersession (a better label takes over; same-intent
   losers alias to the canonical label) and the scheduled sweep that re-applies the
   current rules to stored tags (legacy non-compliance, older judge standards, mass
   decay). Demotion revokes influence only — existence and assignments stay, history
   remains auditable, and the one-way-promotion doctrine is retired.
5. **Completeness mechanics.** Both paths with separated roles: the generation contract
   is the primary producer — it emits the applicable dimensions as the document is read
   — and an audit verifies and repairs as a separate pass. The acceptance test: a
   document carries its dimensions when every dimension whose applicability is
   established is attached, applicability judged as about-ness with evidence and a gray
   band, and attachments counted by coverage class.
6. **Preference channel — none.** Usage does not signal: manual assignments, review
   actions, and hypothetical usage logs carry no weight against recurrence or in any
   decision. The review surfaces serve error correction and gray-zone arbitration;
   preference-like behavior would be an explicit opt-in feature outside the mechanism.
7. **Axis grouping — none.** The vocabulary stays emergent: no stored or derived axis
   structure, and no review depends on one. Missing anchors are the mechanical residue
   of decomposition and completeness or a user action — the contract may propose a
   broad dimension, and users may add or tune values manually; any grouping happens
   outside the system. The composition duty (§6.4 item 2) stays over the compound
   classes.
8. **Corpus relativity — global.** The store's live documents are one universe:
   extensions, mass, and audits are computed store-wide, and no sub-scoping mechanism
   (per-corpus dimensions or thresholds) is built. Grouping counts by document metadata
   is an analysis-time concern, and query-time document filters compose with tag
   intersection without changing extensions. Sub-scoping was rejected as a vocabulary
   feature: its benefits (threshold fairness, coverage visibility) live at the query
   and reporting layer, while it would demand trustworthy per-document membership the
   system does not have, multiply thresholds and review surfaces, and split the scarce
   label pool.
9. **Thresholds — calibration-owned.** The recurrence floor `m`, the set-equality
   threshold for `≈`, and the containment threshold for redundancy stay unnamed by
   design: they are fitted on labels, and nothing conceptual remains open. Until
   calibration exists the mechanisms run on pinned defaults — a missing threshold is
   never a reason a mechanism cannot operate.
10. **Decision layer — staged.** A pinned, versioned rule set over the features is the
    operating mode: it runs with zero labels and is never gated on data. The review
    surfaces and audit verdicts produce labels as a byproduct of operation, and a
    fitted layer is an optional refinement where labels accrue — abstention, drift
    monitoring, re-validation on extractor changes, and label hygiene attach to that
    refinement. The no-logprob-dependence principle constrains which signals may be
    depended on, not which may be observed.

---

## 9. Summary: the operative statements

1. **Definition.** A tag is a reusable dimension of content-meaning: a broad, stable,
   canonical, non-decomposable unit of faceted retrieval, whose meaning is its extension
   and whose retrieval precision comes from intersecting it with other tags.
2. **The three-way test.** A candidate that names one dimension is admitted; a candidate
   that conjoins dimensions is split; a candidate that names no dimension is dropped.
   Lexical, idiomatic, and recurring named units whose parts are not dimensions are
   admitted whole on recurrence.
3. **Meaning is extension.** Same extension, one label; reconstructible by intersection,
   not a tag; singleton extension, not a tag.
4. **The vocabulary is sized by dimensions, not by queries.** Pre-coordination
   enumerates combinations as names and deletes the queries the combinations could have
   composed; post-coordination keeps the algebra.
5. **Roles.** The LLM proposes; the system registers, regularizes, canonicalizes, and
   aggregates evidence; recurrence is a weak, correlated regularity prior — usage is
   not a signal (§6.3); humans arbitrate the gray band. Decisions are combined from
   features — pinned rules by default, a fitted refinement only where labels exist —
   never taken by a single signal (§6.6).
6. **Duties.** Admission alone is not the policy. The definition requires three standing
   operations: canonicalization, composition review, and completeness.
7. **Influence is revisable.** Existence and influence are separate; correctness is a
   normal maintenance operation, not an exception.

The one-sentence standard every layer should cite: **a tag is the canonical atom of
faceted retrieval — what a search string becomes when it is worth naming, sharing, and
intersecting — and every mechanism in the tag pipeline is an enforcement of that
sentence.**

