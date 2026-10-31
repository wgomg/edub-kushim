# 0009. LLM catalogs: seed once, user edits authoritative

**Status:** accepted

## Context

Provider and model catalogs live as editable files in the config directory
(`provider_catalog.json`, `model_catalog.json`) and drive capabilities, request dialects,
and the reasoning policy. The application ships improved defaults over time while
operators edit the same files — custom providers, endpoint tweaks, retiring models. A
refreshed default must reach installations without ever touching an operator's edits.

Rejected alternatives:

- **Overwrite on startup.** Destroys operator edits on every upgrade — the reliable way
  to lose a working custom provider.
- **No embedded defaults.** Every fresh install would require hand-editing JSON before
  the enricher can run.

## Decision

The loader seeds a catalog file only when it is missing and never overwrites an existing
one: embedded defaults are a first-run seed, not a mirror. Format changes are gated by
`catalogVersion` (`internal/llm/registry.go`) — a mismatched file is rejected loudly
rather than rewritten. Edits serialize through a cross-process `flock` on
`<config dir>/catalog.lock` taken before the in-memory mutex, persist atomically per file,
and preserve the file's `_comment` hint. When defaults must land on an existing
installation — a missing or mangled file — the reset is explicit and scoped:
`ResetCatalogs` / `POST /api/v1/llm/reset` repairs only the file it owns.

## Consequences

Operator edits survive upgrades and restarts; a corrupted file fails loudly (catalog
reads answer 503 until an explicit reset) instead of being silently replaced. The cost:
refreshed defaults reach fresh installs and explicit resets only, never existing files —
so a default correction requires an operator action to propagate.

Standing constraint: never overwrite an existing catalog file from code — defaults arrive
by first-run seed or explicit reset only, and any external defaults source must layer over
user-edited entries the same way.
