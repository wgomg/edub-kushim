# Tag Subsystem (`internal/tags/`)

## Overview

`internal/tags` owns the tag-as-dimension domain: the extension-measurement
substrate, birth-time identity, the pinned decision rule sets, the store-facing
reads, and the coverage derivations. The design lives in `docs/tags/` (the
definition is the authority; `matcher.md` and `consolidation.md` are the
subsystem reference pages) with the developer guides `tag-consolidation.md` and
`semantic-matching.md`; this page is the code map.

The package is the home of the **rule and measurement versions**: a criteria,
prompt, or rule change bumps the owning constant, so a stored verdict or reading
applies only under the version that produced it and older rows stay as
superseded evidence. The LLM judge constants live in `internal/llm/analyzer`
(listed in `docs/tags/consolidation.md` §Judge criteria versions and in
`tools.md`); the rule-set constants live here.

## Version constants

| Constant | File | Pins |
| --- | --- | --- |
| `IdentityRuleSetVersion` (`v10`) | `identity.go` | the birth-time identity rules and the identity/pair judges' criteria; stamped on every `tag_identity_event` row and alias row |
| `DecisionRuleSetVersion` (`v7`) | `decision.go` | the admission/promotion/demotion rule sets (log and report provenance) |
| `RuleSetVersion` (`v1`) | `evidence.go` | the algebra's operators and the consumer-side worklist cuts; stamped on measurement records and coverage rows |
| `policy.Version` (`v1`) | `policy/policy.go` | the consolidation policy's bands and guards; filters the verdict-pair worklist |

## `identity.go`

Identity rules, planning, and the composition-row types (`CompositionRow` lives
here although it belongs to decomposition):

- `IdentityRuleSetVersion`, `HouseForm`, `Key`, `StoredName`, `VariantClass`,
  `VariantKey`, `Signature`/`ComputeSignature`, `NameIndex`
- `ProposedFold`, `ProposedDerivationalFold`, `ProposedMorphologicalFold`,
  `AnchoredFold`, `ResidualFoldAnchors`
- `PreferCanonical`, `CanonicalLoser`, `TieBreak`, `DetectNonEnglish`,
  `AllPartsPromoted`
- `Disposition`, `AliasAction`, `AliasOutcome`, `Registration`, `EvidenceRow`,
  `Plan`, `PlanDeterministic`, `StoreView`, `StoreNames`/`StoreNamesOf`,
  `PromotedKeys`, `PromotedGuardKeys`
- `ResolveAliases`, `IdentityHeldNames`
- `CompositionDisposition`, `CompositionRow`, `CompositionReading`,
  `CompositionState`, `CompositionReview`, `CompositionHeldNames`,
  `CompositionStatesForNames`

## `decision.go`

The pinned decision rule sets — the operating mode, not an option:

- `DecideAdmission(AdmissionFeatures)`, `AdmissionFeatures`,
  `AdmissionDecision`, `AdmissionOutcome`
- `DecideComposition(CompositionFeatures)`, `CompositionFeatures`,
  `CompositionDecision`
- `DecidePromotion(outcome, liveMass)`, `DecideMassDemotion(liveMass)`
- `DecisionRuleSetVersion`, `RecurrenceFloor` (10)

## Measurement substrate

- `algebra.go` — the extension operators and readings: `Set`/`NewSet`,
  `EvidenceState`, `Operator`, `MassOf`, `OverlapOf`/`NewOverlapReading`,
  `EqualityOf`/`NewEqualityReading`, `ContainmentOf`/`NewContainmentReading`,
  `ReconstructibilityOf`/`NewReconstructibilityReading`,
  `PartCoverageOf`/`NewPartCoverageReading`, `ComplementOf`/`NewComplementReading`
- `evidence.go` — `RuleSetVersion`, `Record`, `EncodeRows`/`DecodeRows`,
  `CaveatNoAdjectiveFolding`
- `worklist.go` — the v1 cuts (`TwinJaccardCut`, `TwinJaccardGrayFloor`,
  `TwinMinMass`, `SiblingContainmentCut`, `SiblingGrayFloor`, `SiblingMinMass`,
  `CompoundSplitCut`, `CompoundGrayCut`), the `ReconstructibilityBand` values
  (`ReconstructibilityInsufficient`/`Far`/`Gray`/`Firm`),
  `ClassifyReconstructibility` (the one owner of the band rule), and the worklist
  types `Twin`, `CompoundSplit`, `Sibling`, `Worklists`/`DeriveWorklists`

## `store.go`

The store-facing reads over the tag queries:

- `NewStore`, `LiveMass`, `CompoundIntersections`, `PairNames`,
  `PairVerdictStats`, `CompoundIntersection`
- `LatestWorklists` — every worklist derived from the latest measurement run
- `SiblingWorklist` — loads only the run's overlap records and keeps the
  containment worklist (the read-only overlap surface)
- `ReconstructibilityBatch` — the per-cover reconstructibility reading over the
  live store, batched (a cover the store cannot measure yields no reading)
- `WorklistSnapshot` — a run's derived worklists plus `Absent` for "no run yet"

## Shapes and coverage

- `atomicity.go` — `MaxCompoundWords`, `DeriveParts`, `DeriveSegments`,
  `GreedyCover`, `Shape`/`ClassifyShape`, `IsPeriodClass`, `IsTimeDesignation`,
  `IsTimeDesignationDrop`
- `completeness.go` — `CoverageClass`, `CoverageSource`, `CoverageAction`,
  `CoverageRecord`, `ResolvedContent`, `ResolvedBigrams`, `ResolvedSegments`,
  `CoversDimension`, `PendingCoverageSegments`, `TwinPartner`

## `extension.go`

The read-only store snapshot over the extension algebra: `AliasResolver`,
`SelfAliasResolver`, `Tag`, `Pair`, `Snapshot`/`NewSnapshot`,
`ExtensionReader`/`NewExtensionReader`.

## Runs

- `store_signal.go` — the store-changed record's shared surface: the flag names
  (`SignalMeasureChanged`, `SignalReconcileChanged`, `SignalAuditChanged`) and
  values (`SignalSet`/`SignalClear`), `OpenStoreSignal` (a pass's opening gate —
  the read-then-clear of its flag in one committed transaction, an absent row
  counting as changed) and `RearmStoreSignal` (a failed run's re-arm). One
  protocol, three flag-owning passes.
- `measure.go` — `Runner`/`NewRunner`, `Report` (with `ZeroWork`),
  `WorklistCounts`; `Run` opens on the store-changed gate — the
  flag set, or no completed run under the current `RuleSetVersion` — reporting
  `ZeroWork` when it skips.
- `tagmeasure.go` — `TagMeasureDedupKey`, the fixed dedup key shared by the
  enqueue path and the task definition

## Subpackages

- `curation/` — the vocabulary services. `Tag`/`NewTag` (rename, delete,
  promote, **demote**, deleted-tag records, embedder integration),
  `SiblingSnapshot` and `ListSiblings` (the read-only overlap surface),
  `PlanRegistration`/`CreatePlannedTx`, alias application (`ApplyAliasPlan`),
  `TagIdentity`/`NewTagIdentity`, `TagAudit`/`NewTagAudit` plus `CoverageFlag`,
  and the enqueue-surface spec constructors (`ReconcileSpecs`, `MeasureSpecs`,
  `IdentitySpecs`, `AuditSpecs`) with `TagReconcileDedupKey`.
- `admission/` — `Adjudicator`/`NewAdjudicator` (canonical suggestions,
  decomposition via `JudgeCompounds` over the substrate reading, deleted-tag
  policy, identity planning, quality gating), `IdentityResolver`/
  `NewIdentityResolver`, `AuditRunner`/`NewAuditRunner`, the identity and audit
  batch-mode pass handlers, `VerdictStore`/`NewVerdictStore` and `PartDimensionFeature`,
  `PhraseFoldKey`/`PhraseFoldVerdict`; the audit pass opens on its store-changed
  gate — an unchanged vocabulary skips the store-wide pending-coverage
  derivation and the durable rows already written stand, while the sample
  sweep always draws — and a failed run re-arms the flag.
- `policy/` — pure candidate generation: `Evaluate`, `Decision`, `Version` (no
  logging, no config, no I/O).
- `reconcile/` — `Reconciler`/`NewReconciler` and its `Run`, the report types
  (`Report`, `SiblingCounts`, `TwinCounts`, `MeasurementRun`, `ProvisionalCounts`,
  `WorklistCounts`, `CompoundSplitEntry`, `DemotionCounts`, `DemotionEntry`,
  `FlipCounts`, `PairEntry`), the worklist/evidence/apply paths, the sweeps (twin
  canonicalization, orphan, provisional, promoted-mass
  demotion), the stored-compound scan, `gate.go` (the opening gate's work
  probes), `bootstrap.go` (historical log ingest),
  and the `ReconcileTaskHandler` batch-mode pass handler; `Run` opens on its
  store-changed gate — a clear flag with a completed run behind it and every
  work probe empty reports `ZeroWork` without the store-view reads — and a
  failed run re-arms the flag.

## Tests

`docs/reference/tests.md` covers the package's test layout and the database-gated
suites. The version-filtered caches (identity events, composition rows, quality
rows, phrase folds, coverage rows) each have a version test adjacent to their
owner.
