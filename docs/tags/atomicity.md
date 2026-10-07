# Tag atomicity — working specification

**Status:** Working spec. It operationalizes the definition's atomicity predicate (P4,
definition §4.3) for multi-word candidates, as the first slice of the measurement
substrate (D4 in `pipeline-model.md`). Track 1 (the existing store) is the first
worked slice; Track 2 — birth with no documents yet — is the system's normal operating
mode, and its zero-data ladder is required, not optional (§3.4, §5 item 7): no
disposition may depend on observed counts alone. Unqualified § references point into the
definition
(`definition-and-faceted-retrieval.md`).

**Non-goals:** thresholds and calibration; schema or endpoint design; legacy rows in
other languages or longer than three words (separate plan).

## 1. Relationship to the corpus

- **Definition** — the authority. P4 defines reconstructibility as
  `S(c) ≈ ⋂ S(pᵢ)`; §3.6 splits the work between reconstructibility and mass; §5.1
  states the class verdicts; §8 holds the policies this spec consumes.
- **`pipeline-model.md`** — the map. S4 produces the composition reading, S5
  disposes, D4 measures; its §6.3 ranks this slice as the keystone gap, and §7 item 11
  makes prospective-first operation a standing requirement this mechanism must satisfy.
- **`canonical-form-and-identity.md`** — the sibling. It decides the form each
  label takes and when two labels are one value; the adjective folding it specifies is
  assumed by §3.1.
- **Existing machinery** — the emission-time decomposition pass and the LLM adjudicator
  with its cached, versioned verdicts are the precedent this spec extends: the pass
  covers every compound candidate (two judged passes when the adjudicator is on;
  conjunction joints split mechanically without a judge) and disposes them through
  `tags.DecideComposition`, and the reconciler re-uses the conjunction detector for
  stored tags. This spec supplies the measurement and the decision over those candidates.
- **Free text** — tags are search shortcuts for dimensions; named specifics belong to
  free-text search, which composes with tag filters. A phrase being a fixed name is
  never by itself a reason to admit it. The unit branch exists for phrases whose parts
  cannot compose, and admission there means the phrase works as a dimension — not that
  the name deserves representation.

## 2. Givens

Already enforced elsewhere; listed so this spec does not restate them:

- English only; one to three words; facet ownership, place rule and typed contract from
  the generation pipeline.
- Normalized form: candidates arrive in the store's canonical shape (case folding and
  the canonical-form policy of `canonical-form-and-identity.md` applied).
- Single-word candidates carry no composition reading; they are not this spec's subject.
- Rows in the store that violate the givens (another language, longer than three words)
  are handled by separate cleanup work.

## 3. The mechanism

### 3.1 Part derivation

- Strip function words: articles, conjunctions, prepositions. Content modifiers stay
  (`modern` in `modern history`, `rural` in `rural exodus`) — they carry meaning and are
  themselves candidates for values.
- The remaining tokens are the parts `p₁…p_k` (`k ≤ 3`).
- A stripped particle's relation (`of`, `de`) is not a part; if the candidate splits,
  that relation falls back to free text.
- Parts are looked up in canonical form. Adjective→noun folding (`chilean` → `chile`)
  is canonicalization work that runs before this step.
- A part that does not exist as a tag is a **missing value** (see “Dispositions”, below).

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
application incompleteness in opposite directions (a document may lack a part, or lack
the candidate, because tagging missed it). That is recorded as a caveat on the evidence,
not corrected here — the corrective is specified in `completeness.md`; §4.5
already treats observed extensions as evidence rather than truth.

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
    checks.
  - Otherwise the phrase is either covered by the parts' intersection (redundant) or too
    specific — **drop**; free text finds it.
- **Middle** → gray: order test and criteria pass, then review (see “Fallback signals”).

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
(§3.3 step A), the order test, and the criteria pass compose the reading, and
dispositions taken on it are provisional by nature — later measurements confirm or
revise them. The ladder is not optional scaffolding; it is how the mechanism operates at
birth.

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
`extension-algebra.md`.

## 4. Worked examples

Behavior references for annotation; the gray rows are where thresholds and review will
land.

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

## 5. Open items

1. **Thresholds** for tight versus far, and for part-merit counts — calibration's job
   (definition §8.9); v0 stays conservative with a wide gray band.
2. **Corpus scope — decided (2026-09-19): global.** All counts are store-wide, and
   grouping by document metadata stays analysis-time (definition §8.8).
3. **Part-merit metric** — how coherence ("one subject") and search utility are measured
   cheaply: counts, LLM booleans, or both.
4. **Order-test policy** — how many reorderings, exact question wording, and its place in
   the fallback order.
5. **Relational-keep detection** — how "the relation recurs as a subject" is judged:
   family counts where observations exist, the model's reading and review otherwise
   (prospective-first).
6. **Missing-part creation** — when a split requires a part that does not exist, how the
   value is admitted and how documents acquire it (completeness interplay, specified in
   `completeness.md` §3.4).
7. **Prospective ladder — required, composition open.** Which signal carries part merit,
   branch selection, and each disposition with zero documents; how the day-one ladder
   composes (§3.4) and how its readings are revised as observations accrue.
8. **Legacy cleanup** — non-English and longer-than-three-word rows (out of scope here,
   separate plan).
