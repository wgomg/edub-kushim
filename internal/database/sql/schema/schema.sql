CREATE TABLE IF NOT EXISTS document_type (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS people_type (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS people (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_native TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tag (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT,
    api_key TEXT UNIQUE NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS task (
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

CREATE TABLE IF NOT EXISTS document (
    id INTEGER PRIMARY KEY,
    document_id TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    md5_checksum TEXT NOT NULL,
    sha512_checksum TEXT UNIQUE NOT NULL,
    mime_type TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    page_count INTEGER NOT NULL DEFAULT 0,
    word_count INTEGER NOT NULL DEFAULT 0,
    char_count INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'und',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    document_type_id INTEGER NOT NULL DEFAULT 1,
    original_path TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    text_content TEXT,
    FOREIGN KEY (document_type_id) REFERENCES document_type(id)
);

CREATE TABLE IF NOT EXISTS document_people (
    document_id INTEGER NOT NULL,
    people_id INTEGER NOT NULL,
    people_type_id INTEGER NOT NULL,
    PRIMARY KEY (document_id, people_id, people_type_id),
    FOREIGN KEY (document_id) REFERENCES document(id) ON DELETE CASCADE,
    FOREIGN KEY (people_id) REFERENCES people(id) ON DELETE CASCADE,
    FOREIGN KEY (people_type_id) REFERENCES people_type(id)
);

CREATE TABLE IF NOT EXISTS document_tag (
    document_id INTEGER NOT NULL,
    tag_id INTEGER NOT NULL,
    PRIMARY KEY (document_id, tag_id),
    FOREIGN KEY (document_id) REFERENCES document(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tag(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS saved_search (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    filter_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS document_fts USING fts5(
    document_id UNINDEXED,
    title,
    content,
    tokenize = 'unicode61'
);

CREATE TRIGGER IF NOT EXISTS document_ai AFTER INSERT ON document
BEGIN
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;

CREATE TRIGGER IF NOT EXISTS document_ad AFTER DELETE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
END;

CREATE TRIGGER IF NOT EXISTS document_au AFTER UPDATE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;

CREATE INDEX IF NOT EXISTS idx_task_status ON task(status);
CREATE INDEX IF NOT EXISTS idx_task_type ON task(task_type);
CREATE INDEX IF NOT EXISTS idx_task_batch ON task(batch_id);
CREATE INDEX IF NOT EXISTS idx_task_batch_status ON task(batch_id, status);
CREATE INDEX IF NOT EXISTS idx_task_pending ON task(created_at) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_document_md5 ON document(md5_checksum);
CREATE INDEX IF NOT EXISTS idx_document_sha512 ON document(sha512_checksum);
CREATE INDEX IF NOT EXISTS idx_document_created ON document(created_at);
CREATE INDEX IF NOT EXISTS idx_document_people_doc ON document_people(document_id);
CREATE INDEX IF NOT EXISTS idx_document_people_people ON document_people(people_id);
CREATE INDEX IF NOT EXISTS idx_document_tag_doc ON document_tag(document_id);
CREATE INDEX IF NOT EXISTS idx_document_tag_tag ON document_tag(tag_id);
