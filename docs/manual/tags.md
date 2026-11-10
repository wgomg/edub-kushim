# Tags

Tags are the shared vocabulary the system attaches to documents during enrichment — topics, people,
events, document kinds. They are not free text: a tag is a store entry, so the same concept keeps one
spelling and one identity, which is what makes tag-based search and consolidation trustworthy.

This page explains how a tag is born, judged, reviewed and retired, what every badge in the review
queue means, and what each review action does. The New tags tab carries an in-app digest of this
guide behind the **Help** button in the queue header.

## The life of a tag

A tag is **provisional** when it is first assigned: the pipeline proposed it, but the vocabulary has
not accepted it yet. Provisional tags can be used while they wait, and they appear in the **New tags**
queue for review.

A tag leaves provisional standing in two ways:

- **Promotion** — the admission decision admits it *and* enough live documents carry it to clear the
  promotion floor (the configured `recurrence_floor`, ten live documents by default). Assignments on
  trashed documents do not count. Approval by an editor promotes a tag immediately, whatever the floor
  and verdicts say.
- **Removal** — a rejected or retired tag is deleted; rejection also records the name in the
  blocklist so enrichment never proposes it again.

A promoted tag can lose its standing again (**demotion**): its live mass may fall below the floor (the
mass-decay sweep waits until the tag is older than `demotion_grace_days`, disabled by default), a
fresh judgment may reject or defer it, a decided compound drop may go through, or an editor may demote
it from the Vocabulary tab. A demoted value keeps its documents and its history and returns to the
review queue, where approving it restores its standing. Decided non-values that stay unchallenged for
the retention window are eventually removed by the scheduled sweep.

## The queue settles over time

A fresh collection produces the largest queue it will ever have: every name is novel, the judges have
no stored decision to lean on, and candidates accumulate faster than a person can review them. That
surge is expected and mostly does not need a person.

- **Stored decisions are reused.** Quality and composition judgments are recorded with the criteria
  version that produced them, and identity resolutions are stored as aliases or holds; the same name
  emitted again is answered from the stored decision instead of a fresh judge call, and a name already
  in the tag table is not even novel.
- **The scheduled passes dispose of the backlog.** Reconciliation promotes provisional values that
  clear the recurrence floor, demotes values whose live mass decayed, applies recorded folds and
  splits, re-asks stale compound judgments, and retires decided non-values once the retention window
  passes.
- **New emissions follow what the vocabulary already holds.** Once a value is in the store, later
  emissions resolve to it — by name, normalized form or alias — instead of proposing another variant.

Review effort therefore falls off over time: values the judges and the sweeps can settle leave the
queue on their own, and the entries that remain are the genuinely novel or genuinely unresolved ones.
Leaving the queue unattended is safe — provisional tags are searchable without influencing the
vocabulary, and the sweeps keep working.

## Where tags come from

- **Enrichment** — the pipeline's analyzer proposes tags while processing a document; the tag quality
  judge screens them before they become provisional values.
- **The coverage audit** — missing dimensions found on processed documents can be registered as new
  values (Coverage tab → *register a missing name*).
- **Corrections** — an editor can add tags to a document, and a re-enrichment can re-propose values.

## The three judges

Review exists because three different questions are judged automatically, and each can come out
uncertain:

- **Quality (admission) judge** — is this string a subject matter or a dimension, is it atomic,
  does it name a person, is it not a publishing format? Its answers decide admit, defer or reject,
  and are shown as the verdict badge and a rubric line under each queue entry — the failing checks
  plus a score, with every criterion on hover. A documented individual's name is rejected on the
  person question alone; a mythological, legendary, fictional or unclear person reading is held
  for review instead.
- **Identity judge** — is this name the same concept as an existing value, just spelled or formed
  differently? Confirmed identities become aliases (see below); refused ones leave a **hold** for a
  person to resolve.
- **Composition judge** — is this compound one concept (`world war 2`) or the product of its parts
  (`political history`)? Its split, keep and drop decisions can withhold promotion as a **hold**.

## Badges in the New tags queue

Every entry carries the state that keeps it provisional. Click a badge in the app to open the
matching section of the guide.

| Badge | Colour | Meaning |
|---|---|---|
| `no verdict` | grey | never judged under the current rules |
| `awaiting promotion floor` | green | admitted by the judge; waiting for enough live documents |
| `deferred to review` | amber | the judge abstained or the evidence was inconclusive |
| `accepted — awaiting promotion floor` | green | an operator waived the deferral; the value waits for the promotion floor |
| `judge rejected` | amber | the judge found the value failing the rubric |
| `identity pending` | amber | a fold proposal's relation gate or a same-intent claim about this tag awaits the identity pass |
| `identity hold → *value*` | amber | the identity judge refused to fold this tag onto that value |
| `split hold → *reason*` | amber | the composition judge could not settle the compound |
| `composition → judge_unavailable` | grey | the last composition decision predates the current rules and needs a re-judge |
| `demoted → *reason*` | amber | the value's standing was revoked and it is provisional again |

### Split-hold reasons

| Reason | Meaning | Usual resolution |
|---|---|---|
| `split_defer` | the split judge abstained or the parts pass deferred | split now, or keep whole |
| `conflicting_evidence` | parts and compound point in opposite directions; an arbiter call resolves it when the evidence settles, and only an arbiter defer keeps the hold | split now, or keep whole |
| `sample_disagreement` | two corpus samples disagree about the compound | split now, or keep whole |
| `relational_keep` | the compound selects more than its parts share, so it is not their product; a keep resting only on parts the store has not promoted yet waits on store maturity instead of reaching review | apply the parts alongside, or keep whole |
| `judge_unavailable` | no split judge answered; nothing was decided | re-judge once a judge is configured |
| `no_dimension` | the compound was decided to name no dimension | retire it — approving does not stick for a decided drop |
| `part_rejected` | every part of the split was rejected | retire it — approving does not stick for a decided drop |

### Demotion reasons

| Reason | Meaning |
|---|---|
| `mass_decay` | live documents fell below the recurrence floor |
| `no_dimension` | a decided drop that named no dimension |
| `part_rejected` | every part of the decided split was rejected |
| `judge_defer` | deferred by the current criteria |
| `corroborated_failures` | rejected by the current criteria |
| `person_facet` | the name belongs to a documented individual (the people facet) |
| `person_review` | the person reading (mythological, legendary, fictional or unclear) was held for review |
| `deterministic_only` | deferred on deterministic signals alone |
| `single_signal` | deferred on a single failing signal |
| `operator` | an editor demoted it from the Vocabulary tab |

## Actions

Every action is available from the entry that needs it; the queue only shows the actions its badges
justify. The manual fold is the exception: it is offered on every queue entry and on every Vocabulary
row, and the manual split on every compound entry and row, overriding whatever the split judge decided.

| Action | What it does |
|---|---|
| **Approve** | promotes the tag into the shared vocabulary; overrides undecided verdicts and holds, but a decided drop is re-demoted by the next reconciliation run |
| **Reject** | deletes the tag *and blocklists the name*, so enrichment never proposes it again (restorable from the Deleted tab) |
| **Retire** | deletes the tag without blocklisting the name, so enrichment may propose the value again |
| **Accept as provisional** | waives a review deferral: the tag stops being deferred and waits for the promotion floor like any admitted value, promoting once enough live documents carry it; a newer verdict (a rubric version change or a re-judge) re-opens the deferral so the action can be used again; offered only when no identity or split hold still withholds the tag |
| **Rename** | changes the name in place; the journals are keyed by name, so the tag shows no verdict until it is judged again under the new name |
| **Fold into *value*** | collapses this tag onto its held store value: documents move, this name becomes an alias and leaves the vocabulary |
| **Fold into…** | folds this tag onto any other live tag you pick: its documents move onto the kept value, this name becomes an alias and stops existing as a tag; seeded inventory cannot be folded away |
| **Make canonical** | the reverse fold: the held value collapses onto this tag, losing its row and standing; this tag stays provisional until approved |
| **Keep separate** | records that this tag names its own value: the identity hold clears, both values stay, and the tag keeps answering to its verdict and the promotion floor |
| **Split now** | replaces the compound with the components you confirm: components are created, the compound's documents are attached to them, the compound is deleted; the split is recorded, so future emissions of that name are replaced by its recorded components instead of being judged again |
| **Split…** | replaces this compound with the components you choose, whatever the split judge decided: components are created, this tag's documents move onto them and it is deleted, and the split is recorded the same way; seeded values cannot be split |
| **Keep whole** | records that the compound is a value in its own right; the hold clears and nothing else changes |
| **Apply the parts alongside** | keeps the compound and attaches its parts to the same documents, so both are searchable; no documents move |
| **Re-judge** | queues a fresh composition judgment; the queue refreshes when the background task finishes |
| **Judge now** | queues the first quality judgment for a tag that was never judged |
| **Reevaluate identity** (queue header) | runs the identity pass now instead of waiting for its daily schedule |
| **Newest / Oldest** (queue header) | orders the queue by entry age in either direction; the choice is kept in the page URL |

Destructive or document-moving actions ask for confirmation and state the consequence first. Approve
and reject are the two universal queue decisions; the manual fold is offered on every queue entry and
Vocabulary row and the manual split on every compound one. Everything else is contextual to the badge
that motivated it.

## Aliases

An **alias** is a recorded equivalence: a name that means the same concept as a canonical tag. When the
identity pass (or an editor's fold) collapses two values, the losing name's documents are re-pointed
onto the winner, the loser's own aliases are re-targeted (aliases never chain), and the loser's row is
deleted while the alias table keeps the mapping. The alias therefore stops appearing as a tag in the
vocabulary, but the system still knows the old spelling refers to the canonical value.

There is no dedicated alias screen: aliases appear on the Vocabulary tab as chips under the value
they resolve to, and the fold actions — on every New tags entry and on every Vocabulary row — are the
way to create them by hand. Every non-seeded chip offers **remove** (the mapping is deleted, documents
stay on the canonical, and a filter that still uses the old name stops matching) and **promote** (the
name becomes a live tag of its own starting empty, its documents having stayed on the canonical).
Seeded aliases — the shipped alternate spellings the startup seeder restores — refuse removal but can
be promoted.

Aliases resolve wherever names are matched: a tag filter using an alias name matches the canonical's
documents, the Vocabulary `?q=` search finds a value by any of its aliases, and tag suggestions show
the canonical tag with the alias that matched as a hint.

## Related review surfaces

- **Coverage** (`/tags#coverage`) — missing dimensions the audit found on processed documents;
  apply, dismiss, or register the missing value.
- **Overlap** (`/tags#siblings`) — near-containment readings from the latest measurement pass. They are
  informational: nothing is merged or removed automatically.
- **Deleted** (`/tags#deleted`) — the blocklist of rejected names, restorable.
- **Split rules** (`/tags#split-rules`) — every recorded manual split: the name with the components
  future emissions apply in its place. Editors can revert one: the record is dropped and future
  emissions treat the name normally again — a stored tag applies whole, a name the store no longer
  holds is judged afresh — while the components and the documents already on them stay as they are.
- **Vocabulary** (`/tags`) — every live tag with counts and standing; rename, fold, demote, delete and
  the alias chip actions (remove, promote) live there too.

Running the passes by hand and their schedules are described in
[cli.md](cli.md#tag-passes); the configuration keys behind the judges and schedules
(`enricher.tag_quality`, `enricher.tag_adjudicator`, `enricher.tag_reconcile`, `enricher.tag_maintenance`)
are in [configuration.md](configuration.md#enricher). The API workflow is in
[api.md](api.md#tag-review), and the screens themselves in [web-ui.md](web-ui.md#tags).
