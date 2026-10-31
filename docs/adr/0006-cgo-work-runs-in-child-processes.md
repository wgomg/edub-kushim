# 0006. CGo work runs in child processes

**Status:** accepted

## Context

Two hazards come from running heavy CGo inside a batch process that must stay alive and
responsive:

- MuPDF's `pdf_clean_file` can SIGSEGV (use-after-free) on malformed files. Called
  in-process, the crash kills `kushim consume` outright, so the Ghostscript fallback never
  gets the chance to run on the very files that need it.
- Long-running CGo — page rendering, Tesseract recognition, PDF assembly — does not yield
  the Go scheduler. The heartbeat goroutine never fires, the batch reads as stale, and the
  reaper reclaims and SIGTERMs a worker that was making progress.

Neither hazard is fixable with retries or `recover`: a SIGSEGV is not a Go panic, and a
blocked C call cannot be preempted by Go-level scheduling.

Beyond the hazards, executing background work raises two requirements of its own: the same
execution model should serve every kind of work, and the orchestrating processes — the
ones that claim batches, heartbeat leases, and reap crashes — should carry as little of
the actual workload as they can.

## Decision

Crash-prone or long-running CGo runs in a forked sibling `kushim` subcommand —
`kushim internal-mupdf-clean` for PDF cleaning and `kushim internal-ocr` for the OCR
pipeline (both intercepted in `cmd/kushim/main.go` before config loading). The parent
blocks on the child's exit, which the runtime executes as a syscall that yields the
scheduler, so heartbeats keep firing throughout; if the child dies, the parent receives an
ordinary Go error and decides the fallback or the failure itself.

That same shape extends past CGo to the work the queue runs: an action that qualifies as a
task type earns its own `kushim` CLI command, and processing that task type forks the
command instead of executing handlers inside the orchestrating process. The dispatcher and
`kushim batch run` fork the type's CLI rather than registering handlers themselves; a type
that declares no batch-mode CLI is refused when its batch is resolved for launch.

## Consequences

A MuPDF crash costs one child process and the fallback path stays reachable; OCR can run
for as long as it needs without the reaper mistaking silence for death. The cost is one
process spawn and temp-file exchange per call.

Every task type arrives with the same shape — one command, one fork, one exit status — so
a new kind of work needs no new execution path, and the orchestrating processes stay small:
claim, fork, heartbeat, reap. The cost is a process spawn and child bootstrap per batch.

Standing constraint: no long-running or crash-prone CGo call inside a process that must
heartbeat or outlive its work — new heavy CGo operations follow the `internal-ocr` /
`internal-mupdf-clean` fork pattern rather than calling C in-process. Every new task type
ships its own batch-mode CLI command; do not grow an orchestrator that executes task
handlers in-process (the wizard's setup-time migration dispatcher, which runs before any
daemon exists to fork, is the exception).
