# 0005. Two binaries; edub never links CGo

**Status:** accepted

## Context

The system ships two programs from one repository. Document processing — OCR, PDF
rendering, text extraction, tokenizers — needs C libraries through CGo; the API server
and web UI need none of that at runtime. Both binaries must share the task system,
configuration, database access, and the closed type vocabularies. A long-lived HTTP server
built with CGo inherits the C toolchain at build time and the C runtime's crash surface
for no runtime benefit.

Rejected alternatives:

- **One binary with CGo everywhere.** Every edub build, test run, and vuln scan would
  require the host C toolchain, releases would carry the C dependency for a process that
  never calls it, and a C-level crash takes down HTTP serving.
- **Shedding CGo inside one binary with build-constraint partitioning of shared
  packages.** Forces every shared package to be tag-aware: correctness depends on which
  configurations compile, and a stray CGo import fails only some of them instead of
  loudly.

## Decision

`kushim` builds with `CGO_ENABLED=1` and links its C dependencies statically; `edub`
builds with `CGO_ENABLED=0` and is pure Go — one recipe per binary in the Makefile. The
boundary is the import graph: edub's imports must never reach a CGo package, which the
pure-Go build enforces and fails loudly on. Shared code therefore lives only below that
boundary; C-backed work stays on the kushim side.

## Consequences

edub builds, tests, and runs govulncheck with no C toolchain anywhere in its graph, and
the C crash surface cannot reach the API server. The cost is the discipline it puts on
package placement: any new import from edub into a CGo package breaks the build.

Standing constraint: do not import CGo packages from edub's graph, and do not merge the
binaries or guard C-backed paths with build tags in shared code — the build failure is the
mechanism, not an obstacle to route around.
