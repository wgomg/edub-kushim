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
  promotion floor (ten live documents under the current rules). Assignments on trashed documents do
  not count. Approval by an editor promotes a tag immediately, whatever the floor and verdicts say.
- **Removal** — a rejected or retired tag is deleted; rejection also records the name in the
  blocklist so enrichment never proposes it again.

A promoted tag can lose its standing again (**demotion**): its live mass may fall below the floor, a
fresh judgment may reject or defer it, a decided compound drop may go through, or an editor may demote
it from the Vocabulary tab. A demoted value keeps its documents and its history and returns to the
review queue, where approving it restores its standing. Decided non-values that stay unchallenged for
the retention window are eventually removed by the scheduled sweep.

## Where tags come from

- **Enrichment** — the pipeline's analyzer proposes tags while processing a document; the tag quality
  judge screens them before they become provisional values.
- **The coverage audit** — missing dimensions found on processed documents can be registered as new
  values (Coverage tab → *register a missing name*).
- **Corrections** — an editor can add tags to a document, and a re-enrichment can re-propose values.

## The three judges

Review exists because three different questions are judged automatically, and each can come out
uncertain:

- **Quality (admission) judge** — is this string a subject matter or a dimension, is it atomic, is it
  not a person, not a publishing format? Its answers decide admit, defer or reject, and are shown as
  the verdict badge and a rubric line under each queue entry — the failing checks plus a score, with
  every criterion on hover.
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
| `identity pending` | amber | a same-intent claim about this tag awaits the identity pass |
| `identity hold → *value*` | amber | the identity judge refused to fold this tag onto that value |
| `split hold → *reason*` | amber | the composition judge could not settle the compound |
| `composition → judge_unavailable` | grey | the last composition decision predates the current rules and needs a re-judge |
| `demoted → *reason*` | amber | the value's standing was revoked and it is provisional again |

### Split-hold reasons

| Reason | Meaning | Usual resolution |
|---|---|---|
| `split_defer` | the split judge abstained or the parts pass deferred | split now, or keep whole |
| `conflicting_evidence` | parts and compound point in opposite directions | split now, or keep whole |
| `sample_disagreement` | two corpus samples disagree about the compound | split now, or keep whole |
| `named_unit_reconstructible` | the parts rebuild the compound, so it is a name in its own right | apply the parts alongside, or keep whole |
| `relational_keep` | the compound selects more than its parts share, so it is not their product | apply the parts alongside, or keep whole |
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
| `deterministic_only` | deferred on deterministic signals alone |
| `single_signal` | deferred on a single failing signal |
| `operator` | an editor demoted it from the Vocabulary tab |

## Actions

Every action is available from the entry that needs it; the queue only shows the actions its badges
justify.

| Action | What it does |
|---|---|
| **Approve** | promotes the tag into the shared vocabulary; overrides undecided verdicts and holds, but a decided drop is re-demoted by the next reconciliation run |
| **Reject** | deletes the tag *and blocklists the name*, so enrichment never proposes it again (restorable from the Deleted tab) |
| **Retire** | deletes the tag without blocklisting the name, so enrichment may propose the value again |
| **Accept as provisional** | waives a review deferral: the tag stops being deferred and waits for the promotion floor like any admitted value, promoting once enough live documents carry it; offered only when no identity or split hold still withholds the tag |
| **Rename** | changes the name in place; the journals are keyed by name, so the tag shows no verdict until it is judged again under the new name |
| **Fold into *value*** | collapses this tag onto its held store value: documents move, this name becomes an alias and leaves the vocabulary |
| **Make canonical** | the reverse fold: the held value collapses onto this tag, losing its row and standing; this tag stays provisional until approved |
| **Split now** | replaces the compound with the components you confirm: components are created, the compound's documents are attached to them, the compound is deleted |
| **Keep whole** | records that the compound is a value in its own right; the hold clears and nothing else changes |
| **Apply the parts alongside** | keeps the compound and attaches its parts to the same documents, so both are searchable; no documents move |
| **Re-judge** | queues a fresh composition judgment; the queue refreshes when the background task finishes |
| **Judge now** | queues the first quality judgment for a tag that was never judged |
| **Reevaluate identity** (queue header) | runs the identity pass now instead of waiting for its daily schedule |

Destructive or document-moving actions ask for confirmation and state the consequence first. Approve
and reject are the two universal decisions; everything else is contextual to the badge that motivated
it.

## Aliases

An **alias** is a recorded equivalence: a name that means the same concept as a canonical tag. When the
identity pass (or an editor's fold) collapses two values, the losing name's documents are re-pointed
onto the winner, the loser's own aliases are re-targeted (aliases never chain), and the loser's row is
deleted while the alias table keeps the mapping. The alias therefore stops appearing as a tag in the
vocabulary, but the system still knows the old spelling refers to the canonical value.

Aliases have no dedicated screen today: the Vocabulary tab lists live values only. The fold actions in
the New tags queue are the way to create and correct them by hand.

## Related review surfaces

- **Coverage** (`/tags#coverage`) — missing dimensions the audit found on processed documents;
  apply, dismiss, or register the missing value.
- **Overlap** (`/tags#siblings`) — near-containment readings from the latest measurement pass. They are
  informational: nothing is merged or removed automatically.
- **Deleted** (`/tags#deleted`) — the blocklist of rejected names, restorable.
- **Vocabulary** (`/tags`) — every live tag with counts and standing; rename, demote and delete live
  there too.

Running the passes by hand and their schedules are described in
[cli.md](cli.md#tag-passes); the configuration keys behind the judges and schedules
(`enricher.tag_quality`, `enricher.tag_adjudicator`, `enricher.tag_reconcile`, `enricher.tag_maintenance`)
are in [configuration.md](configuration.md#enricher). The API workflow is in
[api.md](api.md#tag-review), and the screens themselves in [web-ui.md](web-ui.md#tags).
