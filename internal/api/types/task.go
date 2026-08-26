package types

type TaskResponse struct {
	TaskID       string  `json:"task_id"`
	BatchID      string  `json:"batch_id"`
	TaskType     string  `json:"task_type"`
	FileName     string  `json:"file_name"`
	PayloadDocID string  `json:"payload_doc_id"`
	Status       string  `json:"status"`
	DocumentID   *int64  `json:"document_id"`
	Error        *string `json:"error"`
	Label        string  `json:"label"`
	CreatedAt    string  `json:"created_at"`
	StartedAt    *string `json:"started_at"`
	CompletedAt  *string `json:"completed_at"`
}

type BatchCounts struct {
	Total      int64  `json:"total"`
	Waiting    int64  `json:"waiting"`
	Pending    int64  `json:"pending"`
	Processing int64  `json:"processing"`
	Completed  int64  `json:"completed"`
	Failed     int64  `json:"failed"`
	Cancelled  int64  `json:"cancelled"`
	Discarded  int64  `json:"discarded"`
	OwnerState string `json:"owner_state,omitempty"`
	Orphaned   bool   `json:"orphaned"`
}

type BatchSummaryResponse struct {
	BatchID  string `json:"batch_id"`
	Status   string `json:"status"`
	BatchCounts
	OwnerPID int64  `json:"owner_pid,omitempty"`
}

type ListBatchesResponse struct {
	Batches []BatchSummaryResponse `json:"batches"`
}

type ListTasksResponse struct {
	BatchID string                `json:"batch_id,omitempty"`
	Summary *BatchSummaryResponse `json:"summary,omitempty"`
	Tasks   []TaskResponse        `json:"tasks"`
}

type OriginalTypeStat struct {
	OriginalType string `json:"original_type"`
	Count        int64  `json:"count"`
	TotalBytes   int64  `json:"total_bytes"`
}

type StorageTrendPoint struct {
	Date            string `json:"date"`
	DailyCount      int64  `json:"daily_count"`
	DailyBytes      int64  `json:"daily_bytes"`
	CumulativeBytes int64  `json:"cumulative_bytes"`
}

type BatchOverviewItem struct {
	BatchID    string `json:"batch_id"`
	Status     string `json:"status"`
	Source     string `json:"source"`
	CreatedAt  string `json:"created_at"`
	BatchCounts
	DurationMs *int64 `json:"duration_ms,omitempty"`
}

type DistributionItem struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type DocumentAnalytics struct {
	LanguageDistribution     []DistributionItem `json:"language_distribution"`
	DocumentTypeDistribution []DistributionItem `json:"document_type_distribution"`
	TagFrequency             []DistributionItem `json:"tag_frequency"`
	MissingLanguageCount     int64              `json:"missing_language_count"`
	MissingTypeCount         int64              `json:"missing_type_count"`
	MissingTagsCount         int64              `json:"missing_tags_count"`
}

type ProcessingHealth struct {
	SuccessRate     float64 `json:"success_rate"`
	CompletedLast7d int64   `json:"completed_last_7d"`
	FailedLast7d    int64   `json:"failed_last_7d"`
	AvgDurationMs   int64   `json:"avg_duration_ms"`
	ActiveBatches   int64   `json:"active_batches"`
	OrphanedBatches int64   `json:"orphaned_batches"`
	MissingTools    int64   `json:"missing_tools"`
}

type DashboardResponse struct {
	RecentBatches    []BatchOverviewItem  `json:"recent_batches"`
	RunningTasks     []TaskResponse       `json:"running_tasks"`
	Analytics        *DocumentAnalytics   `json:"analytics,omitempty"`
	ProcessingHealth *ProcessingHealth    `json:"processing_health,omitempty"`
	TotalBatches     int64                `json:"total_batches"`
	TotalFiles       int64                `json:"total_files"`
	Waiting          int64                `json:"waiting"`
	Pending          int64                `json:"pending"`
	Processing       int64                `json:"processing"`
	Completed        int64                `json:"completed"`
	Failed           int64                `json:"failed"`
	Cancelled        int64                `json:"cancelled"`
	Discarded        int64                `json:"discarded"`
	TotalSizeGB      float64              `json:"total_size_gb"`
	OriginalTypeBreakdown []OriginalTypeStat `json:"original_type_breakdown"`
	StorageTrend      []StorageTrendPoint `json:"storage_trend"`
	AvgFileSizeBytes  int64               `json:"avg_file_size_bytes"`
	TotalPages        int64               `json:"total_pages"`
	TotalWords        int64               `json:"total_words"`
}
