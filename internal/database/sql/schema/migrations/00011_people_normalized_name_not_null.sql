-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

CREATE TABLE IF NOT EXISTS people_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_native TEXT,
    normalized_name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO people_new (id, name, name_native, created_at, normalized_name)
SELECT id, name, name_native, created_at, normalized_name FROM people;

DROP TABLE people;
ALTER TABLE people_new RENAME TO people;

CREATE UNIQUE INDEX IF NOT EXISTS idx_people_normalized_name ON people(normalized_name);

PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

CREATE TABLE IF NOT EXISTS people_old (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_native TEXT,
    normalized_name TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO people_old (id, name, name_native, created_at, normalized_name)
SELECT id, name, name_native, created_at, normalized_name FROM people;

DROP TABLE people;
ALTER TABLE people_old RENAME TO people;

DROP INDEX IF EXISTS idx_people_normalized_name;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd
