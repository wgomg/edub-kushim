package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/cache"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Enricher struct {
	config *config.Config
	logger *utils.Logger
	db     *sql.DB
	runner *tools.Runner
	cache  *cache.Cache
}

func NewEnricher(cfg *config.Config, logger *utils.Logger, db *sql.DB, embeddingCache *cache.Cache) (*Enricher, error) {
	e := &Enricher{
		config: cfg,
		logger: logger,
		db:     db,
		cache:  embeddingCache,
		runner: tools.NewRunner(logger, cfg, []string{"textreducer", "contentanalyzer", "tagmatcher"}),
	}
	return e, nil
}

func (e *Enricher) Enrich(ctx context.Context, document database.Document) (*json.RawMessage, error) {
	start := time.Now()
	e.logger.Info(nil, "starting enrichment for file %s", document.StoragePath)

	defer func() {
		elapsed := time.Since(start)
		e.logger.Info(nil, "finished enrichment %s in %s", document.StoragePath, utils.HumanDuration(elapsed))
	}()

	chunkSize := 150

	llmContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		targetWordCount(int(document.WordCount), e.config.Enricher.TextReducer.TargetWords))
	if err != nil {
		e.logger.Error(nil, "llm text reduction failed, using raw text: %w", err)
	} else {
		if document.WordCount > int64(llmContent.TargetWordCount) {
			e.logger.Info(nil, "long path selected for llm text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
				document.CharCount, llmContent.CharCount, document.WordCount, llmContent.WordCount, llmContent.TargetWordCount)
		}
	}

	tagsContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, chunkSize,
		targetWordCount(int(document.WordCount), e.config.Enricher.TagMatcher.ReduceTargetWords))
	if err != nil {
		e.logger.Error(nil, "tag text reduction failed, using raw text: %w", err)
	} else {
		if document.WordCount > int64(tagsContent.TargetWordCount) {
			e.logger.Info(nil, "long path selected for tag text reduction: document_length=(%d -> %d),  document_word_count=(%d -> %d), target_word_count=%d",
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
	allTags, err := queries.ListAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve tags: %w", err)
	}

	var tagSuggestions []string
	store, ok := e.cache.Get("tags")
	if !ok {
		e.logger.Error(nil, "get tags cache: store not found")
	}
	embStore, ok := store.(*cache.EmbeddingStore)
	if !ok {
		e.logger.Error(nil, "get tags cache: unexpected store type")
	}
	tagsToMatch := embStore.Entries()
	e.logger.Debug(nil, "tags store cache length: %d", len(tagsToMatch))

	var tagsNames []string
	for _, t := range allTags {
		tagsNames = append(tagsNames, t.Name)
	}

	matchTagsStart := time.Now()
	matchedTags, err := e.runner.MatchTags(ctx, tagsContent.Text, tagsToMatch)
	if err != nil {
		e.logger.Error(nil, "tag matching failed, using all tags: %v", err)
		tagSuggestions = tagsNames
	} else {
		tagSuggestions = matchedTags.Tags
		e.logger.Debug(nil, "tag matching: %d tags (%s)", len(tagSuggestions), time.Since(matchTagsStart))
	}

	analysis, err := e.runner.AnalyzeContent(ctx, llmContent.Text, docTypes, peopleTypes, tagSuggestions)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze content: %w", err)
	}

	consolidateStart := time.Now()
	consolidated, err := e.runner.MatchEach(ctx, analysis.Tags, tagsToMatch)
	if err != nil {
		e.logger.Error(nil, "post-LLM consolidation failed: %v", err)
	} else {
		analysis.Tags = consolidated.Tags
		e.logger.Debug(nil, "post-LLM consolidation: %d tags (%s)", len(consolidated.Tags), time.Since(consolidateStart))
	}

	statsStr := "null"
	if analysis.Stats != nil {
		statsStr = string(*analysis.Stats)
	}
	e.logger.Debug(nil, "prompt: %s", analysis.Prompt)
	e.logger.Info(nil, "analysis result: title=%q type=%q tags=%v people=%v lang=%q stats=%s",
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
		ID:             document.ID,
	}); err != nil {
		e.logger.Error(nil, "update document metadata: %w", err)
	}

	tagMap := make(map[string]int64, len(allTags))
	for _, t := range allTags {
		tagMap[t.Name] = t.ID
	}
	var tagIDs []int64
	var newTags []string
	for _, tagName := range analysis.Tags {
		id, ok := tagMap[tagName]
		if !ok {
			result, err := queries.CreateTag(ctx, tagName)
			if err != nil {
				e.logger.Error(nil, "create tag %q: %v", tagName, err)
				continue
			}
			e.logger.Debug(nil, "tag created %s", tagName)
			id, _ = result.LastInsertId()
			tagMap[tagName] = id
			newTags = append(newTags, tagName)
		}
		tagIDs = append(tagIDs, id)
	}

	if len(newTags) > 0 {
		newTagsEmbeddings, err := e.runner.EncodeTags(ctx, newTags)
		if err != nil {
			e.logger.Error(nil, "encode new tags for cache: %w", err)
		} else {
			for i, name := range newTags {
				embStore.Add(name, newTagsEmbeddings[i])
			}
			e.logger.Debug(nil, "cached %v new tag embeddings", newTags)
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
			e.logger.Error(nil, "add document tag: %w", err)
		}
	}

	existingPeople, err := queries.ListAllPeople(ctx)
	if err != nil {
		return nil, fmt.Errorf("list existing people: %w", err)
	}
	peopleMap := make(map[string]int64, len(existingPeople))
	for _, p := range existingPeople {
		peopleMap[p.Name] = p.ID
	}
	var peopleIDs []int64
	for _, p := range analysis.People {
		id, ok := peopleMap[p.Name]
		if !ok {
			result, err := queries.CreatePeople(ctx, p.Name)
			if err != nil {
				e.logger.Error(nil, "create people %q: %v", p.Name, err)
				continue
			}
			e.logger.Debug(nil, "people created %s", p.Name)
			id, _ = result.LastInsertId()
			peopleMap[p.Name] = id
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
			e.logger.Error(nil, "add document people: %w", err)
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
