# 0013. Operator adjudications outrank versioned verdicts

**Status:** accepted

## Context

The tag subsystem pins its admission criteria, identity rule set and decision rules to
versions and re-applies the current rules to stored values on a schedule. That replay is how
legacy verdicts and older judge standards are corrected, but it also reached decisions an
operator had already made: a promotion granted by an editor was re-judged and demoted after a
criteria-version bump, an acceptance was tied to one verdict generation, a decided compound
drop re-demoted an approved compound, and a criteria-version bump could re-impose an identity
hold the operator had cleared with a separation. Operators read those actions as settled
decisions, and each reversal arrived without an operator action.

Treating the operator actions as preference signals does not fit: approving, accepting,
separating and restoring are not votes about a value's worth; they resolve a gray band the
rule set delegated to review. Treating them as permanent overrides would instead freeze
values against the rules forever.

## Decision

Operator adjudications are lifecycle decisions with durable standing, recorded on the row:

- A promotion records its provenance (`tag.standing_source`): `operator` for an approval, an
  operator create, or an acceptance-governed promotion; `auto` otherwise. A criteria-version
  re-judgment of an `operator` row records the fresh verdict and reports the disagreement,
  but revokes nothing; mass decay still applies, and the standing ends through an explicit
  operator action.
- An acceptance is durable while the value is provisional: a later verdict, including a
  rule-set change, never re-opens the deferral it waived; only promotion, rejection or
  retirement ends it. Retention never removes an accepted row.
- An operator separation is terminal for its (candidate, anchor) pair across rule-set
  versions; later fold refusals of the same pair are evidence, not a hold.
- A rule-driven demotion can be restored by an operator: the stamps and the current-version
  judge rows (never operator rows) are cleared in one transaction, the fresh re-judge is
  enqueued before the commit, and the value returns to provisional standing.
- Mass decay and operator demotions are not restorable and are never reversed by rule
  replay: no rule re-derives them.

The operator-precedence predicate is shared: `ListIdentityHeldNames` and the queue's
`identity_hold` flag read the `tag_identity_hold_decision` view, so the identity,
composition and admission arms cannot drift apart.

## Consequences

A rule re-application never silently reverses an operator decision; the disagreement is
visible in the reconciliation report and, for standing values and acceptances, as a
`current criteria` pill in the UI. A criteria-version bump is no longer a complete
re-derivation of the vocabulary: values an editor backed keep their influence until the
editor changes them or mass decay does.

Standing constraint: new promotion paths must record the correct provenance, and new sweeps
must consult the shared hold and standing predicates instead of re-deriving them.
