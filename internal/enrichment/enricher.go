package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	anyascii "github.com/anyascii/go"
	types "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Enricher struct {
	config   *config.Config
	logger   *utils.Logger
	db       *sql.DB
	runner   *tools.Runner
	services *types.CrudServices
}

func NewEnricher(cfg *config.Config, logger *utils.Logger, db *sql.DB, services *types.CrudServices, matcher tagmatcher.Matcher) (*Enricher, error) {
	e := &Enricher{
		config:   cfg,
		logger:   logger,
		db:       db,
		services: services,
		runner:   tools.NewRunnerWithMatcher(logger, cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcher),
	}
	return e, nil
}

func (e *Enricher) Enrich(ctx context.Context, document database.Document) (*json.RawMessage, error) {
	logId := document.DocumentID

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
		if document.WordCount > int64(llmContent.TargetWordCount) {
			e.logger.Info(&logId, "long path selected for llm text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, llmContent.CharCount, document.WordCount, llmContent.WordCount, llmContent.TargetWordCount)
		}
	}

	tagsContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		targetWordCount(int(document.WordCount), e.config.Enricher.TagMatcher.ReduceTargetWords))
	if err != nil {
		e.logger.Error(&logId, "tag text reduction failed, using raw text: %w", err)
	} else {
		if document.WordCount > int64(tagsContent.TargetWordCount) {
			e.logger.Info(&logId, "long path selected for tag text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, tagsContent.CharCount, document.WordCount, tagsContent.WordCount, tagsContent.TargetWordCount)
		}
	}

	queries := database.NewQueries(e.db)

	docTypes, err := queries.ListAllDocumentTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve document types: %w", err)
	}
	peopleTypes, err := queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve people types: %w", err)
	}
	allTags, err := e.services.Tag.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve tags: %w", err)
	}

	var tagSuggestions []string

	var tagsNames []string
	for _, t := range allTags {
		tagsNames = append(tagsNames, t.Name)
	}

	matchTagsStart := time.Now()
	matchedTags, err := e.runner.MatchTags(ctx, document.DocumentID, tagsContent.Text, tagsNames)
	if err != nil {
		e.logger.Error(&logId, "tag matching failed, using all tags: %v", err)
		tagSuggestions = tagsNames
	} else {
		tagSuggestions = matchedTags.Tags
		e.logger.Debug(&logId, "tag matching: %d tags (%s)", len(tagSuggestions), time.Since(matchTagsStart))
	}

	analysis, err := e.runner.AnalyzeContent(ctx, llmContent.Text, docTypes, peopleTypes, tagSuggestions)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze content: %w", err)
	}

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

	docTypeMap := make(map[string]int64, len(docTypes))
	for _, dt := range docTypes {
		docTypeMap[dt.Name] = dt.ID
	}
	if _, ok := docTypeMap[analysis.DocType]; !ok {
		analysis.DocType = "undetermined"
	}
	docTypeID := docTypeMap[analysis.DocType]

	if err := queries.UpdateDocumentMetadata(ctx, database.UpdateDocumentMetadataParams{
		Title:          analysis.Title,
		DocumentTypeID: docTypeID,
		Language:       analysis.Language,
		DocumentID:     document.DocumentID,
	}); err != nil {
		e.logger.Error(&logId, "update document metadata: %w", err)
	}

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

	if err := queries.ClearDocumentTags(ctx, document.ID); err != nil {
		return nil, fmt.Errorf("clear document tags: %w", err)
	}
	for _, tagID := range tagIDs {
		if err := queries.AddDocumentTag(ctx, database.AddDocumentTagParams{
			DocumentID: document.ID,
			TagID:      tagID,
		}); err != nil {
			e.logger.Error(&logId, "add document tag: %w", err)
		}
	}

	existingPeople, err := queries.ListAllPeople(ctx)
	if err != nil {
		return nil, fmt.Errorf("list existing people: %w", err)
	}
	peopleMap := make(map[string]int64, len(existingPeople))
	for _, p := range existingPeople {
		key := utils.NormalizeName(p.Name)
		peopleMap[key] = p.ID
	}
	var peopleIDs []int64
	for _, p := range analysis.People {
		canonicalName, nameNative := canonicalPersonName(p)

		normalized := utils.NormalizeName(canonicalName)
		id, ok := peopleMap[normalized]
		if !ok {
			var nameNativeArg sql.NullString
			if nameNative != "" {
				nameNativeArg = sql.NullString{String: nameNative, Valid: true}
			}
			result, err := queries.CreatePeople(ctx, database.CreatePeopleParams{
				Name:       canonicalName,
				NameNative: nameNativeArg,
			})
			if err != nil {
				e.logger.Error(&logId, "create people %q: %v", canonicalName, err)
				continue
			}
			e.logger.Debug(&logId, "people created %s", canonicalName)
			id, _ = result.LastInsertId()
			peopleMap[normalized] = id
		} else if nameNative != "" {
			if err := queries.UpdatePeopleNative(ctx, database.UpdatePeopleNativeParams{
				NameNative: sql.NullString{String: nameNative, Valid: true},
				ID:         id,
			}); err != nil {
				e.logger.Debug(&logId, "update people native name %q: %v", nameNative, err)
			}
		}
		peopleIDs = append(peopleIDs, id)
	}

	if err := queries.ClearDocumentPeople(ctx, document.ID); err != nil {
		return nil, fmt.Errorf("clear document people: %w", err)
	}

	peopleTypeMap := make(map[string]int64, len(peopleTypes))
	for _, pt := range peopleTypes {
		peopleTypeMap[pt.Name] = pt.ID
	}
	for i, p := range analysis.People {
		if _, ok := peopleTypeMap[p.Type]; !ok {
			analysis.People[i].Type = "unknown"
		}
	}
	for i, peopleID := range peopleIDs {
		typeName := analysis.People[i].Type
		typeID := peopleTypeMap[typeName]
		if err := queries.AddDocumentPeople(ctx, database.AddDocumentPeopleParams{
			DocumentID:   document.ID,
			PeopleID:     peopleID,
			PeopleTypeID: typeID,
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

func (e *Enricher) GetDb() *sql.DB {
	return e.db
}

func targetWordCount(contentWC, targetWC int) int {
	result := targetWC
	if result < 0 {
		result = contentWC / -result
	}
	return max(2000, result)
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
