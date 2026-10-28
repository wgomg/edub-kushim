// Package config is the application configuration: the YAML tree, its
// defaults and validation, the watcher that reloads it, and the
// environment provisioning the setup flow and the API call (bootstrap,
// asset downloads, service files, readiness checks). Everything reads the
// finalized tree; nothing reaches into the YAML file directly.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment is the deployment environment the app runs in.
type Environment string

// The environments the config names.
const (
	Development Environment = "development"
	Production  Environment = "production"
)

// LoggingConfig is the log-file rotation section.
type LoggingConfig struct {
	MaxSize    int  `mapstructure:"max_size" yaml:"max_size" json:"max_size"`
	MaxBackups int  `mapstructure:"max_backups" yaml:"max_backups" json:"max_backups"`
	MaxAge     int  `mapstructure:"max_age" yaml:"max_age" json:"max_age"`
	Compress   bool `mapstructure:"compress" yaml:"compress" json:"compress"`
}

// AppConfig is the application section: environment, log level, log-file
// rotation, and the config directory the process runs from.
type AppConfig struct {
	Env       Environment   `mapstructure:"environment" yaml:"environment" json:"environment"`
	LogLevel  string        `mapstructure:"log_level" yaml:"log_level" json:"log_level"`
	Logging   LoggingConfig `mapstructure:"logging" yaml:"logging" json:"logging"`
	ConfigDir string        `yaml:"-" json:"config_dir"`
}

// ServerConfig is the API server section: bind address, timeouts in seconds, upload and download limits,
// batch limits, and the authentication toggle and secret.
type ServerConfig struct {
	Host                 string `mapstructure:"host" yaml:"host" json:"host"`
	Port                 int    `mapstructure:"port" yaml:"port" json:"port"`
	ReadTimeout          int    `mapstructure:"read_timeout" yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout         int    `mapstructure:"write_timeout" yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout          int    `mapstructure:"idle_timeout" yaml:"idle_timeout" json:"idle_timeout"`
	MaxUploadSize        int64  `mapstructure:"max_upload_size" yaml:"max_upload_size" json:"max_upload_size"`
	MaxConcurrentBatches int    `mapstructure:"max_concurrent_batches" yaml:"max_concurrent_batches" json:"max_concurrent_batches"`
	MaxDownloadFiles     int    `mapstructure:"max_download_files" yaml:"max_download_files" json:"max_download_files"`
	MaxDownloadSizeMB    int64  `mapstructure:"max_download_size_mb" yaml:"max_download_size_mb" json:"max_download_size_mb"`
	MaxBatchDelete       int    `mapstructure:"max_batch_delete" yaml:"max_batch_delete" json:"max_batch_delete"`
	AuthEnabled          bool   `mapstructure:"auth_enabled" yaml:"auth_enabled" json:"auth_enabled"`
	SessionSecret        string `mapstructure:"session_secret" yaml:"session_secret" json:"session_secret"`
}

// DatabaseConfig is the PostgreSQL section: a discrete connection or a
// full DSN, the container runtime used by the database-backed tests, and
// the idempotent seeders to apply.
type DatabaseConfig struct {
	Host      string   `yaml:"host,omitempty" json:"host"`
	Port      int      `yaml:"port,omitempty" json:"port"`
	User      string   `yaml:"user,omitempty" json:"user"`
	Password  string   `yaml:"password,omitempty" json:"password"`
	Database  string   `yaml:"database,omitempty" json:"database"`
	SSLMode   string   `yaml:"sslmode,omitempty" json:"sslmode"`
	DSN       string   `yaml:"dsn,omitempty" json:"dsn"`
	Runtime   string   `yaml:"runtime,omitempty" json:"runtime"`
	Container string   `yaml:"container,omitempty" json:"container"`
	Seeders   []string `yaml:"seeders,omitempty" json:"seeders"`
}

// container name charset per docker/podman naming rules: https://github.com/moby/moby/blob/master/daemon/names/names.go
var reContainerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// ValidateDatabaseRuntime checks the database runtime setting: empty and
// host run against the host, docker and podman require a container name
// matching the container naming rules, remote accepts any server.
func ValidateDatabaseRuntime(runtime, container string) error {
	switch runtime {
	case "", "host":
		return nil
	case "docker", "podman":
		if container == "" {
			return fmt.Errorf("database.container is required when database.runtime is %s", runtime)
		}
		if !reContainerName.MatchString(container) {
			return fmt.Errorf("database.container %q contains invalid characters — use letters, digits, '.', '_' or '-'", container)
		}
		return nil
	case "remote":
		return nil
	default:
		return fmt.Errorf("database.runtime must be one of host, docker, podman, remote, got %q", runtime)
	}
}

// DatabaseConnectionChanged reports whether two database sections point
// at different connections, comparing the effective connection resolved
// through the DSN when one is set.
func DatabaseConnectionChanged(old, new DatabaseConfig) bool {
	return effectiveConnection(old) != effectiveConnection(new)
}

func effectiveConnection(cfg DatabaseConfig) string {
	host, port, user, password, database, sslmode := cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode
	if cfg.DSN != "" {
		u, err := url.Parse(cfg.DSN)
		if err != nil {
			return "dsn:" + cfg.DSN
		}
		host = u.Hostname()
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				port = n
			}
		}
		if u.User != nil {
			user = u.User.Username()
			if pw, ok := u.User.Password(); ok {
				password = pw
			}
		}
		database = strings.TrimPrefix(u.Path, "/")
		sslmode = u.Query().Get("sslmode")
	}
	return fmt.Sprintf("%s:%d:%s:%s:%s:%s", host, port, user, password, database, sslmode)
}

// BuildPostgresDSN returns the configured DSN verbatim, or assembles one
// from the discrete fields; sslmode rides as a query parameter.
func BuildPostgresDSN(cfg DatabaseConfig) string {
	if cfg.DSN != "" {
		return cfg.DSN
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.Database,
	}
	if cfg.User != "" {
		if cfg.Password != "" {
			u.User = url.UserPassword(cfg.User, cfg.Password)
		} else {
			u.User = url.User(cfg.User)
		}
	}
	q := u.Query()
	if cfg.SSLMode != "" {
		q.Set("sslmode", cfg.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// TrashConfig is the trash retention section.
type TrashConfig struct {
	RetentionDays int `mapstructure:"retention_days" yaml:"retention_days" json:"retention_days"`
}

// StorageConfig is the on-disk layout section: consumption and storage
// directories and the migration mode.
type StorageConfig struct {
	ConsumptionDir string      `mapstructure:"consumption_dir" yaml:"consumption_dir" json:"consumption_dir"`
	StorageDir     string      `mapstructure:"storage_dir" yaml:"storage_dir" json:"storage_dir"`
	MigrationMode  string      `mapstructure:"migration_mode" yaml:"migration_mode" json:"migration_mode"`
	Trash          TrashConfig `mapstructure:"trash" yaml:"trash" json:"trash"`
}

// TextExtractorConfig is the text extraction section: engine and call
// timeout.
type TextExtractorConfig struct {
	Engine  string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Timeout int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

// PdfOptimizerConfig is the PDF optimizer section: primary engine,
// fallback engine, timeout.
type PdfOptimizerConfig struct {
	Engine   string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Fallback string `mapstructure:"fallback" yaml:"fallback" json:"fallback"`
	Timeout  int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

// OCRConfig is the OCR section: engine, languages, tessdata directory,
// timeout and worker count.
type OCRConfig struct {
	Engine     string   `mapstructure:"engine" yaml:"engine" json:"engine"`
	Languages  []string `mapstructure:"languages" yaml:"languages" json:"languages"`
	DataDir    string   `mapstructure:"data_dir" yaml:"data_dir" json:"data_dir"`
	Timeout    int      `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	OcrWorkers int      `mapstructure:"ocr_workers" yaml:"ocr_workers" json:"ocr_workers"`
}

// ThumbnailConfig is the thumbnail section: enablement, engine, render
// DPI, output width, quality, timeout, workers, and the backfill schedule.
type ThumbnailConfig struct {
	Enabled          bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Engine           string  `mapstructure:"engine" yaml:"engine" json:"engine"`
	DPI              int     `mapstructure:"dpi" yaml:"dpi" json:"dpi"`
	MaxWidth         int     `mapstructure:"max_width" yaml:"max_width" json:"max_width"`
	Quality          int     `mapstructure:"quality" yaml:"quality" json:"quality"`
	Timeout          int     `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	Workers          int     `mapstructure:"workers" yaml:"workers" json:"workers"`
	BackfillInterval float64 `mapstructure:"backfill_interval" yaml:"backfill_interval" json:"backfill_interval"`
	BackfillTime     string  `mapstructure:"backfill_time" yaml:"backfill_time" json:"backfill_time"`
}

// PollingWindow is one half-open processing window in HH:MM form.
type PollingWindow struct {
	Start string `mapstructure:"start" yaml:"start" json:"start"`
	End   string `mapstructure:"end" yaml:"end" json:"end"`
}

// PollingConfig is the directory polling section: enablement, interval in
// minutes, and the windows the poller works in.
type PollingConfig struct {
	Enabled  bool            `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Interval int             `mapstructure:"interval" yaml:"interval" json:"interval"` // minutes
	Windows  []PollingWindow `mapstructure:"windows" yaml:"windows" json:"windows"`
}

// ReclaimConfig is the stale-task reclaim section: enablement, retries,
// and the staleness threshold.
type ReclaimConfig struct {
	Enabled        bool  `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	MaxRetries     int   `mapstructure:"max_retries" yaml:"max_retries" json:"max_retries"`
	StaleTaskAfter int64 `mapstructure:"stale_task_after" yaml:"stale_task_after" json:"stale_task_after"`
}

// DocxOdtConverterConfig is the office-document conversion section:
// enablement, converter binary, timeout.
type DocxOdtConverterConfig struct {
	Enabled bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Binary  string `mapstructure:"binary" yaml:"binary" json:"binary"`
	Timeout int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

// ConsumerConfig is the consumption intake section: accepted files,
// workers, per-batch limits, the pipeline tool sections, and the polling
// and reclaim behavior.
type ConsumerConfig struct {
	SupportedFiles   []string               `mapstructure:"supported_files" yaml:"supported_files" json:"supported_files"`
	Workers          int                    `mapstructure:"workers" yaml:"workers" json:"workers"`
	MaxFilesPerBatch int                    `mapstructure:"max_files_per_batch" yaml:"max_files_per_batch" json:"max_files_per_batch"`
	Converter        DocxOdtConverterConfig `mapstructure:"converter" yaml:"converter" json:"converter"`
	TextExtractor    TextExtractorConfig    `mapstructure:"textextractor" yaml:"textextractor" json:"textextractor"`
	PdfOptimizer     PdfOptimizerConfig     `mapstructure:"pdfoptimizer" yaml:"pdfoptimizer" json:"pdfoptimizer"`
	OCR              OCRConfig              `mapstructure:"ocr" yaml:"ocr" json:"ocr"`
	Thumbnail        ThumbnailConfig        `mapstructure:"thumbnail" yaml:"thumbnail" json:"thumbnail"`
	Polling          PollingConfig          `mapstructure:"polling" yaml:"polling" json:"polling"`
	Reclaim          ReclaimConfig          `mapstructure:"reclaim" yaml:"reclaim" json:"reclaim"`
}

// TextReducerConfig is the text reduction section: engine, timeout, and
// the reduction target.
type TextReducerConfig struct {
	Engine      string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Timeout     int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	TargetWords int    `mapstructure:"target_words" yaml:"target_words" json:"target_words"`
}

// DocTypeRefinementConfig is the second-pass type refinement section:
// enablement and how much head and tail text the pass reads.
type DocTypeRefinementConfig struct {
	Enabled   bool `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	HeadWords int  `mapstructure:"head_words" yaml:"head_words" json:"head_words"`
	TailWords int  `mapstructure:"tail_words" yaml:"tail_words" json:"tail_words"`
}

// ContentAnalyzerConfig is the LLM document analysis section: enablement, timeout, the minimum tag count,
// the primary model, its fallbacks, the optional prompt template, the type-refinement pass, and the
// credit-pause policy.
type ContentAnalyzerConfig struct {
	Enabled            bool                    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Timeout            int                     `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	MinTags            int                     `mapstructure:"min_tags" yaml:"min_tags" json:"min_tags"`
	Llm                LlmConfig               `mapstructure:"llm" yaml:"llm" json:"llm"`
	Fallbacks          []FallbackConfig        `mapstructure:"fallbacks" yaml:"fallbacks" json:"fallbacks,omitempty"`
	PromptTemplate     string                  `mapstructure:"prompt_template" yaml:"prompt_template" json:"prompt_template,omitempty"`
	DocTypeRefinement  DocTypeRefinementConfig `mapstructure:"doc_type_refinement" yaml:"doc_type_refinement" json:"doc_type_refinement"`
	PauseOnCreditError bool                    `mapstructure:"pause_on_credit_error" yaml:"pause_on_credit_error" json:"pause_on_credit_error"`
}

// FallbackConfig is one fallback analyzer: enablement and its model.
type FallbackConfig struct {
	Enabled bool      `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Llm     LlmConfig `mapstructure:"llm" yaml:"llm" json:"llm"`
}

// TagAdjudicatorConfig is the LLM tag adjudicator section; an empty model
// falls back to the analyzer's.
type TagAdjudicatorConfig struct {
	Enabled bool      `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Timeout int       `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	Llm     LlmConfig `mapstructure:"llm" yaml:"llm" json:"llm"`
}

// TagQualityConfig is the LLM tag quality judge section; an empty model
// falls back to the analyzer's.
type TagQualityConfig struct {
	Enabled bool      `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Timeout int       `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	Llm     LlmConfig `mapstructure:"llm" yaml:"llm" json:"llm"`
}

// TagReconcileConfig is the scheduled reconciliation section:
// enablement, report-only mode, schedule, per-run caps, and the grace
// periods of dead and provisional tags.
type TagReconcileConfig struct {
	Enabled              bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	ReportOnly           bool    `mapstructure:"report_only" yaml:"report_only" json:"report_only"`
	Interval             float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time                 string  `mapstructure:"time" yaml:"time" json:"time"`
	MaxRejudge           int     `mapstructure:"max_rejudge" yaml:"max_rejudge" json:"max_rejudge"`
	MaxRetag             int     `mapstructure:"max_retag" yaml:"max_retag" json:"max_retag"`
	DeadTagGraceDays     int     `mapstructure:"dead_tag_grace_days" yaml:"dead_tag_grace_days" json:"dead_tag_grace_days"`
	ProvisionalGraceDays int     `mapstructure:"provisional_grace_days" yaml:"provisional_grace_days" json:"provisional_grace_days"`
}

// TagMeasureConfig is the measurement section: enablement and schedule for the standalone run, plus whether
// a measurement batch follows every consume and reenrich batch. AfterEnrichmentBatch is independent of
// Enabled: the scheduled run is the aging safety net, the batch-level run observes production as it lands.
type TagMeasureConfig struct {
	Enabled              bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Interval             float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time                 string  `mapstructure:"time" yaml:"time" json:"time"`
	AfterEnrichmentBatch bool    `mapstructure:"after_enrichment_batch" yaml:"after_enrichment_batch" json:"after_enrichment_batch"`
}

// TagAuditConfig is the scheduled completeness audit section: enablement, schedule, and the per-run sample
// size. The sample size is the calibration lever; the trigger similarity is a pinned constant, not a user
// setting.
type TagAuditConfig struct {
	Enabled    bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Interval   float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time       string  `mapstructure:"time" yaml:"time" json:"time"`
	SampleSize int     `mapstructure:"sample_size" yaml:"sample_size" json:"sample_size"`
}

// TagMaintenanceConfig is the scheduled maintenance routine section: enablement and schedule. While it is
// enabled the individual reconciliation, measurement and audit schedules stay idle; the daily identity
// schedule keeps its own cadence.
type TagMaintenanceConfig struct {
	Enabled  bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
}

// EnricherConfig is the enrichment pipeline section: worker count and the
// tool and judged-step settings.
type EnricherConfig struct {
	Workers         int                   `mapstructure:"workers" yaml:"workers" json:"workers"`
	TextReducer     TextReducerConfig     `mapstructure:"textreducer" yaml:"textreducer" json:"textreducer"`
	ContentAnalyzer ContentAnalyzerConfig `mapstructure:"contentanalyzer" yaml:"contentanalyzer" json:"contentanalyzer"`
	TagMatcher      TagMatcherConfig      `mapstructure:"tagmatcher" yaml:"tagmatcher" json:"tagmatcher"`
	TagAdjudicator  TagAdjudicatorConfig  `mapstructure:"tag_adjudicator" yaml:"tag_adjudicator" json:"tag_adjudicator"`
	TagQuality      TagQualityConfig      `mapstructure:"tag_quality" yaml:"tag_quality" json:"tag_quality"`
	TagReconcile    TagReconcileConfig    `mapstructure:"tag_reconcile" yaml:"tag_reconcile" json:"tag_reconcile"`
	TagMeasure      TagMeasureConfig      `mapstructure:"tag_measure" yaml:"tag_measure" json:"tag_measure"`
	TagAudit        TagAuditConfig        `mapstructure:"tag_audit" yaml:"tag_audit" json:"tag_audit"`
	TagMaintenance  TagMaintenanceConfig  `mapstructure:"tag_maintenance" yaml:"tag_maintenance" json:"tag_maintenance"`
}

// ResolvedAdjudicatorLlm returns the adjudicator's model, falling back to
// the content analyzer's when the section does not name one.
func (c *Config) ResolvedAdjudicatorLlm() LlmConfig {
	if c.Enricher.TagAdjudicator.Llm.Provider == "" || c.Enricher.TagAdjudicator.Llm.Model == "" {
		return c.Enricher.ContentAnalyzer.Llm
	}
	return c.Enricher.TagAdjudicator.Llm
}

// ResolvedAdjudicatorTimeout returns the adjudicator's timeout, falling
// back to the content analyzer's.
func (c *Config) ResolvedAdjudicatorTimeout() int {
	if c.Enricher.TagAdjudicator.Timeout == 0 {
		return c.Enricher.ContentAnalyzer.Timeout
	}
	return c.Enricher.TagAdjudicator.Timeout
}

// ResolvedQualityLlm returns the quality judge's model, falling back to
// the content analyzer's when the section does not name one.
func (c *Config) ResolvedQualityLlm() LlmConfig {
	if c.Enricher.TagQuality.Llm.Provider == "" || c.Enricher.TagQuality.Llm.Model == "" {
		return c.Enricher.ContentAnalyzer.Llm
	}
	return c.Enricher.TagQuality.Llm
}

// ResolvedQualityTimeout returns the quality judge's timeout, falling
// back to the content analyzer's.
func (c *Config) ResolvedQualityTimeout() int {
	if c.Enricher.TagQuality.Timeout == 0 {
		return c.Enricher.ContentAnalyzer.Timeout
	}
	return c.Enricher.TagQuality.Timeout
}

// LlmConfig is one LLM endpoint: adapter, provider, model, token,
// temperature, and the pacing delay between requests (capped at load).
type LlmConfig struct {
	Adapter      string  `mapstructure:"adapter" yaml:"adapter" json:"adapter"`
	Provider     string  `mapstructure:"provider" yaml:"provider" json:"provider"`
	Model        string  `mapstructure:"model" yaml:"model" json:"model"`
	Token        string  `mapstructure:"token" yaml:"token" json:"token,omitempty"`
	Reasoning    bool    `yaml:"-"`
	Temperature  float64 `mapstructure:"temperature" yaml:"temperature" json:"temperature"`
	RequestDelay float64 `mapstructure:"request_delay" yaml:"request_delay" json:"request_delay"`
}

// maxRequestDelaySeconds caps llm.request_delay so a misconfiguration can't
// stall an enricher worker well beyond the content-analyzer timeout budget.
const maxRequestDelaySeconds = 60

// HugotConfig is the embedding model section: model and backend as
// configured; the resolved paths and runtime toggles are filled at load
// time.
type HugotConfig struct {
	Model          string `mapstructure:"model" yaml:"model" json:"model"`
	Backend        string `mapstructure:"backend" yaml:"backend" json:"backend"`
	ModelPath      string `yaml:"-" json:"model_path"`
	BackendLibPath string `yaml:"-" json:"backend_lib_path"`
	CpuMemArena    bool   `yaml:"-"`
	MemPattern     bool   `yaml:"-"`
}

// TagMatcherConfig is the semantic matching section: timeouts, the reduce target and chunk size, the
// embedding model, the user-settable suggestion-injection bar and suggestion count (never below three),
// and the model-pinned min_similarity and identity-candidate floors (filled at load).
type TagMatcherConfig struct {
	Timeout                     int         `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	ReduceTargetWords           int         `mapstructure:"reduce_target_words" yaml:"reduce_target_words" json:"reduce_target_words"`
	ChunkSize                   int         `mapstructure:"chunk_size" yaml:"chunk_size" json:"chunk_size"`
	Hugot                       HugotConfig `mapstructure:"hugot" yaml:"hugot" json:"hugot"`
	MinSuggestionSimilarity     float64     `mapstructure:"min_suggestion_similarity" yaml:"min_suggestion_similarity" json:"min_suggestion_similarity"`
	TopN                        int         `mapstructure:"top_n" yaml:"top_n" json:"top_n"`
	MinSimilarity               float64     `yaml:"-" json:"min_similarity"`
	IdentityCandidateSimilarity float64     `yaml:"-" json:"identity_candidate_similarity"`
}

// ToolConfig is one external binary call: command and timeout.
type ToolConfig struct {
	Command string        `yaml:"command" json:"command"`
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

// BackupSchedule is one backup schedule: mode (full, database or
// documents), cadence, destination path, and retention count.
type BackupSchedule struct {
	Mode     string  `mapstructure:"mode" yaml:"mode" json:"mode"`
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
	Path     string  `mapstructure:"path" yaml:"path" json:"path"`
	Keep     int     `mapstructure:"keep" yaml:"keep" json:"keep"`
}

// ValidBackupModes lists the accepted schedule modes.
var ValidBackupModes = map[string]struct{}{
	"full":      {},
	"database":  {},
	"documents": {},
}

// BackupConfig is the backup section: enablement, destination, schedules,
// plus the deprecated flat fields finalize converts into one full
// schedule.
type BackupConfig struct {
	Enabled   bool             `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Path      string           `mapstructure:"path" yaml:"path" json:"path"`
	Schedules []BackupSchedule `mapstructure:"schedules" yaml:"schedules" json:"schedules"`

	// Deprecated flat fields, kept so finalizeConfig can auto-convert
	// legacy configs into a single full schedule.
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
	Keep     int     `mapstructure:"keep" yaml:"keep" json:"keep"`
}

// MirrorConfig is the mirror section: enablement, destination, cadence.
type MirrorConfig struct {
	Enabled  bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Path     string  `mapstructure:"path" yaml:"path" json:"path"`
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
}

// Config is the whole application configuration: the YAML tree as loaded,
// then finalized with defaults and validation.
type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app" json:"app"`
	Srv      ServerConfig   `mapstructure:"server" yaml:"server" json:"server"`
	Db       DatabaseConfig `mapstructure:"database" yaml:"database" json:"database"`
	Storage  StorageConfig  `mapstructure:"storage" yaml:"storage" json:"storage"`
	Consumer ConsumerConfig `yaml:"consumer" json:"consumer"`
	Enricher EnricherConfig `mapstructure:"enricher" yaml:"enricher" json:"enricher"`
	Backup   BackupConfig   `mapstructure:"backup" yaml:"backup" json:"backup"`
	Mirror   MirrorConfig   `mapstructure:"mirror" yaml:"mirror" json:"mirror"`
}

// engine identifiers grouped by tool category.
var (
	OCR = struct {
		Gosseract string
		OcrMyPdf  string
	}{
		Gosseract: "gosseract",
		OcrMyPdf:  "ocrmypdf",
	}

	PdfOptimizer = struct {
		MuPDF string
		GS    string
	}{
		MuPDF: "mupdf",
		GS:    "gs",
	}

	TextExtractor = struct {
		MuPDF     string
		GoPdf     string
		PdfToText string
		Docx      string
		Odt       string
	}{
		MuPDF:     "mupdf",
		GoPdf:     "gopdf",
		PdfToText: "pdftotext",
		Docx:      "docx",
		Odt:       "odt",
	}

	TextReducer = struct {
		TextRank string
	}{
		TextRank: "textrank",
	}

	Converter = struct {
		LibreOffice string
	}{
		LibreOffice: "libreoffice",
	}

	Thumbnail = struct {
		MuPDF       string
		Imagemagick string
	}{
		MuPDF:       "mupdf",
		Imagemagick: "imagemagick",
	}
)

// EngineEntry is one selectable engine for the setup UI: its value and
// display label.
type EngineEntry struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// AvailableEngines maps a tool category to the engines selectable in the
// setup UI.
var AvailableEngines = map[string][]EngineEntry{
	"ocr": {
		{Value: OCR.Gosseract, Label: "gosseract"},
		{Value: OCR.OcrMyPdf, Label: "ocrmypdf"},
	},
	"pdf_optimizer": {
		{Value: PdfOptimizer.MuPDF, Label: "mupdf"},
		{Value: PdfOptimizer.GS, Label: "gs"},
	},
	"text_extractor": {
		{Value: TextExtractor.MuPDF, Label: "mupdf"},
		{Value: TextExtractor.GoPdf, Label: "gopdf"},
		{Value: TextExtractor.PdfToText, Label: "pdftotext"},
	},
	"text_reducer": {
		{Value: TextReducer.TextRank, Label: "textrank"},
	},
	"thumbnail": {
		{Value: Thumbnail.MuPDF, Label: "mupdf"},
	},
}

// DefaultConfig returns the built-in configuration for the given config
// directory.
func DefaultConfig(configDir string) *Config {
	return &Config{
		App: AppConfig{
			Env:      Development,
			LogLevel: "info",
			Logging: LoggingConfig{
				MaxSize:    100,
				MaxBackups: 7,
				MaxAge:     30,
				Compress:   true,
			},
			ConfigDir: configDir,
		},
		Srv: ServerConfig{
			Host:                 "0.0.0.0",
			Port:                 3000,
			ReadTimeout:          60,
			WriteTimeout:         60,
			IdleTimeout:          60,
			MaxUploadSize:        100,
			MaxConcurrentBatches: 4,
			MaxDownloadFiles:     50,
			MaxDownloadSizeMB:    500,
			MaxBatchDelete:       50,
			AuthEnabled:          true,
		},
		Db: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "edub",
			Password: "edub",
			Database: "edub",
			SSLMode:  "disable",
			Runtime:  "host",
		},
		Storage: StorageConfig{
			ConsumptionDir: filepath.Join(configDir, "inbox"),
			StorageDir:     filepath.Join(configDir, "storage"),
			MigrationMode:  "copy",
			Trash: TrashConfig{
				RetentionDays: 30,
			},
		},
		Consumer: ConsumerConfig{
			SupportedFiles:   []string{".pdf"},
			Workers:          1,
			MaxFilesPerBatch: 10,
			Converter: DocxOdtConverterConfig{
				Enabled: false,
				Binary:  "libreoffice",
				Timeout: 300,
			},
			TextExtractor: TextExtractorConfig{
				Engine:  TextExtractor.MuPDF,
				Timeout: 120,
			},
			PdfOptimizer: PdfOptimizerConfig{
				Engine:  PdfOptimizer.MuPDF,
				Timeout: 0,
			},
			OCR: OCRConfig{
				Engine:     OCR.Gosseract,
				Languages:  []string{"eng"},
				DataDir:    filepath.Join(configDir, "ocr/tessdata"),
				Timeout:    120,
				OcrWorkers: 0,
			},
			Thumbnail: ThumbnailConfig{
				Enabled:          true,
				Engine:           Thumbnail.MuPDF,
				DPI:              72,
				MaxWidth:         400,
				Quality:          80,
				Timeout:          30,
				Workers:          1,
				BackfillInterval: 0,
				BackfillTime:     "02:00",
			},
			Polling: PollingConfig{
				Interval: 5,
			},
			Reclaim: ReclaimConfig{
				Enabled:        true,
				MaxRetries:     3,
				StaleTaskAfter: 600,
			},
		},
		Backup: BackupConfig{
			Enabled: false,
		},
		Enricher: EnricherConfig{
			Workers: 1,
			TextReducer: TextReducerConfig{
				Engine:      TextReducer.TextRank,
				Timeout:     120,
				TargetWords: 2000,
			},
			ContentAnalyzer: ContentAnalyzerConfig{
				Enabled:            false,
				Timeout:            120,
				MinTags:            3,
				PromptTemplate:     "",
				PauseOnCreditError: true,
				Llm: LlmConfig{
					RequestDelay: 1,
				},
				DocTypeRefinement: DocTypeRefinementConfig{
					Enabled:   true,
					HeadWords: 600,
					TailWords: 400,
				},
			},
			TagMatcher: TagMatcherConfig{
				Timeout:           120,
				ReduceTargetWords: 4000,
				TopN:              15,
				// 4096 tokens keeps per-request inference memory bounded
				// (~2.2–2.5 GB idle, ~4–6 GB peak with BGE-M3). The model's
				// full context (0 = max_position_embeddings, 8180 tokens for
				// BGE-M3) scales attention memory roughly quadratically and
				// can OOM small hosts. See docs/tags/10-tag-matcher.md.
				ChunkSize: 4096,
				Hugot: HugotConfig{
					Model:       "BAAI/bge-m3",
					Backend:     "ort",
					CpuMemArena: false,
					MemPattern:  false,
				},
			},
			TagAdjudicator: TagAdjudicatorConfig{
				Enabled: false,
			},
			TagQuality: TagQualityConfig{
				Enabled: false,
			},
			TagReconcile: TagReconcileConfig{
				Enabled:              false,
				ReportOnly:           true,
				Interval:             7,
				Time:                 "02:00",
				MaxRejudge:           200,
				MaxRetag:             1000,
				DeadTagGraceDays:     7,
				ProvisionalGraceDays: 30,
			},
			TagMeasure: TagMeasureConfig{
				Enabled:              false,
				Interval:             7,
				Time:                 "03:00",
				AfterEnrichmentBatch: false,
			},
			TagAudit: TagAuditConfig{
				Enabled:    false,
				Interval:   7,
				Time:       "04:00",
				SampleSize: 150,
			},
			TagMaintenance: TagMaintenanceConfig{
				Enabled:  false,
				Interval: 7,
				Time:     "02:00",
			},
		},
	}
}

// Load reads the YAML configuration from configDir, starting from the
// defaults when the file does not exist, and finalizes it: resolved paths,
// validated and clamped sections, deprecated-field conversion. It fails
// on unreadable files, malformed YAML, or any invalid section.
func Load(configDir string) (*Config, error) {
	cfg := DefaultConfig(configDir)

	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}

	if err := finalizeConfig(cfg, configDir); err != nil {
		return nil, err
	}

	return cfg, nil
}

func finalizeConfig(cfg *Config, configDir string) error {
	if cfg.Enricher.ContentAnalyzer.Enabled {
		llmCfg := &cfg.Enricher.ContentAnalyzer.Llm
		if llmCfg.Adapter == "" {
			return fmt.Errorf("enricher.contentanalyzer.llm.adapter is required when content analyzer is enabled")
		}
		if llmCfg.Provider == "" {
			return fmt.Errorf("enricher.contentanalyzer.llm.provider is required when content analyzer is enabled")
		}
		if llmCfg.Model == "" {
			return fmt.Errorf("enricher.contentanalyzer.llm.model is required when content analyzer is enabled")
		}
	}
	if cfg.Enricher.ContentAnalyzer.MinTags == 0 {
		cfg.Enricher.ContentAnalyzer.MinTags = 3
	}
	if cfg.Enricher.ContentAnalyzer.MinTags < 1 || cfg.Enricher.ContentAnalyzer.MinTags > 20 {
		return fmt.Errorf("enricher.contentanalyzer.min_tags must satisfy 1 <= min_tags <= 20")
	}
	if len(cfg.Consumer.OCR.Languages) == 0 {
		return fmt.Errorf("consumer.ocr.languages is required — run 'kushim setup --languages eng,spa,...' first")
	}

	if cfg.Consumer.Reclaim.Enabled {
		if cfg.Consumer.Reclaim.MaxRetries <= 0 {
			return fmt.Errorf("consumer.reclaim.max_retries must be > 0 when reclaim is enabled")
		}
		if cfg.Consumer.Reclaim.StaleTaskAfter < 60 {
			return fmt.Errorf("consumer.reclaim.stale_task_after must be >= 60 when reclaim is enabled")
		}
	}

	if cfg.Srv.MaxBatchDelete < 1 {
		return fmt.Errorf("server.max_batch_delete must be >= 1")
	}
	if cfg.Srv.ReadTimeout < 0 || cfg.Srv.WriteTimeout < 0 || cfg.Srv.IdleTimeout < 0 {
		return fmt.Errorf("server.read_timeout, server.write_timeout and server.idle_timeout must be >= 0")
	}

	if err := ValidateDatabaseRuntime(cfg.Db.Runtime, cfg.Db.Container); err != nil {
		return err
	}

	if cfg.Storage.Trash.RetentionDays < 1 {
		return fmt.Errorf("storage.trash.retention_days must be >= 1")
	}

	if cfg.Consumer.TextExtractor.Timeout < 0 {
		return fmt.Errorf("consumer.textextractor.timeout must be >= 0")
	}
	if cfg.Consumer.OCR.Timeout < 0 {
		return fmt.Errorf("consumer.ocr.timeout must be >= 0")
	}
	if cfg.Consumer.Thumbnail.Timeout < 0 {
		return fmt.Errorf("consumer.thumbnail.timeout must be >= 0")
	}
	if cfg.Consumer.Thumbnail.MaxWidth < 1 {
		return fmt.Errorf("consumer.thumbnail.max_width must be >= 1")
	}
	if cfg.Consumer.Thumbnail.DPI < 1 {
		return fmt.Errorf("consumer.thumbnail.dpi must be >= 1")
	}
	if cfg.Consumer.Thumbnail.Quality < 1 || cfg.Consumer.Thumbnail.Quality > 100 {
		return fmt.Errorf("consumer.thumbnail.quality must be between 1 and 100")
	}
	if cfg.Consumer.Thumbnail.BackfillInterval < 0 {
		return fmt.Errorf("consumer.thumbnail.backfill_interval must be >= 0")
	}
	if cfg.Consumer.Thumbnail.BackfillInterval > 0 {
		t := cfg.Consumer.Thumbnail.BackfillTime
		if t == "" {
			t = "02:00"
		}
		if _, err := parseHHMM(t, false); err != nil {
			return fmt.Errorf("consumer.thumbnail.backfill_time: %w", err)
		}
	}
	if cfg.Enricher.TextReducer.Timeout < 0 {
		return fmt.Errorf("enricher.textreducer.timeout must be >= 0")
	}
	if cfg.Enricher.TagMatcher.Timeout < 0 {
		return fmt.Errorf("enricher.tagmatcher.timeout must be >= 0")
	}
	if cfg.Enricher.TagMatcher.MinSuggestionSimilarity < 0 {
		return fmt.Errorf("enricher.tagmatcher.min_suggestion_similarity must be > 0 (leave unset for the model default)")
	}
	if cfg.Enricher.TagMatcher.TopN < 3 {
		return fmt.Errorf("enricher.tagmatcher.top_n must be >= 3")
	}

	if cfg.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords < 0 {
		return fmt.Errorf("enricher.contentanalyzer.doc_type_refinement.head_words must be >= 0")
	}
	if cfg.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords < 0 {
		return fmt.Errorf("enricher.contentanalyzer.doc_type_refinement.tail_words must be >= 0")
	}
	if cfg.Enricher.ContentAnalyzer.Llm.RequestDelay < 0 || cfg.Enricher.ContentAnalyzer.Llm.RequestDelay > maxRequestDelaySeconds {
		return fmt.Errorf("enricher.contentanalyzer.llm.request_delay must be between 0 and %d", maxRequestDelaySeconds)
	}

	for i := range cfg.Enricher.ContentAnalyzer.Fallbacks {
		fb := &cfg.Enricher.ContentAnalyzer.Fallbacks[i]
		if !fb.Enabled {
			continue
		}
		llmCfg := &fb.Llm
		if llmCfg.Adapter == "" {
			return fmt.Errorf("enricher.contentanalyzer.fallbacks[%d].llm.adapter is required when the fallback is enabled", i)
		}
		if llmCfg.Provider == "" {
			return fmt.Errorf("enricher.contentanalyzer.fallbacks[%d].llm.provider is required when the fallback is enabled", i)
		}
		if llmCfg.Model == "" {
			return fmt.Errorf("enricher.contentanalyzer.fallbacks[%d].llm.model is required when the fallback is enabled", i)
		}
		if llmCfg.RequestDelay < 0 || llmCfg.RequestDelay > maxRequestDelaySeconds {
			return fmt.Errorf("enricher.contentanalyzer.fallbacks[%d].llm.request_delay must be between 0 and %d", i, maxRequestDelaySeconds)
		}
	}

	if cfg.Enricher.TagReconcile.Interval <= 0 {
		cfg.Enricher.TagReconcile.Interval = 7
	}
	if cfg.Enricher.TagReconcile.MaxRejudge <= 0 {
		cfg.Enricher.TagReconcile.MaxRejudge = 200
	}
	if cfg.Enricher.TagReconcile.MaxRetag <= 0 {
		cfg.Enricher.TagReconcile.MaxRetag = 1000
	}
	if cfg.Enricher.TagReconcile.DeadTagGraceDays <= 0 {
		cfg.Enricher.TagReconcile.DeadTagGraceDays = 7
	}
	if cfg.Enricher.TagReconcile.ProvisionalGraceDays <= 0 {
		cfg.Enricher.TagReconcile.ProvisionalGraceDays = 30
	}
	if cfg.Enricher.TagReconcile.Time == "" {
		cfg.Enricher.TagReconcile.Time = "02:00"
	}
	if _, err := parseHHMM(cfg.Enricher.TagReconcile.Time, false); err != nil {
		return fmt.Errorf("enricher.tag_reconcile.time: %w", err)
	}
	if cfg.Enricher.TagReconcile.Enabled && !cfg.Enricher.TagAdjudicator.Enabled {
		slog.Warn("enricher.tag_reconcile.enabled is set but enricher.tag_adjudicator.enabled is false — scheduled runs will be snapshot-only (no LLM re-adjudication, no writes)")
	}
	if cfg.Enricher.TagMeasure.Interval <= 0 {
		cfg.Enricher.TagMeasure.Interval = 7
	}
	if cfg.Enricher.TagMeasure.Time == "" {
		cfg.Enricher.TagMeasure.Time = "03:00"
	}
	if _, err := parseHHMM(cfg.Enricher.TagMeasure.Time, false); err != nil {
		return fmt.Errorf("enricher.tag_measure.time: %w", err)
	}
	if cfg.Enricher.TagAudit.Interval <= 0 {
		cfg.Enricher.TagAudit.Interval = 7
	}
	if cfg.Enricher.TagAudit.Time == "" {
		cfg.Enricher.TagAudit.Time = "04:00"
	}
	if _, err := parseHHMM(cfg.Enricher.TagAudit.Time, false); err != nil {
		return fmt.Errorf("enricher.tag_audit.time: %w", err)
	}
	if cfg.Enricher.TagAudit.SampleSize <= 0 {
		cfg.Enricher.TagAudit.SampleSize = 150
	}
	if cfg.Enricher.TagMaintenance.Interval <= 0 {
		cfg.Enricher.TagMaintenance.Interval = 7
	}
	if cfg.Enricher.TagMaintenance.Time == "" {
		cfg.Enricher.TagMaintenance.Time = "02:00"
	}
	if _, err := parseHHMM(cfg.Enricher.TagMaintenance.Time, false); err != nil {
		return fmt.Errorf("enricher.tag_maintenance.time: %w", err)
	}
	if cfg.Enricher.TagMaintenance.Enabled && !cfg.Enricher.TagAdjudicator.Enabled {
		slog.Warn("enricher.tag_maintenance.enabled is set but enricher.tag_adjudicator.enabled is false — the routine's reconcile and identity steps stay snapshot-only")
	}
	if cfg.Enricher.TagQuality.Enabled {
		q := cfg.ResolvedQualityLlm()
		g := cfg.Enricher.ContentAnalyzer.Llm
		if q.Provider == g.Provider && q.Model == g.Model {
			slog.Warn("enricher.tag_quality.llm resolves to the generator model — judge hygiene requires a different model; shared failure modes will bias every verdict")
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("expand home dir: %w", err)
	}

	cfg.App.ConfigDir = configDir

	modelParts := strings.Split(cfg.Enricher.TagMatcher.Hugot.Model, "/")
	modelShortName := modelParts[len(modelParts)-1]
	cfg.Enricher.TagMatcher.Hugot.ModelPath = filepath.Join(configDir, "tagmatcher", "hugot", "models", modelShortName)
	cfg.Enricher.TagMatcher.MinSimilarity = defaultMinSimilarity(modelShortName)
	cfg.Enricher.TagMatcher.IdentityCandidateSimilarity = defaultIdentityCandidateSimilarity(modelShortName)
	// zero reads as unset: the model default re-applies, so kushim config
	// --unset reverts
	if cfg.Enricher.TagMatcher.MinSuggestionSimilarity == 0 {
		cfg.Enricher.TagMatcher.MinSuggestionSimilarity = defaultMinSuggestionSimilarity(modelShortName)
	}
	cfg.Enricher.TagMatcher.Hugot.BackendLibPath = filepath.Join(configDir, "tagmatcher", "hugot", "libs")

	cfg.Storage.ConsumptionDir = expandPath(cfg.Storage.ConsumptionDir, homeDir)
	cfg.Storage.StorageDir = expandPath(cfg.Storage.StorageDir, homeDir)
	cfg.Consumer.OCR.DataDir = expandPath(cfg.Consumer.OCR.DataDir, homeDir)

	if err := requireAbsPath(cfg.Storage.ConsumptionDir, "storage.consumption_dir"); err != nil {
		return err
	}
	if err := requireAbsPath(cfg.Storage.StorageDir, "storage.storage_dir"); err != nil {
		return err
	}
	if err := requireAbsPath(cfg.Consumer.OCR.DataDir, "consumer.ocr.data_dir"); err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.Storage.ConsumptionDir, 0755); err != nil {
		return fmt.Errorf("failed to create consumption directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.StorageDir, 0755); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	if cfg.Backup.Enabled {
		switch {
		case len(cfg.Backup.Schedules) > 0 && (cfg.Backup.Interval > 0 || cfg.Backup.Time != "" || cfg.Backup.Keep > 0):
			return fmt.Errorf("remove deprecated flat backup fields (interval, time, keep) when using backup.schedules")
		case len(cfg.Backup.Schedules) > 0:
		case cfg.Backup.Interval > 0:
			cfg.Backup.Schedules = []BackupSchedule{{
				Mode:     "full",
				Interval: cfg.Backup.Interval,
				Time:     cfg.Backup.Time,
				Path:     cfg.Backup.Path,
				Keep:     cfg.Backup.Keep,
			}}
		default:
			return fmt.Errorf("at least one backup schedule is required when backup.enabled is true")
		}
		if cfg.Backup.Path == "" {
			cfg.Backup.Path = filepath.Join(configDir, "backups")
		}
		cfg.Backup.Path = expandPath(cfg.Backup.Path, homeDir)
		if err := os.MkdirAll(cfg.Backup.Path, 0755); err != nil {
			return fmt.Errorf("failed to create backup directory: %w", err)
		}
		seen := make(map[string]bool, len(cfg.Backup.Schedules))
		for i := range cfg.Backup.Schedules {
			s := &cfg.Backup.Schedules[i]
			if _, ok := ValidBackupModes[s.Mode]; !ok {
				return fmt.Errorf("backup.schedules[%d].mode must be one of full, database, documents, got %q", i, s.Mode)
			}
			if seen[s.Mode] {
				return fmt.Errorf("backup.schedules: duplicate mode %q — each mode can appear at most once", s.Mode)
			}
			seen[s.Mode] = true
			if s.Interval <= 0 {
				return fmt.Errorf("backup.schedules[%d].interval must be > 0", i)
			}
			if s.Time == "" {
				s.Time = "02:00"
			}
			if _, err := parseHHMM(s.Time, false); err != nil {
				return fmt.Errorf("backup.schedules[%d].time: %w", i, err)
			}
			if s.Path == "" {
				s.Path = cfg.Backup.Path
			}
			s.Path = expandPath(s.Path, homeDir)
			if err := os.MkdirAll(s.Path, 0755); err != nil {
				return fmt.Errorf("failed to create backup directory for schedule %q: %w", s.Mode, err)
			}
		}
	}

	if cfg.Mirror.Enabled {
		if cfg.Mirror.Path == "" {
			return fmt.Errorf("mirror.path is required when mirror.enabled is true")
		}
		if cfg.Mirror.Interval <= 0 {
			return fmt.Errorf("mirror.interval must be > 0")
		}
		if cfg.Mirror.Time == "" {
			cfg.Mirror.Time = "02:00"
		}
		if _, err := parseHHMM(cfg.Mirror.Time, false); err != nil {
			return fmt.Errorf("mirror.time: %w", err)
		}
		if err := ValidateMirrorDestination(cfg.Mirror.Path, cfg.Storage.StorageDir, cfg.Backup.Path); err != nil {
			return err
		}
		if !IsRemoteMirrorTarget(cfg.Mirror.Path) {
			cfg.Mirror.Path = expandPath(cfg.Mirror.Path, homeDir)
			if err := os.MkdirAll(cfg.Mirror.Path, 0755); err != nil {
				return fmt.Errorf("failed to create mirror directory: %w", err)
			}
		}
	}

	return nil
}

// ValidateMirrorDestination refuses mirror destinations that would
// recurse into the backed-up data: the storage directory itself, anything
// inside it, an ancestor of it, or anything inside the backup directory.
// Dash-prefixed destinations are refused outright; remote targets pass
// without local checks.
func ValidateMirrorDestination(dest, storageDir, backupPath string) error {
	if strings.HasPrefix(dest, "-") {
		return fmt.Errorf("mirror destination %q starts with '-' — refusing", dest)
	}
	if IsRemoteMirrorTarget(dest) {
		return nil
	}
	abs, err := resolvePath(dest)
	if err != nil {
		return fmt.Errorf("resolve mirror destination: %w", err)
	}
	storageAbs, err := resolvePath(storageDir)
	if err != nil {
		return fmt.Errorf("resolve storage dir: %w", err)
	}
	if abs == storageAbs {
		return fmt.Errorf("mirror destination %q is the storage directory itself", dest)
	}
	if isWithinPath(abs, storageAbs) {
		return fmt.Errorf("mirror destination %q is inside the storage directory — refusing", dest)
	}
	if isWithinPath(storageAbs, abs) {
		return fmt.Errorf("mirror destination %q contains the storage directory — refusing", dest)
	}
	if backupPath != "" {
		backupAbs, err := resolvePath(backupPath)
		if err != nil {
			return fmt.Errorf("resolve backup path: %w", err)
		}
		if isWithinPath(abs, backupAbs) {
			return fmt.Errorf("mirror destination %q is inside the backup directory — refusing", dest)
		}
	}
	return nil
}

func isWithinPath(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	var rest []string
	cur := abs
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for _, r := range slices.Backward(rest) {
				resolved = filepath.Join(resolved, r)
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// IsRemoteMirrorTarget reports whether a mirror path is remote (host:path
// form), which skips the local-path checks.
func IsRemoteMirrorTarget(path string) bool {
	if strings.HasPrefix(path, "rsync://") {
		return true
	}
	colon := strings.IndexByte(path, ':')
	if colon <= 0 {
		return false
	}
	host := path[:colon]
	return !strings.Contains(host, "/") && !strings.ContainsAny(host, " \t")
}

func expandPath(path, homeDir string) string {
	if len(path) > 0 && path[0] == '~' {
		return homeDir + path[1:]
	}
	return path
}

func requireAbsPath(path, name string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%s must be an absolute path or start with '~', got: %s", name, path)
	}
	return nil
}

var (
	reHHMM    = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	reEndHHMM = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$|^24:00$`)
)

func parseHHMM(s string, allow2400 bool) (int, error) {
	if s == "24:00" && allow2400 {
		return 1440, nil
	}
	r := reHHMM
	if allow2400 {
		r = reEndHHMM
	}
	if !r.MatchString(s) {
		return 0, fmt.Errorf("invalid time %q — expected HH:MM (00:00–23:59)", s)
	}
	hour, _ := strconv.Atoi(s[:2])
	min, _ := strconv.Atoi(s[3:])
	return hour*60 + min, nil
}

// ValidatePollingWindows validates a submitted polling section: every
// bound is HH:MM and the start precedes the end.
func ValidatePollingWindows(body map[string]any) error {
	raw, ok := body["consumer.polling.windows"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("consumer.polling.windows must be an array")
	}
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("consumer.polling.windows[%d]: expected object with start/end", i)
		}
		start, _ := entry["start"].(string)
		end, _ := entry["end"].(string)
		if start == "" || end == "" {
			return fmt.Errorf("consumer.polling.windows[%d]: start and end are required", i)
		}
		startMins, err := parseHHMM(start, false)
		if err != nil {
			return fmt.Errorf("consumer.polling.windows[%d].start: %w", i, err)
		}
		endMins, err := parseHHMM(end, true)
		if err != nil {
			return fmt.Errorf("consumer.polling.windows[%d].end: %w", i, err)
		}
		if end == "00:00" {
			return fmt.Errorf("consumer.polling.windows[%d].end: 00:00 is not a valid end time (use 24:00 for end-of-day)", i)
		}
		if startMins >= endMins {
			return fmt.Errorf("consumer.polling.windows[%d]: start (%s) must be before end (%s)", i, start, end)
		}
	}
	return nil
}

// IsWithinActiveWindows reports whether now falls inside any window; an
// empty list means always active.
func IsWithinActiveWindows(windows []PollingWindow) bool {
	if len(windows) == 0 {
		return true
	}
	now := time.Now()
	currentMins := now.Hour()*60 + now.Minute()
	for _, w := range windows {
		startMins, _ := parseHHMM(w.Start, false)
		endMins, _ := parseHHMM(w.End, true)
		if currentMins >= startMins && currentMins < endMins {
			return true
		}
	}
	return false
}

// defaultMinSimilarity returns a sensible default min_similarity threshold for
// the given model name. Larger models with higher-dimensional embeddings tend
// to produce tighter clusters, requiring a higher threshold to avoid false
// positives when comparing a full document against short tag names.
func defaultMinSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.40
	case "all-mpnet-base-v2":
		return 0.30
	case "all-MiniLM-L6-v2":
		return 0.25
	default:
		return 0.30
	}
}

// defaultIdentityCandidateSimilarity returns the floor at or above which a stored tag reaches the identity
// gate as a candidate neighbour. It must be higher than the document-to-tag threshold because single
// tag names have much sparser semantic signal than full document text. The values are tuned per model
// dimension: higher-dim models produce tighter clusters and need a higher floor.
func defaultIdentityCandidateSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.80 // 1024-dim; reduced from 0.82 due to SentencePiece tokenization variance
	case "all-mpnet-base-v2":
		return 0.75 // 768-dim
	case "all-MiniLM-L6-v2":
		return 0.70 // 384-dim, compressed distribution
	default:
		return 0.75
	}
}

// defaultMinSuggestionSimilarity returns the bar at or above which a matcher suggestion is injected
// into the emission prompt. The bge-m3 bar is 0.52, the lowest that admits the observed true
// positives (christianity 0.542, archaeology 0.530, computer 0.526) while holding the corpus-generic
// band 0.474–0.519 out. The remaining values are the model's document-match floor plus a 0.15 band;
// only bge-m3 is dump-verified.
func defaultMinSuggestionSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.52
	case "all-mpnet-base-v2":
		return 0.45
	case "all-MiniLM-L6-v2":
		return 0.40
	default:
		return 0.45
	}
}
