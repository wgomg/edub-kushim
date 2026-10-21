-- +goose Up
-- the matcher's document-text embedding cache: one pooled vector per chunked input text, keyed by the
-- text's sha256 and the embedding stack's fingerprint; derived data, safe to drop and never read by any
-- other subsystem
CREATE TABLE matcher_text_embedding (
    fingerprint text NOT NULL,
    input_hash bytea NOT NULL,
    embedding bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (fingerprint, input_hash)
);

-- +goose Down
DELETE FROM system_signal WHERE name = 'matcher_text_embedding_generation';
DROP TABLE matcher_text_embedding;
