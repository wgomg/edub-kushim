-- +goose Up
-- the reduction cache: one reduced document text per source text hash, reducer fingerprint and resolved
-- target word count; derived data, safe to drop and never read by any other subsystem
CREATE TABLE text_reduction (
    fingerprint text NOT NULL,
    input_hash bytea NOT NULL,
    target_words integer NOT NULL,
    reduced_text text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (fingerprint, input_hash, target_words)
);

-- +goose Down
DELETE FROM system_signal WHERE name = 'text_reduction_generation';
DROP TABLE text_reduction;
