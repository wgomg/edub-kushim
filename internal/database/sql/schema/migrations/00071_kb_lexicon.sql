-- +goose Up
-- the offline lexical knowledge base: one row-set per installed source, with provenance on every table.
-- concepts are keyed by the source's own stable identifier so an install is a pure delete-and-insert by
-- source and no surrogate ids need remapping.
CREATE TABLE kb_source (
    source TEXT PRIMARY KEY,
    source_version TEXT NOT NULL,
    license TEXT NOT NULL,
    notice TEXT NOT NULL,
    upstream_url TEXT NOT NULL,
    upstream_sha256 TEXT NOT NULL,
    converter_version TEXT NOT NULL,
    concept_count BIGINT NOT NULL,
    lemma_count BIGINT NOT NULL,
    edge_count BIGINT NOT NULL,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE kb_concept (
    source TEXT NOT NULL REFERENCES kb_source(source) ON DELETE CASCADE,
    concept_key TEXT NOT NULL,
    pos TEXT,
    gloss TEXT,
    PRIMARY KEY (source, concept_key)
);

CREATE TABLE kb_lemma (
    source TEXT NOT NULL REFERENCES kb_source(source) ON DELETE CASCADE,
    concept_key TEXT NOT NULL,
    lemma_norm TEXT NOT NULL,
    PRIMARY KEY (source, concept_key, lemma_norm)
);
CREATE INDEX idx_kb_lemma_norm ON kb_lemma (lemma_norm);

CREATE TABLE kb_edge (
    source TEXT NOT NULL REFERENCES kb_source(source) ON DELETE CASCADE,
    from_concept_key TEXT NOT NULL,
    to_concept_key TEXT NOT NULL,
    family TEXT NOT NULL CHECK (family IN ('exact','similar','broader','narrower','instance','part','antonym','distinct')),
    source_reltype TEXT NOT NULL,
    weight REAL,
    PRIMARY KEY (source, from_concept_key, to_concept_key, source_reltype)
);
CREATE INDEX idx_kb_edge_from ON kb_edge (from_concept_key, family);
CREATE INDEX idx_kb_edge_to ON kb_edge (to_concept_key, family);

-- +goose Down
DROP TABLE IF EXISTS kb_edge;
DROP TABLE IF EXISTS kb_lemma;
DROP TABLE IF EXISTS kb_concept;
DROP TABLE IF EXISTS kb_source;
