package types

type TaskResponse struct {
	TaskID      string  `json:"task_id"`
	BatchID     string  `json:"batch_id"`
	FileName    string  `json:"file_name"`
	Status      string  `json:"status"`
	DocumentID  *int64  `json:"document_id"`
	Error       *string `json:"error"`
	CreatedAt   string  `json:"created_at"`
	StartedAt   *string `json:"started_at"`
	CompletedAt *string `json:"completed_at"`
}

type BatchSummaryResponse struct {
	BatchID    string `json:"batch_id"`
	Total      int64  `json:"total"`
	Pending    int64  `json:"pending"`
	Processing int64  `json:"processing"`
	Completed  int64  `json:"completed"`
	Failed     int64  `json:"failed"`
	Cancelled  int64  `json:"cancelled"`
}

type ListBatchesResponse struct {
	Batches []BatchSummaryResponse `json:"batches"`
}

type ListTasksResponse struct {
	BatchID string                `json:"batch_id,omitempty"`
	Summary *BatchSummaryResponse `json:"summary,omitempty"`
	Tasks   []TaskResponse        `json:"tasks"`
}

type GlobalSummaryResponse struct {
	TotalBatches int64   `json:"total_batches"`
	TotalFiles   int64   `json:"total_files"`
	Pending      int64   `json:"pending"`
	Processing   int64   `json:"processing"`
	Completed    int64   `json:"completed"`
	Failed       int64   `json:"failed"`
	Cancelled    int64   `json:"cancelled"`
	TotalSizeGB  float64 `json:"total_size_gb"`
}
