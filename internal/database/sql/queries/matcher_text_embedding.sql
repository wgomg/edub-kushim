-- the matcher's text-embedding cache: the daemon reads a pooled vector before embedding a long input and
-- writes it after; the generation marker records the embedding stack the stored rows belong to, so a
-- changed stack clears the cache without scanning it
-- name: GetMatcherTextEmbedding :one
SELECT embedding FROM matcher_text_embedding
WHERE fingerprint = $1 AND input_hash = $2;

-- name: InsertMatcherTextEmbedding :exec
INSERT INTO matcher_text_embedding (fingerprint, input_hash, embedding)
VALUES ($1, $2, $3)
ON CONFLICT (fingerprint, input_hash) DO NOTHING;

-- name: GetMatcherTextEmbeddingGeneration :one
SELECT value FROM system_signal WHERE name = 'matcher_text_embedding_generation';

-- name: SetMatcherTextEmbeddingGeneration :exec
INSERT INTO system_signal (name, type, value)
VALUES ('matcher_text_embedding_generation', 'text', @value::text)
ON CONFLICT (name) DO UPDATE SET value = @value::text, modified_at = CURRENT_TIMESTAMP;

-- name: TruncateMatcherTextEmbedding :exec
TRUNCATE matcher_text_embedding;
