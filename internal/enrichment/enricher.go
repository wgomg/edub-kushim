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
	types "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	tokenBudgetRatio = 0.9
	minTargetWords   = 100
)

type Enricher struct {
	config   *config.Config
	logger   *utils.Logger
	queries  *database.Queries
	runner   *tools.Runner
	services *types.CrudServices
	mu       sync.Mutex
}

func NewEnricher(cfg *config.Config, logger *utils.Logger, queries *database.Queries, services *types.CrudServices, matcher tagmatcher.Matcher) (*Enricher, error) {
	e := &Enricher{
		config:   cfg,
		logger:   logger,
		queries:  queries,
		services: services,
		runner:   tools.NewRunnerWithMatcher(logger, cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcher),
	}
	return e, nil
}

func (e *Enricher) Enrich(ctx context.Context, document database.Document) (*json.RawMessage, error) {
	logId := document.DocumentID
	ctx = context.WithValue(ctx, "reqid", logId)

	start := time.Now()
	e.logger.Info(&logId, "starting enrichment for file %s", document.StoragePath)

	defer func() {
		elapsed := time.Since(start)
		e.logger.Info(&logId, "finished enrichment %s in %s", document.StoragePath, utils.HumanDuration(elapsed))
	}()

	chunkSize := 150

	llmContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		targetWordCount(int(document.WordCount), e.config.Enricher.TextReducer.TargetWords))
	if err != nil {
		e.logger.Error(&logId, "llm text reduction failed, using raw text: %w", err)
	} else {
		if int64(document.WordCount) > int64(llmContent.TargetWordCount) {
			e.logger.Info(&logId, "long path selected for llm text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, llmContent.CharCount, document.WordCount, llmContent.WordCount, llmContent.TargetWordCount)
		}
	}

	tagsContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		targetWordCount(int(document.WordCount), e.config.Enricher.TagMatcher.ReduceTargetWords))
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
	allTags, err := e.services.Tag.ListAll(ctx)
	if err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("failed to retrieve tags: %w", err)}
	}

	var tagSuggestions []string

	var tagsNames []string
	for _, t := range allTags {
		tagsNames = append(tagsNames, t.Name)
	}

	matchTagsStart := time.Now()
	matchedTags, err := e.runner.MatchTags(ctx, document.DocumentID, tagsContent.Text)
	if err != nil || len(matchedTags.Tags) == 0 {
		if err != nil {
			e.logger.Error(&logId, "tag matching failed, using all tags: %v", err)
		} else {
			e.logger.Debug(&logId, "tag matching returned no matches, using all tags")
		}
		tagSuggestions = tagsNames
	} else {
		tagSuggestions = matchedTags.Tags
		e.logger.Debug(&logId, "tag matching: %d tags (%s)", len(tagSuggestions), time.Since(matchTagsStart))
	}

	var analysis *tools.ContentAnalysisResult
	for i := range 2 {
		analysis, err = e.runner.AnalyzeContent(ctx, llmContent.Text, docTypes, peopleTypes, tagSuggestions)
		if err == nil {
			break
		}

		var ctle *contentanalyzer.ContentTooLargeError
		var tokErr *contentanalyzer.TokenLimitError
		var credErr *contentanalyzer.InsufficientCreditsError

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
		analysis, err = e.runner.AnalyzeContent(ctx, llmContent.Text, docTypes, peopleTypes, tagSuggestions)
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
		headTailText := contentanalyzer.ExtractHeadTailWords(
			document.TextContent.String,
			refinementCfg.HeadWords,
			refinementCfg.TailWords,
		)
		metadata := contentanalyzer.DocMetadata{
			WordCount: document.WordCount,
			PageCount: document.PageCount,
			MimeType:  document.OriginalType,
		}
		refinedType, err := e.runner.AnalyzeDocType(ctx, analysis, headTailText, docTypes, metadata)
		if err != nil {
			e.logger.Error(&logId, "doc type refinement failed, keeping first pass result: %v", err)
		} else {
			e.logger.Info(&logId, "doc type refined: %q → %q", analysis.DocType, refinedType)
			if refinedType != "" {
				analysis.DocType = refinedType
			}
		}
	}

	analysis.Tags = contentanalyzer.NormalizeTags(analysis.Tags)

	for i, p := range analysis.People {
		canonicalName, _ := canonicalPersonName(p)
		if canonicalName == "" && p.NameRomanized != "" {
			canonicalName = p.NameRomanized
		}
		analysis.People[i].NormalizedName = utils.NormalizeForDB(canonicalName)
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
	analysis.Tags = contentanalyzer.FilterTags(analysis.Tags, analysis.People, knownNormalized, analysis.Title, docTypeNames)

	consolidateStart := time.Now()
	consolidated, err := e.services.Tag.Consolidate(ctx, document.DocumentID, analysis.Tags)
	if err != nil {
		e.logger.Error(&logId, "post-LLM consolidation failed: %v", err)
	} else {
		analysis.Tags = consolidated
		e.logger.Debug(&logId, "post-LLM consolidation: %d tags (%s)", len(consolidated), time.Since(consolidateStart))
	}

	statsStr := "null"
	if analysis.Stats != nil {
		statsStr = string(*analysis.Stats)
	}
	e.logger.Debug(&logId, "prompt: %s", analysis.Prompt)
	e.logger.Info(&logId, "analysis result: title=%q type=%q tags=%v people=%v lang=%q stats=%s",
		analysis.Title, analysis.DocType, analysis.Tags, analysis.People, analysis.Language, statsStr)

	analysis.Title = utils.Truncate(analysis.Title, 127)

	docTypeMap := make(map[string]int64, len(docTypes))
	for _, dt := range docTypes {
		docTypeMap[dt.Name] = dt.ID
	}
	if _, ok := docTypeMap[analysis.DocType]; !ok {
		analysis.DocType = "undetermined"
	}
	docTypeID := docTypeMap[analysis.DocType]

	if err := e.queries.UpdateDocumentMetadata(ctx, database.UpdateDocumentMetadataParams{
		Title:          analysis.Title,
		DocumentTypeID: docTypeID,
		Language:       analysis.Language,
		DocumentID:     document.DocumentID,
	}); err != nil {
		e.logger.Error(&logId, "update document metadata: %w", err)
	}

	e.ensureOCRLanguage(ctx, analysis.Language)

	results, err := e.services.Tag.Create(ctx, analysis.Tags)
	if err != nil {
		e.logger.Error(&logId, "batch create tags: %v", err)
	}

	var tagIDs []int64
	for i, r := range results {
		switch r.Status {
		case service.Created:
			e.logger.Debug(&logId, "tag created %s", analysis.Tags[i])
			tagIDs = append(tagIDs, r.Entity.ID)
		case service.Conflict:
			tagIDs = append(tagIDs, r.Entity.ID)
		default:
			e.logger.Error(&logId, "create tag %q: invalid", analysis.Tags[i])
		}
	}

	if err := e.queries.ClearDocumentTags(ctx, document.ID); err != nil {
		return nil, &task.Error{ReqID: logId, Err: fmt.Errorf("clear document tags: %w", err)}
	}
	for _, tagID := range tagIDs {
		if err := e.queries.AddDocumentTag(ctx, database.AddDocumentTagParams{
			DocumentID: document.ID,
			TagID:      tagID,
		}); err != nil {
			e.logger.Error(&logId, "add document tag: %w", err)
		}
	}

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

		normalized := utils.NormalizeForDB(canonicalName)
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

func targetWordCount(contentWC, targetWC int) int {
	result := targetWC
	if result < 0 {
		result = contentWC / -result
	}
	return max(2000, result)
}

func normalizedPeopleKey(p database.People) string {
	key := p.NormalizedName
	if key == "" {
		key = utils.NormalizeForDB(p.Name)
	}
	return key
}

// canonicalPersonName returns the canonical (Latin-script) name and the
// original non-Latin name (if any) for a person result from the LLM.
// If the LLM provided a romanized name for a non-Latin name, it uses that.
// Otherwise it falls back to anyascii transliteration.
func canonicalPersonName(p contentanalyzer.PeopleResult) (canonical, native string) {
	raw := p.Name
	hasNonLatin := utils.ContainsNonLatin(raw)

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
