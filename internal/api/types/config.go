package types

import "github.com/wgomg/edub-kushim/internal/config"

type ConfigStatusResponse struct {
	Configured   bool     `json:"configured"`
	PendingTasks int      `json:"pending_tasks"`
	Errors       []string `json:"errors"`
}

type ConfigResponse struct {
	Consumer         ConsumerConfigResponse          `json:"consumer"`
	Enricher         EnricherConfigResponse          `json:"enricher"`
	AvailableEngines map[string][]config.EngineEntry `json:"available_engines"`
}

type ConsumerConfigResponse struct {
	DeleteOriginal bool                  `json:"delete_original"`
	Workers        int                   `json:"workers"`
	TextExtractor  TextExtractorResponse `json:"textextractor"`
	PdfOptimizer   PdfOptimizerResponse  `json:"pdfoptimizer"`
	OCR            OCRResponse           `json:"ocr"`
}

type TextExtractorResponse struct {
	Engine  string `json:"engine"`
	Timeout int    `json:"timeout"`
}

type PdfOptimizerResponse struct {
	Engine   string `json:"engine"`
	Fallback string `json:"fallback"`
	Timeout  int    `json:"timeout"`
}

type OCRResponse struct {
	Engine    string   `json:"engine"`
	Languages []string `json:"languages"`
	DataDir   string   `json:"data_dir"`
	Timeout   int      `json:"timeout"`
}

type EnricherConfigResponse struct {
	Workers         int                     `json:"workers"`
	TextReducer     TextReducerResponse     `json:"textreducer"`
	ContentAnalyzer ContentAnalyzerResponse `json:"contentanalyzer"`
	TagMatcher      TagMatcherResponse      `json:"tagmatcher"`
}

type TextReducerResponse struct {
	Engine      string `json:"engine"`
	Timeout     int    `json:"timeout"`
	TargetWords int    `json:"target_words"`
}

type ContentAnalyzerResponse struct {
	Engine  string `json:"engine"`
	Timeout int    `json:"timeout"`
}

type TagMatcherResponse struct {
	Engine            string        `json:"engine"`
	Timeout           int           `json:"timeout"`
	ReduceTargetWords int           `json:"reduce_target_words"`
	ChunkSize         int           `json:"chunk_size"`
	Hugot             HugotResponse `json:"hugot"`
}

type HugotResponse struct {
	Model   string `json:"model"`
	Backend string `json:"backend"`
}

func ConfigResponseFrom(cfg *config.Config) ConfigResponse {
	var resp ConfigResponse
	resp.Consumer.DeleteOriginal = cfg.Consumer.DeleteOriginal
	resp.Consumer.Workers = cfg.Consumer.Workers
	resp.Consumer.TextExtractor.Engine = cfg.Consumer.TextExtractor.Engine
	resp.Consumer.TextExtractor.Timeout = cfg.Consumer.TextExtractor.Timeout
	resp.Consumer.PdfOptimizer.Engine = cfg.Consumer.PdfOptimizer.Engine
	resp.Consumer.PdfOptimizer.Fallback = cfg.Consumer.PdfOptimizer.Fallback
	resp.Consumer.PdfOptimizer.Timeout = cfg.Consumer.PdfOptimizer.Timeout
	resp.Consumer.OCR.Engine = cfg.Consumer.OCR.Engine
	resp.Consumer.OCR.Languages = cfg.Consumer.OCR.Languages
	resp.Consumer.OCR.DataDir = cfg.Consumer.OCR.DataDir
	resp.Consumer.OCR.Timeout = cfg.Consumer.OCR.Timeout
	resp.Enricher.Workers = cfg.Enricher.Workers
	resp.Enricher.TextReducer.Engine = cfg.Enricher.TextReducer.Engine
	resp.Enricher.TextReducer.Timeout = cfg.Enricher.TextReducer.Timeout
	resp.Enricher.TextReducer.TargetWords = cfg.Enricher.TextReducer.TargetWords
	resp.Enricher.ContentAnalyzer.Engine = cfg.Enricher.ContentAnalyzer.Engine
	resp.Enricher.ContentAnalyzer.Timeout = cfg.Enricher.ContentAnalyzer.Timeout
	resp.Enricher.TagMatcher.Engine = cfg.Enricher.TagMatcher.Engine
	resp.Enricher.TagMatcher.Timeout = cfg.Enricher.TagMatcher.Timeout
	resp.Enricher.TagMatcher.ReduceTargetWords = cfg.Enricher.TagMatcher.ReduceTargetWords
	resp.Enricher.TagMatcher.ChunkSize = cfg.Enricher.TagMatcher.ChunkSize
	resp.Enricher.TagMatcher.Hugot.Model = cfg.Enricher.TagMatcher.Hugot.Model
	resp.Enricher.TagMatcher.Hugot.Backend = cfg.Enricher.TagMatcher.Hugot.Backend
	resp.AvailableEngines = config.AvailableEngines
	return resp
}
