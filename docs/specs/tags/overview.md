# Tag subsystem — specifications

Tags are the system's filter vocabulary: an LLM assigns them to documents during
enrichment, and retrieval filters documents by intersecting several of them. That puts
one object at the center of everything — the tag — and these specifications say what that
object is and how the machinery around it must behave. They are living, normative
documents: they constrain future changes, not only the current code.

## The documents

**[definition.md](definition.md)** is the authority. It states the object (a tag is a
reusable dimension of content-meaning), formalizes it in set terms, derives the admission
trichotomy (a candidate that names one dimension is admitted, one that conjoins
dimensions is split, one that names none is dropped), defines the governance model that
materializes it, and records the policies the model resolves. Every other spec and every
mechanism defers to it.

**[pipeline.md](pipeline.md)** is the map. It lays the subsystem out as stages — emission,
facet gate, canonicality, atomicity, admission, lifecycle, vocabulary services, retrieval
— and attaches the standing duties that keep it honest: canonicalization (D1),
composition review (D2), completeness (D3), the measurement substrate (D4), and the
review surfaces (D5).

The mechanism specs operationalize one mechanism each:

- **[atomicity.md](atomicity.md)** — how a multi-word candidate gets its composition
  reading: part derivation, measurements, branch selection, dispositions.
- **[canonical-form.md](canonical-form.md)** — how a label takes shape and resolves
  identity at birth: lemmatization, conventional forms, same-intent judgment, aliases.
- **[completeness.md](completeness.md)** — how documents come to carry their dimensions:
  the applicability line, the generation contract, the verification audit, coverage
  classes.
- **[extension-algebra.md](extension-algebra.md)** — the measurement substrate every
  consumer reads: extensions, operators, evidence states, computation and versioning.

## How to read

Working on tag behavior starts with the definition and the pipeline model, then the spec
for the mechanism being touched. The specs assume the definition's vocabulary and the
pipeline's stage names, and each states what it consumes and what falls outside its scope.

For the machinery as it operates today — matcher configuration and sizing, consolidation
end to end — see the developer guides
([consolidation](../../developer-guide/consolidation.md),
[semantic matching](../../developer-guide/semantic-matching.md)); for the standing design
decisions behind the architecture, see the [decision records](../../adr/README.md).
