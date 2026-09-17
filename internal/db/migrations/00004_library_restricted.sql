-- +goose Up
-- Library visibility is now an explicit flag rather than "does this library
-- have any library_access rows". Grants alone no longer restrict a library,
-- so removing the last grant can never turn a private library public.
ALTER TABLE libraries ADD COLUMN restricted INTEGER NOT NULL DEFAULT 0;

-- Every library that was restricted under the old rule (it has at least one
-- grant) keeps that meaning.
UPDATE libraries SET restricted = 1
WHERE EXISTS (SELECT 1 FROM library_access la WHERE la.library_id = libraries.id);

-- +goose Down
ALTER TABLE libraries DROP COLUMN restricted;
