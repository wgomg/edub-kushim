// Package enrichment is the document enrichment coordinator: it reduces
// the stored text for the model budgets, runs the LLM analysis pass(es),
// matches tags against the vocabulary, delegates the tag admission
// decisions to tags/admission, normalizes people, and persists the
// document's analysis. It decides nothing about tags itself; writes go
// through curation and the journal.
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	anyascii "github.com/anyascii/go"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/tools/textreducer"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	tokenBudgetRatio   = 0.9
	minTargetWords     = 100
	fallbackVocabLimit = 200
)

// Enricher coordinates one document's enrichment: reduction, analysis
// (with type refinement), matching, admission delegation, people and
// tag persistence, and progress reporting. The runner and the admission
// machinery are built inside; the enricher holds no shared mutable state
// beyond the mutex the flow serializes on.
type Enricher struct {
	config    *config.Config
	logger    *log.Logger
	client    *database.Client
	queries   *database.Queries
	runner    *tools.Runner
	tag       *curation.Tag
	admission *admission.Adjudicator
	mu        sync.Mutex
}

// NewEnricher wires the coordinator: the config, the database, the
// curation tag service every tag write goes through, and the matcher. It
// builds the tools runner (reduction, analysis, matching) and the
// admission adjudicator from the same parts.
func NewEnricher(cfg *config.Config, logger *log.Logger, client *database.Client, tag *curation.Tag, matcher matcher.Matcher) *Enricher {
	runner := tools.NewRunnerWithMatcher(logger, cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcher,
		textreducer.NewReductionCache(logger, client, cfg))
	return &Enricher{
		config:    cfg,
		logger:    logger,
		client:    client,
		queries:   client.Queries,
		tag:       tag,
		runner:    runner,
		admission: admission.NewAdjudicator(cfg, logger, client.Queries, runner),
	}
}

// DocumentCounts is one document's enrichment outcome tally, aggregated by
// the batch bookend. Assignments is the tag assignments written
// (len(tagIDs)); Births counts the novel tags born (types.Created persist
// statuses); Promotions counts the provisional names that cleared the floor
// (zero when the quality layer is off); Rejects counts the names the
// quality gate dropped and Defers the admission deferrals (both zero when
// the quality layer is off); Splits, Keeps and Drops count the composition
// dispositions after the part-rejected degrade.
type DocumentCounts struct {
	Assignments int `json:"assignments"`
	Births      int `json:"births"`
	Promotions  int `json:"promotions"`
	Rejects     int `json:"rejects"`
	Defers      int `json:"defers"`
	Splits      int `json:"splits"`
	Keeps       int `json:"keeps"`
	Drops       int `json:"drops"`
}

// Add accumulates another document's counts into c, so the batch bookend
// sums a batch's documents without re-listing every field.
func (c *DocumentCounts) Add(other DocumentCounts) {
	c.Assignments += other.Assignments
	c.Births += other.Births
	c.Promotions += other.Promotions
	c.Rejects += other.Rejects
	c.Defers += other.Defers
	c.Splits += other.Splits
	c.Keeps += other.Keeps
	c.Drops += other.Drops
}

// Enrich runs the whole enrichment pass for one stored document and
// returns the result payload stored on the task plus the per-document
// counts the batch bookend aggregates. The document row must exist;
// progress callbacks update the task row. An empty document (no usable
// text) skips the LLM steps and still updates what it can. report must be
// non-nil; pass task.NoopProgress to report nothing.
func (e *Enricher) Enrich(ctx context.Context, document database.Document, report task.ProgressFunc) (*json.RawMessage, DocumentCounts, error) {
	logId := document.DocumentID
	ctx = log.WithReqID(ctx, logId)

	start := time.Now()
	e.logger.Info(logId, "starting enrichment for file %s", document.StoragePath)

	defer func() {
		elapsed := time.Since(start)
		e.logger.Info(logId, "finished enrichment %s in %s", document.StoragePath, log.HumanDuration(elapsed))
	}()

	report("reduce", "", 0)
	llmContent, err := e.runner.ReduceContent(ctx, document.TextContent.String,
		matcher.TargetWordCount(int(document.WordCount), e.config.Enricher.TextReducer.TargetWords))
	if err != nil {
		e.logger.Error(logId, "llm text reduction failed, using raw text: %w", err)
	} else {
		if int64(document.WordCount) > int64(llmContent.TargetWordCount) {
			e.logger.Info(logId, "long path selected for llm text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, llmContent.CharCount, document.WordCount, llmContent.WordCount, llmContent.TargetWordCount)
		}
	}

	tagsContent, err := e.runner.ReduceContent(ctx, document.TextContent.String,
		matcher.TargetWordCount(int(document.WordCount), e.config.Enricher.TagMatcher.ReduceTargetWords))
	if err != nil {
		e.logger.Error(logId, "tag text reduction failed, using raw text: %w", err)
	} else {
		if int64(document.WordCount) > int64(tagsContent.TargetWordCount) {
			e.logger.Info(logId, "long path selected for tag text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, tagsContent.CharCount, document.WordCount, tagsContent.WordCount, tagsContent.TargetWordCount)
		}
	}

	docTypes, err := e.queries.ListAllDocumentTypes(ctx)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve document types: %w", err)}
	}
	peopleTypes, err := e.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve people types: %w", err)}
	}
	docTypeRefs := make([]analyzer.TypeRef, 0, len(docTypes))
	for _, dt := range docTypes {
		docTypeRefs = append(docTypeRefs, analyzer.TypeRef{Name: dt.Name, Description: dt.Description})
	}
	peopleTypeRefs := make([]analyzer.TypeRef, 0, len(peopleTypes))
	for _, pt := range peopleTypes {
		peopleTypeRefs = append(peopleTypeRefs, analyzer.TypeRef{Name: pt.Name, Description: pt.Description})
	}
	allTags, err := e.tag.ListAll(ctx)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve tags: %w", err)}
	}
	storeTagKeys, err := e.queries.ListStoreTagKeys(ctx)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve tag keys: %w", err)}
	}
	storeKeys := make(map[string]struct{}, len(storeTagKeys))
	for _, key := range storeTagKeys {
		storeKeys[tags.Key(key)] = struct{}{}
	}
	deletedTags, err := e.queries.ListAllDeletedTags(ctx)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve deleted tags: %w", err)}
	}
	blocklist := make(map[string]struct{}, len(deletedTags))
	for _, row := range deletedTags {
		blocklist[row.Name] = struct{}{}
	}

	var tagSuggestions []string

	dbNames := make(map[string]struct{}, len(allTags))
	var tagsNames []string
	for _, t := range allTags {
		if t.PromotedAt.Valid {
			tagsNames = append(tagsNames, t.Name)
		}
		dbNames[t.Name] = struct{}{}
	}
	promotedStoreNames := tags.StoreNames(tagsNames)

	matchTagsStart := time.Now()
	report("tag-match", "", 0)
	matchedTags, err := e.runner.MatchTags(ctx, document.DocumentID, tagsContent.Text)
	if err != nil || len(matchedTags.Matches) == 0 {
		if err != nil {
			e.logger.Error(logId, "tag matching failed: %v", err)
		} else {
			e.logger.Debug(logId, "tag matching returned no matches")
		}
		if len(tagsNames) <= fallbackVocabLimit {
			tagSuggestions = tagsNames
			e.logger.Info(logId, "using all %d tags as suggestions (vocabulary within fallback limit)", len(tagsNames))
		} else {
			e.logger.Info(logId, "no tag suggestions (vocabulary of %d tags exceeds fallback limit)", len(tagsNames))
		}
	} else {
		// the bar applies before alias collapse: only the matched
		// candidates carry similarities
		bar := e.config.Enricher.TagMatcher.MinSuggestionSimilarity
		for _, c := range matchedTags.Matches {
			if c.Similarity >= bar {
				tagSuggestions = append(tagSuggestions, c.Tag)
			}
		}
		e.logger.Debug(logId, "tag matching: %d/%d suggestions above bar %.2f (%s)",
			len(tagSuggestions), len(matchedTags.Matches), bar, time.Since(matchTagsStart))
	}
	tagSuggestions = e.admission.CanonicalSuggestions(ctx, tagSuggestions, logId)

	var analysis *tools.ContentAnalysisResult
	report("analyze", "", 0)
	for i := range 2 {
		analysis, err = e.runner.AnalyzeContent(ctx, llmContent.Text, docTypeRefs, peopleTypeRefs, tagSuggestions)
		if err == nil {
			break
		}

		var ctle *analyzer.ContentTooLargeError
		var tokErr *analyzer.TokenLimitError
		var credErr *analyzer.InsufficientCreditsError

		var maxTokens, actualTokens int
		switch {
		case errors.As(err, &ctle):
			maxTokens = ctle.MaxInputTokens
			actualTokens = ctle.EstimatedTokens
		case errors.As(err, &tokErr):
			maxTokens = tokErr.MaxTokens
			actualTokens = tokErr.RequestedTokens
		case errors.As(err, &credErr):
			return nil, DocumentCounts{}, e.creditPause(logId, err)
		default:
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to analyze content: %w", err)}
		}

		if actualTokens <= 0 {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to analyze content: %w", err)}
		}

		ratio := float64(maxTokens) / float64(actualTokens) * tokenBudgetRatio
		newTarget := int(float64(llmContent.TargetWordCount) * ratio)
		if i == 1 || newTarget < minTargetWords {
			return nil, DocumentCounts{}, &task.Error{
				ReqID: logId,
				Err: fmt.Errorf("document too large for model %s/%s: %d tokens exceeds budget (max_input_tokens=%d)",
					e.config.Enricher.ContentAnalyzer.Llm.Provider, e.config.Enricher.ContentAnalyzer.Llm.Model, actualTokens, maxTokens),
			}
		}
		reduced, rerr := e.runner.ReduceContent(ctx, document.TextContent.String, newTarget)
		if rerr != nil {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to reduce content: %w", rerr)}
		}
		llmContent = reduced
	}

	if isEmptyAnalysis(analysis) {
		e.logger.Info(logId, "empty analysis result—retrying once")
		analysis, err = e.runner.AnalyzeContent(ctx, llmContent.Text, docTypeRefs, peopleTypeRefs, tagSuggestions)
		if err != nil {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to analyze content on retry: %w", err)}
		}
		if isEmptyAnalysis(analysis) {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("analysis returned empty result after retry")}
		}
	}

	refinementCfg := e.config.Enricher.ContentAnalyzer.DocTypeRefinement
	if refinementCfg.Enabled && llmContent.TargetWordCount > 0 &&
		int64(document.WordCount) > int64(llmContent.TargetWordCount) {
		headTailText := analyzer.ExtractHeadTailWords(
			document.TextContent.String,
			refinementCfg.HeadWords,
			refinementCfg.TailWords,
		)
		metadata := analyzer.DocMetadata{
			WordCount: document.WordCount,
			PageCount: document.PageCount,
			MimeType:  document.OriginalType,
		}
		refinedType, err := e.runner.AnalyzeDocType(ctx, analysis, headTailText, docTypeRefs, metadata)
		if err != nil {
			e.logger.Error(logId, "doc type refinement failed, keeping first pass result: %v", err)
		} else {
			e.logger.Info(logId, "doc type refined: %q → %q", analysis.DocType, refinedType)
			if refinedType != "" {
				analysis.DocType = refinedType
			}
		}
	}

	analysis.Tags = analyzer.NormalizeTags(analysis.Tags)

	for i, p := range analysis.People {
		canonicalName, _ := canonicalPersonName(p)
		if canonicalName == "" && p.NameRomanized != "" {
			canonicalName = p.NameRomanized
		}
		analysis.People[i].NormalizedName = textnorm.NormalizeForDB(canonicalName)
	}

	docTypeNames := make([]string, 0, len(docTypes))
	for _, dt := range docTypes {
		docTypeNames = append(docTypeNames, dt.Name)
	}

	existingPeople, err := e.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("list existing people: %w", err)}
	}
	knownNormalized := make([]string, 0, len(existingPeople))
	for _, p := range existingPeople {
		knownNormalized = append(knownNormalized, normalizedPeopleKey(p))
	}
	kept, facetDrops := analyzer.SeparateFacets(analysis.Tags, analysis.TagDetails, docTypeNames)
	for _, drop := range facetDrops {
		e.logger.Info(logId, "tags[facet]: drop %q (category=%s reason=%s)", drop.Term, drop.Category, drop.Reason)
	}
	analysis.Tags = kept

	// the deterministic time-designation gate: a year, decade, month, month+year, or unseeded ordinal
	// period unit is not a dimension; it is admissible only when the store already holds it (a seeded
	// period or an operator-seeded exception). It runs before decomposition, and again after the language
	// guard, so a foreign time designation the guard translates into English is dropped too.
	dropTimeDesignations := func(list []string) []string {
		keptTime := make([]string, 0, len(list))
		for _, tag := range list {
			if tags.IsTimeDesignationDrop(tag, storeKeys) {
				e.logger.Info(logId, "tags[time]: drop %q (reason=time_designation)", tag)
				continue
			}
			keptTime = append(keptTime, tag)
		}
		return keptTime
	}
	analysis.Tags = dropTimeDesignations(analysis.Tags)

	tagFilter := analyzer.NewTagFilter(analysis.People, knownNormalized, docTypeNames)
	analysis.Tags = analyzer.FilterTags(analysis.Tags, tagFilter)

	// the language guard runs before the split gate so a translated English name is what gets decomposed,
	// registered, applied, and keyed by every downstream row; a foreign compound never reaches the split
	// gate, so its composition evidence and parts are keyed to the English form
	pendingLanguage, err := e.admission.ResolveLanguages(ctx, analysis, storeKeys, logId)
	if err != nil {
		if te := e.creditPause(logId, err); te != nil {
			return nil, DocumentCounts{}, te
		}
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("language resolution failed: %w", err)}
	}
	// a time designation the guard newly produced in English (a translated month+year, month, or period) is
	// not admissible; drop it before decomposition so it never reaches the split gate or the store
	analysis.Tags = dropTimeDesignations(analysis.Tags)

	decomposed, compositionRows, err := e.admission.DecomposeTags(ctx, analysis.Tags, storeKeys, promotedStoreNames, tagFilter, document.ID, logId)
	if err != nil {
		if te := e.creditPause(logId, err); te != nil {
			return nil, DocumentCounts{}, te
		}
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("tag decomposition failed: %w", err)}
	}
	analysis.Tags = e.admission.ApplyDeletedTagPolicy(decomposed, blocklist, logId)

	identityStart := time.Now()
	report("identity", "", 0)
	ranked, err := e.runner.RankTags(ctx, document.DocumentID, analysis.Tags)
	var decisions []policy.Decision
	var identityPlan tags.Plan
	if err != nil {
		e.logger.Error(logId, "identity planning: matcher rank failed: %v", err)
		for _, tag := range analysis.Tags {
			e.logger.Info(logId, "policy[%s]: keep %q (reason=rank_unavailable)", policy.Version, tag)
		}
		identityPlan, err = e.tag.PlanRegistration(ctx, analysis.Tags)
		if err != nil {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("identity planning failed: %w", err)}
		}
	} else {
		var planned []string
		planned, identityPlan, decisions, err = e.admission.PlanIdentity(ctx, admission.IdentityPlanInput{
			DocID:   document.ID,
			Names:   analysis.Tags,
			Ranked:  ranked,
			DBNames: dbNames,
		}, logId)
		if err != nil {
			if te := e.creditPause(logId, err); te != nil {
				return nil, DocumentCounts{}, te
			}
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("identity planning failed: %w", err)}
		}
		analysis.Tags = planned
		e.logger.Debug(logId, "identity planning: %d tags (%s)", len(planned), time.Since(identityStart))
	}
	analysis.TagDetails = admission.RenameTagDetails(analysis.TagDetails, identityPlan)
	// the quality gate keys rejections by the plan's output names, so the
	// gate's pre-plan parts are resolved through the same mapping before
	// the rejection test
	planOutputs := admission.OutputByInput(identityPlan)

	var qualityDecisions map[string]tags.AdmissionDecision
	rejected := make(map[string]bool)
	if e.config.Enricher.TagQuality.Enabled {
		planned := analysis.Tags
		analysis.Tags, qualityDecisions, err = e.admission.ApplyTagQuality(ctx, admission.TagQualityInput{
			Analysis:    analysis,
			Decisions:   decisions,
			DBNames:     dbNames,
			ReducedText: llmContent.Text,
			DocID:       document.ID,
			ForceDefer:  pendingLanguage,
		}, logId)
		if err != nil {
			if te := e.creditPause(logId, err); te != nil {
				return nil, DocumentCounts{}, te
			}
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("tag quality gate failed: %w", err)}
		}
		kept := make(map[string]bool, len(analysis.Tags))
		for _, name := range analysis.Tags {
			kept[name] = true
		}
		for _, name := range planned {
			if !kept[name] {
				rejected[name] = true
			}
		}
		identityPlan = identityPlan.DropTagNames(rejected)
	}

	// a split degrades to drop only when every part was rejected; when some parts survive the compound
	// keeps whole and applies the survivors alongside, so a partial rejection never destroys the
	// phrase's coverage. The compound is re-registered because the split expansion had
	// already replaced it before identity planning; a kept compound whose part was rejected only drops
	// that part from what it applies alongside
	for i := range compositionRows {
		row := &compositionRows[i]
		row.DocumentID = document.ID
		if row.Disposition == tags.CompositionKeep {
			row.Parts = pruneRejectedParts(row.Parts, planOutputs, rejected)
			continue
		}
		if row.Disposition != tags.CompositionSplit {
			continue
		}
		survived, rejectedParts := 0, 0
		for _, part := range row.Parts {
			if rejected[planOutputName(part, planOutputs)] {
				rejectedParts++
				continue
			}
			survived++
		}
		switch {
		case rejectedParts == 0:
			// the split stands unchanged, but the row records the applied names its components resolve to
			row.Parts = pruneRejectedParts(row.Parts, planOutputs, rejected)
		case survived == 0:
			row.Disposition = tags.CompositionDrop
			row.Reason = tags.CompositionReasonPartRejected
			e.logger.Info(logId, "tag split[%s]: drop %q (reason=part_rejected all parts rejected)",
				analyzer.TagSplitVersion, row.Candidate)
		default:
			row.Disposition = tags.CompositionKeep
			row.Reason = tags.CompositionReasonPartialParts
			row.Parts = pruneRejectedParts(row.Parts, planOutputs, rejected)
			e.applyPartialKeep(&identityPlan, analysis, row.Candidate)
			e.logger.Info(logId, "tag split[%s]: keep %q (reason=%s survivors=%d)",
				analyzer.TagSplitVersion, row.Candidate, tags.CompositionReasonPartialParts, survived)
		}
	}
	identityPlan.Composition = compositionRows

	statsStr := "null"
	if analysis.Stats != nil {
		statsStr = string(*analysis.Stats)
	}
	e.logger.Debug(logId, "prompt: %s", analysis.Prompt)
	e.logger.Info(logId, "analysis result: title=%q type=%q tags=%v people=%v lang=%q stats=%s",
		analysis.Title, analysis.DocType, analysis.Tags, analysis.People, analysis.Language, statsStr)

	analysis.Title = truncate(analysis.Title, 127)

	docTypeMap := make(map[string]int64, len(docTypes))
	for _, dt := range docTypes {
		docTypeMap[dt.Name] = dt.ID
	}
	if _, ok := docTypeMap[analysis.DocType]; !ok {
		analysis.DocType = "undetermined"
	}
	docTypeID := docTypeMap[analysis.DocType]

	report("persist", "", 0)
	if err := e.queries.UpdateDocumentMetadata(ctx, database.UpdateDocumentMetadataParams{
		Title:          analysis.Title,
		DocumentTypeID: docTypeID,
		Language:       analysis.Language,
		DocumentID:     document.DocumentID,
	}); err != nil {
		e.logger.Error(logId, "update document metadata: %w", err)
	}

	e.ensureOCRLanguage(ctx, analysis.Language)

	quarantine := e.config.Enricher.TagQuality.Enabled
	var promotedNames []string

	tx, err := e.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("begin tag transaction: %w", err)}
	}
	defer tx.Rollback()
	txQ := e.client.WithTx(tx)

	results, newNames, err := e.tag.CreatePlannedTx(ctx, txQ, identityPlan, quarantine)
	if err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("batch create tags: %w", err)}
	}

	var tagIDs []int64
	seenTagIDs := make(map[int64]bool, len(results))
	for i, r := range results {
		switch r.Status {
		case types.Created:
			e.logger.Debug(logId, "tag created %s", r.Entity.Name)
			if quarantine {
				e.logger.Info(logId, "tagquality[%s]: quarantine %q (reason=birth)", analyzer.TagQualityVersion, r.Entity.Name)
			}
			if !seenTagIDs[r.Entity.ID] {
				seenTagIDs[r.Entity.ID] = true
				tagIDs = append(tagIDs, r.Entity.ID)
			}
		case types.Conflict, types.Apply, types.Alias:
			if !seenTagIDs[r.Entity.ID] {
				seenTagIDs[r.Entity.ID] = true
				tagIDs = append(tagIDs, r.Entity.ID)
			}
		default:
			e.logger.Error(logId, "create tag %q: invalid", identityPlan.Registrations[i].Input)
		}
	}

	if err := txQ.ClearEnrichDocumentTags(ctx, document.ID); err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("clear document tags: %w", err)}
	}
	for _, tagID := range tagIDs {
		if err := txQ.AddDocumentTag(ctx, database.AddDocumentTagParams{
			DocumentID: document.ID,
			TagID:      tagID,
			Source:     curation.DocumentTagSourceEnrich,
		}); err != nil {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("add document tag: %w", err)}
		}
	}
	if len(tagIDs) > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, tagIDs); err != nil {
			return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("clear orphan tag marks: %w", err)}
		}
	}

	if quarantine {
		promotedNames = e.promoteEligible(ctx, txQ, results, qualityDecisions, logId)
	}

	if err := tx.Commit(); err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("commit tag transaction: %w", err)}
	}

	if len(tagIDs) == 0 {
		e.logger.Warn(logId, "document enriched with %d tags, below min_tags floor %d",
			len(tagIDs), e.config.Enricher.ContentAnalyzer.MinTags)
	}

	if quarantine {
		e.tag.AddToStore(ctx, promotedNames)
	} else {
		e.tag.AddToStore(ctx, newNames)
	}
	e.tag.RemoveFromStore(ctx, identityPlan.RemovedStoreNames())

	peopleMap := make(map[string]int64, len(existingPeople))
	for _, p := range existingPeople {
		peopleMap[normalizedPeopleKey(p)] = p.ID
	}
	peopleTypeMap := make(map[string]int64, len(peopleTypes))
	for _, pt := range peopleTypes {
		peopleTypeMap[pt.Name] = pt.ID
	}

	type docPerson struct {
		peopleID int64
		typeID   int64
	}

	var docPeople []docPerson
	for _, p := range analysis.People {
		canonicalName, nameNative := canonicalPersonName(p)
		if canonicalName == "" && p.NameRomanized != "" {
			canonicalName = p.NameRomanized
			nameNative = ""
		}
		if canonicalName == "" {
			continue
		}

		normalized := textnorm.NormalizeForDB(canonicalName)
		id, ok := peopleMap[normalized]
		if !ok {
			var nameNativeArg sql.NullString
			if nameNative != "" {
				nameNativeArg = sql.NullString{String: nameNative, Valid: true}
			}
			var err error
			id, err = e.queries.CreatePeople(ctx, database.CreatePeopleParams{
				Name:           canonicalName,
				NameNative:     nameNativeArg,
				NormalizedName: normalized,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					existing, err := e.queries.GetPeopleByNormalizedName(ctx, normalized)
					if err != nil {
						e.logger.Error(logId, "get people by normalized name after conflict %q: %v", canonicalName, err)
						continue
					}
					id = existing.ID
					if nameNative != "" {
						if err := e.queries.UpdatePeopleNative(ctx, database.UpdatePeopleNativeParams{
							NameNative: sql.NullString{String: nameNative, Valid: true},
							ID:         id,
						}); err != nil {
							e.logger.Debug(logId, "update people native name %q: %v", nameNative, err)
						}
					}
				} else {
					e.logger.Error(logId, "create people %q: %v", canonicalName, err)
					continue
				}
			} else {
				e.logger.Debug(logId, "people created %s", canonicalName)
			}
			peopleMap[normalized] = id
		} else if nameNative != "" {
			if err := e.queries.UpdatePeopleNative(ctx, database.UpdatePeopleNativeParams{
				NameNative: sql.NullString{String: nameNative, Valid: true},
				ID:         id,
			}); err != nil {
				e.logger.Debug(logId, "update people native name %q: %v", nameNative, err)
			}
		}

		typeName := p.Type
		if _, ok := peopleTypeMap[typeName]; !ok {
			typeName = "unknown"
		}
		docPeople = append(docPeople, docPerson{peopleID: id, typeID: peopleTypeMap[typeName]})
	}

	if err := e.queries.ClearDocumentPeople(ctx, document.ID); err != nil {
		return nil, DocumentCounts{}, &task.Error{ReqID: logId, Err: fmt.Errorf("clear document people: %w", err)}
	}

	for _, dp := range docPeople {
		if err := e.queries.AddDocumentPeople(ctx, database.AddDocumentPeopleParams{
			DocumentID:   document.ID,
			PeopleID:     dp.peopleID,
			PeopleTypeID: dp.typeID,
		}); err != nil {
			e.logger.Error(logId, "add document people: %w", err)
		}
	}

	counts := documentCounts(results, tagIDs, promotedNames, qualityDecisions, rejected, compositionRows)
	if analysis.Stats == nil {
		emptyStats := json.RawMessage("{}")
		return &emptyStats, counts, nil
	}
	return analysis.Stats, counts, nil
}

// creditPause converts a provider credit-exhaustion failure into the batch
// pause task error governed by the content analyzer's pause flag; every
// other error yields nil.
func (e *Enricher) creditPause(logId string, err error) *task.Error {
	var credErr *analyzer.InsufficientCreditsError
	if !errors.As(err, &credErr) {
		return nil
	}
	return &task.Error{
		ReqID:      logId,
		Err:        fmt.Errorf("LLM credit exhausted (%s): %w", credErr.Provider, credErr),
		PauseBatch: e.config.Enricher.ContentAnalyzer.PauseOnCreditError,
	}
}

// documentCounts tallies one document's persist results plus the quality
// gate's rejection set and deferral decisions into the counts the task
// result carries. rejected and decisions are empty when the quality layer
// is off, leaving their counts zero.
func documentCounts(results []types.CreateResult[database.Tag], tagIDs []int64, promotedNames []string, decisions map[string]tags.AdmissionDecision, rejected map[string]bool, rows []tags.CompositionRow) DocumentCounts {
	counts := DocumentCounts{
		Assignments: len(tagIDs),
		Promotions:  len(promotedNames),
		Rejects:     len(rejected),
	}
	for _, r := range results {
		if r.Status == types.Created {
			counts.Births++
		}
	}
	for _, d := range decisions {
		if d.Outcome == tags.AdmissionDefer {
			counts.Defers++
		}
	}
	for _, row := range rows {
		switch row.Disposition {
		case tags.CompositionSplit:
			counts.Splits++
		case tags.CompositionKeep:
			counts.Keeps++
		case tags.CompositionDrop:
			counts.Drops++
		}
	}
	return counts
}

func (e *Enricher) promoteEligible(ctx context.Context, q *database.Queries, results []types.CreateResult[database.Tag], decisions map[string]tags.AdmissionDecision, reqID string) []string {
	type candidate struct {
		name string
		id   int64
	}
	var candidates []candidate
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.Status != types.Created && r.Status != types.Conflict {
			continue
		}
		if r.Entity.PromotedAt.Valid || seen[r.Entity.Name] {
			continue
		}
		seen[r.Entity.Name] = true
		candidates = append(candidates, candidate{name: r.Entity.Name, id: r.Entity.ID})
	}
	if len(candidates) == 0 {
		return nil
	}

	// the emission decided the novel names; a provisional already in the
	// store was skipped there, so its cached verdict row decides it
	decisionByName := make(map[string]tags.AdmissionDecision, len(candidates))
	var undecided []string
	for _, c := range candidates {
		if decision, ok := decisions[c.name]; ok {
			decisionByName[c.name] = decision
			continue
		}
		undecided = append(undecided, c.name)
	}
	if len(undecided) > 0 {
		rows, err := e.tag.LatestQualityEventsForNames(ctx, undecided)
		if err != nil {
			e.logger.Error(reqID, "tag quality promotion verdict lookup failed: %v", err)
		}
		for _, row := range rows {
			decisionByName[row.TagName] = admission.DecisionFromStored(row.Criteria, row.Pass, tags.AdmissionFeatures{})
		}
	}

	ids := make([]int64, len(candidates))
	names := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.id
		names[i] = c.name
	}
	held, err := tags.IdentityHeldNames(ctx, q, names)
	if err != nil {
		e.logger.Error(reqID, "tag quality promotion identity hold lookup failed: %v", err)
		return nil
	}
	compositionHeld, err := tags.CompositionHeldNames(ctx, q, names, analyzer.TagSplitVersion)
	if err != nil {
		e.logger.Error(reqID, "tag quality promotion composition hold lookup failed: %v", err)
		return nil
	}
	counts, err := tags.NewStore(q).LiveMass(ctx, ids)
	if err != nil {
		e.logger.Error(reqID, "tag quality promotion document count failed: %v", err)
		return nil
	}

	var eligible []candidate
	var promoteIDs []int64
	heldSkips := 0
	for _, c := range candidates {
		decision, ok := decisionByName[c.name]
		if !ok || !tags.DecidePromotion(decision.Outcome, counts[c.id]) {
			continue
		}
		if anchor, ok := held[c.name]; ok {
			e.logger.Debug(reqID, "tagquality[%s]: identity hold %q (anchor=%q) — left for review",
				analyzer.TagQualityVersion, c.name, anchor)
			heldSkips++
			continue
		}
		if reason, ok := compositionHeld[c.name]; ok {
			e.logger.Debug(reqID, "tagquality[%s]: composition hold %q (reason=%s) — left for review",
				analyzer.TagQualityVersion, c.name, reason)
			heldSkips++
			continue
		}
		eligible = append(eligible, c)
		promoteIDs = append(promoteIDs, c.id)
	}
	e.logger.Debug(reqID, "tagquality[%s]: promotion run considered=%d promoted=%d held=%d (rule_set=decision-%s floor=%d)",
		analyzer.TagQualityVersion, len(candidates), len(promoteIDs), heldSkips, tags.DecisionRuleSetVersion, tags.RecurrenceFloor)
	if len(promoteIDs) == 0 {
		return nil
	}

	if _, err := q.PromoteTags(ctx, promoteIDs); err != nil {
		e.logger.Error(reqID, "tag quality promotion failed: %v", err)
		return nil
	}

	promoted := make([]string, len(eligible))
	for i, c := range eligible {
		e.logger.Info(reqID, "tagquality[%s]: promote %q (docs=%d reason=%s rule_set=decision-%s floor=%d)",
			analyzer.TagQualityVersion, c.name, counts[c.id], decisionByName[c.name].Reason, tags.DecisionRuleSetVersion, tags.RecurrenceFloor)
		promoted[i] = c.name
	}
	return promoted
}

// planOutputName resolves a pre-plan part name through the identity plan's output mapping, the recipe the
// quality-gate rejection test reads: the split-degrade check and the keep-row prune must agree on which
// name a rejected part is tested by. A part without a mapping stands as itself.
func planOutputName(part string, outputs map[string]string) string {
	if output, ok := outputs[textnorm.NormalizeTag(part)]; ok {
		return output
	}
	return part
}

// pruneRejectedParts drops from a row the parts the quality gate rejected and records the survivors under
// their resolved applied names (the plan's output mapping), so the composition evidence names what the
// store actually applies — `states` records as `state` — and the compound's rejection test runs
// on the applied name too.
func pruneRejectedParts(parts []string, outputs map[string]string, rejected map[string]bool) []string {
	if len(parts) == 0 {
		return parts
	}
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		applied := planOutputName(part, outputs)
		if rejected[applied] {
			continue
		}
		kept = append(kept, applied)
	}
	return kept
}

// applyPartialKeep re-registers a compound that a partial part rejection kept whole: the split expansion
// replaced it before identity planning, so without a fresh registration the phrase would not be persisted
// even though the row now disposes as a keep. It appends a plain registration the persist step
// resolves against the live store (house form, alias, key equality, blocklist), so no extra store read is
// needed; the compound is appended to the emitted tags under its stored name. The registration carries no
// admission decision, so with the quality gate on the compound is born provisional and the reconcile
// provisional sweep judges it — the sweep, not a second in-run gate call, is the single owner of a
// provisional tag's rubric. A compound the plan already aliases onto another name is skipped whole: the
// alias action moves its documents to the winner, and a fresh registration would resolve onto the row that
// action deletes.
func (e *Enricher) applyPartialKeep(plan *tags.Plan, analysis *tools.ContentAnalysisResult, candidate string) {
	if slices.Contains(analysis.Tags, candidate) {
		return
	}
	name := tags.StoredName(candidate)
	if slices.ContainsFunc(plan.Aliases, func(a tags.AliasAction) bool {
		return a.AliasName == name
	}) {
		return
	}
	plan.Registrations = append(plan.Registrations, tags.Registration{
		Input:       candidate,
		Name:        name,
		Disposition: tags.DispositionRegister,
	})
	if name != "" && !slices.Contains(analysis.Tags, name) {
		analysis.Tags = append(analysis.Tags, name)
	}
}

func normalizedPeopleKey(p database.People) string {
	key := p.NormalizedName
	if key == "" {
		key = textnorm.NormalizeForDB(p.Name)
	}
	return key
}

// canonicalPersonName returns the canonical (Latin-script) name and the
// original non-Latin name (if any) for a person result from the LLM.
// If the LLM provided a romanized name for a non-Latin name, it uses that.
// Otherwise it falls back to anyascii transliteration.
func canonicalPersonName(p analyzer.PeopleResult) (canonical, native string) {
	raw := p.Name
	hasNonLatin := analyzer.ContainsNonLatin(raw)

	if !hasNonLatin {
		return raw, ""
	}

	if p.NameRomanized != "" {
		return p.NameRomanized, raw
	}

	// Fallback: transliterate non-Latin script to ASCII
	romanized := anyascii.Transliterate(raw)
	return romanized, raw
}

func isEmptyAnalysis(a *tools.ContentAnalysisResult) bool {
	return a.Title == "" && a.DocType == "" && len(a.Tags) == 0 && len(a.People) == 0 && a.Language == ""
}

func (e *Enricher) ensureOCRLanguage(ctx context.Context, detectedLang string) {
	lang := strings.ToLower(strings.TrimSpace(detectedLang))
	if lang == "" || lang == "und" || len(lang) != 3 {
		return
	}

	if slices.Contains(e.config.Consumer.OCR.Languages, lang) {
		return
	}

	// For non-gosseract engines, persist unconditionally (no tessdata needed).
	if e.config.Consumer.OCR.Engine != config.OCR.Gosseract {
		e.mu.Lock()
		if slices.Contains(e.config.Consumer.OCR.Languages, lang) {
			e.mu.Unlock()
			return
		}
		e.config.Consumer.OCR.Languages = append(e.config.Consumer.OCR.Languages, lang)
		if err := config.SaveMap(e.config.App.ConfigDir, map[string]any{
			"consumer.ocr.languages": e.config.Consumer.OCR.Languages,
		}); err != nil {
			e.logger.Error("", "auto-detect OCR language: failed to add %q to config: %v", lang, err)
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		e.logger.Info("", "auto-detected OCR language %q added to consumer.ocr.languages", lang)
		return
	}

	// Gosseract: download tessdata first, persist only on success.
	go func() {
		if err := config.DownloadTessdataLanguage(context.Background(), e.config, lang); err != nil {
			e.logger.Error("", "auto-detect OCR language: tessdata download for %q failed: %v", lang, err)
			return
		}

		e.mu.Lock()
		if slices.Contains(e.config.Consumer.OCR.Languages, lang) {
			e.mu.Unlock()
			return
		}
		e.config.Consumer.OCR.Languages = append(e.config.Consumer.OCR.Languages, lang)
		if err := config.SaveMap(e.config.App.ConfigDir, map[string]any{
			"consumer.ocr.languages": e.config.Consumer.OCR.Languages,
		}); err != nil {
			e.logger.Error("", "auto-detect OCR language: failed to persist %q after download: %v", lang, err)
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		e.logger.Info("", "auto-detected OCR language %q added to consumer.ocr.languages", lang)
	}()
}

// truncate caps s at maxLength runes, trimming trailing whitespace; a
// blank s becomes "Unknown" rather than an empty title.
func truncate(s string, maxLength int) string {
	defaultString := "Unknown"

	if strings.ReplaceAll(s, " ", "") == "" {
		return defaultString
	}

	runes := []rune(s)
	if len(runes) <= maxLength {
		return s
	}

	trunc := string(runes[:maxLength])
	trunc = strings.TrimRight(trunc, " \t\n\r")
	return trunc
}
