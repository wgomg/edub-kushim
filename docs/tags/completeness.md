# Tag completeness — working specification

**Status:** Working spec. It operationalizes the completeness duty (definition §6.4
item 3) and specifies the completeness policy the definition records as decided (§8.5).
It covers both paths: the prospective
one, where the generation contract asks for the applicable dimensions as the document
is read, and the retrospective one, where an audit verifies and repairs the existing
corpus. It sits in the pipeline model's D3 (`pipeline-model.md` §6.3), beside
the atomicity and canonical-form specs. No rule requires prior document-tag data, and
no model commitment is made here. Unqualified § references point into the definition
(`definition-and-faceted-retrieval.md`).

**Non-goals:** thresholds and calibration; schema or endpoint design; model or tooling
selection (the NLI entry stays a tooling-register candidate); legacy rows in other
languages or longer than three words (separate plan).

## 1. Relationship to the corpus

- **Definition** — the authority. The completeness convention is §3.3; the lower-bound
  caveat §4.5; the duty §6.4; the risk §7.3; the policy resolved here (§8.5).
  Scope is global: one store, one universe (§8.8, decided).
- **`pipeline-model.md`** — the map. D3 is the second missing mechanism; S1's
  contract is the prospective producer; D4 supplies the counts; D5 is the gray-band
  sink; the prospective-first requirement (§7 item 11) binds every rule here.
- **`atomicity.md`** — the sibling. Its excess/deficit evidence is contaminated
  by application incompleteness (§3.2 there); missing-part creation (§3.5, §5 item 6
  there) is the only admission route for anchors this spec finds missing.
- **`canonical-form-and-identity.md`** — the sibling. The audit counts canonical
  labels; aliases resolve to them. A fragmented inventory would report phantom gaps.
- **Admission and free text** — separate. This spec asks whether a dimension applies
  to a document, not whether the dimension deserves to exist.

## 2. Givens

Already enforced elsewhere; listed so this spec does not restate them:

- English only; one to three words; facet ownership, place rule, and typed contract
  from the generation pipeline.
- The inventory is the store's canonical labels (the shape and identity rules of
  `canonical-form-and-identity.md` applied); aliases resolve to them.
- Provisional anchors are part of the inventory but are counted separately until
  promotion.
- Names arrive in the store's established shape; normalization is a given.
- Rows in the store that violate the givens (another language, longer than three words)
  are handled by separate cleanup work.

## 3. The mechanism

### 3.1 The line — applicability

- A dimension `d` applies to a document `x` iff a searcher filtering by `d` would count
  `x` as a correct hit — the definition's retrieval act used as the test. About-ness,
  not mention.
- **The acceptance test.** A document carries its dimensions when every dimension
  whose applicability is established is attached. Applicability is established when
  the contract emitted it, or the audit found it and review did not overturn it.
  Attached means a canonical label (aliases resolved) or a provisional anchor recorded
  as a coverage class (§3.4).
- The judgment is per (document, dimension), from the document's content and whatever
  the case carries; the result is evidence with a gray band, never a binary truth.
- Ambiguity defers to review; no attachment is forced.
- The judgment must be available with zero observations (prospective-first): the
  contract makes it where the document is read, and the audit makes it later over the
  same line.

### 3.2 Production — the contract

- The generation contract asks the reader for the dimensions applicable to the
  document at emission, by the reading that produces the tags.
- **Coverage, not salience.** The contract must deliver every dimension the document
  is about — not a top-N ranking. Whatever caps or ranking pressure the current
  emission carries are the re-criteria conflict to resolve, not a constraint this
  spec accepts.
- **Inventory first, proposals as residue.** The reader applies the canonical
  inventory (promoted values plus provisional anchors) and proposes dimension names
  for what has none; proposals take the normal admission path (S3/S4) and are not
  completeness fills.
- The contract separates "applied an existing dimension" from "proposed a new name" in
  its output.
- **Cold start.** With an empty inventory the contract still works: the reader proposes
  the broad dimension names it believes apply, and the admission path admits them
  provisionally as needed (§4.5; `pipeline-model.md` §7 item 11).
- Emission and verification are separate passes (§3.3); the producer never grades its
  own output.

### 3.3 Verification — the audit

- The audit verifies coverage on samples and on flagged documents; it never runs as the
  same pass that produced the tags it checks (the separation discipline of
  `../research/tag-quality-control-mechanisms.md` §2.3).
- Instruments, cheapest first:
  - the existing doc→dimension suggestion scores, already computed per document, as the
    trigger and worklist — a high score without the tag flags a candidate gap;
  - lexical support (stemmed term presence, in-house TextRank keyphrases) to narrow the
    flags;
  - a sampled LLM audit in the existing judge shape — one call per document, batched
    binary criteria over the flagged dimensions, cached per document content and
    criteria version.
- **Known limitation — the recall hole.** The similarity trigger misses
  about-but-dissimilar documents, so audit-detected gaps are a lower bound: sampling
  mitigates, and the contract path is unaffected because it does not use the trigger.
  When review uncovers a gap the trigger missed, the case is recorded as a **measured
  miss**; the miss rate is the trigger's recall estimate and feeds calibration and the
  tooling register. Closing the hole is not this spec's work.
- The NLI entry and other candidates stay in the tooling register; nothing is
  committed here.
- The audit's output is evidence: worklists, labels, and repair proposals.

### 3.4 Repair — gaps, anchors, and transition

- The gap is `A(x) \ C(x)`: dimensions whose applicability is established and that the
  document does not carry.
- Dispositions: **fill** (attach the dimension under its `14` canonical label) ·
  **create anchor** (the dimension does not exist yet — the audit emits a proposal into
  the normal admission path, 13's missing-part creation included, and fills only after
  the value exists) · **flag** (review) · **no-op**.
- The audit never creates a value on its own authority: anchor admission keeps its
  single route (the admission machinery and 13's missing-part creation), and
  completeness counts the gap until the value exists.
- **Coverage classes.** The carried set is recorded as classes, counted separately:
  **explicit** (canonical label attached, aliases resolved) · **pending-split** (a
  pre-split compound whose decomposition will produce the dimension) ·
  **pending-canonicalization** (a stored label that denotes the dimension under `14`'s
  rules but has not been swept) · **missing** · **gray**.
- **The audit does not read compounds.** Whether a part applies is 13's branch
  selection to make; the audit counts pending-split coverage and does not fill, so no
  fill duplicates the split work. Metrics report explicit versus pending so
  retrieval-readiness is not overstated.
- Repairs are evidence-recorded and reversible; attaching a label gives existence, not
  influence — promotion rules are unchanged (S6).
- Provisional anchors are applied as documents are processed, and their coverage is
  counted separately until promotion, so the audit does not silently inflate the
  promoted inventory.
- Over-application (a document carrying a dimension it is not about) is not a
  completeness gap; the quality and review paths own it.

### 3.5 Measurement

- Per-document coverage (how many of the applicable dimensions are attached) and
  per-dimension coverage (how many of the documents that should carry it do); counts by
  coverage class (§3.4), raw, global scope, with §4.5's lower-bound caveat.
- Consumers: D2's audit worklists, D5's review queues, and later calibration — numbers
  are evidence, not a scoreboard.
- Thresholds ("enough") are calibration's; v0 operates with a wide gray band.

### 3.6 Evidence record

Per (document, dimension): the proposal source (contract, trigger, lexical support,
audit), the evidence, the audit verdict with criteria and model versions, the coverage
class, the action taken, the review outcome, and the rule-set version. Consumers are the
review surfaces, calibration, and the D4 substrate.

## 4. Worked examples

| Case | Line | Outcome |
|---|---|---|
| Guide whose content is about repairing but the document lacks `repair` | applies | fill (`repair`) |
| Document that mentions a dimension in passing | not applies (mention) | no-op |
| Subject whose dimension does not exist yet | applies, anchor missing | proposal into the admission path; fill only after the value exists |
| About-ness genuinely unclear | gray | flag to review; no fill |
| Document carrying a dimension it is not about | not a completeness gap | quality/review path owns it |
| Pre-split compound covering a dimension | pending-split coverage | counted, not read; 13 materializes; no fill |
| Split-created anchor applied on the next pass | applies, anchor now exists | fill; provisional coverage counted separately |

## 5. Open items

Owned elsewhere; listed so the handoffs are explicit.

1. **Contract wording, typed details, and the version bump** — S1 re-criteria and the
   measurement-versioning decision (`12` §6.3 item 3, §6.4 item 10).
2. **Sampling policy and flag thresholds** — calibration.
3. **Acceptance tolerance ("enough coverage")** — calibration.
4. **Recall-hole closure** (better triggers, NLI and alternatives) — tooling register
   plus calibration monitoring; the limitation and its measurement are recorded in
   §3.3.
5. **Tooling selection** — tooling register and implementation plan; no commitment in
   this spec.
6. **Anchor admission rules** — 13's missing-part creation and the admission machinery;
   this spec triggers and accounts only (§3.4).
