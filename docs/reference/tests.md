# Testing Reference

## Overview

The project has **37 test packages and 920 unit/integration tests**, with a three-tier
run strategy (Go backend + web UI):

- **`make test`** (18 packages, no database required) — LLM registry, config, API types,
  tools runner, logging (`internal/log`), text normalization (`internal/textnorm`), tagmatch, storage walk, auth tokens,
  auth middleware + permissions, content analyzer (prompt building, tag filtering, token
  estimation), error classification (`errs`), worker pool lifecycle, mirror (rsync
  stats parsing, remote-target detection, destination guards), the task-system registry
  (`types`), the tag extension algebra (`tags`), the setup wizard routes
  (`internal/api/wizard`), and the shared flag parser (`ops/flags`).
- **`make test-db`** (8 additional packages, requires PostgreSQL) — database CRUD queries,
  search engine, task system (store/dispatcher/runner/pool), service layer (batch,
  orphaned, errored files, users, enrichment, API keys), the tag curation services
  (`internal/tags/curation`: tag CRUD, merge, identity-run enqueue), API handlers,
  consumption pipeline, and the tag measurement runner.
- **`make test-web`** (vitest, no database) — 115 unit + component tests across 13 files
  in `web/`: pure logic (`searchFilter`, `html` utils, `tagReconcile` utils), stores
  (`filterStore`, `authStore`, `toastStore`, `confirmStore`), the `api` client (fetch
  stubbing, auth header, 401 handling), and the logic-dense components (`DataTable`,
  `FilterPanel`, `UploadModal`, settings page — the settings test covers the tag
  adjudicator/quality/reconciliation cards, inherit-from-analyzer options,
  default-fill blocks, and the full save payload — including the tag-reconcile
  grace fields). Component tests run under jsdom
  with `@testing-library/svelte`.
- **`make test-web-e2e`** (Playwright, no backend) — 4 smoke specs in `web/e2e/`
  (login, documents list + detail, upload) driven against the static SPA build with a
  mocked API (`page.route` intercepts `/api|/wizard|/health`); no `edub`/PostgreSQL
  needed. Requires `npx playwright install chromium` once.

All non-CGo tiers run with `CGO_ENABLED=0` (no Tesseract, MuPDF, or
Ghostscript required); `make test-cgo` and `make test-cgo-db` run with
`CGO_ENABLED=1` and need `make build-deps` first. Every invocation goes through
the Makefile, which exports the `-tags "XLA,ORT"` tags and the CGo environment
— bare `go test` is not supported.

### Quick Start

```bash
make test          # runs all non-CGo tests
make test-verbose  # same with verbose output
make test-db       # database-dependent tests (requires TEST_DATABASE_URL)
make test-cgo      # CGo-gated tests (requires make build-deps first)
make test-cgo-db   # consumption + ops/configtask + ops/cli + ops/consume + ops/queue + enrichment + tags + tags/admission + tags/curation + tags/reconcile + api/handlers with CGo + DB (requires make build-deps + TEST_DATABASE_URL)
make test-one PKG=./internal/errs/   # single package; add RUN=Name to filter
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium first)
make vuln          # govulncheck over the Go vuln DB (CGO_ENABLED=0)
make vuln-cgo      # CGo-enabled variant, full call graph (requires make build-deps first)
```

### Continuous Integration

`.github/workflows/ci.yml` runs the Go tiers and the web tests on every push to `dev`/`master` and every PR to `master`:

- `web` job: lints, runs `npm run test` (vitest), and builds the main SPA (`npm ci && npm run lint && npm run test && npm run build` in `web/`), then runs the E2E smokes (`npx playwright install --with-deps chromium && npm run test:e2e` — the E2E `webServer` builds the SPA itself and serves it with an SPA fallback). Then builds the wizard SPA, stages both with `make stage-web`, and uploads them as the `web-assets` artifact. Staging is required because `internal/static/build` and `internal/api/wizard/static` are gitignored but embedded via `//go:embed` — a fresh checkout has no assets to compile against.
- `test` job (depends on `web`): downloads `web-assets`, stages it with `make stage-web-artifact`, then runs `make test` and `make test-db` against a postgres:17 service container (`TEST_DATABASE_URL` pointing at `localhost:5432`).
- `test-cgo` job (depends on `web`): installs the C build prerequisites, builds the C libraries with `make build-deps TOKENIZERS_ARCH=amd64` (cached under `build/` keyed by `hashFiles('Makefile')`), then runs `make test-cgo` (no DB; DB-gated tests in `internal/ops/consume`, `internal/ops/queue`, `internal/enrichment`, `internal/tags`, `internal/tags/admission` and `internal/tags/reconcile` skip) and `make test-cgo-db` (with DB; those tests actually run) against the same postgres:17 service container. This surfaces the CGo-only packages (`internal/ops/cli`, `internal/ops/consume`, consumption under CGo) on every push/PR.
- `vulncheck` job (depends on `web`): stages the `web-assets` artifact, installs govulncheck (pinned `@v1.7.0`), and runs `make vuln`. Like `make test`, it runs with `CGO_ENABLED=0`, so the CGo-gated packages (`cmd/kushim`, `internal/ops/cli`, `internal/ops/queue`) are excluded from the CI scan; `make vuln-cgo` covers them locally.

The `global` ruleset requires the `test` and `web` checks to pass before a PR to `master` can merge.

---

## Package Layout

| Package | Tests | What it covers |
|---------|-------|---------------|
| `internal/llm` | 27 | Registry creation (fixture + embedded fallback), model lookup for known/missing providers and models, adapter/provider/model listing, default URL derivation, catalog reload, concurrent access safety |
| `internal/config` | 54 | Default config values (incl. `tag_reconcile`, `tag_measure` and `provisional_grace_days` defaults), the identity-candidate-floor defaults (`TestDefaultIdentityCandidateSimilarity`: per-model floors held above the document-to-tag threshold), ParseHHMM, polling window validation (valid + invalid), IsWithinActiveWindows, OCR workers, finalizeConfig (reclaim max retries, doc-type refinement non-negative, request-delay bounds, tag reconcile `dead_tag_grace_days` and `provisional_grace_days` zero/negative → default, tag measure interval/time defaults and invalid time rejection, fallback validation: missing adapter/provider/model rejected, negative fallback request_delay rejected, valid enabled fallback accepted, nil/disabled fallback skipped), Load (applies OcrWorkers, and the suggestion-injection bar: derived per-model default, explicit value honored, explicit zero reverts, negative rejected), SaveMap (merge/no file/invalid file), watcher (fire on change/missing file/invalid YAML), IsRemoteMirrorTarget (11 cases: rsync://, user@host, host-only colon, slash/whitespace rejections), ValidateMirrorDestination (dash-prefixed refused, remote skipped, inside-storage refused, storage-itself refused, ancestor-of-storage refused, inside-backup refused, unrelated local accepted, symlink-into-storage refused) |
| `internal/documents/mime` | 9 | IsPDF/IsImage/IsOfficeDoc predicates, ExtensionFromMimeType, MimeTypeFromExtension (canonical + alias extensions, case-insensitive, unknown → octet-stream), ExtensionsFor (canonical + aliases per type, nil for unknown), BuildExtensionSet, Supported constants + slice invariants |
| `internal/tools` | 28 | OptimizePdf (no timeout/with timeout/file not found/cancellation/nil optimizer), ConvertToPdf (nil converter/file not found/with converter/timeout deadline), AnalyzeContent/AnalyzeDocType (timeout deadline, nil analyzer), isProviderError (generic/credits true; content-too-large, token-limit, canceled, deadline-exceeded false — direct and wrapped), fallback paths (provider error → fallback result; ContentTooLargeError, cancellation, and DeadlineExceeded never call the fallback; no fallback configured → error; fallback failure propagates with the fallback provider; doc-type refinement fallback), and the judged-call timeout and missing-adapter contract (`TestJudgeTagParts_TimeoutAndMissingAdapter`: the configured adjudicator timeout reaches the judge's context; no adjudicator is a not-configured failure) |
| `internal/log` | 23 | Logger level parsing, SetLevel gating (Info/Error/Debug/Warn), REQID prefix formatting (non-empty and empty ids), format-string interpolation, console format (priority prefix, source location, timestamp), discard logger, file logging (append/invalid path/all levels/source exclusion/level prefix), SlogLogger bridge |
| `internal/textnorm` | 4 | NormalizeTag (tag canonicalization), SplitJointTag (23 cases: conjunction set incl. `vs.`/`versus`, leading/trailing conjunction rejection, multi-conjunction, multi-word segments, mixed case, non-joint inputs), NormalizeForDB (accent folding, punctuation, spaces), NormalizeForDB_Concurrent (8-goroutine race guard pinning exact outputs) |
| `internal/matcher` | 12 | `MaxMatchBodyBytes` derivation with floor/ceiling clamping; `TargetWordCount` budget (positive target passthrough, negative target as a divisor of the content word count, 2000-word floor); `truncateUTF8` UTF-8-safe truncation with CJK and ASCII boundary tests; idempotency check; `TagEmbeddingStore` nil-map construction with `Add` replacement and `Remove`; `Entries` deep-copy isolation from caller mutation |
| `internal/documents/storage` | 32 | DetectFileType (uuid/dbid/random), WalkStorageDir (finds PDFs/skips non-PDF/recent/unknown key types/missing dirs), QuarantineFile, RemoveOrphanedFile, CopyToConsumptionDir, TrashDir/DocumentTrashDir, MoveToTrash (happy path, missing original, missing storage), RestoreFromTrash (happy path, missing trash original, both missing, round-trip), RemoveFromTrash (exists, nonexistent), CountFilePaths (supported-only, missing directory, non-regular skipped), SameDevice (same dir, missing target falls back to parent's device), SHA256Hex (known vectors, different inputs) |
| `internal/auth` | 7 | Session secret generation, JWT generation/validation round-trip with role, wrong secret rejection, expired token rejection, malformed token rejection, token part structure, ValidRole cases |
| `internal/api` | 25 | Auth middleware: public path bypass, no-token 401, invalid token 401, wrong secret 401, valid token passes with context injection (includes role), missing bearer prefix, empty authorization header, disabled bypasses all paths, valid API key 200 (includes role), invalid API key 401, wrong prefix falls through to JWT, auth-disabled bypasses API key check, internal error returns 500, valid cookie, invalid cookie, header takes priority over cookie. Permission middleware: RequireRole for admin/editor/viewer (allows+forbids), missing role, invalid role |
| `internal/api/wizard` | 1 | Setup wizard server: route assembly and serving with a nil database client (`TestStart_NilClientServesRoutes` — the status endpoint answers with `configured` and the static root serves 200) |
| `internal/llm/analyzer` | 90 | the OpenAI-compatible Responses dialect (endpoint selection, the `instructions`/`input` envelope with `store: false`, `output_text` folding with reasoning items skipped, usage mapping, and `incomplete`/`max_output_tokens` → `OutputCapError`), the two-state reasoning policy across its three default states (no cataloged levels, lowest cataloged effort, provider disable form), and budget doubling for reasoning-by-default models; NormalizeTags (transforms, accent folding, dedup/drop), estimateTokens (ASCII/CJK/mixed), NormalizeCore_Concurrent (8-goroutine race guard pinning exact outputs), BuildPrompt (default/custom/malformed/execution error/whitespace/empty tag list), BuildDocTypePrompt (with/without metadata), ExtractHeadTailWords, FilterTags (18 cases: person token overlap, doc-type match, known name subset, >3-word cap, no count cap, combined rules, title-contained tags kept), ParseAnalysisResponse (string/object/mixed tag arrays, skipped malformed and empty-term elements, category folding, unknown→other, non-array error, missing/null/empty tags), SeparateFacets (form_genre drops with the document-type/self-classification reason split, person drops, place and work ordinary-candidate passthrough, topic/other and untyped passthrough, nil/empty details passthrough, normalization alignment), normalizeTagCategory, DocMetadata.Format (all fields/partial/zero-valued), checkContentTooLarge (nil caps/zero/negative/within limit/exceeds), parseTokenLimitError (no match/empty/valid OpenAI/zero tokens/partial match), BuildTagPartPrompt (the v4 lexicalized-derivation and abstract-field clauses), the v5 quality envelope (a reply that omits the `dimension` key parses complete, since the parts pass owns it), BuildTagTranslationPrompt, ParseTagTranslationResponse (a full reply with translations and abstentions, an omitted entry reported as incomplete with its missing indices, an omitted `defer` read as false), BuildTagPhraseFoldPrompt (phrase/part/anchor lines and the fold/defer questions), ParseTagPhraseFoldResponse (a full reply with folds and abstentions, an omitted entry and an entry without a fold answer reported as incomplete with their missing indices, an omitted `defer` read as false), ParseTagPartResponse (dimensional and container answers, the abstention, a missing answer and an out-of-range index as incomplete, code-block strip, bare-array root and positional indexes, invalid JSON), BuildTagCompoundPrompt, ParseTagCompoundResponse (the three readings, the abstention, an unrecognized or missing status as incomplete, code-block strip, bare-array root and positional indexes, invalid JSON), SplitComponents (dedup, empty drop, normalization, 4-word/too-long rejection, 3-word keep, hard per-document filter: person/doc-type drops, the title-echo exemption for parts), BuildTagQualityPrompt (document text, per-candidate line, the four-question contract and criteria keywords, the person-only second criterion, the publishing-format/genre boundary and the language carve-out wording), ParseTagQualityResponse's typed `IncompleteReplyError` on a truncated reply (the missing indices carried alongside the entries it read) and the adapter partial-reply salvage branch (`TestJudgedOpenAI_PartialQualityReplyKeepsResults` / `TestJudgedAnthropic_PartialQualityReplyKeepsResults`), BuildAdjudicationPrompt (the reworded `same` reachability clause, the absent doubt clause, and the terms-only contract — `TestBuildAdjudicationPrompt_TermsOnlyEvidence`: the prompt renders only the pair's two terms), the default prompt template's publishing-format clause, ParseTagQualityResponse (passing/failing verdicts, missing-index and incomplete-verdict entries as typed incomplete replies with the results preserved, invalid JSON, the `defer` abstention key, bare-array positional), ParseTagIdentityResponse (complete/fenced/incomplete and missing-index as typed incomplete replies with the results preserved, invalid JSON, bare-array positional), ParseAdjudicationResponse (verdict and direction mapping, bare-array root, positional and mixed indexes, unindexed extras beyond n dropped, and missing/unknown/out-of-range entries as a typed incomplete reply with the results preserved), the judged-step bridges (request envelope and fenced parsing, capability-gated fields, step error labels, empty response, content gate, bare-array replies, bounded parse errors), the judged-call output budgets (analysis 4096, judged `512 + 128 × candidates`, the catalog clamp, nil/zero-caps pass-through, the `max_completion_tokens` field swap), the output-cap error (`OutputCapError` on `finish_reason: "length"` with the step-label prefix), and TagDetail presence semantics (string entries carry no detail; object entries carry term and category) |
| `internal/enrichment` | 9 | truncate (ASCII, CJK, mixed, trim), the enrich task handler's dedup key (`TestEnrichTaskHandlerDedupKey`: document id → `enrich:doc:<id>`, missing/undecodable payload → empty key), the result envelope (`TestEnrichResultEnvelope`: counts round-trip, nil stats as JSON null, pre-envelope and undecodable payloads yield no counts, invalid stats fail the marshal), the per-document count tally plus its accumulator (`TestDocumentCounts`: births from `Created` results, rejects from the rejection set, defers from the decisions map, composition dispositions, quality layer off as zeros; `TestDocumentCountsAdd` sums every field), the suggestion-injection bar at the emission seam (`TestEnrich_SuggestionInjectionBar`: above-bar matches kept, below-bar dropped, empty list when nothing clears; DB-gated), and the credit-pause mapping (`TestCreditPause`: credit error with the flag on → `PauseBatch`, flag off → no pause, non-credit error → nil), and the keep-row part prune (`TestPruneRejectedParts`: parts resolved through the plan's output mapping before the rejection test, rejected parts dropped, survivors keeping their emitted spelling) |
| `internal/tags/admission` | 59 | The shared compound judgment (`TestJudgeCompounds_RowContract`: one row per candidate in candidate order, split rows carrying their components and keep rows the parts applied alongside, and no document on the rows; `TestConjunctionComponents`: a conjunction folds each segment onto its store anchor, a plain compound and an over-bound name return nil), Compound decomposition (`TestDecomposeTags_*`: no-judge conjunction fallback and pass-through, the two-pass decision (a composition with dimensional parts splits, a named unit with non-dimensional parts keeps, a no-dimension phrase with non-dimensional parts drops, a deferred reading keeps), the part-count bound, per-document component filtering, judge-failure keep, store-keyed compounds skipped before judgment with no judge call and no composition row (only once a decided verdict exists, so a rowless or degraded store-present compound is re-judged until then), a decided store-existing keep applying its cached parts alongside idempotently, the no-adjudicator skip writing no row and never re-judging, per-candidate composition rows (disposition, reason, validated segments, criteria version, judge model for fresh verdicts), fold resolution before filtering (`TestDecomposeTags_FoldResolvesPartsBeforeFilter`: parts land on store anchors in the output and the row, no store view passes through, the hard rules evaluate the resolved part), persisted per-value and per-compound verdict caches (a re-emitted value or compound resolves with no judge call; a cache-applied split row is marked `cached` and model-less), the reconstructibility signal on both sides (`named_unit_reconstructible` with an established store view, `relational_keep` without one), the verdict store's conflict reread (a matching abstention agrees, a differing reading disagrees and is flagged), partial-reply salvage for all three judged families (parts, compounds, and phrase folds; returned entries persist and apply, omitted indices defer with no row and no retry, and a salvaged phrase fold answers the cache on re-read), versioned split log, the store-existing period fiat skip (a seeded period keeps whole with no judge call and no row, `TestDecomposeTags_StoreExistingPeriodIsFiatSkip`), the language guard (`ResolveLanguages`: detected non-English values rewritten in place including their details, English and store-existing values untouched, an abstained value held in the pending set), the held-for-review gate (a `ForceDefer` value kept with `reason=language_review` and no judge call), the dimension abstain (a missing parts verdict defers instead of counting as a failing dimension), and the parts false guard (a promoted value's false defers with no second sample, a contradicted false defers, a corroborated false persists)), identity planning (`TestPlanIdentity_*`: pending-pair logging, fold-stays-pending without a judge, store-exact emissions applying onto their own row with the name set and no evidence, a confirmed fold-birth applying without pending rows or a provisional flag, and a derivational proposal reaching the judge and resolving onto the store noun), the plan's input→output mapping (`TestOutputByInput`: an alias action resolves its loser onto the canonical the store applies, a registration outranks a colliding alias, and an entry missing a name or a target adds nothing), DB-gated pair adjudication (`TestAdjudicateIdentityPairs`: cached verdict applies without a call, fresh verdicts recorded, errors stay pending, a pinned variant class resolves with no judge call and records the class with no model), resolver folds and same-intent aliasing (`TestResolvePending_*`: confirmed fold renames, deleted fold target is not resurrected, disabled adjudicator leaves evidence pending, derivational fold onto the store noun with one proposal feeding the judge input, the stored criteria and the alias apply), verdict-event conflict re-read (`TestWriteVerdictEvent_ConflictReReadsStoredVerdict`), the partial-reply salvage (`TestApplyTagQuality_PartialSalvage`: salvaged entries decided and row-written, missing indices deferred `judge_unavailable` with no row; the label-wrapped `IncompleteReplyError` is not retried), the stored-verdict decision mapping (`TestDecisionFromStored`: rubric re-derivation beats `pass`, caller deterministic signals join the rubric, legacy/malformed criteria fall back to the pass mapping), the DB-gated admission flow (`TestApplyTagQuality`: admit kept with its recorded outcome, corroborated failure dropped, a lone deterministic signal defers, cached rejection answered without a judge call, judge outage keeps the tag with a defer decision and writes no row), the failed-judge WARN paths (`TestDecomposeTags_NoVerdictWarns` for the parseable no-verdict response, `TestPlanIdentity_JudgeFailureWarns`, `TestApplyTagQuality_JudgeFailureWarns`), and the DB-gated fresh-verdict insert + cache read path (`TestTagQualityEvent_InsertAndCacheRead`, including the `outcome` column round-trip), the judged-retry classification (`TestRetryableJudgeFailure`: transport/generic/empty failures retry once; parse, incomplete-reply, output-cap, credit, token-limit, content-too-large and context errors never — direct and label-wrapped), and the credit-error propagation and loop-stop at every judged step (`TestDecomposeTags_CreditErrorEscapes`, `TestDecomposeTags_TransientFailureRetriesOnce`, `TestApplyTagQuality_CreditErrorEscapes`, `TestPlanIdentity_CreditErrorEscapes`, `TestIdentityAnswers_CreditErrorStopsRemainingBatches` — 41 candidates force two batches and the first stops the loop, `TestAdjudicateIdentityPairs_CreditErrorEscapes`), the identity and pair partial-reply salvage (`TestIdentityAnswers_PartialSalvage` — the returned answers recorded and the missing candidate left unanswered; `TestAdjudicateIdentityPairs`'s partial subtest — the returned pair decided and recorded, the missing pair undecided with no row), the resolver partial-reply salvage (`TestResolvePending_PartialCandidateReply` / `TestResolvePending_PartialPairReply` — the returned events resolved, the missing ones deferred and left pending), a pinned variant class resolving mechanically onto the tie-break winner (`TestResolvePending_DeterministicVariantPair`: orthographic and inverted pairs alias with no judge call, events carry the class and no model), the shared fan helpers (`TestResolveContentSingles`: the time drop and the fold resolution aligned with `DeriveParts`; `TestBuildFan`: singles and bigrams resolved, a nil singles slice, a dropped word, a time-designated bigram; `TestPhraseFoldKeys`: residual-word questions only; `TestApplyPhraseFolds`: fold, abstention, and ambiguity; `TestCoverParts`: a full cover versus the flat unit branch and a fan-scoped abstention; `TestSeedStoreValues`: store values seeded dimensional, existing verdicts kept), the birth-time orthographic pair (`TestPlanIdentity_OrthographicVariantResolvesWithoutJudge`: a store twin aliases with no judge call and no pending evidence), the derived-parts time gate (`TestDecomposeTags_TimeDesignationPartDropped`: an unseeded month never judged and only the survivor applied alongside, a store-held month exempt and judged), and the quality-judge batching (`TestApplyTagQuality_BatchesJudgeCalls` — one past `qualityBatchSize` splits into two calls of 40 and 1) |
| `internal/tags/reconcile` | 41 | Bootstrap log parsing (pre-rewrite/unversioned/band-keep including `reason=rank_unavailable`, REQID resolution, malformed lines ignored), worklist building (stale/unjudged/drifted/aged categories, ordering and caps, conjunction pair exclusion), split-component evidence parsing, report-only planning, sample-name suffix rendering, deleted-tag keep-flip logging, provisional-sweep helpers (`verdictModelLabel` branches, `ProvisionalCounts` JSON shape and `Report.Provisional` presence), the provisional outcome helper (`TestProvisionalOutcome`: admit at the floor promotes, admit below the floor is a silent pending candidate, defer and reject stay for review), and the DB-gated `TestApplyKeepFlip_ZeroDocsCreatesNoTag` (zero resolvable documents must not create the query tag; runs under `make test-cgo-db`), and the compound-sweep disposal (`TestDecideCompoundSplits_DeterministicWithoutAdjudicator`: explicit coordination splits mechanically and a name whose parts all filter away reports `split_invalid`; `TestDecideCompoundSplits_DisposesThroughDecision`: cached verdicts dispose through the composition decision — a composition with dimensional parts splits, a named unit with non-dimensional parts keeps with the decision reason as its skip reason; DB-gated), the decided-drop demotion worklist (`TestBuildCompoundWorklist_DecidedDropQueuesDemotion`: a decided drop of a standing value queues a revocation with the journal reason, the same drop on a provisional value is skipped as decided; DB-gated), and the demotion arm (`TestSweepPromotedTags_MassDecayDemotesAndSparesSeeded`: decay below the floor demotes and records `mass_decay`, seeded values stay out of the worklist; `TestSweepPromotedTags_RunBoundIsSharedWithRejudge`: revocations and re-judgments draw on one per-run bound with the deferral counted as capped and the value above the floor untouched; DB-gated), and the band/confirmation partial-reply salvage (`TestAdjudicateBand_PartialReply`: the returned verdicts decided and the missing entries skipped `ambiguous`/`confirmation_ambiguous`, while a non-typed failure keeps `adjudication_error`), and the deterministic variant bypass (`TestAdjudicateBand_DeterministicVariant`: both loops resolve a pinned variant class with no judge call, the fresh pass marks the verdict `deterministic` and the confirmation pass confirms it), and the twin canonicalization arm (`TestSweepTwins_MechanicalVariantAliases`: a variant pair aliases onto the `PreferCanonical` winner and the loser survives as an alias row; `TestSweepTwins_Filters`: a held, settled, conjunction, band-worklist, duplicate, or dead pair is left alone and only the eligible pair queues pending evidence; `TestSweepTwins_ReportOnlyPlansWithoutWriting`: report-only plans without touching the alias table or the journal; `TestSweepTwins_RejudgeCapBoundsTheWorklist`: the measured worklist is uncapped but only `max_rejudge` pairs dispose; `TestBoundTwinAliases_RespectsRetagBudget`: the document budget truncates the batch and tallies the deferred pair; `TestMeasurementRunJSON`: an absent run omits the timestamp) |
| `internal/errs` | 6 | Error constructors preserve kind/op/cause, Error() string formatting, Unwrap(), KindOf through plain/wrapped/nil errors, FromDB (nil→nil, ErrNoRows→NotFound, unique violation→Conflict, other→Internal), PgError predicates (unique/foreign key/deadlock/serialization/plain/wrapped) |
| `internal/database` | 60 | sqlc-generated CRUD, task lifecycle, enrich waiting flow, enrich discard/restore/sweep queries (SetEnrichTaskWaiting, RestoreDiscardedEnrichTasks, DiscardWaitingEnrichesOfFailedConsumes), batch ownership, FTS-adjacent operations, store-key listing (`ListStoreTagKeys` unions tag names and alias names for the decomposition gate), the split-verdict schema/query contract (`TestSplitVerdictQueries`: first-writer-wins conflict, NULL-model cache rows outside the partial index, status CHECK rejection, version-scoped latest reads, the `reconstructible` tri-state; `TestSplitVerdictQueries_NullDocumentID`: both verdict tables accept a document-less row and read it back), document/tag/people/document-type CRUD, saved searches, dashboard analytics queries (empty DB + mixed data), structured search missing filters (MissingLanguage/MissingType/Untagged), structured search people filters (typed relationship scoping, `PersonAnyType` across relationship types, unknown-person miss, count/search parity), structured search sort by `page_count` (asc/desc ordering, unknown sort key falls back to default), WithDocumentCount queries, backup lock lifecycle, tagmeasure batch queries (active-batch count, completed-only schedule anchor, queued batches never anchor, consume-slot exclusion), gated task claiming, soft delete (TestUpdateDeleteDocument now asserts soft-delete semantics), CheckRestoreTooling (psql/docker/podman present, missing binary, missing container, remote-block, invalid runtime), tag merge queries (ReassignDocumentTags re-points source-only rows, skips documents already carrying the target, DeleteDocumentTagsForTag clears leftovers), TagPairEvidenceStats aggregation (per-orientation event/document/verdict counts across policy versions), orphan tag-mark lifecycle (only document-less tags get marked, per-id clearing, aged marks listed/deleted, tags holding documents excluded, rolled-back transaction leaves no marks), tag promotion correction migration predicate (`TestMigration00026TagPromotionCorrection`: a backfill-signature row with one document is demoted, a backfill-signature row with two documents, a runtime-promoted row and a NULL-`created_at` row all survive, and the Down re-promotes assigned rows and restores the backfill signature), and the maintenance-lock helper contract (`TryHoldMaintenanceLock` refuses a held lock without waiting, the waiting acquire ends with the context, a claim token's lock stays with its owner) |
| `internal/search` | 8 | tsvector search with snippets, ranking, pagination; structured search with mime/language/date/missing filters; query sanitization; engine construction; tsvector index rebuild (`Engine.Reindex`) |
| `internal/task` | 51 | Store (create/get/claim/complete/fail), dedup key uniqueness, dispatcher enqueue with custom status/ID, runner (complete/fail/no-tasks), panic recovery (panicking handler → task failed with `panic: <text>`, not claimable), nil payload handling, backup lock gating for consume/enrich/config, pool lifecycle — StartStop (workers call runner, stop terminates), ContextCancellation (cancel stops pool), DoubleStop (sync.Once no panic), PanicRecovery_Restart (worker survives panic and restarts), PanicRecovery_StopDuringRestart (clean shutdown during restart delay), Retry (restores discarded enrich of a retried consume), consume-handler early-exit discards (external `task_test` package exercising `consumption.ConsumeTaskHandler` against a DB-backed store), batch bookends (processing-mark and finalize failures are logged naming the batch; the DB-gated settle writes the final status and logs the completed/failed/cancelled counts), `DeleteBatchTasks` query-error propagation, and the payload display decoder (`ReadPayload` nil/malformed/decode, `DisplayName` precedence) |
| `internal/service` | 101 | Batch create/get/owner-state/pending/active/cancel/queue, RetryFailed (restores discarded enriches, batch-scoped), ResetStaleProcessingTasks (restore + global sweep; blocking-class ownership gate: live holder keeps task+lock, token-mismatch recovery spares the fresh foreign holder, window-expiry recovery releases+resets once, quarantine obeys the gate), ResetProcessingTasksByBatch restore, orphaned scan/delete/restore/move-to-inbox, errored files list/download/delete/delete-all, user API key create/revoke/rotate/validate, user Create with role defaults/explicit/invalid, UpdateRole (valid/invalid), Update with role change, password validation (12+ rules) |
| `internal/tags/curation` | 38 | alias-plan application (`TestTag_ApplyAliasPlan`: one per-action `AliasOutcome` in input order — a loser collapse applies and reports the re-pointed document count (documents already carrying the canonical are not counted), an alias-only action applies with a zero count, and a blocklisted name reports the action as not applied; the loser's own aliases retarget onto the canonical so chains never form, the loser name becomes an alias at the current rule-set version, and registration through the alias resolves `Apply` to the canonical entity), tag lifecycle (CreateAndUnblock drops the blocklist row on Created/Conflict while Create keeps it; RestoreDeleted re-creates the blocked name and drops the blocklist row only after success, and reports unknown names as not-found), Create batch atomicity (a mid-batch insert failure rolls the whole batch back, fault injected with a table CHECK constraint), tag promotion trust semantics (Create births land promoted; `CreatePlannedTx` with the quarantine flag leaves births provisional; renaming a promoted tag adds the new normalized name to the embedder store, asserted via `MockEmbedder.Has`), name normalization at save, write-boundary name normalization through the plan and alias paths (`TestTag_CreatePlannedTx_StoresNormalizedName`, `TestTag_ApplyAliasPlan_StoresNormalizedName`), composition-evidence writes (`TestTag_CreatePlannedTx_WritesCompositionEvidence`: one `tag_composition_event` row per verdict, names normalized at the write boundary, `[]` parts never NULL, model NULL when no fresh judge judged the name, and the `reconstructible` tri-state preserved with true, NULL and false staying distinct), store add on write, provisional listing/count agreement under every filter combination against a real database (rubric tri-state including absent keys and unjudged names, derived reject/defer, recorded outcomes, AND-combined criteria), latest quality-event lookup for a name, promote (promoted / already-promoted / not-found statuses, re-fetched `promoted_at`), demote (the store copy leaves, the reason is recorded, re-promotion clears the record, seeded values refused, not-promoted and not-found statuses), and the identity-run enqueue (deduplicated while a run is active) |
| `internal/tags/policy` | 1 | Policy evaluation over ranked matches (`TestEvaluate`: short-slice and nil-ranked passthroughs, empty candidates, identity replacement over the ranked candidates, band subjects for neighbour/high-similarity/token-overlap/negation/subset/disjoint shapes) |
| `internal/api/handlers` | 105 | the model-catalog capability wire contract (`disable_reasoning` key and value wiring) through `ListModels`; Document CRUD, tag/people/DocumentType CRUD, user CRUD (with role), task endpoints, saved searches, concurrent operations, dashboard running tasks + analytics + processing health, analytics error path, config handler get/status, PutConfig runtime/container immediate-persistence alongside db migration, batch delete limits, error helpers (writeServiceError REQID resolution with and without an id), auth login (valid/invalid/empty/claims/role), auth logout, API key generate/revoke/rotate/status/forbidden/invalid-id/not-found + wire-format contract (has_api_key present, has_key absent, api_key_prefix present), MeHandler (valid/missing-id/not-found), self-service API key handlers (MeGenerateKey/MeRevokeKey/MeRotateKey/MeGetKeyStatus/unauthorized + wire-format contract), orphaned handler (list/scan/delete/restore/move-to-inbox/delete-all/move-all), errored handler (list/download/delete/delete-all), logs handler (invalid name/file not found/success/line clamping/large file tail/empty file/oversized line truncation/no trailing newline), people-type reserved-name guard (create/update rejected for `person`, case-insensitive; non-reserved name accepted), manual tag assignment clearing the assigned tag's orphan mark, provisional tag list (200 + envelope with live `document_count` and verdict), provisional tag filters (criteria and verdict narrow rows and the filtered total; 400 for an unknown key, state, or verdict; `pending_identity_total` present and equal to the queue count), provisional identity hold (a seeded refused fold gate surfaces the anchor as `identity_hold`), promote endpoint (204 / 409 already promoted / 404 missing / 400 malformed id), demote endpoint (204 / 409 not promoted or seeded / 404 missing / 400 malformed id); ConfigResponseFrom: logging defaults/custom, reclaim max retries, prompt template, max batch delete, doc type refinement, pause on credit error, fallback (nil → omitted; enabled fallback maps all llm fields), consumer fields (supported_files, converter), available file types (required flag, extension aliases), tag measure block |
| `internal/documents/consumption` | 27 | Full consumer pipeline via mock runner (file discovery, DB transaction, file movement, duplicate detection), file I/O helpers (get, move, copy, remove, clean up), checksum calculation, orphaned file management |
| `internal/documents/converter` | 3 | LibreOffice conversion adapter (`TestLibreOfficeCanHandle` MIME dispatch, `TestLibreOfficeName`, `TestNewLibreOfficeNotFound` without the binary). Not wired into a Makefile tier |
| `internal/documents/pdfoptimizer` | 1 | Ghostscript PDF optimizer (`TestGhostscript_Optimize_RejectsRelativePath`; skips when `gs` is absent). Not wired into a Makefile tier |
| `internal/documents/textextractor` | 21 | Text extraction adapters: composite MIME dispatch, unknown MIME, can-handle, name and constructor composition; DOCX/ODT extraction (single/multi paragraph, empty documents, invalid ZIP, missing `document.xml`/`content.xml`); `pdftotext` relative-path rejection. Not wired into a Makefile tier |
| `internal/ops/cli` | 22 | Config handler (help/path/validate valid+invalid/get/missing key/set/invalid set/unset/missing key/unset without key/dump/unknown args), parseValue, deleteNestedKey, highlight snippet ANSI markers. CGo-gated package: compiles only under `CGO_ENABLED=1` (runs via `make test-cgo` / `make test-cgo-db`) |
| `internal/ops/consume` | 4 | Pool selection per batch source (`poolSet.forSource`), `totalFiles` index/count fallback, the `setBatchTerminalStatus` outcome matrix (DB-gated via `t.Skip` when `TEST_DATABASE_URL` unset; runs under `make test-cgo-db`; the already-terminal case asserts the bookend line fires), and the batch bookend (`TestLogBatchBookend`: status counts plus the enrich fields aggregated over decoded completed enrich results, and no enrich segment without them) |
| `internal/ops/configtask` | 6 | Config migration tasks: DB-gated migrate-db and migrate-storage runs plus the no-op/directory helpers (`TestMigrateDBTask`, `TestMigrateDBTask_RefusesForeignDestination`, `TestMigrateStorageTask`, `TestDirChanged`, `TestMigrateStorage_NoOpReturnsErrNoOp`, `TestHandleMigrateStorage_NoOpReturnsNoOpStatus`) — runs under `make test-cgo-db`, exec'ing `psql` inside the test container |
| `internal/ops/flags` | 4 | `FlagParser` `String`/`Int`/`Bool`/`Help` happy paths and error paths (missing value, non-integer, out of bounds, valueless flag), repeated occurrences, an argument consumed as one flag's value not satisfying another, and `Rest` |
| `internal/ops/queue` | 2 | Empty-queued-batch sweep (DB-gated via `t.Skip` when `TEST_DATABASE_URL` unset; runs under `make test-cgo-db`) and the schedule-anchor rephase contract (`scheduleState.nextDue`/`markRun`) |
| `internal/backup` | 66 | Create (full backup/missing DB/missing storage/no files/SQL dump content), ApplyRetention (delete oldest/keep all/keep 0), ValidateArchive (valid/invalid gzip/missing manifest/missing file), ExtractArchive (valid/path traversal/symlink skip), ReplaceFiles (SQL dump/unknown format/documents-mode storage swap, same-device rename path), CopyDir, NextBackupTime (daily/sub-daily/clamp/empty-time), NextScheduledTime (daily/weekly closest occurrence, sub-daily parity with NextBackupTime), ScheduleTiming equality (identical/different interval/different time/zero-padded normalization/empty and invalid time default to 02:00), ScheduleTimingFromPayload (nil/empty/malformed/no-key/with-key/null-key/JSON round-trip), FirstRunAnchor (round-trip through NextBackupTime for daily/weekly/sub-daily/empty-time/invalid-time/fixed-offset; zero/negative interval clamp), dueFromHistory (recent blocks, null-completed due, daily recent-completion not due, sub-daily past-window due, re-phase branch: changed timing due vs unchanged/absent timing steady); mirror tests: Available (sanity), IsRemoteTarget (user@host/rsync:///local), parseStats (stats2 format with reg count + comma-formatted total, deleted-file lines ignored per P1 fix, empty/malformed degrade to zero, commas in the outer count) |
| `internal/tags` | 50 | The shared J4 resolution (`TestCanonicalLoser`: a judged direction names the winner, a tie or a deterministic variant falls to `PreferCanonical`) and the single-read journal seam (`TestCompositionReview`: one read returns both the hold and state views, matching the single-view wrappers, and a degraded keep is both a hold and undecided), the measurement read side (`TestDecodeRows`: numbers round-trip as the cuts read them, a malformed payload fails with the row index, the run columns stay zero; `TestLatestWorklists`: an empty journal is absent, only the newest run is derived), Identity planning (`HouseForm` house-form cases, `TieBreak`, the deterministic variant-class table (`TestVariantClass`: pinned orthographic suffixes, literal token permutations, and the classes that stay judged — plurals, mixed-language, morphological/function-word inversions, spelling-plus-permutation), `PlanDeterministic` apply/conflict/key-equal/blocked/fold-pending/derivational paths), the rejected-name plan filter (`TestPlan_DropTagNames`: output-name and raw-input matches, evidence dropped by candidate and partner, receiver untouched, empty set no-op), the conservative plural fallback for folds (`TestPluralStrip` rule table; `TestProposedFold_PluralFallback` with its dictionary-membership preconditions pinned), the store-anchored derivational fold proposal (`TestProposedDerivationalFold`: suffix hits, the store-anchor requirement, two hits as ambiguity, multi-token/length/uppercase guards, irregular demonyms excluded), the bounded morphographic proposal (`TestProposedMorphologicalFold`: distance-1/2 hits, the distance-3 cap, two store hits as ambiguity, multi-token/length/uppercase and multi-word-store-key guards), the chained split-seam fold (`TestAnchoredFold`: attested adjective→noun folds (chilean→chile, mexican→mexico, german→germany, political→politics), an orthographic variant folding onto the promoted spelling (labour→labor, single- and multi-token), ambiguity, the no-anchor and multi-token guards, an unattested near-spelling standing unreduced, and an attested base the store lacks), the primary-sense seam rule (`TestAnchoredFold_LiveView`: a multi-base part folds onto its live primary-sense base, a live non-primary base neither attracts nor blocks the fold, the primary base must be live to fold, and a variant anchor folds regardless), the canonical direction (`TestPreferCanonical`: the house form wins over a foreign label in either order, a deterministic variant resolves through the tie-break, and labels the language step cannot separate fall through), the deterministic period class (`TestIsPeriodClass`: the ordinal+unit shape, the house-form precondition, the two-token bound, a plural unit, and non-Latin script), the store-existing skip's state read (`TestCompositionStatesForNames`: StoredName keying incl. the house-form case, a degraded keep reported undecided, stale-version rows invisible, a rowless name absent), and the key-to-name store view (`TestStoreNames`: the first name wins a shared key); extension-algebra readings (mass, overlap coefficients, reconstructibility excess/deficit, part coverage, equality/complement) and set operations; rule-set v1 worklist cuts (twin/sibling/compound bands + gray ranges + both mass floors); compound shape classification and part derivation (`TestDeriveSegments` fan shapes; `TestGreedyCover` coarsest-cover cases: longest-span and leftmost-bigram wins, a whole-sequence bigram excluded, an uncovered word and gap failing the cover, and the alongside partial cover); the time-designation gate (`TestIsTimeDesignation` shapes and non-shapes, `TestIsTimeDesignationDrop` with the store exemption); the variant-key parity the birth-time scan precomputes (`TestVariantKey`: pinned pairs share a signature, unrelated names — plurals included — share none); the pinned admission rules (`TestDecideAdmission`: fail-count boundaries, judge-defer precedence over counted failures, lone and corroborated deterministic signals; `TestDecidePromotion`: floor boundary, defer/reject never promote; `TestDecideMassDemotion`: the retention floor — zero and below-floor live mass demote, at and above the floor keep standing); the pinned composition rule table (`TestDecideComposition`: every branch, evaluation order and the fail-safe) and the composition promotion hold (`TestCompositionHeldNames`: latest reason per version, version scoping, name normalization, a decided drop holding); evidence-row JSON encoding (nil caveats → `[]`) and operand normalization at the write boundary (`TestEncodeRows_NormalizesOperands`); prefixed-report extraction (`ExtractReport`); DB-gated runner (seeded store → persisted records, report/persisted parity, canceled-context error path) and the DB-gated evidence-derived promotion hold (`TestIdentityHeldNames`: gate rejection, base_form=stand despite a confirming gate, dead anchor, conventional-form exclusion, empty input — both run under `make test-db`) |
| `internal/types` | 7 | Registry completeness (all batch sources/task types/statuses declared, validity and parse round-trips, defensive-clone isolation, non-consume source set, blocking and lock-gated classifications incl. `tagmeasure` as blocking-not-gated) |

The conversion, optimization and text-extraction adapter tests
(`internal/documents/converter`, `internal/documents/pdfoptimizer`,
`internal/documents/textextractor`) are not wired into any Makefile tier: no
target invokes them.

---

## How Tests Work

### PostgreSQL via TEST_DATABASE_URL

Every test creates a connection to a PostgreSQL database specified by the `TEST_DATABASE_URL`
environment variable. The schema is initialized via `database.InitializeSchema` on each
connection.

**Per-package isolation**: Each test package gets its own dedicated database
(`edub_test_<package_dir>`, e.g. `edub_test_database`, `edub_test_task`). The database
name is derived automatically from the calling test file's package directory via
`runtime.Caller`. This allows packages to run in parallel without cross-contamination
from concurrent `ResetTestDatabase` calls.

**Auto-cleanup**: Each call to `NewTestDB` registers a `t.Cleanup` handler that closes
the connection and releases a reference. When the last reference to a test database
is released, the database is dropped via `DROP DATABASE ... WITH (FORCE)` (PostgreSQL
13+), which terminates any lingering connections before dropping.

Within a test that needs a clean slate regardless of isolation, each call starts with
truncated tables, reset sequences, and re-inserted seed data via `ResetTestDatabase`:

```go
q, db := database.NewTestQueries(t)
defer db.Close()
resetDB(t, q)
```

The `resetDB` helper deletes all rows from every table, resets identity sequences, and
re-runs the seed SQL for `document_type`, `people_type`, `tag`, and `tag_alias`.

Foreign key constraints are enforced by PostgreSQL just as in production.
The connection pool uses `SetMaxOpenConns(25)`.

### CGo-Free Runner Mock

The consumption pipeline integration tests use a **mock runner** that satisfies the
`runner` interface (defined in `internal/documents/consumption/consumer.go`). This avoids
the need for real Tesseract (gosseract) or MuPDF:

```go
type runner interface {
    ExtractText(ctx context.Context, path string) (*tools.TextExtractionResult, error)
    OCR(ctx context.Context, docId, path string) (*tools.OCRResult, error)
    OptimizePdf(ctx context.Context, docId, path string) (*tools.PdfOptimizationResult, error)
}
```

The mock (`integrationTestRunner` in `internal/documents/consumption/integration_test.go`)
returns configurable text, OCR results, and optimization results, or injects
errors to test failure paths.

### Function Variable Override Pattern

The OCR adapter uses a function variable + `init()` override to make CGo-dependent
code testable without CGo:

```go
// adapter.go — always compiled
var newGosseract = func(...) (OCR, error) {
    return nil, fmt.Errorf("gosseract requires CGo")
}
```

```go
// gosseract.go — //go:build cgo
func init() {
    newGosseract = func(...) (OCR, error) { return NewGosseract(...) }
}
```

On non-CGo builds, the default error-returning stub is used. When CGo is available,
`init()` overrides the variable with the real constructor. This pattern is borrowed
from `crypto/x509` root cert loading in the Go standard library.

### Test Fixtures

- **Minimal PDFs**: `testutil.MinimalTextPDF(content)` generates a spec-compliant
  PDF with the given text content. Used by both database tests (for `FileFromPath`)
  and consumption tests.
- **Temp directories**: `testutil.NewTestConfig(t)` creates a full config with temp
  inbox, storage, data, and OCR directories. Cleanup is handled via the returned
  `func()`.
- **Deterministic UUIDs**: `database.CreateTestDocument` generates UUIDs from
  the current nanosecond timestamp (unique per call, no external dep).

---

## Running Tests

### Makefile targets

```bash
make test          # CGO_ENABLED=0, 18 packages, 60s timeout (no DB needed)
make test-verbose  # same with -v output
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium)
```

The Makefile explicitly sets `CGO_ENABLED=0` for test targets, overriding the
`export CGO_ENABLED := 1` used for build targets. This ensures tests run in
any environment regardless of C library availability.

**18 packages run without a database:** `llm`, `config`, `tools`, `log`,
`textnorm`, `matcher`, `documents/mime`, `documents/storage`, `auth`, `api`,
`api/wizard`, `llm/analyzer`,
`errs`, `task`, `backup`, `types`, `tags`, `ops/flags`. The DB-backed tests inside `task` and `backup`
`t.Skip` when `TEST_DATABASE_URL` is unset, so both packages run in this tier
and again under `make test-db` / `make test-backup`.

### Database-dependent tests (`make test-db`)

Packages requiring PostgreSQL 16+ (via `TEST_DATABASE_URL`):

```bash
make test-db
```

The `internal/backup` tests also require PostgreSQL (via `database.NewTestDB`);
they run through `make test-backup` with the same `TEST_DATABASE_URL`
environment. The SQL-dump restore tests execute `psql` through the `podman`
runtime against the `edub-test-pg` container (see the AGENTS.md test-database
instructions) instead of requiring `psql` on the host PATH.

### CGo-dependent tests (`make test-cgo`)

These 283 tests require `CGO_ENABLED=1` to compile because their source files
are tagged `//go:build cgo` (or, for `internal/llm/analyzer`, `internal/ops/cli`,
`internal/ops/consume`, `internal/ops/queue`, `internal/tools`,
`internal/enrichment`, `internal/tags`, `internal/tags/admission` and
`internal/tags/reconcile`, the packages import or link the cgo-gated adapters).
Most use only pure-Go code; only the MuPDF tests and the command handlers
exercise actual CGo function calls.

Requires the C toolchain and built C libraries (Tesseract, Leptonica, MuPDF,
libtokenizers) on the host. Run inside the builder containers to avoid
installing C deps locally:

```bash
make test-cgo          # host, uses Makefile's CGO_ENABLED=1 export
make test-cgo-glibc    # podman: kushim-glibc-builder
make test-cgo-musl     # podman: kushim-musl-builder
```

**12 packages, 283 tests:**

| Package | Tests | CGo at runtime? |
|---------|-------|-----------------|
| `internal/cgo` | 2 | Yes — MuPDF page render |
| `internal/documents/ocr` | 9 | No (pure Go, build-tag gated) |
| `internal/llm/analyzer` | 90 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/matcher` | 12 | No (pure Go, build-tag gated) |
| `internal/tools` | 28 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/cli` | 22 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/consume` | 4 | No (pure Go tests + one DB-gated test; package links the cgo adapters transitively) |
| `internal/ops/queue` | 2 | No (pure Go tests + one DB-gated test; package links the cgo adapters transitively) |
| `internal/enrichment` | 9 | No (pure Go tests + one DB-gated test; package links the cgo adapters transitively) |
| `internal/tags` | 46 | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |
| `internal/tags/admission` | 57 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/tags/reconcile` | 32 | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |

`internal/ops/cli` is not part of `make test`: the package only compiles with
CGo (`hugot.go` imports the `//go:build cgo`-gated matcher engine), and
kushim — the only binary importing it — is always built with CGo.
`internal/tools` runs in both tiers: `make test` builds it without CGo, and
`make test-cgo` re-runs it with the cgo adapters linked.
`internal/llm/analyzer` likewise runs in both tiers: `make test` builds it
without CGo, and `make test-cgo` re-runs it with the cgo adapters linked.

### Full CGo+DB run (`make test-cgo-db`)

Runs the consumption pipeline tests with `CGO_ENABLED=1` against PostgreSQL —
the same suite as `make test-db` but with the package linked under CGo (the
cgo-gated adapters resolve at compile time, MuPDF is available). Page-count
assertions remain mode-independent: `setupConsumerTest` stubs `pageCounter` to
return 1 unconditionally, so the assertions are deterministic whether MuPDF
is reachable or not. The tier's value is exercising the full CGo+DB
pipeline (link paths, transactions, future code paths that touch
`countPages`) — not asserting on `countPages` output.

Also runs `internal/ops/configtask` (migrate-db/migrate-storage tests). The
migrate-db tests exec `psql` inside the test DB container via
`database.runtime=podman|docker` — `containerRuntime(t)` picks the first CLI
on PATH that can see the running container (so podman on a runner whose
container runs under docker falls through to docker) — so no host
`postgresql-client` is needed; the container name comes from
`TEST_DATABASE_CONTAINER` (default `edub-test-pg`, CI sets `postgres`):

Also runs `internal/ops/queue` (DB-gated tests in a CGo package — e.g.
`TestSweepEmptyQueuedBatches`). Those tests `t.Skip` when `TEST_DATABASE_URL`
is unset (as in the `make test-cgo` step), so the package must be listed here
for them to actually exercise the behavior in CI.

Also runs `internal/ops/consume` (the pure pool-selection and file-count tests
plus the DB-gated `TestSetBatchTerminalStatus` outcome matrix). The DB-gated
test also `t.Skip`s when `TEST_DATABASE_URL` is unset, so the package is listed
in both CGo tiers.

Also runs `internal/tags/reconcile` (DB-gated reconciler tests — e.g.
`TestApplyKeepFlip_ZeroDocsCreatesNoTag`, which asserts the keep-flip
document-ID ordering fix against a real database). Those tests also `t.Skip`
when `TEST_DATABASE_URL` is unset, so the package is listed in both tiers.

The `internal/tags/admission` package (identity planning, adjudication,
compound decomposition, verdict-event and tag-quality tests) runs under both
`make test-cgo` and `make test-cgo-db`: its DB-gated tests
(`TestPlanIdentity_LogsPendingPairs`, `TestPlanIdentity_FoldStaysPendingWithoutJudge`,
`TestPlanIdentity_StoreExactAppliesOntoExistingRow`,
`TestAdjudicateIdentityPairs`, `TestResolvePending_Fold`,
`TestResolvePending_SameIntentPair`, `TestResolvePending_AdjudicatorDisabled`,
`TestResolvePending_FoldTargetDeleted`,
`TestTagQualityEvent_InsertAndCacheRead`, `TestApplyTagQuality_PartialSalvage`) `t.Skip` when `TEST_DATABASE_URL` is
unset, and the `test-cgo-db` listing is what makes them exercise PostgreSQL in
CI. The parent `internal/enrichment` package also runs under both tiers: its
DB-gated tests (`TestEnrich_SuggestionInjectionBar`,
`TestPromoteEligible_CompositionHold`) `t.Skip` when `TEST_DATABASE_URL` is
unset, and the `test-cgo-db` listing runs them against PostgreSQL.

Also runs `internal/tags` (identity planning, the extension algebra, the
admission and composition rules, and the composition promotion hold). Its
DB-gated tests `t.Skip` when `TEST_DATABASE_URL` is unset, so the package is
listed in both CGo tiers.

Also runs `internal/tags/curation` (tag CRUD, merge, aliases,
composition-evidence writes). Its tests are ungated database tests, so it is
listed only in the DB tier, like `internal/api/handlers`.

```bash
make test-cgo-db   # requires make build-deps first + TEST_DATABASE_URL
```

### Single package

```bash
# Without DB (CGO_ENABLED=0)
make test-one PKG=./internal/errs/

# Specific test
make test-one PKG=./internal/database/ RUN=TestTaskLifecycle

# CGo-gated package (requires make build-deps first)
make test-cgo
```

### Web UI tests (`make test-web` / `make test-web-e2e`)

**`make test-web`** runs Vitest over two projects defined in `web/vite.config.js`
(`test.projects`, both `extends: true` so they inherit the `sveltekit()` plugin —
which resolves `$app/*` modules — and the `child_process`/`url` stub aliases):

- `unit` (node env) — pure logic and runes stores: `searchFilter.js` (tokenizer,
  query-string parser, size/date parsing, serialize round-trip), `utils/html.js`
  (escaping, size/duration/relative formatting), `filterStore.js`, `toastStore.svelte.js`,
  `confirmStore.svelte.js`.
- `components` (jsdom + `svelteTesting()` from `@testing-library/svelte/vite` for
  auto-cleanup) — `authStore.js` and `api.js` (both touch `localStorage` at import
  time, so they need the DOM), plus the component tests: `DataTable.svelte` (rows,
  sort refetch, empty/error states, `onTotal` reporting), `FilterPanel.svelte` (filter-store wiring,
  tag autocomplete, failure toast), `UploadModal.svelte` (upload success/error paths),
  `tags/ProvisionalReview.svelte` (verdict labels: recorded outcome preferred, legacy rubric
  fallback, unjudged names, the identity-hold, split-hold and degraded-reason pills, the
  `not_form_genre` criterion chip; filters
  and paging: select → API args and `prov_*` URL sync incl. the tab hash, URL round-trip with
  invalid entries dropped, pager end states, last-page clamp after a reject, and the
  `pending_identity_total`-driven reevaluate button).

Test files are co-located next to their sources as `*.test.js` (mirroring Go's
`_test.go` convention) and import `describe/it/expect/vi` explicitly — no globals,
so `eslint.config.js` needed no changes. Mocking conventions:

- `$app/*` modules are mocked with `vi.mock` where a unit imports them (`$app/navigation`
  `goto`, `$app/paths` `resolve`, `$app/state` `page`).
- `$lib/api` is mocked with `vi.hoisted` factories in component tests (`FilterPanel`,
  `UploadModal`, `ProvisionalReview`) so no network calls escape.
- `api.test.js` stubs the global `fetch` (`vi.stubGlobal`) and asserts the Bearer
  header, 401 → logout + redirect, null propagation, `requestRaw` shape, and the
  `supportedMimeTypes` cache.

**`make test-web-e2e`** runs Playwright against the static SPA build
(`web/playwright.config.js`): the `webServer` runs `npm run build` and then serves
`build/` via `web/scripts/serve-static.mjs` — a tiny zero-dependency static server
that falls back to `index.html` for unknown routes, mirroring adapter-static's
`fallback` behavior in production (SvelteKit's own `vite preview` cannot be used:
it runs the SSR server, and `authStore.js`'s import-time `localStorage` access 500s
every route). The three specs (`e2e/login.spec.js`, `documents.spec.js`,
`upload.spec.js`) share `e2e/helpers.js`, which seeds `localStorage` auth via
`addInitScript` and installs `page.route` mocks for `/api|/wizard|/health` returning
minimal realistic payloads matching `web/src/lib/api.js` expectations. First run
requires `npx playwright install chromium` (CI uses `--with-deps`).

### Vulnerability scanning (`make vuln` / `make vuln-cgo`)

`make vuln` runs govulncheck against the Go vuln DB with `CGO_ENABLED=0`, so no
C toolchain is needed. It scans every package except `cmd/kushim`,
`internal/ops/cli` and `internal/ops/queue` — those reference CGo-gated symbols
and cannot load without CGo, the same split that separates `make test` from
`make test-cgo`.
`make vuln-cgo` is the CGo-enabled variant (full call graph incl. the
gosseract/hugot adapters) and requires `make build-deps` first:

```bash
make vuln          # CGO_ENABLED=0, all loadable packages
make vuln-cgo      # full call graph, requires make build-deps first
```

govulncheck exits non-zero on reachable findings, which fails the CI
`vulncheck` job. Fix findings via `go get <module>@<fixed>` + `go mod tidy`.

### Test Helpers

The `internal/testutil` package provides reusable test infrastructure:

| Function | Purpose |
|----------|---------|
| `NewTestLogger()` | Discard logger for silent test output |
| `NewTestConfig(t)` | Config with temp dirs, cleanup function |
| `MinimalTextPDF(content)` | Valid PDF byte slice with text |
| `CreateTestPDF(t, path, content)` | Writes minimal PDF to disk |
| `CreateTestFile(t, path, content)` | Writes arbitrary file to disk |
| `AssertEqual(t, got, want, msg)` | Value equality check |
| `AssertNoError(t, err, msg)` | Nil error check |
| `AssertError(t, err, msg)` | Non-nil error check |

The `internal/database` package also exports:

| Function | Purpose |
|----------|---------|
| `NewTestDB(t)` | PostgreSQL via `TEST_DATABASE_URL` — per-package isolated database (`edub_test_<pkg>`), schema initialized, `t.Cleanup` closes connection + drops database on last reference |
| `NewTestQueries(t)` | Helper returning both `*Queries` and `*sql.DB` |
| `CreateTestDocument(t, queries, title)` | Inserts a document, returns auto-ID and UUID |
| `SeedTagByName(t, queries, name)` | Returns a seeded tag (by name or first) |
| `insertDoc(t, queries, title, md5, sha512)` | Helper for inserting docs with raw checksums (unexported, same-package use) |

---

## Patterns & Conventions

### Assertion duplication (by design)

The `database` package has local `assertNoError` / `assertEqual`
helpers rather than importing from `testutil`. This is because `testutil` imports
`config`, which imports `database` (through `setup.go`), creating an import cycle
when the `database` test package imports `testutil`. Other packages (search, task,
handlers, consumption) use `testutil` assertions directly.

### Import cycle avoidance

```
database.test → testutil → config → database  (CYCLE)
search.test  → testutil ✓  (no cycle)
```

The `testutil` package avoids importing any `database`-adjacent types to minimize
the cycle surface. `newMockTagService` lives in the handler test file for this reason.

### No CGo in non-CGo tiers

The `make test`/`make test-db` tiers run with `CGO_ENABLED=0`. The CGo-dependent
code paths (gosseract OCR, MuPDF text extraction/optimization) are tested via
mocks at the `runner` interface level and via build-constrained stubs at the
adapter level; the CGo packages themselves run under `make test-cgo` /
`make test-cgo-db` (see above).

---

## Adding Tests

### New database query

1. Add SQL to `internal/database/sql/queries/`
2. Run `sqlc generate`
3. Add a test in `internal/database/integration_test.go`

### New API endpoint

1. Add the handler function
2. Add tests in `internal/api/handlers/handlers_test.go` using `httptest.ResponseRecorder`
   and the `req()` / `rec()` helpers

### New consumption path

1. Implement the adapter
2. For CGo adapters, add a `//go:build cgo` tag and a `//go:build !cgo` stub
3. For new consumer behavior, add mock responses to `integrationTestRunner`
   and test in `internal/documents/consumption/integration_test.go`

### New task type

1. Implement `task.Handler`
2. Add test in `internal/task/task_test.go` using the `mockHandler` utility
