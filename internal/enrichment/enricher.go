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
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/tools"
)

const (
	tokenBudgetRatio   = 0.9
	minTargetWords     = 100
	fallbackVocabLimit = 200
)

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

func NewEnricher(cfg *config.Config, logger *log.Logger, client *database.Client, tag *curation.Tag, matcher matcher.Matcher) (*Enricher, error) {
	runner := tools.NewRunnerWithMatcher(logger, cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcher)
	e := &Enricher{
		config:    cfg,
		logger:    logger,
		client:    client,
		queries:   client.Queries,
		tag:       tag,
		runner:    runner,
		admission: admission.NewAdjudicator(cfg, logger, client.Queries, runner),
	}
	return e, nil
}

func (e *Enricher) Enrich(ctx context.Context, document database.Document, progress ...task.ProgressFunc) (*json.RawMessage, error) {
	logId := document.DocumentID
	ctx = context.WithValue(ctx, "reqid", logId)

	report := task.Progress(progress)

	start := time.Now()
	e.logger.Info(&logId, "starting enrichment for file %s", document.StoragePath)

	defer func() {
		elapsed := time.Since(start)
		e.logger.Info(&logId, "finished enrichment %s in %s", document.StoragePath, log.HumanDuration(elapsed))
	}()

	chunkSize := 150

	report("reduce", "", 0)
	llmContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		matcher.TargetWordCount(int(document.WordCount), e.config.Enricher.TextReducer.TargetWords))
	if err != nil {
		e.logger.Error(&logId, "llm text reduction failed, using raw text: %w", err)
	} else {
		if int64(document.WordCount) > int64(llmContent.TargetWordCount) {
			e.logger.Info(&logId, "long path selected for llm text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, llmContent.CharCount, document.WordCount, llmContent.WordCount, llmContent.TargetWordCount)
		}
	}

	tagsContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		matcher.TargetWordCount(int(document.WordCount), e.config.Enricher.TagMatcher.ReduceTargetWords))
	if err != nil {
		e.logger.Error(&logId, "tag text reduction failed, using raw text: %w", err)
	} else {
		if int64(document.WordCount) > int64(tagsContent.TargetWordCount) {
			e.logger.Info(&logId, "long path selected for tag text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, tagsContent.CharCount, document.WordCount, tagsContent.WordCount, tagsContent.TargetWordCount)
		}
	}

	docTypes, err := e.queries.ListAllDocumentTypes(ctx)
	if err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve document types: %w", err)}
	}
	peopleTypes, err := e.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve people types: %w", err)}
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
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve tags: %w", err)}
	}
	deletedTags, err := e.queries.ListAllDeletedTags(ctx)
	if err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve deleted tags: %w", err)}
	}
	blocklist := make(map[string]database.ListAllDeletedTagsRow, len(deletedTags))
	for _, row := range deletedTags {
		blocklist[row.Name] = row
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

	matchTagsStart := time.Now()
	report("tag-match", "", 0)
	matchedTags, err := e.runner.MatchTags(ctx, document.DocumentID, tagsContent.Text)
	if err != nil || len(matchedTags.Tags) == 0 {
		if err != nil {
			e.logger.Error(&logId, "tag matching failed: %v", err)
		} else {
			e.logger.Debug(&logId, "tag matching returned no matches")
		}
		if len(tagsNames) <= fallbackVocabLimit {
			tagSuggestions = tagsNames
			e.logger.Info(&logId, "using all %d tags as suggestions (vocabulary within fallback limit)", len(tagsNames))
		} else {
			e.logger.Info(&logId, "no tag suggestions (vocabulary of %d tags exceeds fallback limit)", len(tagsNames))
		}
	} else {
		tagSuggestions = matchedTags.Tags
		e.logger.Debug(&logId, "tag matching: %d tags (%s)", len(tagSuggestions), time.Since(matchTagsStart))
	}
	tagSuggestions = e.admission.CanonicalSuggestions(ctx, tagSuggestions, &logId)

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
			pauseBatch := e.config.Enricher.ContentAnalyzer.PauseOnCreditError
			return nil, &task.Error{
				ReqID:      logId,
				Err:        fmt.Errorf("LLM credit exhausted (%s): %w", credErr.Provider, credErr),
				PauseBatch: pauseBatch,
			}
		default:
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to analyze content: %w", err)}
		}

		if actualTokens <= 0 {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to analyze content: %w", err)}
		}

		ratio := float64(maxTokens) / float64(actualTokens) * tokenBudgetRatio
		newTarget := int(float64(llmContent.TargetWordCount) * ratio)
		if i == 1 || newTarget < minTargetWords {
			return nil, &task.Error{
				ReqID: logId,
				Err: fmt.Errorf("document too large for model %s/%s: %d tokens exceeds budget (max_input_tokens=%d)",
					e.config.Enricher.ContentAnalyzer.Llm.Provider, e.config.Enricher.ContentAnalyzer.Llm.Model, actualTokens, maxTokens),
			}
		}
		reduced, rerr := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize, newTarget)
		if rerr != nil {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to reduce content: %w", rerr)}
		}
		llmContent = reduced
	}

	if isEmptyAnalysis(analysis) {
		e.logger.Info(&logId, "empty analysis result—retrying once")
		analysis, err = e.runner.AnalyzeContent(ctx, llmContent.Text, docTypeRefs, peopleTypeRefs, tagSuggestions)
		if err != nil {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to analyze content on retry: %w", err)}
		}
		if isEmptyAnalysis(analysis) {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("analysis returned empty result after retry")}
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
			e.logger.Error(&logId, "doc type refinement failed, keeping first pass result: %v", err)
		} else {
			e.logger.Info(&logId, "doc type refined: %q → %q", analysis.DocType, refinedType)
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
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("list existing people: %w", err)}
	}
	knownNormalized := make([]string, 0, len(existingPeople))
	for _, p := range existingPeople {
		knownNormalized = append(knownNormalized, normalizedPeopleKey(p))
	}
	kept, facetDrops := analyzer.SeparateFacets(analysis.Tags, analysis.TagDetails, docTypeNames)
	for _, drop := range facetDrops {
		e.logger.Info(&logId, "tags[facet]: drop %q (category=%s reason=%s)", drop.Term, drop.Category, drop.Reason)
	}
	analysis.Tags = kept

	analysis.Tags = analyzer.FilterTags(analysis.Tags, analysis.People, knownNormalized, analysis.Title, docTypeNames, e.config.Enricher.ContentAnalyzer.MaxTags)
	tagFilter := analyzer.NewTagFilter(analysis.People, knownNormalized, analysis.Title, docTypeNames)
	analysis.Tags = e.admission.SplitJointTags(ctx, analysis.Tags, tagFilter, &logId)
	analysis.Tags = e.admission.ApplyDeletedTagPolicy(analysis.Tags, dbNames, blocklist, &logId)

	consolidateStart := time.Now()
	report("consolidate", "", 0)
	ranked, err := e.runner.RankTags(ctx, document.DocumentID, analysis.Tags)
	var decisions []policy.Decision
	var identityPlan tags.Plan
	if err != nil {
		e.logger.Error(&logId, "post-LLM consolidation failed: %v", err)
		for _, tag := range analysis.Tags {
			e.logger.Info(&logId, "consolidation[%s]: keep %q (reason=rank_unavailable)", policy.Version, tag)
		}
		identityPlan, err = e.tag.PlanRegistration(ctx, analysis.Tags)
		if err != nil {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("identity planning failed: %w", err)}
		}
	} else {
		var consolidated []string
		consolidated, identityPlan, decisions = e.admission.PlanIdentity(ctx, document.ID, document.DocumentID, analysis.Tags, ranked, dbNames, &logId)
		analysis.Tags = consolidated
		e.logger.Debug(&logId, "post-LLM consolidation: %d tags (%s)", len(consolidated), time.Since(consolidateStart))
	}
	analysis.TagDetails = admission.RenameTagDetails(analysis.TagDetails, identityPlan)

	if e.config.Enricher.TagQuality.Enabled {
		analysis.Tags = e.admission.ApplyTagQuality(ctx, analysis, decisions, dbNames, llmContent.Text, document.ID, &logId)
	}

	statsStr := "null"
	if analysis.Stats != nil {
		statsStr = string(*analysis.Stats)
	}
	e.logger.Debug(&logId, "prompt: %s", analysis.Prompt)
	e.logger.Info(&logId, "analysis result: title=%q type=%q tags=%v people=%v lang=%q stats=%s",
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
		e.logger.Error(&logId, "update document metadata: %w", err)
	}

	e.ensureOCRLanguage(ctx, analysis.Language)

	quarantine := e.config.Enricher.TagQuality.Enabled
	var promotedNames []string

	tx, err := e.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("begin tag transaction: %w", err)}
	}
	defer tx.Rollback()
	txQ := e.client.WithTx(tx)

	results, newNames, err := e.tag.CreatePlannedTx(ctx, txQ, identityPlan, quarantine)
	if err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("batch create tags: %w", err)}
	}

	var tagIDs []int64
	seenTagIDs := make(map[int64]bool, len(results))
	for i, r := range results {
		switch r.Status {
		case service.Created:
			e.logger.Debug(&logId, "tag created %s", r.Entity.Name)
			if quarantine {
				e.logger.Info(&logId, "tagquality[%s]: quarantine %q (reason=birth)", analyzer.TagQualityVersion, r.Entity.Name)
			}
			if !seenTagIDs[r.Entity.ID] {
				seenTagIDs[r.Entity.ID] = true
				tagIDs = append(tagIDs, r.Entity.ID)
			}
		case service.Conflict, service.Apply, service.Alias:
			if !seenTagIDs[r.Entity.ID] {
				seenTagIDs[r.Entity.ID] = true
				tagIDs = append(tagIDs, r.Entity.ID)
			}
		default:
			e.logger.Error(&logId, "create tag %q: invalid", identityPlan.Registrations[i].Input)
		}
	}

	if err := txQ.ClearEnrichDocumentTags(ctx, document.ID); err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("clear document tags: %w", err)}
	}
	for _, tagID := range tagIDs {
		if err := txQ.AddDocumentTag(ctx, database.AddDocumentTagParams{
			DocumentID: document.ID,
			TagID:      tagID,
			Source:     curation.DocumentTagSourceEnrich,
		}); err != nil {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("add document tag: %w", err)}
		}
	}
	if len(tagIDs) > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, tagIDs); err != nil {
			return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("clear orphan tag marks: %w", err)}
		}
	}

	if quarantine {
		promotedNames = e.promoteEligible(ctx, txQ, results, &logId)
	}

	if err := tx.Commit(); err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("commit tag transaction: %w", err)}
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
						e.logger.Error(&logId, "get people by normalized name after conflict %q: %v", canonicalName, err)
						continue
					}
					id = existing.ID
					if nameNative != "" {
						if err := e.queries.UpdatePeopleNative(ctx, database.UpdatePeopleNativeParams{
							NameNative: sql.NullString{String: nameNative, Valid: true},
							ID:         id,
						}); err != nil {
							e.logger.Debug(&logId, "update people native name %q: %v", nameNative, err)
						}
					}
				} else {
					e.logger.Error(&logId, "create people %q: %v", canonicalName, err)
					continue
				}
			} else {
				e.logger.Debug(&logId, "people created %s", canonicalName)
			}
			peopleMap[normalized] = id
		} else if nameNative != "" {
			if err := e.queries.UpdatePeopleNative(ctx, database.UpdatePeopleNativeParams{
				NameNative: sql.NullString{String: nameNative, Valid: true},
				ID:         id,
			}); err != nil {
				e.logger.Debug(&logId, "update people native name %q: %v", nameNative, err)
			}
		}

		typeName := p.Type
		if _, ok := peopleTypeMap[typeName]; !ok {
			typeName = "unknown"
		}
		docPeople = append(docPeople, docPerson{peopleID: id, typeID: peopleTypeMap[typeName]})
	}

	if err := e.queries.ClearDocumentPeople(ctx, document.ID); err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("clear document people: %w", err)}
	}

	for _, dp := range docPeople {
		if err := e.queries.AddDocumentPeople(ctx, database.AddDocumentPeopleParams{
			DocumentID:   document.ID,
			PeopleID:     dp.peopleID,
			PeopleTypeID: dp.typeID,
		}); err != nil {
			e.logger.Error(&logId, "add document people: %w", err)
		}
	}

	if analysis.Stats == nil {
		emptyStats := json.RawMessage("{}")
		return &emptyStats, nil
	}
	return analysis.Stats, nil
}

func (e *Enricher) promoteEligible(ctx context.Context, q *database.Queries, results []service.CreateResult[database.Tag], logId *string) []string {
	provisional := make(map[string]int64, len(results))
	for _, r := range results {
		if r.Status != service.Created && r.Status != service.Conflict {
			continue
		}
		if r.Entity.PromotedAt.Valid {
			continue
		}
		provisional[r.Entity.Name] = r.Entity.ID
	}
	if len(provisional) == 0 {
		return nil
	}

	names := make([]string, 0, len(provisional))
	for name := range provisional {
		names = append(names, name)
	}
	rows, err := q.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: analyzer.TagQualityVersion,
		TagNames:      names,
	})
	if err != nil {
		e.logger.Error(logId, "tag quality promotion verdict lookup failed: %v", err)
		return nil
	}

	type passRow struct {
		name  string
		id    int64
		model string
	}
	var passed []passRow
	for _, row := range rows {
		if !row.Pass {
			continue
		}
		id, ok := provisional[row.TagName]
		if !ok {
			continue
		}
		passed = append(passed, passRow{name: row.TagName, id: id, model: row.VerdictModel.String})
	}
	if len(passed) == 0 {
		return nil
	}

	ids := make([]int64, len(passed))
	for i, p := range passed {
		ids[i] = p.id
	}
	counts, err := tags.NewStore(q).LiveMass(ctx, ids)
	if err != nil {
		e.logger.Error(logId, "tag quality promotion document count failed: %v", err)
		return nil
	}

	var eligible []passRow
	var promoteIDs []int64
	for _, p := range passed {
		if counts[p.id] < 2 {
			continue
		}
		eligible = append(eligible, p)
		promoteIDs = append(promoteIDs, p.id)
	}
	if len(promoteIDs) == 0 {
		return nil
	}

	if _, err := q.PromoteTags(ctx, promoteIDs); err != nil {
		e.logger.Error(logId, "tag quality promotion failed: %v", err)
		return nil
	}

	promoted := make([]string, len(eligible))
	for i, p := range eligible {
		model := p.model
		if model == "" {
			model = "cached"
		}
		e.logger.Info(logId, "tagquality[%s]: promote %q (docs=%d model=%s)", analyzer.TagQualityVersion, p.name, counts[p.id], model)
		promoted[i] = p.name
	}
	return promoted
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
			e.logger.Error(nil, "auto-detect OCR language: failed to add %q to config: %v", lang, err)
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		e.logger.Info(nil, "auto-detected OCR language %q added to consumer.ocr.languages", lang)
		return
	}

	// Gosseract: download tessdata first, persist only on success.
	go func() {
		if err := config.DownloadTessdataLanguage(context.Background(), e.config, lang); err != nil {
			e.logger.Error(nil, "auto-detect OCR language: tessdata download for %q failed: %v", lang, err)
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
			e.logger.Error(nil, "auto-detect OCR language: failed to persist %q after download: %v", lang, err)
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		e.logger.Info(nil, "auto-detected OCR language %q added to consumer.ocr.languages", lang)
	}()
}
