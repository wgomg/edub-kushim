# Documentation

This is the map of the documentation tree. Each area below holds one kind of document:
living docs describe the system as it is now, specs state standing rules and invariants,
ADRs record frozen decisions with their rejected alternatives, and research holds
point-in-time analysis that is never treated as current truth.

## Where to start

| Reading for | Start at |
|---|---|
| Using the application (CLI, API, configuration) | [user-manual.md](user-manual.md) |
| Understanding the system's design | [architecture.md](architecture.md) |
| Working on the code (one subsystem at a time) | [developer-guide/](developer-guide/) |
| What a domain object must be (normative rules) | [specs/tags/overview.md](specs/tags/overview.md) |
| Why a design decision was made / must not be undone | [adr/README.md](adr/README.md) |
| What is done and what is next | [roadmap.md](roadmap.md) |

## The areas

### User docs

- [user-manual.md](user-manual.md) — how to install, configure, and operate edub-kushim
  (CLI + API reference).

### Developer guide

`developer-guide/` — living, per-subsystem and per-language explanation of how the code
works now: `golang.md`, `frontend.md`, `postgresql.md`, `cgo.md`, `algorithms.md`,
`ocr-pipeline.md`, `llm.md`, `semantic-matching.md`, `consolidation.md`,
`task-system.md`. Read one as you touch that area.

### Specs

`specs/` — living normative specifications: what a domain object is, the predicates and
invariants every implementation must respect, and the standing duties. They constrain
future changes, not only the current code. Entry point: `specs/tags/overview.md`.

### Decision records

`adr/` — Architecture Decision Records, frozen once accepted. Each records one standing
decision with its context, consequences, and the alternatives that were rejected. The
index with status and summaries is `adr/README.md`.

### Research

`research/` — point-in-time analyses and design notes. Useful context; never normative,
never current truth.

### Root docs

- [architecture.md](architecture.md) — design and pipeline overview.
- [roadmap.md](roadmap.md) — implementation status and backlog.
- [frontend.md](frontend.md) — SPA route and key-file map.
- [tests.md](tests.md) — testing reference.

### Generated artifacts

- [api.md](api.md) — REST API reference rendered from the OpenAPI spec
  (`make api-docs`, `make api-spec-check`).
- [project-tree.md](project-tree.md) — directory map (`make docs-tree`).
