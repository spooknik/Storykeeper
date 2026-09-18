-- +goose Up
-- Per-user playback preferences. One row per user; a missing row means the
-- caller has never saved anything and the API answers with defaults.
CREATE TABLE user_prefs (
    user_id               INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    playback_rate         REAL    NOT NULL DEFAULT 1.0,
    skip_back_sec         INTEGER NOT NULL DEFAULT 30,
    skip_forward_sec      INTEGER NOT NULL DEFAULT 30,
    auto_rewind           INTEGER NOT NULL DEFAULT 1,
    default_sleep_minutes INTEGER NOT NULL DEFAULT 0,
    updated_at            INTEGER NOT NULL
);

-- A per-book playback rate override. NULL means "use the user's default rate".
ALTER TABLE progress ADD COLUMN playback_rate REAL;

-- +goose Down
ALTER TABLE progress DROP COLUMN playback_rate;
DROP TABLE user_prefs;
