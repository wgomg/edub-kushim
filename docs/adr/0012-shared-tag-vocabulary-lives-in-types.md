# 0012. Shared tag vocabulary lives in internal/types

**Status:** accepted

## Context

The tag quality rubric and the pair adjudication call answer in closed wire vocabularies —
the person classes and the same-verdict directions — that both the LLM analyzer (prompt and
parser) and the tags domain package (the pinned decision rules) must read. When each side
declares its own constants, one value domain is written twice and can drift; when the domain
package imports the analyzer to borrow them, the dependency points from the domain core into
the LLM adapter and blocks the reverse edge forever: any future analyzer need for tag
vocabulary would close an import cycle. Inverting the edge is not an option either — the
tags root package carries `internal/database` and `internal/task`, which the analyzer graph
must not drag in.

Alternatives rejected: duplicating the constants in both packages (two sources for one wire
vocabulary, changed in lockstep by hand), and declaring the vocabulary inside the analyzer's
tag consumers (the admission, curation and reconcile subpackages are orchestration layers,
not the owner of values defined by the judge's wire shape).

## Decision

Cross-boundary tag wire vocabularies are declared once in `internal/types`, next to
`types.TagVerdict`: the `TagDirection*` values and the five `PersonClass*` values with
`ValidPersonClass`. `internal/types` stays a stdlib-only leaf. The `internal/tags` root
package never imports `internal/llm/analyzer`; the analyzer never imports the tags root; the
`tags/admission`, `tags/curation` and `tags/reconcile` subpackages are the only meeting
points, and they may import both.

## Consequences

A new vocabulary shared between the judge's wire shape and the domain rules goes to
`internal/types`, not to either side. The rubric's versioned persistence makes the person
classes part of the wire contract: changing the class set requires a `TagQualityVersion`
bump, while a pure relocation of constants does not.

Standing constraint: never re-introduce an `internal/tags` root → `internal/llm/analyzer`
import, and keep `internal/types` free of internal imports.
