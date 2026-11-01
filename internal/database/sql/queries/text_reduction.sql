-- the reduction cache: callers read a stored reduction before running TextRank and write it after; the
-- generation marker records the reducer configuration the stored rows belong to, so a changed configuration
-- clears the cache without scanning it
-- name: GetTextReduction :one
SELECT reduced_text FROM text_reduction
WHERE fingerprint = $1 AND input_hash = $2 AND target_words = $3;

-- name: InsertTextReduction :exec
INSERT INTO text_reduction (fingerprint, input_hash, target_words, reduced_text)
VALUES ($1, $2, $3, $4)
ON CONFLICT (fingerprint, input_hash, target_words) DO NOTHING;

-- name: GetTextReductionGeneration :one
SELECT value FROM system_signal WHERE name = 'text_reduction_generation';

-- name: SetTextReductionGeneration :exec
INSERT INTO system_signal (name, type, value)
VALUES ('text_reduction_generation', 'text', @value::text)
ON CONFLICT (name) DO UPDATE SET value = @value::text, modified_at = CURRENT_TIMESTAMP;

-- name: LockTextReductionGeneration :exec
SELECT pg_advisory_xact_lock(hashtext('text_reduction_generation'));

-- name: TruncateTextReduction :exec
TRUNCATE text_reduction;
