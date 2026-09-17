# Storykeeper HTTP API (v1)

Base path `/api/v1`. All bodies are JSON. Timestamps are Unix milliseconds from the
**server** clock unless named `client_*`. Go wire types live in `internal/api/types.go`
and the TypeScript mirror in `web/src/lib/api/types.ts`; keep all three in sync.

## Conventions

- **Auth**: cookie `sk_session` (HttpOnly, SameSite=Lax, Secure over HTTPS). Set by
  login, cleared by logout. Sliding 30-day expiry.
- **CSRF**: every mutating request (`POST`/`PUT`/`PATCH`/`DELETE`) under `/api/`
  must send header `X-Storykeeper: 1`. The single exception is the sendBeacon alias
  `POST /progress/{bookId}/beacon`, which carries `csrf_token` in the body.
- **Errors**: `{"error": {"code": "snake_case", "message": "human text"}}` with an
  appropriate 4xx/5xx. `/media/*` never returns JSON; unauthenticated media requests
  get a bare `401` with no body and never a redirect.
- **Roles**: `admin` or `user`. Admin-only routes return `403 forbidden` for users.
- **Library visibility**: a library with no `library_access` rows is visible to all
  users. With rows, only listed users (and admins) can see it or its books/media.

## Auth

| Method | Path | Body | Returns |
| --- | --- | --- | --- |
| POST | `/auth/login` | `{username, password, device_name}` | `{user, session}` + cookie. `401 invalid_credentials` |
| POST | `/auth/logout` | | `204` |
| GET | `/auth/me` | | `{user, session}` |

`session` = `{id, csrf_token, device_id, device_name, created_at, last_seen_at, expires_at}`.
`device_id` is generated per login and is what progress records carry.

## Libraries

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/libraries` | `[Library]`. `path` only present for admins |
| POST | `/libraries` | admin. `{name, path}`. Path must be a directory on the server. `201 Library`, starts a scan |
| DELETE | `/libraries/{id}` | admin. `204`. Cascades books, files, progress |
| POST | `/libraries/{id}/scan` | admin. `202` |

## Books

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/books` | `{items: [BookSummary], total}`. Query: `library`, `q` (title/authors/series/narrators), `sort` = `title` (default) / `author` / `series` / `added` / `recent` / `duration`, `dir` = `asc` (default) / `desc`, `author` / `series` / `narrator` (exact, case-insensitive), `in_progress=1`, `finished=1\|0`, `not_started=1`, `limit` (≤500), `offset`. `sort=series` orders by series name then numeric `series_seq`; `sort=author` uses the first author |
| GET | `/authors` | `[Facet]` = `[{name, book_count}]`, distinct authors across visible books, sorted by name |
| GET | `/series` | `[Facet]` distinct series across visible books |
| GET | `/narrators` | `[Facet]` distinct narrators across visible books |
| GET | `/books/{id}` | `BookDetail` |
| PATCH | `/books/{id}` | admin. Metadata edit. *Wave A* |

`BookSummary`: `{id, library_id, title, subtitle, authors[], narrators[], series,
series_seq, duration_ms, cover_url, added_at, updated_at, progress?}`.
`BookDetail` adds `{description, published_year, language, asin, isbn, files[],
chapters[], bookmarks[]}` where a file is `{index, rel_path, size, duration_ms, codec,
bitrate, url}` and a chapter is `{index, title, start_ms, end_ms}` on the book's
virtual timeline (sum of preceding file durations plus offset).

## Media

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/media/books/{id}/files/{index}` | Audio bytes. Honors `Range`, answers `206` with `Content-Range` and `Accept-Ranges: bytes` |
| GET | `/media/books/{id}/cover?size=600\|200` | Cover image. `404` when the book has none |

## Progress (*Wave A*)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/progress?since={seq}` | `[Progress]` changed since `seq` (0 = all) |
| GET | `/progress/{bookId}` | `Progress` or `404` |
| PUT | `/progress/{bookId}` | body `ProgressReport`. `200 Progress` if accepted, `409 Progress` (the current record) if the stored listen is newer |
| POST | `/progress/{bookId}/beacon` | same body plus `csrf_token`; for `navigator.sendBeacon`. Always `204` |

`ProgressReport` = `{position_ms, file_index, client_listened_at, client_now, base_seq,
finished?, csrf_token?}`. Server derives `listened_at = server_now - (client_now -
client_listened_at)` and applies the most-recent-listen-wins rule.

## Events (*Wave A*)

`GET /events` — `text/event-stream`. Events: `progress` (`Progress`), `library`
(`{library_id, action}`), `resync` (client must refetch). `: ping` comment every 20 s.
Supports `Last-Event-ID` for replay within a 5-minute window.

## Upload (*Wave A*)

`/upload/` is a tus 1.0.0 endpoint (`POST`, `HEAD`, `PATCH`, `DELETE`). Metadata keys:
`filename`, `library_id`, `filetype`. Requires the CSRF header on all methods.

## Users and sessions (*Wave A*)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/users` | admin |
| POST | `/users` | admin. `{username, password, role}` |
| PATCH | `/users/{id}` | admin. `{password?, role?}` |
| DELETE | `/users/{id}` | admin |
| PUT | `/users/{id}/libraries` | admin. `{library_ids: []}` restricted set |
| GET | `/sessions` | own sessions |
| DELETE | `/sessions/{id}` | revoke one of your own sessions |

## Bookmarks (*Wave A*)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/books/{id}/bookmarks` | |
| POST | `/books/{id}/bookmarks` | `{position_ms, note}` |
| DELETE | `/bookmarks/{id}` | |

## Health

`GET /health` → `{"status":"ok"}`. Unauthenticated, for Docker healthchecks.
