package tags

// TagMeasureDedupKey is the active-dedup key shared by the enqueue path and the task type definition so
// they agree; it is declared here because this package sits below the curation package that shares it.
const TagMeasureDedupKey = "tagmeasure"
