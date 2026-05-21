CREATE TABLE document_type (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE author (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tag (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE user (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT,
    api_key TEXT UNIQUE NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE task (
    id INTEGER PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    batch_id TEXT,
    payload JSON,
    result JSON,
    dedup_key TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    error TEXT
);

CREATE TABLE document (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    md5_checksum TEXT NOT NULL,
    sha512_checksum TEXT UNIQUE NOT NULL,
    mime_type TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    document_type_id INTEGER,
    original_path TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    text_content TEXT,
    FOREIGN KEY (document_type_id) REFERENCES document_type(id)
);

CREATE TABLE document_author (
    document_id INTEGER NOT NULL,
    author_id INTEGER NOT NULL,
    PRIMARY KEY (document_id, author_id),
    FOREIGN KEY (document_id) REFERENCES document(id) ON DELETE CASCADE,
    FOREIGN KEY (author_id) REFERENCES author(id) ON DELETE CASCADE
);

CREATE TABLE document_tag (
    document_id INTEGER NOT NULL,
    tag_id INTEGER NOT NULL,
    PRIMARY KEY (document_id, tag_id),
    FOREIGN KEY (document_id) REFERENCES document(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tag(id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE document_fts USING fts5(
    document_id UNINDEXED,
    title,
    content,
    tokenize = 'unicode61'
);

-- Add these triggers after creating the document_fts table
CREATE TRIGGER document_ai AFTER INSERT ON document
BEGIN
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;

CREATE TRIGGER document_ad AFTER DELETE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
END;

CREATE TRIGGER document_au AFTER UPDATE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;

CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_task_type ON task(task_type);
CREATE INDEX idx_task_batch ON task(batch_id);
CREATE INDEX idx_task_batch_status ON task(batch_id, status);
CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';

CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
CREATE INDEX idx_document_md5 ON document(md5_checksum);
CREATE INDEX idx_document_sha512 ON document(sha512_checksum);
CREATE INDEX idx_document_created ON document(created_at);
CREATE INDEX idx_document_author_doc ON document_author(document_id);
CREATE INDEX idx_document_author_author ON document_author(author_id);
CREATE INDEX idx_document_tag_doc ON document_tag(document_id);
CREATE INDEX idx_document_tag_tag ON document_tag(tag_id);
