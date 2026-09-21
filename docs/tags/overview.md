# Tag subsystem — overview

Tags are the system's filter vocabulary: an LLM assigns them to documents during
enrichment, and retrieval filters documents by intersecting several of them. That puts
one object at the center of everything — the tag — and this folder is the design corpus
that says what that object is and how the machinery around it should behave. It is the
single source of truth directing the subsystem's implementation, and this page is its
entry point.

## The model

A tag is a **reusable dimension of content-meaning**: broad, stable, canonical,
non-decomposable. Precision is not a property of a tag; it is a property of the query.
`cheating` and `uncle` are each vague; their intersection is a precise retrieval. The
vocabulary therefore stores dimensions and queries compose specificity — the opposite
of the encyclopedia style, where combinations are named as headings and the query
algebra those combinations could have expressed is lost.

Two failure directions motivate the design. The vocabulary drifts toward
*pre-coordinated phrases* — compounds that name a combination as if it were one concept
— and toward *dictionary-generic words*, broad concepts nobody would filter by. Both
are what happens when every layer of machinery enforces a proxy (document relevance,
dictionary standardness, recurrence) and none enforces the object.

## The documents

**[definition-and-faceted-retrieval.md](definition-and-faceted-retrieval.md)** is the
authority. It states the object, formalizes it in set terms, derives the admission
trichotomy (a candidate that names one dimension is admitted, one that conjoins
dimensions is split, one that names none is dropped), defines the governance model that
materializes it, and records the policies the model resolves. Implementation plans cite
this document and must not contradict it.

**[pipeline-model.md](pipeline-model.md)** is the map. It lays the subsystem out as
stages — emission, facet gate, canonicality, atomicity, admission, lifecycle,
vocabulary services, retrieval — attaches the standing duties to them, analyzes the
current implementation against the model, and keeps the deltas visible as ranked gaps
and conflicts.

The working specs operationalize the mechanisms, one per piece:

- **[atomicity.md](atomicity.md)** — how a multi-word candidate gets its composition
  reading: part derivation, measurements, branch selection, dispositions.
- **[canonical-form-and-identity.md](canonical-form-and-identity.md)** — how a label
  takes shape and resolves identity at birth: lemmatization, conventional forms,
  same-intent judgment, aliases.
- **[completeness.md](completeness.md)** — how documents come to carry their
  dimensions: the applicability line, the generation contract, the verification audit,
  coverage classes.
- **[extension-algebra.md](extension-algebra.md)** — the measurement substrate every
  consumer reads: extensions, operators, evidence states, computation and versioning.

The subsystem reference pages describe the machinery as it operates:

- **[matcher.md](matcher.md)** — the embedding matcher process: configuration, memory
  and CPU sizing, backends, operation.
- **[consolidation.md](consolidation.md)** — consolidation end to end: replacement
  policy, adjudication, merge review, reconciliation.

## How to read

Implementing or reviewing tag behavior starts with the definition and the pipeline
model, then the spec for the mechanism being touched. The specs assume the definition's
vocabulary and the map's stage names, and each states what it consumes and what it
leaves outside its scope. The reference pages are operational: read them before
changing configuration, capacity, or the consolidation paths.
