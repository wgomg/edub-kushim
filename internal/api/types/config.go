package types

import "github.com/wgomg/edub-kushim/internal/config"

type FailedTaskSummary struct {
	TaskID string `json:"task_id"`
	Op     string `json:"op"`
	Lang   string `json:"lang,omitempty"`
	Error  string `json:"error"`
}

type ConfigStatusResponse struct {
	Configured   bool                  `json:"configured"`
	PendingTasks int                   `json:"pending_tasks"`
	FailedTasks  []FailedTaskSummary   `json:"failed_tasks,omitempty"`
	Errors       []string              `json:"errors"`
	Tools        []config.ExternalTool `json:"tools"`
	MissingTools []config.ExternalTool `json:"missing_tools"`
}

type LoggingConfigResponse struct {
	MaxSize    int  `json:"max_size"`
	MaxBackups int  `json:"max_backups"`
	MaxAge     int  `json:"max_age"`
	Compress   bool `json:"compress"`
}

type AppConfigResponse struct {
	Initialized bool                `json:"initialized"`
	LogLevel    string              `json:"log_level"`
	Logging     LoggingConfigResponse `json:"logging"`
}

type StorageConfigResponse struct {
	ConsumptionDir string `json:"consumption_dir"`
	StorageDir     string `json:"storage_dir"`
}

type DatabaseConfigResponse struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`
	SSLMode  string `json:"sslmode"`
}

type ConfigResponse struct {
	App              AppConfigResponse               `json:"app"`
	Server           ServerConfigResponse            `json:"server"`
	Storage          StorageConfigResponse           `json:"storage"`
	Database         DatabaseConfigResponse          `json:"database"`
	Consumer         ConsumerConfigResponse          `json:"consumer"`
	Enricher         EnricherConfigResponse          `json:"enricher"`
	Backup           BackupConfigResponse            `json:"backup"`
	AvailableEngines map[string][]config.EngineEntry `json:"available_engines"`
}

type ServerConfigResponse struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	MaxUploadSize int64  `json:"max_upload_size"`
	AuthEnabled   bool   `json:"auth_enabled"`
}

type PollingWindowResponse struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type PollingConfigResponse struct {
	Enabled  bool                  `json:"enabled"`
	Interval int                   `json:"interval"`
	Windows  []PollingWindowResponse `json:"windows"`
}

type ReclaimConfigResponse struct {
	Enabled        bool  `json:"enabled"`
	MaxRetries     int   `json:"max_retries"`
	StaleTaskAfter int64 `json:"stale_task_after"`
}

type ConsumerConfigResponse struct {
	Workers          int                   `json:"workers"`
	MaxFilesPerBatch int                   `json:"max_files_per_batch"`
	TextExtractor    TextExtractorResponse `json:"textextractor"`
	PdfOptimizer     PdfOptimizerResponse  `json:"pdfoptimizer"`
	OCR              OCRResponse           `json:"ocr"`
	Polling          PollingConfigResponse `json:"polling"`
	Reclaim          ReclaimConfigResponse `json:"reclaim"`
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
	Engine     string   `json:"engine"`
	Languages  []string `json:"languages"`
	DataDir    string   `json:"data_dir"`
	Timeout    int      `json:"timeout"`
	OcrWorkers int      `json:"ocr_workers"`
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
	Enabled        bool              `json:"enabled"`
	Timeout        int               `json:"timeout"`
	Llm            LlmConfigResponse `json:"llm"`
	PromptTemplate string            `json:"prompt_template,omitempty"`
}

type LlmConfigResponse struct {
	Adapter         string  `json:"adapter"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Token           string  `json:"token,omitempty"`
	Reasoning       bool    `json:"reasoning"`
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`
	Temperature     float64 `json:"temperature"`
}

type TagMatcherResponse struct {
	Timeout           int           `json:"timeout"`
	ReduceTargetWords int           `json:"reduce_target_words"`
	ChunkSize         int           `json:"chunk_size"`
	Hugot             HugotResponse `json:"hugot"`
}

type HugotResponse struct {
	Model   string `json:"model"`
	Backend string `json:"backend"`
}

type BackupConfigResponse struct {
	Enabled  bool    `json:"enabled"`
	Interval float64 `json:"interval"`
	Time     string  `json:"time"`
	Path     string  `json:"path"`
	Keep     int     `json:"keep"`
}

type LlmModelsResponse struct {
	Adapters  map[string][]string        `json:"adapters"`
	Providers map[string][]LlmModelEntry `json:"providers"`
}

type LlmModelEntry struct {
	ID           string `json:"id"`
	Capabilities struct {
		SupportsReasoning    bool     `json:"supports_reasoning"`
		ReasoningEfforts     []string `json:"reasoning_efforts,omitempty"`
		MaxInputTokens       int      `json:"max_input_tokens"`
		MaxOutputTokens      int      `json:"max_output_tokens"`
		SupportsTemperature     bool     `json:"supports_temperature"`
		SupportsResponseSchema bool   `json:"supports_response_schema"`
	} `json:"capabilities"`
}

func ConfigResponseFrom(cfg *config.Config) ConfigResponse {
	var resp ConfigResponse
	resp.Consumer.Workers = cfg.Consumer.Workers
	resp.Consumer.MaxFilesPerBatch = cfg.Consumer.MaxFilesPerBatch
	resp.Consumer.Polling.Enabled = cfg.Consumer.Polling.Enabled
	resp.Consumer.Polling.Interval = cfg.Consumer.Polling.Interval
	resp.Consumer.Polling.Windows = make([]PollingWindowResponse, len(cfg.Consumer.Polling.Windows))
	for i, w := range cfg.Consumer.Polling.Windows {
		resp.Consumer.Polling.Windows[i] = PollingWindowResponse{Start: w.Start, End: w.End}
	}
	resp.Consumer.Reclaim.Enabled = cfg.Consumer.Reclaim.Enabled
	resp.Consumer.Reclaim.MaxRetries = cfg.Consumer.Reclaim.MaxRetries
	resp.Consumer.Reclaim.StaleTaskAfter = cfg.Consumer.Reclaim.StaleTaskAfter
	resp.Consumer.TextExtractor.Engine = cfg.Consumer.TextExtractor.Engine
	resp.Consumer.TextExtractor.Timeout = cfg.Consumer.TextExtractor.Timeout
	resp.Consumer.PdfOptimizer.Engine = cfg.Consumer.PdfOptimizer.Engine
	resp.Consumer.PdfOptimizer.Fallback = cfg.Consumer.PdfOptimizer.Fallback
	resp.Consumer.PdfOptimizer.Timeout = cfg.Consumer.PdfOptimizer.Timeout
	resp.Consumer.OCR.Engine = cfg.Consumer.OCR.Engine
	resp.Consumer.OCR.Languages = cfg.Consumer.OCR.Languages
	resp.Consumer.OCR.DataDir = cfg.Consumer.OCR.DataDir
	resp.Consumer.OCR.Timeout = cfg.Consumer.OCR.Timeout
	resp.Consumer.OCR.OcrWorkers = cfg.Consumer.OCR.OcrWorkers
	resp.Enricher.Workers = cfg.Enricher.Workers
	resp.Enricher.TextReducer.Engine = cfg.Enricher.TextReducer.Engine
	resp.Enricher.TextReducer.Timeout = cfg.Enricher.TextReducer.Timeout
	resp.Enricher.TextReducer.TargetWords = cfg.Enricher.TextReducer.TargetWords
	resp.Enricher.ContentAnalyzer.Enabled = cfg.Enricher.ContentAnalyzer.Enabled
	resp.Enricher.ContentAnalyzer.Timeout = cfg.Enricher.ContentAnalyzer.Timeout
	resp.Enricher.ContentAnalyzer.PromptTemplate = cfg.Enricher.ContentAnalyzer.PromptTemplate
	resp.Enricher.ContentAnalyzer.Llm.Adapter = cfg.Enricher.ContentAnalyzer.Llm.Adapter
	resp.Enricher.ContentAnalyzer.Llm.Provider = cfg.Enricher.ContentAnalyzer.Llm.Provider
	resp.Enricher.ContentAnalyzer.Llm.Model = cfg.Enricher.ContentAnalyzer.Llm.Model
	resp.Enricher.ContentAnalyzer.Llm.Token = cfg.Enricher.ContentAnalyzer.Llm.Token
	resp.Enricher.ContentAnalyzer.Llm.Reasoning = cfg.Enricher.ContentAnalyzer.Llm.Reasoning
	resp.Enricher.ContentAnalyzer.Llm.ReasoningEffort = cfg.Enricher.ContentAnalyzer.Llm.ReasoningEffort
	resp.Enricher.ContentAnalyzer.Llm.Temperature = cfg.Enricher.ContentAnalyzer.Llm.Temperature
	resp.Enricher.TagMatcher.Timeout = cfg.Enricher.TagMatcher.Timeout
	resp.Enricher.TagMatcher.ReduceTargetWords = cfg.Enricher.TagMatcher.ReduceTargetWords
	resp.Enricher.TagMatcher.ChunkSize = cfg.Enricher.TagMatcher.ChunkSize
	resp.Enricher.TagMatcher.Hugot.Model = cfg.Enricher.TagMatcher.Hugot.Model
	resp.Enricher.TagMatcher.Hugot.Backend = cfg.Enricher.TagMatcher.Hugot.Backend
	resp.Storage.ConsumptionDir = cfg.Storage.ConsumptionDir
	resp.Storage.StorageDir = cfg.Storage.StorageDir
	resp.Database.Host = cfg.Db.Host
	resp.Database.Port = cfg.Db.Port
	resp.Database.User = cfg.Db.User
	resp.Database.Database = cfg.Db.Database
	resp.Database.SSLMode = cfg.Db.SSLMode
	resp.App.Initialized = cfg.App.ConfigDir != ""
	resp.App.LogLevel = cfg.App.LogLevel
	resp.App.Logging = LoggingConfigResponse{
		MaxSize:    cfg.App.Logging.MaxSize,
		MaxBackups: cfg.App.Logging.MaxBackups,
		MaxAge:     cfg.App.Logging.MaxAge,
		Compress:   cfg.App.Logging.Compress,
	}
	resp.Server.Host = cfg.Srv.Host
	resp.Server.Port = cfg.Srv.Port
	resp.Server.MaxUploadSize = cfg.Srv.MaxUploadSize
	resp.Server.AuthEnabled = cfg.Srv.AuthEnabled
	resp.Backup.Enabled = cfg.Backup.Enabled
	resp.Backup.Interval = cfg.Backup.Interval
	resp.Backup.Time = cfg.Backup.Time
	resp.Backup.Path = cfg.Backup.Path
	resp.Backup.Keep = cfg.Backup.Keep
	resp.AvailableEngines = config.AvailableEngines
	return resp
}
