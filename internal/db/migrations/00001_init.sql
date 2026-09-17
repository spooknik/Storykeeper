-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin','user')),
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    csrf_token   TEXT NOT NULL,
    device_id    TEXT NOT NULL,
    device_name  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions(user_id);

CREATE TABLE libraries (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    path       TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

-- Rows exist only for restricted libraries. A library with no rows is visible to all users.
CREATE TABLE library_access (
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (library_id, user_id)
);

CREATE TABLE books (
    id             INTEGER PRIMARY KEY,
    library_id     INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    folder_path    TEXT NOT NULL,            -- relative to library root; '' for a root-level single file
    title          TEXT NOT NULL,
    subtitle       TEXT NOT NULL DEFAULT '',
    authors        TEXT NOT NULL DEFAULT '[]', -- JSON array of strings
    narrators      TEXT NOT NULL DEFAULT '[]', -- JSON array of strings
    series         TEXT NOT NULL DEFAULT '',
    series_seq     TEXT NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    published_year INTEGER,
    language       TEXT NOT NULL DEFAULT '',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    cover_path     TEXT NOT NULL DEFAULT '',  -- relative to data dir, '' if none
    asin           TEXT NOT NULL DEFAULT '',
    isbn           TEXT NOT NULL DEFAULT '',
    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    scan_hash      TEXT NOT NULL DEFAULT '',
    UNIQUE (library_id, folder_path)
);
CREATE INDEX books_title_idx ON books(title);

CREATE TABLE book_files (
    id          INTEGER PRIMARY KEY,
    book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,             -- 0-based play order
    rel_path    TEXT NOT NULL,                -- relative to the book folder
    size        INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    codec       TEXT NOT NULL DEFAULT '',
    bitrate     INTEGER NOT NULL DEFAULT 0,
    mtime       INTEGER NOT NULL,
    UNIQUE (book_id, idx),
    UNIQUE (book_id, rel_path)
);

-- Chapter times are on the book's virtual timeline (sum of preceding file durations + offset).
CREATE TABLE chapters (
    id       INTEGER PRIMARY KEY,
    book_id  INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    idx      INTEGER NOT NULL,
    title    TEXT NOT NULL,
    start_ms INTEGER NOT NULL,
    end_ms   INTEGER NOT NULL,
    UNIQUE (book_id, idx)
);

CREATE TABLE progress (
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    file_index  INTEGER NOT NULL DEFAULT 0,
    seq         INTEGER NOT NULL DEFAULT 0,   -- monotonic per (user, book); server-assigned
    listened_at INTEGER NOT NULL,             -- server clock ms, age-corrected
    received_at INTEGER NOT NULL,             -- server clock ms
    device_id   TEXT NOT NULL,
    device_name TEXT NOT NULL DEFAULT '',
    finished    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, book_id)
);
CREATE INDEX progress_user_listened_idx ON progress(user_id, listened_at DESC);

CREATE TABLE progress_log (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL,
    book_id     INTEGER NOT NULL,
    position_ms INTEGER NOT NULL,
    device_id   TEXT NOT NULL,
    listened_at INTEGER NOT NULL,
    received_at INTEGER NOT NULL,
    accepted    INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX progress_log_received_idx ON progress_log(received_at);

CREATE TABLE bookmarks (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX bookmarks_user_book_idx ON bookmarks(user_id, book_id);

-- +goose Down
DROP TABLE bookmarks;
DROP TABLE progress_log;
DROP TABLE progress;
DROP TABLE chapters;
DROP TABLE book_files;
DROP TABLE books;
DROP TABLE library_access;
DROP TABLE libraries;
DROP TABLE sessions;
DROP TABLE users;
