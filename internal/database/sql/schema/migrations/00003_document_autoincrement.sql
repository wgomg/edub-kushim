-- +goose Up
CREATE TABLE document_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
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

INSERT INTO document_new (
    id, document_id, title, md5_checksum, sha512_checksum, mime_type,
    file_size, page_count, word_count, char_count, language,
    created_at, modified_at, document_type_id, original_path, storage_path, text_content
)
SELECT
    id, document_id, title, md5_checksum, sha512_checksum, mime_type,
    file_size, page_count, word_count, char_count, language,
    created_at, modified_at, document_type_id, original_path, storage_path, text_content
FROM document;

PRAGMA foreign_keys = OFF;
DROP TABLE document;
ALTER TABLE document_new RENAME TO document;
PRAGMA foreign_keys = ON;

CREATE INDEX IF NOT EXISTS idx_document_md5 ON document(md5_checksum);
CREATE INDEX IF NOT EXISTS idx_document_sha512 ON document(sha512_checksum);
CREATE INDEX IF NOT EXISTS idx_document_created ON document(created_at);

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS document_ai AFTER INSERT ON document
BEGIN
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS document_ad AFTER DELETE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS document_au AFTER UPDATE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;
-- +goose StatementEnd

-- +goose Down
CREATE TABLE document_old (
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

INSERT INTO document_old SELECT * FROM document;
PRAGMA foreign_keys = OFF;
DROP TABLE document;
ALTER TABLE document_old RENAME TO document;
PRAGMA foreign_keys = ON;

CREATE INDEX IF NOT EXISTS idx_document_md5 ON document(md5_checksum);
CREATE INDEX IF NOT EXISTS idx_document_sha512 ON document(sha512_checksum);
CREATE INDEX IF NOT EXISTS idx_document_created ON document(created_at);

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS document_ai AFTER INSERT ON document
BEGIN
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS document_ad AFTER DELETE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS document_au AFTER UPDATE ON document
BEGIN
    DELETE FROM document_fts WHERE document_id = old.id;
    INSERT INTO document_fts(document_id, title, content)
    VALUES (new.id, new.title, COALESCE(new.text_content, ''));
END;
-- +goose StatementEnd
