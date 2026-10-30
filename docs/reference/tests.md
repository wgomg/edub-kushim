# Testing Reference

## Overview

The project has **38 test packages** with a three-tier
run strategy (Go backend + web UI):

- **`make test`** (19 packages, no database required) — LLM registry, config, API types,
  tools runner, logging (`internal/log`), text normalization (`internal/textnorm`), tagmatch, storage walk, auth tokens,
  auth middleware + permissions, content analyzer (prompt building, tag filtering, token
  estimation), error classification (`errs`), mirror (rsync
  stats parsing, remote-target detection, destination guards), the task-system registry
  (`types`), the tag extension algebra (`tags`), the task type catalog
  (`ops/container`), the setup wizard routes
  (`internal/api/wizard`), and the shared flag parser (`ops/flags`).
- **`make test-db`** (8 additional packages, requires PostgreSQL) — database CRUD queries,
  search engine, task system (enqueuer/store/runner), service layer (batch,
  orphaned, errored files, users, enrichment, API keys), API handlers,
  consumption pipeline, and the tag measurement runner.
- **`make test-web`** (vitest, no database) — 130 unit + component tests across 16 files
  in `web/`: pure logic (`searchFilter`, `html` utils, `tagReconcile` utils), stores
  (`filterStore`, `authStore`, `toastStore`, `confirmStore`), the `api` client (fetch
  stubbing, auth header, 401 handling), and the logic-dense components (`DataTable`,
  `FilterPanel`, `UploadModal`, settings area — the settings tests cover the tag
  adjudicator/quality/reconciliation cards, inherit-from-analyzer options,
  default-fill blocks, and the full save payload — including the tag-reconcile
  grace fields; the settings store tests cover the staged config+catalog apply
  (config-before-operations ordering, failed catalog operations staying staged
  with the server reason, the apply/reset mutual exclusion, token redaction,
  and discard of changes to omitted keys) and the Models page used-by mapping
  (disabled fallbacks and inherited provider references report as in use). Component tests run under jsdom
  with `@testing-library/svelte`.
- **`make test-web-e2e`** (Playwright, no backend) — 4 smoke specs in `web/e2e/`
  (login, documents list + detail, upload) driven against the static SPA build with a
  mocked API (`page.route` intercepts `/api|/wizard|/health`); no `edub`/PostgreSQL
  needed. Requires `npx playwright install chromium` once.

All non-CGo tiers run with `CGO_ENABLED=0` (no Tesseract, MuPDF, or
Ghostscript required); `make test-cgo` and `make test-cgo-db` run with
`CGO_ENABLED=1` and need `make build-deps` first. Every invocation goes through
the Makefile, which exports the `-tags "XLA,ORT"` tags, the CGo environment and
the GOTOOLCHAIN setting that pins every go invocation to the toolchain version
declared in go.mod — the same version the CI and release workflows install via
`setup-go`, so the matching toolchain module is fetched into the Go module cache
on first use — bare `go test` is not supported, and to use a different toolchain
override the variable on the make command line (`make GOTOOLCHAIN=go1.27 test`),
since the Makefile assignment wins over the process environment.

### Quick Start

```bash
make test          # runs all non-CGo tests
make test-verbose  # same with verbose output
make test-db       # database-dependent tests (requires TEST_DATABASE_URL)
make test-cgo      # CGo-gated tests (requires make build-deps first)
make test-cgo-db   # consumption + ops/configtask + ops/cli + ops/consume + ops/container + ops/queue + enrichment + tags + tags/admission + tags/curation + tags/reconcile + api/handlers with CGo + DB (requires make build-deps + TEST_DATABASE_URL)
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
- `test-cgo` job (depends on `web`): installs the C build prerequisites, builds the C libraries with `make build-deps TOKENIZERS_ARCH=amd64` (cached under `build/` keyed by `hashFiles('Makefile')`), then runs `make test-cgo` (no DB; DB-gated tests in `internal/ops/queue`, `internal/enrichment`, `internal/tags`, `internal/tags/admission` and `internal/tags/reconcile` skip) and `make test-cgo-db` (with DB; those tests actually run) against the same postgres:17 service container. This surfaces the CGo-only packages (`internal/ops/cli`) and the cgo-linked packages (`internal/ops/consume`, consumption under CGo) on every push/PR.
- `vulncheck` job (depends on `web`): stages the `web-assets` artifact, installs govulncheck (pinned `@v1.7.0`), and runs `make vuln`. Like `make test`, it runs with `CGO_ENABLED=0`, so the CGo-gated packages (`cmd/kushim`, `internal/ops/cli`, `internal/ops/queue`) are excluded from the CI scan; `make vuln-cgo` covers them locally.

The `global` ruleset requires the `test` and `web` checks to pass before a PR to `master` can merge.

---

## Package Layout

| Package | Tests | What it covers |
|---------|-------|---------------|
| `internal/llm` | 40 | Registry creation (fixture + seeding of missing catalogs from the embedded defaults), model lookup for known/missing providers and models, adapter/provider/model listing, default URL derivation, disk-only user catalogs (an on-disk entry loads verbatim, a model absent from disk stays uncataloged; a user provider entry supplies the provider's endpoint), load rejection of a missing or wrong `version`, a legacy bare-array file, and a model naming an undefined provider, a failed load leaving the config directory untouched (existing file preserved, nothing seeded), reload picking up disk edits and keeping the previous catalogs on failure, concurrent access safety, and the catalog mutation API (add/set/remove persisted across a fresh disk load with the duplicate-conflict, dotted-name and unknown-provider validation kinds; the orphan-model and in-use delete guards with a nil config lifting the guard and cascade leaving a loadable pair; each reset scope repairing its own mangled file while the other file only validates; and the `_comment` hint surviving rewrites) |
| `internal/config` | 54 | Default config values (incl. `tag_reconcile`, `tag_measure` and `provisional_grace_days` defaults), the identity-candidate-floor defaults (`TestDefaultIdentityCandidateSimilarity`: per-model floors held above the document-to-tag threshold), ParseHHMM, polling window validation (valid + invalid), IsWithinActiveWindows, OCR workers, finalizeConfig (reclaim max retries, doc-type refinement non-negative, request-delay bounds, tag reconcile `dead_tag_grace_days` and `provisional_grace_days` zero/negative → default, tag measure interval/time defaults and invalid time rejection, the `after_enrichment_batch` default, tag maintenance defaults, interval/time healing and invalid time rejection, fallback validation: missing adapter/provider/model rejected, negative fallback request_delay rejected, valid enabled fallback accepted, nil/disabled fallback skipped), Load (applies OcrWorkers, and the suggestion-injection bar: derived per-model default, explicit value honored, explicit zero reverts, negative rejected), SaveMap (merge/no file/invalid file), watcher (fire on change/missing file/invalid YAML), IsRemoteMirrorTarget (11 cases: rsync://, user@host, host-only colon, slash/whitespace rejections), ValidateMirrorDestination (dash-prefixed refused, remote skipped, inside-storage refused, storage-itself refused, ancestor-of-storage refused, inside-backup refused, unrelated local accepted, symlink-into-storage refused) |
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
| `internal/enrichment` | 9 | truncate (ASCII, CJK, mixed, trim), the result envelope (`TestEnrichResultEnvelope`: counts round-trip, nil stats as JSON null, pre-envelope and undecodable payloads yield no counts, invalid stats fail the marshal), the per-document count tally plus its accumulator (`TestDocumentCounts`: births from `Created` results, rejects from the rejection set, defers from the decisions map, composition dispositions, quality layer off as zeros; `TestDocumentCountsAdd` sums every field), the suggestion-injection bar at the emission seam (`TestEnrich_SuggestionInjectionBar`: above-bar matches kept, below-bar dropped, empty list when nothing clears; DB-gated), and the credit-pause mapping (`TestCreditPause`: credit error with the flag on → `PauseBatch`, flag off → no pause, non-credit error → nil), and the keep-row part prune (`TestPruneRejectedParts`: parts resolved through the plan's output mapping before the rejection test, rejected parts dropped, survivors keeping their emitted spelling), and the partial-keep re-registration rule (`TestApplyPartialKeep`: a compound the plan already aliases is skipped whole — no registration and no emitted tag beside the alias action's canonical link; an unaliased compound registers and joins the emitted tags) |
| `internal/tags/admission` | 63 | The shared compound judgment (`TestJudgeCompounds_RowContract`: one row per candidate in candidate order, split rows carrying their components and keep rows the parts applied alongside, and no document on the rows; `TestConjunctionComponents`: a conjunction folds each segment onto its store anchor, a plain compound and an over-bound name return nil), Compound decomposition (`TestDecomposeTags_*`: no-judge conjunction fallback and pass-through, the two-pass decision (a composition with dimensional parts splits, a named unit with non-dimensional parts keeps, a no-dimension phrase with non-dimensional parts drops, a deferred reading keeps), the part-count bound, per-document component filtering, judge-failure keep, store-keyed compounds skipped before judgment with no judge call and no composition row (only once a decided verdict exists, so a rowless or degraded store-present compound is re-judged until then), a decided store-existing keep applying its cached parts alongside idempotently, the no-adjudicator skip writing no row and never re-judging, per-candidate composition rows (disposition, reason, validated segments, criteria version, judge model for fresh verdicts), fold resolution before filtering (`TestDecomposeTags_FoldResolvesPartsBeforeFilter`: parts land on store anchors in the output and the row, no store view passes through, the hard rules evaluate the resolved part), persisted per-value and per-compound verdict caches (a re-emitted value or compound resolves with no judge call; a cache-applied split row is marked `cached` and model-less), the reconstructibility signal on both sides (`named_unit_reconstructible` with an established store view, `relational_keep` without one), the fold's audit contract (`TestCompositionFeatures_ReadingRecord`: the substrate source with its band and copied numbers, the promoted-parts proxy over promoted and unpromoted parts, and the absent marker when the parts test never ran), the verdict store's conflict reread (a matching abstention agrees, a differing reading disagrees and is flagged), partial-reply salvage for all three judged families (parts, compounds, and phrase folds; returned entries persist and apply, omitted indices defer with no row and no retry, and a salvaged phrase fold answers the cache on re-read), versioned split log, the store-existing period fiat skip (a seeded period keeps whole with no judge call and no row, `TestDecomposeTags_StoreExistingPeriodIsFiatSkip`), the language guard (`ResolveLanguages`: detected non-English values rewritten in place including their details, English and store-existing values untouched, an abstained value held in the pending set), the held-for-review gate (a `ForceDefer` value kept with `reason=language_review` and no judge call), the dimension abstain (a missing parts verdict defers instead of counting as a failing dimension), and the parts false guard (a promoted value's false defers with no second sample, a contradicted false defers, a corroborated false persists)), identity planning (`TestPlanIdentity_*`: pending-pair logging, fold-stays-pending without a judge, store-exact emissions applying onto their own row with the name set and no evidence, a confirmed fold-birth applying without pending rows or a provisional flag, and a derivational proposal reaching the judge and resolving onto the store noun; a store-exact emission that loses a decided pair registers as an alias onto the winner beside the alias action's loser-row removal), the plan's input→output mapping (`TestOutputByInput`: an alias action resolves its loser onto the canonical the store applies, a registration outranks a colliding alias, and an entry missing a name or a target adds nothing), DB-gated pair adjudication (`TestAdjudicateIdentityPairs`: cached verdict applies without a call, fresh verdicts recorded, errors stay pending, a pinned variant class resolves with no judge call and records the class with no model), resolver folds and same-intent aliasing (`TestResolvePending_*`: confirmed fold renames, deleted fold target is not resurrected, disabled adjudicator leaves evidence pending, derivational fold onto the store noun with one proposal feeding the judge input, the stored criteria and the alias apply), verdict-event conflict re-read (`TestWriteVerdictEvent_ConflictReReadsStoredVerdict`), the partial-reply salvage (`TestApplyTagQuality_PartialSalvage`: salvaged entries decided and row-written, missing indices deferred `judge_unavailable` with no row; the label-wrapped `IncompleteReplyError` is not retried), the stored-verdict decision mapping (`TestDecisionFromStored`: rubric re-derivation beats `pass`, caller deterministic signals join the rubric, legacy/malformed criteria fall back to the pass mapping), the DB-gated admission flow (`TestApplyTagQuality`: admit kept with its recorded outcome, corroborated failure dropped, a lone deterministic signal defers, cached rejection answered without a judge call, judge outage keeps the tag with a defer decision and writes no row), the failed-judge WARN paths (`TestDecomposeTags_NoVerdictWarns` for the parseable no-verdict response, `TestPlanIdentity_JudgeFailureWarns`, `TestApplyTagQuality_JudgeFailureWarns`), and the DB-gated fresh-verdict insert + cache read path (`TestTagQualityEvent_InsertAndCacheRead`, including the `outcome` column round-trip), the judged-retry classification (`TestRetryableJudgeFailure`: transport/generic/empty failures retry once; parse, incomplete-reply, output-cap, credit, token-limit, content-too-large and context errors never — direct and label-wrapped), and the credit-error propagation and loop-stop at every judged step (`TestDecomposeTags_CreditErrorEscapes`, `TestDecomposeTags_TransientFailureRetriesOnce`, `TestApplyTagQuality_CreditErrorEscapes`, `TestPlanIdentity_CreditErrorEscapes`, `TestIdentityAnswers_CreditErrorStopsRemainingBatches` — 41 candidates force two batches and the first stops the loop, `TestAdjudicateIdentityPairs_CreditErrorEscapes`), the identity and pair partial-reply salvage (`TestIdentityAnswers_PartialSalvage` — the returned answers recorded and the missing candidate left unanswered; `TestAdjudicateIdentityPairs`'s partial subtest — the returned pair decided and recorded, the missing pair undecided with no row), the resolver partial-reply salvage (`TestResolvePending_PartialCandidateReply` / `TestResolvePending_PartialPairReply` — the returned events resolved, the missing ones deferred and left pending), a pinned variant class resolving mechanically onto the tie-break winner (`TestResolvePending_DeterministicVariantPair`: orthographic and inverted pairs alias with no judge call, events carry the class and no model), the shared fan helpers (`TestResolveContentSingles`: the time drop and the fold resolution aligned with `DeriveParts`; `TestBuildFan`: singles and bigrams resolved, a nil singles slice, a dropped word, a time-designated bigram; `TestPhraseFoldKeys`: residual-word questions only; `TestApplyPhraseFolds`: fold, abstention, and ambiguity; `TestCoverParts`: a full cover versus the flat unit branch and a fan-scoped abstention; `TestSeedStoreValues`: store values seeded dimensional, existing verdicts kept), the birth-time orthographic pair (`TestPlanIdentity_OrthographicVariantResolvesWithoutJudge`: a store twin aliases with no judge call and no pending evidence), the derived-parts time gate (`TestDecomposeTags_TimeDesignationPartDropped`: an unseeded month never judged and only the survivor applied alongside, a store-held month exempt and judged), and the quality-judge batching (`TestApplyTagQuality_BatchesJudgeCalls` — one past `qualityBatchSize` splits into two calls of 40 and 1), and the audit's derivation gate (`TestRunDerivesThenSkipsOnClearSignal`: a changed store derives the durable pending-coverage rows the report and the table agree on and clears the flag at open, while the next run over the same store skips the store-wide read — the durable rows stand untouched — and still samples, reporting `zero_work` false; `TestRunRearmsSignalWhenRunFails`: a run that fails after its open on a live context re-arms the flag, so a pre-failure mutation's derivation is never stranded) |
| `internal/tags/reconcile` | 50 | Bootstrap log parsing (pre-rewrite/unversioned/band-keep including `reason=rank_unavailable`, REQID resolution, malformed lines ignored), worklist building (stale/unjudged/drifted/aged categories, ordering and caps, conjunction pair exclusion), split-component evidence parsing, report-only planning, sample-name suffix rendering, deleted-tag keep-flip logging, provisional-sweep helpers (`verdictModelLabel` branches, `ProvisionalCounts` JSON shape and `Report.Provisional` presence), the provisional outcome helper (`TestProvisionalOutcome`: admit at the floor promotes, admit below the floor is a silent pending candidate, defer and reject stay for review), and the DB-gated `TestApplyKeepFlip_ZeroDocsCreatesNoTag` (zero resolvable documents must not create the query tag; runs under `make test-cgo-db`), and the compound-sweep disposal (`TestDecideCompoundSplits_DeterministicWithoutAdjudicator`: explicit coordination splits mechanically and a name whose parts all filter away reports `split_invalid`; `TestDecideCompoundSplits_DisposesThroughDecision`: cached verdicts dispose through the composition decision — a composition with dimensional parts splits, a named unit with non-dimensional parts keeps with the decision reason as its skip reason; DB-gated), the decided-drop demotion worklist (`TestBuildCompoundWorklist_DecidedDropQueuesDemotion`: a decided drop of a standing value queues a revocation with the journal reason, the same drop on a provisional value is skipped as decided; DB-gated), and the demotion arm (`TestSweepPromotedTags_MassDecayDemotesAndSparesSeeded`: decay below the floor demotes and records `mass_decay`, seeded values stay out of the worklist; `TestSweepPromotedTags_RunBoundIsSharedWithRejudge`: revocations and re-judgments draw on one per-run bound with the deferral counted as capped and the value above the floor untouched; DB-gated), and the band/confirmation partial-reply salvage (`TestAdjudicateBand_PartialReply`: the returned verdicts decided and the missing entries skipped `ambiguous`/`confirmation_ambiguous`, while a non-typed failure keeps `adjudication_error`), and the deterministic variant bypass (`TestAdjudicateBand_DeterministicVariant`: both loops resolve a pinned variant class with no judge call, the fresh pass marks the verdict `deterministic` and the confirmation pass confirms it), and the twin canonicalization arm (`TestSweepTwins_MechanicalVariantAliases`: a variant pair aliases onto the `PreferCanonical` winner and the loser survives as an alias row; `TestSweepTwins_Filters`: a held, settled, conjunction, band-worklist, duplicate, or dead pair is left alone and only the eligible pair queues pending evidence; `TestSweepTwins_ReportOnlyPlansWithoutWriting`: report-only plans without touching the alias table or the journal; `TestSweepTwins_RejudgeCapBoundsTheWorklist`: the measured worklist is uncapped but only `max_rejudge` pairs dispose; `TestBoundTwinAliases_RespectsRetagBudget`: the document budget truncates the batch and tallies the deferred pair; `TestMeasurementRunJSON`: an absent run omits the timestamp), and the fixed reconciliation dedup key (`TestTagReconcileTaskHandlerDedupKey`: the handler and the enqueue path share one per-type key, independent of payload), and the opening gate (`TestRunZeroWorkOnQuietStore`: a completed run behind a clear flag and empty probes reports `zero_work` and leaves the flag clear; `TestRunFullAfterStoreMutation`: a mutation re-arms the flag through the trigger into a full pass that clears it again at open; `TestRunWorksWhenProvisionalGraceElapsed`: a provisional tag past its grace window with a clear signal reaches the arm as rejudged=1, isolating the time-driven probe; `TestRunBootstrapsWithoutCompletedRun`: the gate never fires before a first completed run, so the bootstrap stays report-only; `TestRunRearmsSignalWhenRunFails`: the open's clear followed by a failed read on a live context is re-armed) |
| `internal/errs` | 7 | Error constructors preserve kind/op/cause, Error() string formatting, Unwrap(), KindOf through plain/wrapped/nil errors, FromDB (nil→nil, ErrNoRows→NotFound, unique violation→Conflict, other→Internal), PgError predicates (unique/foreign key/deadlock/serialization/plain/wrapped, and `IsConstraintOn` matching the constraint name, distinguishing names, non-unique codes, plain and wrapped errors) |
| `internal/database` | 73 | sqlc-generated CRUD, successor-link flow and queries (the waiting-successor activation flip with the derived dedup key, the recursive branch discard, the recursive restore through chained ancestors, the recursive orphan sweep clearing a dead depth-2 branch in one pass while live chains keep their tails, the settle-refusal successor diagnostic; the guarded batch transitions — settle/pause/cancel/claim flip and `ResumeBatch`/`RequeueProcessingBatch`, permanent-failure quarantine identity queries), batch ownership, FTS-adjacent operations, store-key listing (`ListStoreTagKeys` unions tag names and alias names for the decomposition gate), the split-verdict schema/query contract (`TestSplitVerdictQueries`: first-writer-wins conflict, NULL-model cache rows outside the partial index, status CHECK rejection, version-scoped latest reads, the `reconstructible` tri-state; `TestSplitVerdictQueries_NullDocumentID`: both verdict tables accept a document-less row and read it back), document/tag/people/document-type CRUD, saved searches, dashboard analytics queries (empty DB + mixed data), structured search missing filters (MissingLanguage/MissingType/Untagged), structured search people filters (typed relationship scoping, `PersonAnyType` across relationship types, unknown-person miss, count/search parity), structured search sort by `page_count` (asc/desc ordering, unknown sort key falls back to default), WithDocumentCount queries, the resource-lock row contract (`TestLockQueries`: guarded acquire holds until its token releases, a held lock refuses another holder, a waited lock blocks, an aged holder is taken over), lock lifecycle tests, tagmeasure queued-batch listing and the tagidentity active-run count (only queued or processing runs gate; foreign, failed and completed batches never block the reevaluate endpoint), the schedule-name keyed scheduler reads (`TestScheduleQueries`: the active count covers only that name's queued/processing batches and terminals never count, the due-ness anchor reads only the latest terminal batch of that name — paused states and other schedules' terminals stay invisible, an empty schedule answers `ErrNoRows`), wait-gated task claiming (maintenance, and thumbnail's additional storage wait), soft delete (TestUpdateDeleteDocument now asserts soft-delete semantics), CheckRestoreTooling (psql/docker/podman present, missing binary, missing container, remote-block, invalid runtime), tag merge queries (ReassignDocumentTags re-points source-only rows, skips documents already carrying the target, DeleteDocumentTagsForTag clears leftovers), TagPairEvidenceStats aggregation (per-orientation event/document/verdict counts across policy versions), orphan tag-mark lifecycle (only document-less tags get marked, per-id clearing, aged marks listed/deleted, tags holding documents excluded, rolled-back transaction leaves no marks), tag promotion correction migration predicate (`TestMigration00026TagPromotionCorrection`: a backfill-signature row with one document is demoted, a backfill-signature row with two documents, a runtime-promoted row and a NULL-`created_at` row all survive, and the Down re-promotes assigned rows and restores the backfill signature), the 00047/00048 rollback round trip (`TestMigrationOriginScheduleDown`: the down migrations restore `batch.source` and revert migration tasks to the config type and `config:` key prefix, and re-applying re-keys them onto the new task types), the 00049 lock-row carry-over (a pre-migration holder and sweep counter survive the rename with the lock gate seeing them), and the store-change record's narrowing (`TestMigrationStoreChangeRecordNarrowsToThreeFlags`: 00044 seeds four flags and its trigger upserts all four; after 00045 the identity flag is deleted and the same mutation sets exactly the three remaining flags) |
| `internal/search` | 8 | tsvector search with snippets, ranking, pagination; structured search with mime/language/date/missing filters; query sanitization; engine construction; tsvector index rebuild (`Engine.Reindex`) |
| `internal/task` | 72 | Enqueuer (batch+task creation atomically, duplicate specs skipped with no batch row, a task-id collision on a keyed spec aborts and leaves no sibling behind, a waiting spec requires its parent link and carries no dedup key until activation, unknown task type rejected, custom task id, `follow-up declarations`: `TestWithFollowUp` pins the declaration constructor — an empty, id-carrying, parent-linked, non-pending or payload-less task refused and the accepted spec storing a copy — and `TestEnqueueBatchFollowUpPersistence` pins the declaration riding the batch row), follow-up settlement (`TestSettleBatchFollowUp`: a completed settle creates the declared follow-up batch — origin `trigger`, queued, no schedule, task pending with the dedup key derived from the settle catalog — a failed settle fires it too, live work on the declared key collapses the creation to no batch, and a broken declaration rolls back under the savepoint without failing the settle), store (get/claim under the batch lease with owner-scoped selection and wait gating, complete/fail refusing a transition whose batch lease changed hands), dedup key uniqueness, runner (complete/fail/no-tasks), lifecycle ops (`TestSettleBatch`: settles completed/failed by task outcomes, refuses on an active task and a foreign lease, keeps terminal states immutable; `TestPauseBatchAuthorization`: a lease-holder pause releases lock and lease, a foreign pause writes nothing; `TestResumeBatch`: retried tasks restored with their discarded successors in one call, live-owner and non-resumable refusals, nothing-to-resume left untouched; `TestSuccessorActivation`: successors activated under their derived keys with inherited fields merged, a colliding activation discards the duplicate with its branch without failing the parent, an unplannable successor discarded with its reason (an empty derived key among them), a failed parent cascading its whole waiting branch, a completion that lost its claim arming nothing, and a malformed parent result failing only the plans that declare inheritance; `TestCancelBatch`: pending+waiting+processing cancel with lease release, terminal refusal; `TestIsAlive`: pid 0/negative never alive, own pid alive; `TestClaimNextPendingSkipsUnclaimableBatch`: a pending task of a batch this execution does not own cannot be claimed nor stall the queue), runner pause contract (`TestRunnerPauseRequestPausesInsteadOfSettling`: a plain handler failure settles the batch failed while a requested pause leaves it `paused` with the task itself failed), claim gating (`TestClaimNextPendingGating`: consume and enrich claims defer to a held maintenance lock while a maintenance-holding type claims through), guard identity (a foreign-guard transition writes zero rows, a stale claimant whose batch lease changed hands writes nothing), lock lifecycle (waits verification with all-or-nothing acquisition at the batch claim, batch-id release of a child-held lock, the manual holder's run/release and `--no-wait` refusals), panic recovery (panicking handler → task failed with `panic: <text>`, not claimable), nil payload handling, lock-gate deferrals for consume/enrich, the pid-0 adoption (`adoptPID`: a hosted run keeps the pid 0 that cancel and the reaper never signal), consume-handler failure reporting (external `task_test` package exercising `consumption.ConsumeTaskHandler` against a DB-backed store: unmarshal, missing path and missing file all fail loudly while successor state belongs to the runner), the payload display decoder (`ReadPayload` nil/malformed/decode, `DisplayName` precedence), the claimed-batch executor (`TestRunBatch_ClaimedBatchRunsTasksAndSettles`: the forked child adopts a real dispatcher claim, runs it and settles completed with the lease released; `TestRunBatch_PreAdoptionChecks`: a paused batch returns paused without adopting and an unclaimed batch refuses), and the manual release scope (`TestRunDirect_ReleasesOnlyItsOwnHolds`: a direct run's release drops only the holds its own policy acquired, a concurrent manual run's lock survives), plus the task type catalog's constructor contract (`TestNewCatalogRejectsUndeclaredWiringKeys`: a wiring key outside the static table fails construction; `TestNewCatalogPartialWiring`: each single-map entry delivers its dedup hook, worker count and preconditions to its definition and an unwired type resolves its static policy with zero values the enqueue refuses; `TestDedupKeys`/`TestDailyDedupKeys` pin the per-type and per-day derivations over it, including the thumbnail payload's `inherit` declaration), and the forked-executor pool budget (`TestBatchConnBudget`: a type the catalog does not know counts one worker, a `Concurrency` above one contributes its value, `Concurrency` at or below one counts one, mixed types sum their workers, all plus the fixed auxiliary count) |
| `internal/service` | 90 | Batch enqueue through the transactional enqueuer (explicit batch specs, batch origin persisted, zero survivors create nothing), the batch read model (get/owner state/pending/active/queue), ResetStaleProcessingTasks (restore + global sweep; ownership gate over the lock rows: a live holder whose fresh lock row carries the batch keeps task+lock, a fresh foreign lock for another batch spares only its own batch's task, window-expiry recovery resets once, quarantine obeys the gate, and a live batch-held lock exempts the batch's task), ResetProcessingTasksByBatch restore, orphaned scan/delete/restore/move-to-inbox (restore now rides the enqueuer: conflict when the consume task is already queued, file cleanup on refusal; the restore batch declares the measurement follow-up while `after_enrichment_batch` is set), errored files list/download/delete/delete-all, user API key create/revoke/rotate/validate, user Create with role defaults/explicit/invalid, UpdateRole (valid/invalid), Update with role change, password validation (12+ rules) |
| `internal/tags/curation` | 22 | alias-plan application (`TestTag_ApplyAliasPlan`: one per-action `AliasOutcome` in input order — a loser collapse applies and reports the re-pointed document count (documents already carrying the canonical are not counted), an alias-only action applies with a zero count, and a blocklisted name reports the action as not applied; the loser's own aliases retarget onto the canonical so chains never form, the loser name becomes an alias at the current rule-set version, and registration through the alias resolves `Apply` to the canonical entity), tag lifecycle (CreateAndUnblock drops the blocklist row on Created/Conflict while Create keeps it; RestoreDeleted re-creates the blocked name and drops the blocklist row only after success, and reports unknown names as not-found), Create batch atomicity (a mid-batch insert failure rolls the whole batch back, fault injected with a table CHECK constraint), tag promotion trust semantics (Create births land promoted; `CreatePlannedTx` with the quarantine flag leaves births provisional; renaming a promoted tag adds the new normalized name to the embedder store, asserted via `MockEmbedder.Has`), name normalization at save, write-boundary name normalization through the plan and alias paths (`TestTag_CreatePlannedTx_StoresNormalizedName`, `TestTag_ApplyAliasPlan_StoresNormalizedName`), composition-evidence writes (`TestTag_CreatePlannedTx_WritesCompositionEvidence`: one `tag_composition_event` row per verdict, names normalized at the write boundary, `[]` parts never NULL, model NULL when no fresh judge judged the name, and the `reconstructible` tri-state preserved with true, NULL and false staying distinct), store add on write, provisional listing/count agreement under every filter combination against a real database (rubric tri-state including absent keys and unjudged names, derived reject/defer, recorded outcomes, AND-combined criteria), latest quality-event lookup for a name, promote (promoted / already-promoted / not-found statuses, re-fetched `promoted_at`), demote (the store copy leaves, the reason is recorded, re-promotion clears the record, seeded values refused, not-promoted and not-found statuses), and the run enqueues (identity, reconciliation, and measurement, each deduplicated while a run of the type is active, a duplicate-key race reported as already queued, and a batch-insert failure cleaning up the orphan task; `TestMaintenanceSpecs_ChainsRoutineOrder`: the routine's four chained specs in pass order with the pending reconcile root, waiting successors parented to their predecessor, chain parents carrying explicit task ids, non-nil JSON payloads, and fresh task ids across calls), and the containment review read (`TestListSiblings`: absent before any run, the review band ordered first regardless of insertion order, and the run identity with the carried containment and container mass) |
| `internal/tags/policy` | 1 | Policy evaluation over ranked matches (`TestEvaluate`: short-slice and nil-ranked passthroughs, empty candidates, identity replacement over the ranked candidates, band subjects for neighbour/high-similarity/token-overlap/negation/subset/disjoint shapes) |
| `internal/api/handlers` | 256 | the model-catalog capability wire contract (`disable_reasoning` key and value wiring, the on-disk catalog surfaced whole) through `ListModels`, the catalog CRUD status contract (a valid add answering with the updated catalog, a duplicate provider → 409, an invalid name → 400, an unknown set target → 404), the enricher in-use conflict on referenced-model removal, and the nil-registry recovery path (503 reads answered until a reset installs a fresh registry, after which reads serve again without a restart); Document CRUD, tag/people/DocumentType CRUD, user CRUD (with role), task endpoints, saved searches, concurrent operations, dashboard running tasks + analytics + processing health, analytics error path, config handler get/status, PutConfig runtime/container immediate-persistence alongside db migration, batch delete limits, error helpers (writeServiceError REQID resolution with and without an id), auth login (valid/invalid/empty/claims/role), auth logout, API key generate/revoke/rotate/status/forbidden/invalid-id/not-found + wire-format contract (has_api_key present, has_key absent, api_key_prefix present), MeHandler (valid/missing-id/not-found), self-service API key handlers (MeGenerateKey/MeRevokeKey/MeRotateKey/MeGetKeyStatus/unauthorized + wire-format contract), orphaned handler (list/scan/delete/restore/move-to-inbox/delete-all/move-all), errored handler (list/download/delete/delete-all), logs handler (invalid name/file not found/success/line clamping/large file tail/empty file/oversized line truncation/no trailing newline), people-type reserved-name guard (create/update rejected for `person`, case-insensitive; non-reserved name accepted), manual tag assignment clearing the assigned tag's orphan mark, provisional tag list (200 + envelope with live `document_count` and verdict), provisional tag filters (criteria and verdict narrow rows and the filtered total; 400 for an unknown key, state, or verdict; `pending_identity_total` present and equal to the queue count), provisional identity hold (a seeded refused fold gate surfaces the anchor as `identity_hold`), promote endpoint (204 / 409 already promoted / 404 missing / 400 malformed id), demote endpoint (204 / 409 not promoted or seeded / 404 missing / 400 malformed id); ConfigResponseFrom: logging defaults/custom, reclaim max retries, prompt template, max batch delete, doc type refinement, pause on credit error, fallback (nil → omitted; enabled fallback maps all llm fields), consumer fields (supported_files, converter), available file types (required flag, extension aliases), tag measure block (defaults surfaced, the mapped values include `after_enrichment_batch`), tag maintenance block (defaults surfaced and overridden values honored), and the overlapping-tags endpoint (`TestTagSiblingsEndpoint`: the absent envelope, and one pair per page with the run-wide total and the review band) |
| `internal/documents/consumption` | 29 | Full consumer pipeline via mock runner (file discovery, DB transaction, file movement, duplicate detection), file I/O helpers (get, move, copy, remove, clean up), checksum calculation, orphaned file management, and the shared ingest task-set assembly (`TestIngestTaskSpecsWithoutThumbnails`/`WithThumbnails`: consume first with the file identity fields, parent-linked waiting children, thumbnail toggle) |
| `internal/documents/converter` | 3 | LibreOffice conversion adapter (`TestLibreOfficeCanHandle` MIME dispatch, `TestLibreOfficeName`, `TestNewLibreOfficeNotFound` without the binary). Not wired into a Makefile tier |
| `internal/documents/pdfoptimizer` | 1 | Ghostscript PDF optimizer (`TestGhostscript_Optimize_RejectsRelativePath`; skips when `gs` is absent). Not wired into a Makefile tier |
| `internal/documents/textextractor` | 21 | Text extraction adapters: composite MIME dispatch, unknown MIME, can-handle, name and constructor composition; DOCX/ODT extraction (single/multi paragraph, empty documents, invalid ZIP, missing `document.xml`/`content.xml`); `pdftotext` relative-path rejection. Not wired into a Makefile tier |
| `internal/ops/cli` | 31 | Config handler (help/path/validate valid+invalid/get/missing key/set/invalid set/unset/missing key/unset without key/dump/unknown args), parseValue, deleteNestedKey, highlight snippet ANSI markers, the tag commands' dispatch and flag surface (`TestTagCommandsDispatchHelp`, `TestTagCommandsValidateRunID`: a batch-mode invocation requires the execution token and a malformed one is refused, and the removed `--run` alias is rejected as an unknown flag; `TestTagCommandsWithoutFlagsRunDirect`: all five tag verbs (the maintenance routine included) registered and dispatching with a no-flag invocation reaching the direct-run path instead of demanding an execution token), and the `llm` verb (dump/get over the seeded catalogs, string and boolean field sets reaching the catalog files, the in-use and orphan-model unset guards with `--cascade` dropping provider and models together, and `--reset` recovering a mangled catalog), and the batch-create paused-guard refusal (`TestBatchCreateConsumePausedGuard`: refuse with the paused ids while a credit-paused batch exists, proceed once none remains; DB-gated). CGo-gated package: compiles only under `CGO_ENABLED=1` (runs via `make test-cgo` / `make test-cgo-db`) |
| `internal/ops/consume` | 3 | The executor-token contract (`TestParseExecToken`: an empty or malformed token refused, a dispatcher token adopted unchanged), the `totalFiles` index/count fallback, and the direct-run summary arithmetic (`TestDirectRunSummaryCountsFailedSteps`: an unhashable file counts one failed step with the handler unreached and the successful count never negative; a successful file counts the consume and enrich steps once each — stdout-captured) |
| `internal/ops/configtask` | 6 | Config migration tasks: DB-gated migrate-db and migrate-storage runs plus the no-op/directory helpers (`TestMigrateDBTask`, `TestMigrateDBTask_RefusesForeignDestination`, `TestMigrateStorageTask`, `TestDirChanged`, `TestMigrateStorage_NoOpReturnsErrNoOp`, `TestHandleMigrateStorage_NoOpReturnsNoOpStatus`) — runs under `make test-cgo-db`, exec'ing `psql` inside the test container |
| `internal/ops/container` | 4 | The task type catalog's dedup derivations (`TestDedupKeys`: per-type key formats pinned, empty keys for payloads without the identity fields; `TestDailyDedupKeys`: the backup mode + UTC date and mirror date keys), the lock-declaration pin (`TestLockDeclarationsMatchVocabulary`: every type's queued lock policy matches the closed vocabulary, plus the three direct-mode overrides — consume/enrich hold `maintenance`, `thumbnails` holds `storage` and waits `maintenance`), and the schema-role split (`TestSchemaRoleSplitsTheBootstrap`, DB-gated via `t.Skip` when `TEST_DATABASE_URL` unset — a forked-executor container fails against and never creates a fresh package test database its launcher never bootstrapped, while the owner container creates it and applies the full schema, asserted over the goose history; the database is dropped `WITH (FORCE)` before and after the run) |
| `internal/ops/flags` | 4 | `FlagParser` `String`/`Int`/`Bool`/`Help` happy paths and error paths (missing value, non-integer, out of bounds, valueless flag), repeated occurrences, an argument consumed as one flag's value not satisfying another, and `Rest` |
| `internal/ops/queue` | 25 | The zero-task enqueue invariant (`TestEnqueueWithNoTasksCreatesNoBatch`: the transactional enqueue leaves no batch row behind when the task set is empty; DB-gated via `t.Skip` when `TEST_DATABASE_URL` unset), the identity schedule definition (`TestTagIdentityScheduleDefinition`: the pinned daily 03:30 cadence no config key can move, gated on the tag adjudicator), the declarative schedule table (`TestScheduleNamesAreUnique`: uniqueness plus the eleven-entry count — due-ness and the active check are keyed by the name; `TestBackupSchedulesArePerMode`: per-mode `backup:<mode>` names with per-mode timing and no backup entry while disabled; `TestScheduleEnabledGates`: the seven fixed schedules' config gates including off-by-default, enabled-without-interval and the maintenance routine's suppression of the individual reconcile/measure/audit gates while identity's cadence stays), the origin pairing (`TestPollingEntryOriginPairing`: exactly one unnamed entry carrying origin `polling` with no timing, every named entry carrying origin `schedule` — the pairing `NewBatchSpec` enforces), the polling gate (`TestPollingEntryEnabledGates`: off while the polling flag is off, on with the flag, on inside an all-day active window), the unnamed entry's cadence (`TestIntervalDue`: due with no recorded fire, pending and elapsed at one-minute and multi-minute intervals, the zero-interval one-minute floor), `fire`'s error contract (`TestFireSkipsUnmetPrecondition`: an enqueue-side precondition skips the occurrence and creates no batch; `TestFirePropagatesProducerError`: a producer failure is wrapped and returned; the precondition one DB-gated), the schedule-name due-ness contract (`TestSchedulerDueDerivesFromTerminalBatches`: a never-run schedule anchors its next preferred slot exactly once, a fresh failed batch defers to the next occurrence instead of retrying, an aged terminal batch leaves the schedule due; DB-gated), the stale-reclaim recovery (`TestReclaimStaleBatches`: the listed ownership row released before recovery — requeue with reset on live work, guarded settle to `failed` when tasks are already at the retry bound, lease gone in both branches; `TestReclaimStaleTasksQuarantinesInboxFiles`: the global stale sweep's quarantined consume tasks have their inbox files moved to `storage/errors/` immediately, the sweep's quarantined identities are logged alongside the count; both DB-gated), and the dispatcher/launcher (`TestResolveLaunchTarget`: per-type eligibility and the batch's owning command resolved by fixed precedence, with chained all-tag batches resolving to the `tagmaintenance` routine command while a pure tag batch keeps its own verb; `TestLaunchQueuedBatchClaimsAndForks`/`TestLaunchQueuedBatchRefusals`: the claim-and-fork against a forked probe child and the pointed refusals; `TestBatchLockSets`, `TestLaunchBackoff`, `TestRequeueAfterLaunchFailure`; `TestDispatchHonorsBudgetAndSkipsUnknownTypes`, `TestDispatchClaimsHoldingBatchOutsideTheBudget`, `TestDispatchBoundsLaunchFailures`, `TestChildExitedClassification`; most DB-gated, and the forked-child probe self-registers through the test binary's `TestMain` hook), the daemon pool's formula (`TestQueueConnBudget`: a configured `max_concurrent_batches` passes through, a zero or negative one falls back to the dispatcher's two under the shared `EffectiveBudget` rule, all plus the fixed daemon headroom), and the fork marker's verb contract (`TestIsExecutorVerb`: every command the launcher forks — the batch-mode CLIs and the `tagmaintenance` routine verb — is accepted, anything else refused) |
| `internal/backup` | 54 | Create (full backup/missing DB/missing storage/no files/SQL dump content), ApplyRetention (delete oldest/keep all/keep 0), ValidateArchive (valid/invalid gzip/missing manifest/missing file), ExtractArchive (valid/path traversal/symlink skip), ReplaceFiles (SQL dump/unknown format/documents-mode storage swap, same-device rename path), CopyDir, NextBackupTime (daily/sub-daily/clamp/empty-time), NextOccurrence (before/after the preferred time, weekly closest, empty and invalid time default to 02:00, fixed-offset zone; every case strictly after now); mirror tests: Available (sanity), MirrorPrecondition (rsync reachable = nil error, absent = the install-hint error), IsRemoteTarget (user@host/rsync:///local), parseStats (stats2 format with reg count + comma-formatted total, deleted-file lines ignored per P1 fix, empty/malformed degrade to zero, commas in the outer count) |
| `internal/tags` | 61 | The shared J4 resolution (`TestCanonicalLoser`: a judged direction names the winner, a tie or a deterministic variant falls to `PreferCanonical`) and the single-read journal seam (`TestCompositionReview`: one read returns both the hold and state views, matching the single-view wrappers, and a degraded keep is both a hold and undecided), the measurement read side (`TestDecodeRows`: numbers round-trip as the cuts read them, a malformed payload fails with the row index, the run columns stay zero; `TestLatestWorklists`: an empty journal is absent, only the newest run is derived; `TestSiblingWorklist`: absent before a run, run identity, the container-first pair, and a containment-only snapshot that leaves twins and compound splits underived; `TestReconstructibilityBatch`, DB-gated: the live counts over the judged cover, a house-form spelling resolving to the stored compound, and an unknown compound or part leaving no reading), Identity planning (`HouseForm` house-form cases, `TieBreak`, the deterministic variant-class table (`TestVariantClass`: pinned orthographic suffixes, literal token permutations, and the classes that stay judged — plurals, mixed-language, morphological/function-word inversions, spelling-plus-permutation), `PlanDeterministic` apply/conflict/key-equal/blocked/fold-pending/derivational paths), the rejected-name plan filter (`TestPlan_DropTagNames`: output-name and raw-input matches, evidence dropped by candidate and partner, receiver untouched, empty set no-op), the conservative plural fallback for folds (`TestPluralStrip` rule table; `TestProposedFold_PluralFallback` with its dictionary-membership preconditions pinned), the store-anchored derivational fold proposal (`TestProposedDerivationalFold`: suffix hits, the store-anchor requirement, two hits as ambiguity, multi-token/length/uppercase guards, irregular demonyms excluded), the bounded morphographic proposal (`TestProposedMorphologicalFold`: distance-1/2 hits, the distance-3 cap, two store hits as ambiguity, multi-token/length/uppercase and multi-word-store-key guards), the chained split-seam fold (`TestAnchoredFold`: attested adjective→noun folds (chilean→chile, mexican→mexico, german→germany, political→politics), an orthographic variant folding onto the promoted spelling (labour→labor, single- and multi-token), ambiguity, the no-anchor and multi-token guards, an unattested near-spelling standing unreduced, and an attested base the store lacks), the primary-sense seam rule (`TestAnchoredFold_LiveView`: a multi-base part folds onto its live primary-sense base, a live non-primary base neither attracts nor blocks the fold, the primary base must be live to fold, and a variant anchor folds regardless), the canonical direction (`TestPreferCanonical`: the house form wins over a foreign label in either order, a deterministic variant resolves through the tie-break, and labels the language step cannot separate fall through), the deterministic period class (`TestIsPeriodClass`: the ordinal+unit shape, the house-form precondition, the two-token bound, a plural unit, and non-Latin script), the store-existing skip's state read (`TestCompositionStatesForNames`: StoredName keying incl. the house-form case, a degraded keep reported undecided, stale-version rows invisible, a rowless name absent), and the key-to-name store view (`TestStoreNames`: the first name wins a shared key); extension-algebra readings (mass, overlap coefficients, reconstructibility excess/deficit, part coverage, equality/complement) and set operations; rule-set v1 worklist cuts (twin/sibling/compound bands + gray ranges + both mass floors) and the band classifier's boundaries (`TestClassifyReconstructibility`: the strict split and gray cuts on either ratio, and the insufficient state), the containment pair's direction (`TestDeriveWorklists_SiblingDirection`: the broader tag leads with its own mass whichever operand contains the other, and equal containment keeps operand order); compound shape classification and part derivation (`TestDeriveSegments` fan shapes; `TestGreedyCover` coarsest-cover cases: longest-span and leftmost-bigram wins, a whole-sequence bigram excluded, an uncovered word and gap failing the cover, and the alongside partial cover); the time-designation gate (`TestIsTimeDesignation` shapes and non-shapes, `TestIsTimeDesignationDrop` with the store exemption); the variant-key parity the birth-time scan precomputes (`TestVariantKey`: pinned pairs share a signature, unrelated names — plurals included — share none); the pinned admission rules (`TestDecideAdmission`: fail-count boundaries, judge-defer precedence over counted failures, lone and corroborated deterministic signals; `TestDecidePromotion`: floor boundary, defer/reject never promote; `TestDecideMassDemotion`: the retention floor — zero and below-floor live mass demote, at and above the floor keep standing); the pinned composition rule table (`TestDecideComposition`: every branch, evaluation order and the fail-safe, including the far leg that keeps a judged composition as a relational keep) and the composition promotion hold (`TestCompositionHeldNames`: latest reason per version, version scoping, name normalization, a decided drop holding); evidence-row JSON encoding (nil caveats → `[]`) and operand normalization at the write boundary (`TestEncodeRows_NormalizesOperands`); prefixed-report extraction (`ExtractReport`); DB-gated runner (seeded store → persisted records, report/persisted parity, canceled-context error path) and the DB-gated evidence-derived promotion hold (`TestIdentityHeldNames`: gate rejection, base_form=stand despite a confirming gate, dead anchor, conventional-form exclusion, empty input — both run under `make test-db`), and the opening-gate protocol (`TestOpenStoreSignal`: a set flag reads as changed and clears in one committed step, a clear flag stays clear without another write, a failed run's re-arm returns its cause and sets the flag, and an absent row counts as changed so a gate never skips on missing state — DB-gated, runs under `make test-db`) |
| `internal/types` | 8 | Registry completeness (all batch origins/task types/statuses declared, validity and parse round-trips, defensive-clone isolation, the closed lock declarations — `LockDeclarationFor`, `HoldsLocks`, `TaskTypesWaitingOn`, `LockHoldingTaskTypes`, `AllLockNames`) |

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
The connection pool opens with the neutral default ceiling
(`database.DefaultMaxOpenConns`: 25 max open, idle capped at five, 5 min
lifetime). The per-execution derived ceilings (a forked executor's pool sized
to its batch's workers, the daemon's budget-derived pool) exist only in the
production paths; test databases always run at the default ceiling.

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
make test          # CGO_ENABLED=0, 19 packages, 60s timeout (no DB needed)
make test-verbose  # same with -v output
make test-web      # web/ vitest unit + component tests (no database)
make test-web-e2e  # web/ Playwright smokes with mocked API (requires npx playwright install chromium)
```

The Makefile explicitly sets `CGO_ENABLED=0` for test targets, overriding the
`export CGO_ENABLED := 1` used for build targets. This ensures tests run in
any environment regardless of C library availability.

**19 packages run without a database:** `llm`, `config`, `tools`, `log`,
`textnorm`, `matcher`, `documents/mime`, `documents/storage`, `auth`, `api`,
`api/wizard`, `llm/analyzer`,
`errs`, `task`, `backup`, `types`, `tags`, `ops/flags`, `ops/container`. The DB-backed tests inside `task`, `backup` and `tags`
`t.Skip` when `TEST_DATABASE_URL` is unset, so each of these packages runs in this tier
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

These 417 tests require `CGO_ENABLED=1` to compile because their source files
are tagged `//go:build cgo` (or, for `internal/llm/analyzer`,
`internal/ops/cli`, `internal/ops/consume`, `internal/ops/queue`,
`internal/tools`, `internal/enrichment`, `internal/api`,
`internal/api/wizard`, `internal/ops/container`, `internal/tags`,
`internal/tags/admission` and
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

**15 packages, 417 tests:**

| Package | Tests | CGo at runtime? |
|---------|-------|-----------------|
| `internal/cgo` | 2 | Yes — MuPDF page render |
| `internal/documents/ocr` | 9 | No (pure Go, build-tag gated) |
| `internal/llm/analyzer` | 97 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/matcher` | 18 | No (pure Go, build-tag gated) |
| `internal/tools` | 28 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/api` | 25 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/api/wizard` | 1 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/container` | 4 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/cli` | 31 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/consume` | 3 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/ops/queue` | 16 | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |
| `internal/enrichment` | 9 | No (pure Go tests + one DB-gated test; package links the cgo adapters transitively) |
| `internal/tags` | 61 | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |
| `internal/tags/admission` | 63 | No (pure Go tests; package links the cgo adapters transitively) |
| `internal/tags/reconcile` | 50 | No (pure Go tests + DB-gated tests; package links the cgo adapters transitively) |

`internal/ops/cli` is not part of `make test`: the package only compiles with
CGo (`hugot.go` imports the `//go:build cgo`-gated matcher engine), and
kushim — the only binary importing it — is always built with CGo.
`internal/tools` runs in both tiers: `make test` builds it without CGo, and
`make test-cgo` re-runs it with the cgo adapters linked.
`internal/llm/analyzer` likewise runs in both tiers: `make test` builds it
without CGo, and `make test-cgo` re-runs it with the cgo adapters linked.
`internal/api`, `internal/api/wizard` and `internal/ops/container` also run
in both tiers for the same reason: their packages link the cgo adapters
transitively, so the cgo-linked build is exercised on every push/PR.

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

Also runs `internal/ops/queue` (DB-gated tests in a CGo package — the stale-reclaim
recovery `TestReclaimStaleBatches` and `TestReclaimStaleTasksQuarantinesInboxFiles`).
Those tests `t.Skip` when `TEST_DATABASE_URL`
is unset (as in the `make test-cgo` step), so the package must be listed here
for them to actually exercise the behavior in CI.

Also runs `internal/ops/container` (the schema-role split test
`TestSchemaRoleSplitsTheBootstrap`, DB-gated: a forked-executor container fails
against — and never creates — a database the launcher never bootstrapped, while
the owner container creates it and applies the schema). The package is listed in
this tier so the DB-gated test exercises PostgreSQL under the cgo-linked build.

Also runs `internal/ops/consume` (the executor-token, file-count and
direct-run summary tests — pure Go; the per-batch pool removal left no
DB-gated test in the package, so nothing here skips).

Also runs `internal/tags/reconcile` (DB-gated reconciler tests — the opening-gate
contract `TestRunZeroWorkOnQuietStore`, `TestRunFullAfterStoreMutation`,
`TestRunWorksWhenProvisionalGraceElapsed`, `TestRunBootstrapsWithoutCompletedRun`
and `TestRunRearmsSignalWhenRunFails`, plus `TestApplyKeepFlip_ZeroDocsCreatesNoTag`,
which asserts the keep-flip document-ID ordering fix against a real database).
Those tests also `t.Skip` when `TEST_DATABASE_URL` is unset, so the package is
listed in both tiers.

The `internal/tags/admission` package (identity planning, adjudication,
compound decomposition, verdict-event and tag-quality tests) runs under both
`make test-cgo` and `make test-cgo-db`: its DB-gated tests
(`TestPlanIdentity_LogsPendingPairs`, `TestPlanIdentity_FoldStaysPendingWithoutJudge`,
`TestPlanIdentity_StoreExactAppliesOntoExistingRow`,
`TestPlanIdentity_StoreLoserRegistersAsPairAlias`,
`TestAdjudicateIdentityPairs`, `TestResolvePending_Fold`,
`TestResolvePending_SameIntentPair`, `TestResolvePending_AdjudicatorDisabled`,
`TestResolvePending_FoldTargetDeleted`,
`TestTagQualityEvent_InsertAndCacheRead`, `TestApplyTagQuality_PartialSalvage`,
`TestRunDerivesThenSkipsOnClearSignal`, `TestRunRearmsSignalWhenRunFails`) `t.Skip` when `TEST_DATABASE_URL` is
unset, and the `test-cgo-db` listing is what makes them exercise PostgreSQL in
CI. The parent `internal/enrichment` package also runs under both tiers: its
DB-gated tests (`TestEnrich_SuggestionInjectionBar`,
`TestPromoteEligible_CompositionHold`) `t.Skip` when `TEST_DATABASE_URL` is
unset, and the `test-cgo-db` listing runs them against PostgreSQL.

Also runs `internal/tags` (identity planning, the extension algebra, the
admission and composition rules, the composition promotion hold, and the
store-changed signal protocol `TestOpenStoreSignal`). Its
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
  auto-cleanup) — `authStore.svelte.js` and `api.js` (both touch `localStorage` at import
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
  it runs the SSR server, and `authStore.svelte.js`'s import-time `localStorage` access 500s
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
The scan covers the standard library of the pinned toolchain (the version
declared in go.mod via the Makefile's GOTOOLCHAIN export), so standard-library
findings are fixed by raising the `go` line in go.mod rather than by upgrading
the locally installed Go.

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
