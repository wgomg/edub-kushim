# Tag atomicity

Operationalizes the definition's atomicity predicate (P4, [definition](definition.md)
§4.3) for multi-word candidates, as the first slice of the measurement substrate (D4,
[pipeline](pipeline.md) §5). Prospective-first is a standing requirement: no disposition
may depend on observed counts alone (definition §4.5); the zero-data ladder in §3.4 is how
the mechanism operates at birth, not scaffolding. Thresholds and calibration are not
specified here (definition §8).

## 1. Scope

- The **definition** is the authority: P4 defines reconstructibility as
  `S(c) ≈ ⋂ S(pᵢ)`; §3.6 splits the work between reconstructibility and mass; §5 states
  the class verdicts; §8 holds the policies this spec consumes.
- The **pipeline** places this mechanism at S4 (produce the composition reading), S5
  (dispose), with D4 measuring.
- **[canonical-form](canonical-form.md)** is the sibling: it decides the form each label
  takes and when two labels are one value; the adjective folding it specifies is assumed
  by §3.1 here.
- **Free text** — tags are search shortcuts for dimensions; named specifics belong to
  free-text search, which composes with tag filters. A phrase being a fixed name is never
  by itself a reason to admit it. The unit branch exists for phrases whose parts cannot
  compose, and admission there means the phrase works as a dimension — not that the name
  deserves representation.

## 2. Givens

Already enforced elsewhere; listed so this spec does not restate them:

- English only; one to three words; facet ownership, place rule and typed contract from
  the generation pipeline.
- Normalized form: candidates arrive in the store's canonical shape (case folding and
  the canonical-form policy of [canonical-form](canonical-form.md) applied).
- Single-word candidates carry no composition reading; they are not this spec's subject.
- Rows in the store that violate the givens (another language, longer than three words)
  are handled by separate cleanup work.

## 3. The mechanism

### 3.1 Part derivation

- Strip function words: articles, conjunctions, prepositions. Content modifiers stay
  (`modern` in `modern history`, `rural` in `rural exodus`) — they carry meaning and are
  themselves candidates for values.
- The remaining tokens are the **content words**; the decomposition **fan** over them is
  every content word plus every contiguous bigram, in order (`{w₁, w₂, w₁w₂}` for two
  content words; `{w₁, w₂, w₃, w₁w₂, w₂w₃}` for three).
- A **decomposition** is a full cover of the content sequence by in-order **segments**
  drawn from the fan: a segment counts as a value when the store already holds it or the
  parts pass judges it dimensional. The chosen decomposition is the **coarsest cover** —
  the fewest, longest segments — selected deterministically by a greedy longest-leftmost
  scan; a span that covers the whole sequence is never a segment, so a two-word compound
  decomposes into its two words or keeps whole. A segment that fails admission fails the
  cover, and the compound keeps whole with its surviving segments applied alongside
  (all-or-keep). The split bound is 2–`MaxCompoundWords` **segments**, not tokens.
- A stripped particle's relation (`of`, `de`) is not a content word; if the candidate
  splits, that relation falls back to free text.
- Content words and segments are looked up in canonical form. Adjective→noun folding
  (`chilean` → `chile`) is canonicalization work that runs before this step; a time
  designation the store does not hold (a year, decade, month, or unseeded ordinal period
  unit) is dropped, never a segment.
- A segment that does not exist as a tag is a **missing value** (see "Dispositions",
  below).

### 3.2 Measurements

Per part:

- exists as tag (yes/no);
- set size `|S(p)|` — documents carrying it;
- productivity — how many distinct partners it appears with across compounds;
- standalone usage.

Per candidate:

- `|S(c)|` — documents carrying the candidate;
- `|⋂ S(pᵢ)|` — documents carrying all parts;
- the two leftovers:
  - **Excess** `E = S(c) \ ⋂ S(p)`: "the phrase says more than the parts";
  - **Deficit** `D = ⋂ S(p) \ S(c)`: "the parts meet without the phrase".

All recorded as raw numbers — no thresholds live here. Both leftovers are contaminated by
application incompleteness in opposite directions (a document may lack a part, or lack the
candidate, because tagging missed it). That is recorded as a caveat on the evidence, not
corrected here — the corrective is specified in [completeness](completeness.md); the
definition already treats observed extensions as evidence rather than truth (§4.5).

### 3.3 Branch selection

**Step A — part merit.** For each part, the dimension questions:

- Does it name one subject — coherent intent — rather than a generic word covering
  several unrelated ones (`change`, `issues`)?
- Would a user filter by it alone?
- Does it recur and combine productively?

Mass alone does not qualify: generic heads are populous by construction. If every part
passes → combination branch. If some part fails → unit branch.

**Step B — combination branch** (all parts are dimensions):

- **Tight intersection** (both leftovers small): the phrase is its parts' product →
  **split** into the parts.
- **Far from the intersection** (a substantial leftover): the whole is not its parts'
  product. Then:
  - **Relational case** — the relation itself recurs as a subject (a family of `X
    violence`-style compounds): keep as its own dimension, under the same dimension
    checks. A judged composition whose reconstructibility reading places it far from its
    parts' intersection keeps whole as a relational keep; a cover the store cannot measure
    falls back to the promoted-parts proxy.
  - Otherwise the phrase is either covered by the parts' intersection (redundant) or too
    specific — **drop**; free text finds it.
- **Middle** → gray: order test and criteria pass, then review (see "Fallback signals").

**Step C — unit branch** (some part is not a dimension):

- The parts cannot compose; the phrase is a candidate dimension on its own.
- Admit iff it recurs **and** passes the dimension checks for phrases: one subject,
  searchable, broad rather than specific.
- Otherwise drop.

### 3.4 Fallback signals

- **Order test (bounded).** Compare the candidate with one or a few reorderings — a
  single swap for two tokens, a reversal for three. The question: does the reordering
  describe the same narrower theme, or does the meaning break or broaden? Preserved
  meaning supports the combination reading; a broken one supports the unit reading. This
  is a fallback only: where the measurements decide, they decide.
- **LLM criteria pass.** Only for gray cases, and only per criterion as binary answers
  in a single call — never one call per criterion, never a single "what should we do"
  question. The criteria text is pinned and versioned; answers are cached per normalized
  candidate, and they become labels.

In the prospective mode the ladder *is* the procedure: with no observations, part merit
(§3.3 step A), the order test, and the criteria pass compose the reading, and dispositions
taken on it are provisional by nature — later measurements confirm or revise them. The
ladder is not optional scaffolding; it is how the mechanism operates at birth.

### 3.5 Dispositions and missing parts

Dispositions: **split** · **keep as dimension** · **keep provisional** (exists, no
influence, reversible) · **drop** · **review**.

- A split implies its parts exist or are created: when a part does not exist as a tag,
  the disposition includes admitting it as a value — vocabulary repair. A split whose
  parts cannot survive (below the mass floor) degrades to drop.
- **Ambiguity routes to review, not reject.** Rejection is terminal here (blocklist, no
  rebirth), and the gray cases are exactly the label supply the later decision layer
  needs. Reject only the clear drops.

### 3.6 Evidence record

Per candidate, the record carries: the normalized candidate; its parts; per-part
existence, count, productivity; `|S(c)|`; the intersection count; `E` and `D`; the branch
taken; the signals applied (order test, criteria answers); the disposition and its
reason; the model and criteria versions. Consumers are the composition review (D2), the
review surfaces, and later the decision layer — it is evidence, not a verdict machine.
The measurement layer that generalizes this record is specified in
[extension-algebra](extension-algebra.md).

## 4. Worked examples

Behavior references; the gray rows are where review lands.

| Candidate | Parts | Reading | Expected disposition |
|---|---|---|---|
| `history of china` | history, china | Particle stripped; both parts are dimensions; order-free | Split |
| `chilean history` | chile (folded), history | Both dimensions | Split |
| `bike repair` | bike, repair | Both dimensions; device × operation; meaning survives reordering | Split |
| `labor movement` | labor, movement | Both dimensions; narrower than the plain intersection | Split — the target outcome; the deficit should shrink as application completeness improves, and `movement` must exist as a value for "all movements" |
| `climate change` | climate, change | `change` fails part merit (generic, several intents) | Unit branch: recurs as a broad subject → keep as a dimension |
| `cold war` | cold, war | `cold` fails part merit (no standalone subject) | Unit branch: recurs as a subject → keep as a dimension |
| `social media` | social, media | `social` is not a stable dimension alone; the phrase is specific | Leaning drop — free text covers the specific; keep only if it passes the dimension checks as a broad subject, never because the name is fixed |
| `ritual violence` | ritual, violence | Both dimensions; the whole is not the plain intersection (relational) | Keep only if the relation recurs as a subject; otherwise split |
