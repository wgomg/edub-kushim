-- +goose Up
CREATE TABLE document_type (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE people_type (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE people (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_native TEXT,
    normalized_name TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tag (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "user" (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT,
    api_key_hash TEXT UNIQUE,
    api_key_prefix TEXT,
    api_key_created_at TIMESTAMPTZ,
    role TEXT NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE task (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    batch_id TEXT,
    payload JSONB,
    result JSONB,
    dedup_key TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT,
    attempts INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE document (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    md5_checksum TEXT NOT NULL,
    sha512_checksum TEXT UNIQUE NOT NULL,
    mime_type TEXT NOT NULL,
    file_size BIGINT NOT NULL,
    page_count INTEGER NOT NULL DEFAULT 0,
    word_count INTEGER NOT NULL DEFAULT 0,
    char_count INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'und',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    modified_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    document_type_id BIGINT NOT NULL DEFAULT 1 REFERENCES document_type(id),
    original_path TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    text_content TEXT
);

CREATE TABLE document_people (
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    people_id BIGINT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    people_type_id BIGINT NOT NULL REFERENCES people_type(id),
    PRIMARY KEY (document_id, people_id, people_type_id)
);

CREATE TABLE document_tag (
    document_id BIGINT NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    tag_id BIGINT NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
    PRIMARY KEY (document_id, tag_id)
);

CREATE TABLE saved_search (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    filter_json TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE batch (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'unknown',
    status TEXT NOT NULL DEFAULT 'queued',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE batch_owner (
    batch_id TEXT PRIMARY KEY REFERENCES batch(id),
    owner_id TEXT NOT NULL,
    pid BIGINT NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orphaned_file (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_key TEXT NOT NULL,
    document_key_type TEXT NOT NULL CHECK (document_key_type IN ('uuid', 'dbid')),
    file_path TEXT NOT NULL,
    original_path TEXT NOT NULL,
    source_dir TEXT NOT NULL CHECK (source_dir IN ('originals', 'processed')),
    file_size BIGINT NOT NULL DEFAULT 0,
    mime_type TEXT NOT NULL DEFAULT 'application/pdf',
    detected_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'deleted', 'restored', 'reingested')),
    action_at TIMESTAMPTZ,
    action_type TEXT CHECK (action_type IN ('delete', 'restore', 'move_to_inbox'))
);

CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_task_type ON task(task_type);
CREATE INDEX idx_task_batch ON task(batch_id);
CREATE INDEX idx_task_batch_status ON task(batch_id, status);
CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
CREATE INDEX idx_task_pending_type ON task(task_type, created_at) WHERE status = 'pending';
CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
CREATE INDEX idx_document_md5 ON document(md5_checksum);
CREATE INDEX idx_document_sha512 ON document(sha512_checksum);
CREATE INDEX idx_document_created ON document(created_at);
CREATE INDEX idx_document_doctype ON document(document_type_id);
CREATE INDEX idx_document_people_doc ON document_people(document_id);
CREATE INDEX idx_document_people_people ON document_people(people_id);
CREATE INDEX idx_document_tag_doc ON document_tag(document_id);
CREATE INDEX idx_document_tag_tag ON document_tag(tag_id);
CREATE INDEX idx_people_normalized_name ON people(normalized_name);
CREATE INDEX idx_batch_status ON batch(status);
CREATE INDEX idx_batch_owner_owner ON batch_owner(owner_id);
CREATE INDEX idx_batch_owner_heartbeat ON batch_owner(last_heartbeat);
CREATE INDEX idx_orphaned_status ON orphaned_file(status);
CREATE INDEX idx_orphaned_detected ON orphaned_file(detected_at);

-- +goose Down
DROP TABLE IF EXISTS orphaned_file;
DROP TABLE IF EXISTS batch_owner;
DROP TABLE IF EXISTS batch;
DROP TABLE IF EXISTS document_tag;
DROP TABLE IF EXISTS document_people;
DROP TABLE IF EXISTS document;
DROP TABLE IF EXISTS task;
DROP TABLE IF EXISTS saved_search;
DROP TABLE IF EXISTS "user";
DROP TABLE IF EXISTS tag;
DROP TABLE IF EXISTS people;
DROP TABLE IF EXISTS people_type;
DROP TABLE IF EXISTS document_type;
