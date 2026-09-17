-- +goose Up
-- A single global counter backs the progress `seq`. Using one counter for every
-- user keeps `seq` monotonic per user across all of their books (so
-- `GET /progress?since=seq` returns every record a client has not yet seen) and
-- keeps issuing new values after progress rows are deleted, which a
-- MAX(seq)-style scheme would not.
CREATE TABLE counters (
    name  TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);

INSERT INTO counters (name, value) VALUES ('progress_seq', 0);

-- +goose Down
DROP TABLE counters;
