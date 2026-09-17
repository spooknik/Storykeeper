-- +goose Up
-- Admin metadata edits (PATCH /books/{id}) set this so the scanner does not
-- clobber them the next time it re-derives metadata from file tags.
ALTER TABLE books ADD COLUMN metadata_locked INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE books DROP COLUMN metadata_locked;
