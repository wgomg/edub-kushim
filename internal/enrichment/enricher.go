package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

	documentWordCount := len(strings.Fields(document.TextContent.String))

	textReducerTargetWords := e.config.Enricher.TextReducer.TargetWords
	if textReducerTargetWords < 0 {
		textReducerTargetWords = documentWordCount / -textReducerTargetWords
	}

	tagMatcherReduceTargetWords := e.config.Enricher.TagMatcher.ReduceTargetWords
	if tagMatcherReduceTargetWords < 0 {
		tagMatcherReduceTargetWords = documentWordCount / -tagMatcherReduceTargetWords
	}

	textReducerTargetWords = max(2000, textReducerTargetWords)
	tagMatcherReduceTargetWords = max(2000, tagMatcherReduceTargetWords)

	// estimatedTokens := estimateTokens(document.TextContent.String)
	shouldReduceLlm := shouldReduceContent(documentWordCount, textReducerTargetWords)
	shouldReduceTags := shouldReduceContent(documentWordCount, tagMatcherReduceTargetWords)

	llmContent := document.TextContent.String
	tagsContent := document.TextContent.String

	if shouldReduceLlm {
		e.logger.Info(nil, "long path selected for llm text reduction: document_length=%d,  document_word_count=%d, reducer_target_word_count=%d, should_reduce=%v", utf8.RuneCountInString(llmContent), documentWordCount, textReducerTargetWords, shouldReduceLlm)

		reduceStart := time.Now()
		reducedContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, 150, textReducerTargetWords)
		if err != nil {
			return nil, fmt.Errorf("llm text reduction failed, using raw text: %w", err)
		}
		llmContent = reducedContent.Text
		e.logger.Debug(nil, "llm text reduction: %d length (%s)", utf8.RuneCountInString(llmContent), time.Since(reduceStart))
	}

	if shouldReduceTags {
		e.logger.Info(nil, "long path selected for tags matcher text reduction: document_length=%d, document_word_count=%d, reducer_target_word_count=%d, should_reduce=%v", utf8.RuneCountInString(tagsContent), documentWordCount, tagMatcherReduceTargetWords, shouldReduceTags)

		tagReduceStart := time.Now()
		tagReducedContent, err := e.runner.ReduceContent(ctx, document.TextContent.String, 150, tagMatcherReduceTargetWords)
		if err != nil {
			e.logger.Error(nil, "tag text reduction failed, using raw text: %v", err)
		} else {
			tagsContent = tagReducedContent.Text
			e.logger.Debug(nil, "tag text reduction: %d length (%s)", utf8.RuneCountInString(tagsContent), time.Since(tagReduceStart))
		}
	}

	queries := database.NewQueries(e.db)

	docTypes, err := queries.ListAllDocumentTypesNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve document types: %w", err)
	}

	allTags, err := queries.ListAllTagsNames(ctx)
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

	matchTagsStart := time.Now()
	matchedTags, err := e.runner.MatchTags(ctx, tagsContent, tagsToMatch)
	if err != nil {
		e.logger.Error(nil, "tag matching failed, using all tags: %v", err)
		tagSuggestions = allTags
	} else {
		tagSuggestions = matchedTags.Tags
		e.logger.Debug(nil, "tag matching: %d tags (%s)", len(tagSuggestions), time.Since(matchTagsStart))
	}

	analysis, err := e.runner.AnalyzeContent(ctx, llmContent, docTypes, tagSuggestions)
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
	e.logger.Debug(nil, "analysis result: title=%q type=%q tags=%v authors=%v lang=%q stats=%s",
		analysis.Title, analysis.DocType, analysis.Tags, analysis.Authors, analysis.Language, statsStr)
	e.logger.Debug(nil, "prompt: %s", analysis.Prompt)

	// save analysis results
	// update cache

	if analysis.Stats == nil {
		emptyStats := json.RawMessage("{}")
		return &emptyStats, nil
	}
	return analysis.Stats, nil
}

func (e *Enricher) GetDb() *sql.DB {
	return e.db
}

func shouldReduceContent(wordCount int, targetWordCount int) bool {
	return wordCount > targetWordCount
}

// func shouldReduceContent(estimatedTokens int, thresholdTokens int) bool {
// 	return estimatedTokens > thresholdTokens
// }

// func estimateTokens(content string) int {
// 	cleanedUpContent := utils.CleanUp(content)

// 	wordCount := utils.CountWords(cleanedUpContent)
// 	estimatedTokens := utils.EstimateTokensFromWords(wordCount)

// 	return estimatedTokens
// }
