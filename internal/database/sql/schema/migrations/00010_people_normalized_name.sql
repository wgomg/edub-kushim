-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

DROP TABLE IF EXISTS people_new;
CREATE TABLE people_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_native TEXT,
    normalized_name TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO people_new (id, name, name_native, created_at, normalized_name)
SELECT id, name, name_native, created_at, NULL FROM people;

DROP TABLE people;
ALTER TABLE people_new RENAME TO people;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

DROP TABLE IF EXISTS people_old;
CREATE TABLE people_old (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_native TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO people_old (id, name, name_native, created_at)
SELECT id, name, name_native, created_at FROM people;

DROP TABLE IF EXISTS people;
ALTER TABLE people_old RENAME TO people;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd
