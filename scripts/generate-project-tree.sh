#!/usr/bin/env bash
#
# regenerates docs/project-tree.md from the tracked directory set
# usage: scripts/generate-project-tree.sh [--check]
#
# the structure comes from git ls-files, so the file never lists untracked or
# gitignored paths; only the one-line descriptions below are curated by hand.
# a directory that is tracked but missing from this map renders bare and is
# reported on stderr at generation time.

set -eu

repo_root=$(git rev-parse --show-toplevel)
target="$repo_root/docs/project-tree.md"

declare -A description=(
  ["build"]="build root for the static C dependencies (contents gitignored)"
  ["cmd"]="binary entry points"
  ["cmd/apidocs"]="renders the committed OpenAPI spec into docs/api.md"
  ["cmd/edub"]="API server and web UI host, CGO_ENABLED=0"
  ["cmd/kushim"]="CLI, document processing, matcher and queue daemons, CGo"
  ["deploy"]="deployment assets"
  ["deploy/systemd"]="systemd unit and target templates"
  ["docker"]="container files: glibc and musl builders, deployment entrypoint"
  ["docs"]="project documentation"
  ["docs/adr"]="architecture decision records"
  ["docs/developer-guide"]="language and feature onboarding guides (Go, frontend, PostgreSQL, cgo, OCR, tasks, LLM, semantic matching, consolidation, algorithms)"
  ["docs/manual"]="user documentation: entry point, CLI, API usage, Web UI, configuration"
  ["docs/manual/images"]="screenshots for the Web UI guide"
  ["docs/research"]="design notes and investigation reports"
  ["docs/specs"]="living normative specifications"
  ["docs/specs/tags"]="tag subsystem specifications, entry point overview.md"
  [".github"]="GitHub configuration"
  [".github/workflows"]="ci.yml (tests, CGo, web, vulncheck) and release.yml"
  ["internal"]="Go packages"
  ["internal/api"]="HTTP server, middleware, permission gates, wizard server, embedded SPA"
  ["internal/api/handlers"]="one file per resource: handlers with their API DTOs"
  ["internal/api/spec"]="generated OpenAPI spec (make api-spec), embedded by edub"
  ["internal/api/wizard"]="setup wizard HTTP server and embedded SPA"
  ["internal/auth"]="JWT tokens, claims, roles, API key hashing"
  ["internal/backup"]="backup, restore, scheduling, rsync document mirror"
  ["internal/cgo"]="MuPDF CGo glue and its non-CGo stub"
  ["internal/config"]="configuration structs, loading, defaults, watcher, service files"
  ["internal/database"]="pgx connection, sqlc queries, schema bootstrap, dumps, restore and migration helpers"
  ["internal/database/sql"]="embedded SQL assets"
  ["internal/database/sql/queries"]="sqlc query sources, regenerated into *.sql.go"
  ["internal/database/sql/schema"]="seed SQL files"
  ["internal/database/sql/schema/migrations"]="goose migrations, the schema source of truth"
  ["internal/documents"]="document domain: ingestion, storage, MIME, processing adapters"
  ["internal/documents/consumption"]="processing engine and its task handlers"
  ["internal/documents/converter"]="LibreOffice headless DOCX/ODT to PDF conversion"
  ["internal/documents/mime"]="MIME predicates, extension mapping, supported types"
  ["internal/documents/ocr"]="OCR engines: gosseract, ocrmypdf, standalone pipeline"
  ["internal/documents/pdfoptimizer"]="PDF optimization adapters"
  ["internal/documents/storage"]="storage layout and document, orphaned and trash file operations"
  ["internal/documents/textextractor"]="text extraction adapters (MuPDF, gopdf, pdftotext, DOCX, ODT)"
  ["internal/documents/thumbnail"]="page thumbnail rendering"
  ["internal/enrichment"]="LLM enrichment engine and its task handler"
  ["internal/errs"]="error classification and database error mapping"
  ["internal/llm"]="LLM capability registry and provider/model catalogs"
  ["internal/llm/analyzer"]="LLM classification adapters, prompts, gates and judges"
  ["internal/llm/testdata"]="analyzer test fixtures"
  ["internal/log"]="structured logging and memory metrics"
  ["internal/matcher"]="tag matcher: Hugot engine, RPC client, embedding store"
  ["internal/ops"]="process layer: CLI verbs, queue daemon, dispatcher, consume child, setup"
  ["internal/ops/cli"]="kushim command table and verbs"
  ["internal/ops/configtask"]="migrate-db and migrate-storage task handlers"
  ["internal/ops/consume"]="consume child process and tool advisories"
  ["internal/ops/container"]="shared-services container: config, database, matcher, tag tools"
  ["internal/ops/dispatch"]="batch dispatcher and launcher: claim, fork, in-process runs"
  ["internal/ops/flags"]="CLI flag parser"
  ["internal/ops/queue"]="queue daemon: scheduler, reaper, hot reload"
  ["internal/ops/setup"]="setup command, web wizard and terminal mode"
  ["internal/ops/tagmaintenance"]="tag maintenance routine task handler"
  ["internal/search"]="full-text and structured search engine"
  ["internal/service"]="shared domain services: people, document types, batches, trash, users"
  ["internal/static"]="embedded main SPA build"
  ["internal/tags"]="tag subsystem: store, measurement, identity, decisions, worklists"
  ["internal/tags/admission"]="tag admission: adjudicator, identity resolver, audit runner"
  ["internal/tags/curation"]="tag vocabulary services for operators and adapters"
  ["internal/tags/policy"]="pure candidate generation: identity and band subjects"
  ["internal/tags/reconcile"]="scheduled re-adjudication: worklist, evidence, apply, sweeps"
  ["internal/task"]="task system: batches, enqueue, catalog, runner, locks"
  ["internal/testutil"]="test helpers: config fixtures, minimal PDFs, assertions"
  ["internal/textnorm"]="tag and database-key canonicalization"
  ["internal/tools"]="tool adapter runner and text reduction"
  ["internal/tools/textreducer"]="TextRank summarization"
  ["internal/types"]="closed task-system vocabulary registry"
  ["internal/version"]="application version"
  ["scripts"]="repository maintenance scripts"
  ["web"]="main SvelteKit SPA, embedded into edub"
  ["web/e2e"]="Playwright smoke specs against the static build with a mocked API"
  ["web/e2e/fixtures"]="E2E fixtures"
  ["web/scripts"]="SPA helper scripts"
  ["web/src"]="SPA source"
  ["web/src/lib"]="shared SPA modules"
  ["web/src/lib/assets"]="static assets"
  ["web/src/lib/components"]="reusable Svelte components"
  ["web/src/lib/components/settings"]="settings-area section components"
  ["web/src/lib/components/tags"]="tag-review components"
  ["web/src/lib/pdf"]="self-hosted pdf.js loader, worker wrapper, polyfills"
  ["web/src/lib/stores"]="runes-based state stores"
  ["web/src/lib/stubs"]="build stubs for optional modules"
  ["web/src/lib/utils"]="formatting, status-chip and tag-report helpers"
  ["web/src/routes"]="SvelteKit routes"
  ["web/src/routes/documents"]="document list: search, filters, saved searches"
  ["web/src/routes/documents/[id]"]="document detail: PDF viewer and metadata editor"
  ["web/src/routes/documents/orphaned"]="orphaned and errored file management"
  ["web/src/routes/document-types"]="document type management"
  ["web/src/routes/docs"]="generated API reference rendered by Scalar"
  ["web/src/routes/login"]="login page"
  ["web/src/routes/logs"]="log viewer with per-daemon tabs"
  ["web/src/routes/people"]="people and person-type management"
  ["web/src/routes/profile"]="self-service profile and API keys"
  ["web/src/routes/settings"]="settings hub"
  ["web/src/routes/settings/access"]="settings page: access and authentication"
  ["web/src/routes/settings/enrichment"]="settings page: enrichment"
  ["web/src/routes/settings/intake"]="settings page: intake and polling"
  ["web/src/routes/settings/models"]="settings page: AI providers and models"
  ["web/src/routes/settings/processing"]="settings page: processing pipeline"
  ["web/src/routes/settings/safety"]="settings page: backups, mirror and trash safety"
  ["web/src/routes/settings/storage"]="settings page: storage"
  ["web/src/routes/settings/system"]="settings page: system"
  ["web/src/routes/tags"]="tag workspace: vocabulary, new tags, coverage, overlap, deleted"
  ["web/src/routes/tasks"]="batch and task monitoring"
  ["web/src/routes/tasks/[id]"]="task detail page"
  ["web/src/routes/trash"]="trash: soft-deleted documents"
  ["web/static"]="SPA static files"
  ["web-wizard"]="setup wizard SPA, embedded into kushim"
  ["web-wizard/src"]="wizard source"
  ["web-wizard/src/lib"]="wizard API client"
  ["web-wizard/src/routes"]="six-step setup flow"
)

generate() {
  local tmpdir=$1

  (cd "$repo_root" && git ls-files) |
    awk -F/ '{p=""; for (i = 1; i < NF; i++) {p = p (i > 1 ? "/" : "") $i; print p}}' |
    LC_ALL=C sort -u >"$tmpdir/dirs"

  for dir in "${!description[@]}"; do
    printf '%s\t%s\n' "$dir" "${description[$dir]}"
  done >"$tmpdir/descriptions"

  cat <<'HEADER'
# Project tree

Directory layout derived from the tracked file set (`git ls-files`), so it cannot list paths that
are absent from the repository. Regenerate with `make docs-tree`; the one-line descriptions live in
`scripts/generate-project-tree.sh`.

```
HEADER

  awk -F'\t' '
    FNR == NR { desc[$1] = $2; next }
    { paths[++count] = $0 }
    END {
      for (i = 1; i <= count; i++) {
        depth[i] = split(paths[i], seg, "/")
        parent[i] = ""
        for (j = 1; j < depth[i]; j++) parent[i] = parent[i] (j > 1 ? "/" : "") seg[j]
      }
      for (i = 1; i <= count; i++) {
        for (j = i + 1; j <= count; j++) {
          if (depth[j] == depth[i] && parent[j] == parent[i]) { sib[paths[i]] = 1; break }
        }
      }
      print "edub-kushim/"
      for (i = 1; i <= count; i++) {
        n = split(paths[i], seg, "/")
        line = ""
        anc = ""
        for (k = 1; k < n; k++) {
          anc = anc (k > 1 ? "/" : "") seg[k]
          line = line (sib[anc] ? "│   " : "    ")
        }
        line = line (sib[paths[i]] ? "├── " : "└── ") seg[n]
        if (paths[i] in desc) line = line "  # " desc[paths[i]]
        print line
      }
      for (i = 1; i <= count; i++) {
        if (!(paths[i] in desc)) print "warning: no description for " paths[i] > "/dev/stderr"
      }
    }
  ' "$tmpdir/descriptions" "$tmpdir/dirs"

  printf '```\n'
}

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

if [ "${1:-}" = "--check" ]; then
  generate "$tmpdir" >"$tmpdir/expected"
  if ! diff -u "$target" "$tmpdir/expected"; then
    echo "docs/project-tree.md is stale: run make docs-tree" >&2
    exit 1
  fi
  exit 0
fi

generate "$tmpdir" >"$tmpdir/tree.md"
mv "$tmpdir/tree.md" "$target"
