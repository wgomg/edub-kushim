# Offline lexical knowledge base

The knowledge base installs reviewed lexical data into Postgres and exposes typed
relations between words as evidence for the tag system. It is independent of the corpus:
a document that never contains `ethics` can still be related to it through WordNet or
ConceptNet. The data is **source-scoped** and advisory — a label that does not resolve, or
a source that is not installed, is a miss, never proof that a relation does not hold.

The read interface feeds tag candidate generation as **nomination evidence**, never as a
verdict: relations nominate candidates, and the existing split, matcher, identity and quality
stages judge them exactly like analyzer emissions. A disabled, empty or erroring store leaves
tag behavior unchanged.

Code map:

| Path | Responsibility |
|---|---|
| `internal/kb/kb.go` | Package doc, store, source report, family vocabulary, lemma form |
| `internal/kb/install.go` | Manifest validation, TSV streaming, batched row inserts |
| `internal/kb/ensure.go` | Pins, currency check, LFS download, artifact cache |
| `internal/kb/read.go` | `Reader` interface, `Resolve`, `Neighbours`, `PairProbe` |
| `internal/tags/admission/containment.go` | Containment channel: uphill container nominations |
| `internal/tags/reconcile/sweeps.go` | Tag reconciliation, including the KB mint sweep |
| `internal/database/sql/queries/kb.sql` | Source-scoped reads and install-time writes |
| `internal/database/sql/schema/migrations/00071_kb_lexicon.sql` | `kb_*` tables and indexes |
| `scripts/gen_kb_artifacts.py` | Upstream download, conversion, artifact packaging |
| `assets/kb/` | Published LFS artifacts, licenses and attribution |

## 1. Artifact contract

Each artifact is a gzipped tar with four entries:

```text
manifest.json
concepts.tsv
lemmas.tsv
edges.tsv
```

The TSVs are UTF-8, tab-separated and headerless:

| File | Columns |
|---|---|
| `concepts.tsv` | `concept_key`, `pos`, `gloss` |
| `lemmas.tsv` | `concept_key`, `lemma_norm` |
| `edges.tsv` | `from_concept_key`, `to_concept_key`, `family`, `source_reltype`, `weight` |

`weight` is empty when the source has no weights. The manifest is the machine-readable
provenance and license record: `source`, `source_version`, `upstream_url`,
`upstream_sha256`, `license`, `notice`, `converter_version`, `filters`, `counts` and
`generated_at`. The installer refuses an artifact whose manifest lacks a source version,
upstream identity, license, notice, converter version or non-negative counts, and it
verifies that the loaded row counts equal the manifest's.

`converter_version` identifies the mapping and filters that produced the bytes. It is
bumped whenever either changes, so an installed store and the compiled-in pin can tell
whether the data needs re-installing.

## 2. Sources and mappings

Two sources are published:

| Source | Version | Content | Artifact size |
|---|---|---|---|
| Open English WordNet | 2025 | synsets with part of speech and gloss, lemmas, WordNet relations | ~5 MB |
| ConceptNet (English subset) | 5.7.0-en | word nodes and weighted assertions | ~7 MB |

All sources map onto one family vocabulary: `exact`, `similar`, `broader`, `narrower`,
`instance`, `part`, `antonym`, `distinct`. The edge keeps the source's native relation
name beside the family.

OEWN is parsed from the WN-LMF XML. Synsets become concepts; each `Sense` links a lemma to
a synset; the first definition becomes the gloss.

| Upstream | Family | Notes |
|---|---|---|
| `hypernym` / `hyponym` | broader / narrower | Synset relations |
| `instance_hypernym` / `instance_hyponym` | instance | Reserved for editions that carry them; absent in 2025 |
| `mero_member` / `mero_part` / `mero_substance` and the `holo_*` counterparts | part | Direction stays on the edge |
| `similar` (synset and sense) | similar | Adjective similarity graph |
| `antonym` (sense) | antonym | Targets resolve from sense to synset |

OEWN does not materialize synonym edges. The read interface derives `exact` from lemmas
that share a concept, so every synset's synonyms answer the same query as ConceptNet's
synonym graph.

ConceptNet assertions are filtered and normalized:

| Upstream | Family |
|---|---|
| `/r/Synonym` | exact |
| `/r/SimilarTo` | similar |
| `/r/Antonym` | antonym |
| `/r/DistinctFrom` | distinct |
| `/r/IsA` | instance |
| `/r/PartOf`, `/r/HasA` | part |

Both endpoints must be English word nodes (`/c/en/…`); POS and sense suffixes are
stripped, so the concept key is the word node. Terms are lowercased, underscores become
spaces, and a term survives only with one to three tokens made of letters, digits, spaces
or hyphens. Self-relations left behind by sense collapsing are dropped, duplicate
`(from, to, relation)` rows keep the first weight, and everything else (for example
`RelatedTo`, `FormOf`, `DerivedFrom`, `UsedFor`) is excluded.

`lemma_norm` is the lookup form: lowercase, underscores as spaces, whitespace collapsed.
The converter writes it and the Go side normalizes query labels with the same rule
(`kb.lemmaForm`); the two must stay in sync when either changes.

## 3. Install and ensure

`kb.NewStore(client, configDir, sources)` returns the store scoped to the configured source
list. Callers name a configured source and the store owns the pin:

```go
store := kb.NewStore(client, cfg.App.ConfigDir, cfg.KnowledgeBase.Sources)
err := store.Ensure(ctx, "oewn")
```

Reads are scoped to that list: a source that is installed but not configured contributes
nothing, and an empty list answers nothing.

`Ensure` compares the installed `kb_source` row's source version and converter version with
the pin. When they match it does nothing. Otherwise it:

1. reuses `<config-dir>/kb/<artifact>` when its sha256 matches the pin, or downloads the
   artifact through its Git LFS pointer and the LFS Batch API (no `git-lfs` client);
2. verifies the pointer oid and the downloaded bytes against the pinned sha256;
3. installs the artifact.

A pointer or download that does not match the pin fails loudly and installs nothing. The
pins live in `internal/kb/ensure.go` as unexported values: source, source version,
converter version, filename, pointer URL and sha256. The pointer URLs name the branch that
serves the artifacts (`master`). The artifact bytes and sha256 pins do not change when that
branch changes.

`Store.Missing(ctx, configured)` applies the same currency rule to a list of source names
and returns the ones that need ensuring; the setup wizard uses it for asset status.
`Store.Sources(ctx)` returns the installed sources with versions, licenses, notices,
upstream identity and row counts.

`Install` runs in one transaction: delete the source's edges, lemmas, concepts and source
row; insert the new `kb_source` row; stream the TSVs and insert rows in batches of 5000
through `jsonb_to_recordset`; then verify the loaded counts against the manifest and
commit. Any error, including a count mismatch, rolls the whole install back. Reinstalling
a source therefore replaces exactly that source's rows and leaves other sources untouched.

## 4. Storage

| Table | Key | Purpose |
|---|---|---|
| `kb_source` | `source` | Installed version, license, notice, upstream URL/hash, converter version, row counts |
| `kb_concept` | `(source, concept_key)` | Source concept with optional part of speech and gloss |
| `kb_lemma` | `(source, concept_key, lemma_norm)` | Lookup path from a normalized label to concepts; indexed on `lemma_norm` |
| `kb_edge` | `(source, from_concept_key, to_concept_key, source_reltype)` | Directional relation with family, native reltype and optional weight |

`kb_edge.family` is constrained to the pinned vocabulary, and the from/to keys are indexed
by family. Rows cascade from `kb_source`, so removing a source row removes its data. The
installer still deletes in dependency order because the new source row is inserted before
the rows that reference it.

## 5. Read interface

`kb.Store` implements `kb.Reader`:

```go
type Reader interface {
    Resolve(ctx context.Context, label string) ([]Anchor, error)
    Neighbours(ctx context.Context, label string, families []Family) ([]TypedEdge, error)
    PairProbe(ctx context.Context, labelA, labelB string, families []Family) ([]TypedEdge, error)
}
```

`Resolve` returns every concept whose normalized lemma equals the label's house form,
across installed sources, ordered by source and concept key. `Anchor` carries the source,
concept key, part of speech and gloss (the last two are empty for ConceptNet, whose nodes
are unsensed). It does not carry a sense rank: the artifact format and `kb_lemma` have no
sense-order column, so a rank cannot be served without extending both and bumping
`converter_version`.

`Neighbours` returns the edges of the requested families leaving the label's anchors,
projected onto the lemma strings both endpoints carry. With the `exact` family requested,
it also expands each anchor's co-lemmas into derived `exact` edges; those carry the
synthetic reltype `co-lemma` and the anchor's concept on both ends. `PairProbe` returns the
edges directly connecting either label's anchors in both directions, plus one derived
`exact` edge per concept the two labels share.

Families are validated; an unknown family is an error. ConceptNet edges carry a weight,
OEWN edges do not (`Weight` is nil). An empty result means the installed sources assert
nothing about the label or pair — callers must not treat it as negative evidence.

## 6. Consumption by the tag system

Three channels read the store. All are advisory, and all are disabled when the store is
absent, when no source is configured, or when a read fails.

**Pair nomination.** While the identity planner collects candidate pairs, each unresolved
candidate asks the store for its `exact` and `similar` neighbours. Neighbours that are live
store names become pending identity evidence, minted exactly like the matcher's candidate
pairs; the existing resolver and pair judge settle them. Per candidate the channel takes at
most `admission.KBMaxPairsPerCandidate` targets — exact before similar, then by descending
weight, deduplicated by stored name. That ordering is defined once
(`admission.KBPairTargets`) and shared with the mint sweep below.

**Containment.** During document enrichment the labels the analyzer emitted are probed for
their uphill containers: `hypernym`, `instance_hypernym` and `holo_*` edges for OEWN, plus
`/r/IsA` and `/r/PartOf` for ConceptNet. Containers that are not already candidates enter
the candidate list as ordinary candidates and pass the existing name-list, time and quality
gates; about-ness is the quality gate's call. A nomination the quality judge answers is not
a subject matter of the document is retired at birth (reason `kb_not_subject`) instead of
being deferred to review: the container was nominated without the document in hand, so the
judge's answer is the only aboutness evidence and the single-signal deferral band does not
apply. The retire records no verdict row, and a cached verdict that answers the same way is
deleted with the retire: the name carries no cached answer, so a later document judges it on
its own text. An abstained answer still defers, and the parts of a split nomination inherit
the rule. The scheduled reconciliation pass applies the same answer to stored values
([consolidation](consolidation.md) §9): an `origin='kb'` tag whose latest recorded verdict,
under any policy version, answers the subject-matter question with no abstention loses its
row, its assignments and every recorded verdict for its name, without a blocklist entry,
while an explicit operator decision — an acceptance or operator standing — keeps the value.
A container absent from the store is nominated only when an OEWN edge attests it. The
uphill filter keys on each source's native reltype spelling (bare for OEWN, `/r/`-prefixed
for ConceptNet), because the family vocabulary alone cannot carry edge direction.
Nominations are capped per document (`kbMaxContainersPerDocument`).

**Mint sweep.** The scheduled reconciliation pass drains the zero-evidence provisional
backlog: for each provisional tag that never received a same-intent judgment, the store's
exact and similar neighbours that already exist become pending pair evidence, and
identity-held names are skipped like every other destructive pass. A report-only run counts
without writing; the count appears in the run report as `kb_minted`.

Provenance is recorded on both write planes. Document tag rows the containment channel
attaches carry `document_tag.source = 'kb'`, and tags born from knowledge base nominations
record `tag.origin = 'kb'`; the other origins are `analyzer`, `split` and `operator`
(migration `00072_tag_origin.sql`, whose default backfills pre-existing rows as
`analyzer`). Re-enrichment clears `kb` rows together with `enrich` rows; rows an operator or
a split wrote are never cleared. The stage model these nominations feed is
[the tag pipeline](../specs/tags/pipeline.md).

The enricher ensures its configured sources once, on first use; the tag pass entry points
(`tagreconcile`, `tagrejudge`, `tagmaintenance`) ensure them before the pass. An ensure
failure is logged and skipped, and each ensure is time-bounded, so the knowledge base never
blocks enrichment or a tag pass.

## 7. Regenerating and publishing

```sh
python3 scripts/gen_kb_artifacts.py --source oewn
python3 scripts/gen_kb_artifacts.py --source conceptnet
python3 scripts/gen_kb_artifacts.py --all --out build/kb
```

The script is standard-library only. It downloads each pinned upstream into
`<out>/upstream/`, verifies the upstream sha256 from its own pin table, and never silently
re-fetches a mismatched cached file. It writes `build/kb/<source>.tar.gz`, which is
gitignored. OEWN 2025 currently produces 107,519 concepts, 185,081 lemmas and 220,808
edges; the ConceptNet English subset produces 259,357 concepts, 259,357 lemmas and 430,857
edges.

Packaging fixes the tar and gzip metadata, but the manifest contains `generated_at`, so a
regeneration produces different bytes and therefore a different artifact sha256. Publishing
a generation means:

1. copy the exact tarballs from `build/kb/` to `assets/kb/` (do not regenerate after
   copying);
2. update both pins in `internal/kb/ensure.go` (filename and sha256, plus version and
   converter version when those changed);
3. commit the artifacts and the pins together.

The artifacts are tracked through Git LFS (`.gitattributes`). LFS objects are
repository-scoped: pushing the artifacts on any branch uploads the blobs, and the pointer's
branch determines availability for a given URL. Artifact filenames are stable per upstream
edition, so the download URL does not change across regenerations; the sha256 pin selects
the generation.

## 8. Licensing boundary

The data and the code carry different licenses. This boundary is the `assets/kb/`
directory and each artifact's manifest:

- OEWN is CC BY 4.0, including the chained Princeton WordNet notice. The attribution and
  notice text is reproduced in the artifact manifest and `assets/kb/README.md`.
- ConceptNet is CC BY-SA 4.0; the converted artifact is a derivative distributed under the
  same license. Cite Speer, Chin and Havasi (2017).
- The surrounding source code is AGPL-3.0.

The installer requires a license and notice on every manifest, so an artifact that cannot
state its obligations is never installed.
