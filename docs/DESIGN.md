# Storykeeper design

Snapshot of the approved plan (2026-09-17). The plan file is the source of truth while the build is in progress; this copy is what agents and future contributors read.


## Context

Audiobookshelf works, but three things keep breaking for the owner: playback progress
does not reliably sync between devices, the iOS app has been a buggy beta for years, and
large uploads through the web UI often fail. Storykeeper is a from-scratch replacement
built around those three problems. The PWA is the only client, including on iOS, so it
must be first-class: installable, lock-screen controls, survives backgrounding, and
never loses your place.

Repo: `C:\Users\Steven\Documents\GitHub\Storykeeper` (empty, not yet a git repo).

## Decisions (settled with the owner)

| Area | Decision |
| --- | --- |
| Backend | Go 1.26, SQLite, single static binary, Docker image |
| Frontend | SvelteKit PWA, built with `adapter-static` and embedded in the Go binary |
| Users | Multi-user, admin role manages users and libraries |
| Library | Scan existing folders in place + upload through the web UI |
| Network | Tailscale/LAN normally, sometimes public behind a reverse proxy, so harden for public |
| Sync rule | Most recent listen wins, using server-assigned, age-corrected timestamps |
| Offline downloads | Phase 2. Phase 1 designs the storage layer so it can be added |
| Migration | None. Start fresh, metadata from file tags and online providers |
| Scope | Audiobooks only. No podcasts, no ebooks, no transcoding in phase 1 |

## Architecture

```
┌──────────────┐  HTTPS   ┌────────────────────────────────────────────┐
│ SvelteKit PWA│◄────────►│ Go binary                                  │
│ (any device) │  SSE     │  ├ REST API  (/api/v1/...)                 │
│              │◄─────────│  ├ SSE hub   (/api/v1/events)              │
│ service      │  range   │  ├ tus upload (/api/v1/upload/)            │
│ worker +     │◄─────────│  ├ Scanner   (fsnotify + periodic walk)     │
│ single Audio │          │  ├ Static    (embedded SvelteKit build)     │
│ element      │          │  └ SQLite    (WAL, one writer, N readers)   │
└──────────────┘          └────────────────────────────────────────────┘
                          ┌─────────────────┐
                          │ /library (bind) │  audio files stay in place
                          │ /data (bind)    │  sqlite db, covers, tus tmp
                          └─────────────────┘
```

Single process, single binary, no Redis/Postgres/queue. SQLite in WAL mode is plenty
for a household plus a few friends.

### Go libraries (verified 2026-09-17)

| Need | Pick | Why |
| --- | --- | --- |
| SQLite | `modernc.org/sqlite` | Pure Go, `database/sql`, cross-compiles with no C toolchain |
| Migrations | `github.com/pressly/goose/v3` + `embed.FS` | SQL files embedded, no CLI at runtime |
| Router | stdlib `net/http.ServeMux` (1.22+ patterns) + small middleware chain | No dependency; move to chi only if groups sprawl |
| Tags, duration, art | `go.senan.xyz/taglib` | TagLib 2 via wazero, pure Go, MP3/M4A/M4B/FLAC/OGG |
| Chapters | `ffprobe -show_chapters` (optional, if present in image) | taglib does not expose MP4 chapters |
| Cover resize | `golang.org/x/image/draw` (CatmullRom) + `x/image/webp` | Pure Go |
| Uploads | `github.com/tus/tusd/v2/pkg/handler` (v2.10.1) | See Uploads section |
| Watcher | `github.com/fsnotify/fsnotify` | |
| Passwords | `golang.org/x/crypto/argon2` | |
| SSE / WebSocket | stdlib only (SSE). If bidirectional ever needed: `github.com/coder/websocket` | gorilla is archived |

## Repository layout

```
Storykeeper/
├── cmd/storykeeper/main.go        entrypoint, flags/env, embeds web build
├── internal/
│   ├── api/                       HTTP handlers, one file per resource
│   ├── auth/                      sessions, argon2id, roles, rate limit
│   ├── db/                        goose migrations (embedded .sql), queries
│   ├── library/                   scanner, folder → book grouping, tag reading
│   ├── media/                     serve.go: range-correct file serving; covers
│   ├── metadata/                  Audnexus/Audible lookup (admin action)
│   ├── progress/                  store.go: write rule, seq, 409 conflict
│   ├── events/                    sse.go: per-user hub, ring buffer, pings
│   └── upload/                    tus.go: embedded tusd, completion hook
├── web/                           SvelteKit app (adapter-static)
│   └── src/lib/player/            machine.ts, mediasession.ts, reporter.ts
├── Dockerfile                     node build → go build → distroless/alpine (+ffprobe)
├── docker-compose.yml
└── docs/                          API.md, DESIGN.md (this memo), PROXY.md
```

## Data model (SQLite)

```sql
users          (id, username, password_hash, role ENUM('admin','user'), created_at)
sessions       (id, user_id, token_hash, device_id, device_name, created_at,
                last_seen_at, expires_at)
libraries      (id, name, path, created_at)
library_access (library_id, user_id)          -- rows only for restricted libraries
books          (id, library_id, folder_path, title, subtitle, authors, narrators,
                series, series_seq, description, published_year, language,
                duration_ms, cover_path, asin, isbn, added_at, updated_at, scan_hash)
book_files     (id, book_id, rel_path, size, duration_ms, codec, bitrate, track_no, mtime)
chapters       (id, book_id, idx, title, start_ms, end_ms)   -- on the virtual timeline
progress       (user_id, book_id, position_ms, duration_ms, file_index,
                seq INTEGER,            -- monotonic per (user, book), server-assigned
                listened_at INTEGER,    -- server clock ms, age-corrected (see Sync)
                received_at INTEGER,    -- server clock ms, arrival time
                device_id, device_name, finished BOOLEAN,
                PRIMARY KEY (user_id, book_id))
progress_log   (id, user_id, book_id, position_ms, device_id, listened_at, received_at)
                -- append-only, pruned at 30 days; answers "who moved my position"
bookmarks      (id, user_id, book_id, position_ms, note, created_at)
```

Chapters and durations are computed at scan time and cached. The player never probes
media on the client.

## Key behaviours

### Library scanner (`internal/library`)
- One book = one folder, or one `.m4b` at the library root. Multi-file folders are
  ordered by disc/track tag, then natural filename sort.
- Tags, length and embedded art via taglib. Chapters via ffprobe when available.
- `scan_hash` = hash of (rel paths, sizes, mtimes); unchanged books are skipped.
- fsnotify on library roots with a 5 s debounce, a full walk every 12 h, and an
  admin "rescan" button.
- Metadata match: tags first. Audnexus lookup by ASIN is an admin action, never an
  automatic overwrite. Covers resized to 600 px + 200 px thumb under `/data/covers`.
- Opus files are accepted but flagged "may not play on iOS < 18.4".

### Media serving (`internal/media/serve.go`)
- Use `http.ServeContent`. Do not hand-roll range handling. Safari probes with
  `Range: bytes=0-1` and refuses to play on a 200 instead of a 206.
- Auth middleware on `/media/*` must never redirect or return a JSON 401 to a range
  request; it checks the session cookie and returns a bare 401.
- Correct `Content-Type` per extension (audio/mp4 for m4b, audio/mpeg, audio/ogg).
- The service worker never intercepts `/media/*` in phase 1.
- Multi-file books: the client swaps `src` on the same element at file boundaries.
  Book position = sum of prior file durations + `currentTime`.

### Playback engine (`web/src/lib/player/machine.ts`)
Constraints from iOS (verified against WebKit bug tracker and Apple forums):
- Background audio on lock screen only stays alive for an `HTMLMediaElement`. Web
  Audio and MSE are suspended when backgrounded. So: **one `HTMLAudioElement`,
  created on the first user gesture, reused forever, never recreated.** Once
  gesture-blessed, later `src` swaps and `play()` calls are allowed.
- Set `navigator.audioSession.type = 'playback'` when available (iOS 17+), otherwise
  audio follows the ringer switch.
- Media Session: metadata + handlers for play, pause, seekbackward, seekforward,
  seekto, previoustrack, nexttrack (skip buttons mapped to ±30 s). Call
  `setPositionState` on every play/pause/seek/rate change.
- `playbackRate` 0.5–3.0 with `preservesPitch` on. Sleep timer, chapters, bookmarks.

Open WebKit bugs to design around:
- **295518 (iOS 26, status NEW):** after reopening a home-screen PWA, `play()`
  resolves but produces no sound. Mitigation: re-assign `src` (with `#t=position`)
  immediately before `play()` on every resume-from-background.
- **261858:** when a file ends while backgrounded, the next file does not start and
  lock-screen controls go dead. Mitigation: in `ended`, swap `src` and call `play()`
  synchronously; on becoming visible, detect "ended but not playing" and show a
  one-tap resume.
- Lock-screen pause longer than ~30 s kills the audio session until foregrounded.
  Treated as unfixable; the journal below makes it recoverable.

State machine: `idle → loading → playing ⇄ paused → ended`, plus `suspended`.
1. **Local journal**: every 5 s while playing and on pause/seek/rate/ended, write
   `{bookId, fileIdx, positionMs, rate, playing, clientTs, serverSeq}` to
   `localStorage` (synchronous, survives iOS killing the page).
2. **Server heartbeat**: every 15 s while playing, plus immediately on pause, seek,
   rate change, ended, file change. On `visibilitychange→hidden` use
   `fetch(..., {keepalive: true})`; on `pagehide` use `navigator.sendBeacon`.
3. **On visible or cold launch**: read journal, fetch server progress, reconcile (see
   Sync), then re-arm the element (`src` reset + `load()`).
4. **Stall watchdog**: `playing` but `currentTime` unchanged for 4 s → `suspended`,
   journal, show "Tap to resume" (a fresh gesture may be required).

### Progress sync (`internal/progress/store.go` + `web/src/lib/player/reporter.ts`)
Client sends `{book_id, position_ms, file_index, client_listened_at, client_now,
base_seq, device_id}`. Server computes
`listened_at = server_now − (client_now − client_listened_at)`, which makes device
clocks irrelevant while letting an offline device honestly report an old listen.

Write rule:
```sql
UPDATE progress SET ... WHERE user_id=? AND book_id=? AND listened_at < :new
```
Zero rows changed → respond 409 with the current record and the client adopts it.
Ties within 2 s prefer the larger position. Reject reports older than 30 days. `seq`
increments on every accepted write and is returned. Every accepted write is appended
to `progress_log` and fanned out over SSE to the user's other sessions.

Reconciliation on foreground:
- Local audio still advancing since hide → local is newest, push it.
- Local paused/stalled and server `listened_at` newer than journal → adopt server
  position silently, toast "Resumed from iPhone at 2:14:07" with Undo (Undo re-pushes
  local with `client_listened_at = now`).
- Journal newer than server (was offline while listening) → push with original
  `client_listened_at`; the server's age correction decides.

Worked case: device B listens offline Tue 1:00 to 3:00:00. Device A listens online
Tue 2:00 to 3:30:00. B reconnects Wed: server derives `listened_at ≈ Tue 1:00`,
older than A's, returns 409, B jumps to 3:30:00. This is exactly the clobber that
Audiobookshelf gets wrong.

Library list is rendered from a local cache tagged with `max_seq`; on visible it
fetches `/api/v1/progress?since=max_seq` before painting positions.

### Events (`internal/events/sse.go`)
- **SSE, not WebSocket.** Both die silently when iOS backgrounds a PWA, so pick the
  cheaper reconnect: `EventSource` auto-reconnects with `Last-Event-ID`, needs no
  upgrade headers, passes every proxy and Cloudflare, and carries cookies. There is
  also an open iPadOS 26 bug closing LAN `ws://` in PWAs. Bidirectional is not needed
  since the heartbeat is a plain PUT.
- One stream per session at `/api/v1/events`. Events: `progress`, `library`
  (scan/upload done), and a `: ping` comment every 20 s. Client: no ping for 45 s →
  tear down and reconnect; on `visibilitychange→visible` always close and reopen
  without waiting for `onerror`.
- Per-user 5-minute ring buffer keyed by `seq` for replay; if `Last-Event-ID` is older
  than the buffer, send `event: resync` and the client refetches progress.
- Send `X-Accel-Buffering: no`; docs note `proxy_buffering off` for nginx.
- Client ignores events carrying its own `device_id`.

### Uploads (`internal/upload/tus.go`)
- **tus protocol** via embedded `tusd/v2` handler with `filestore` + `filelocker`,
  mounted at `/api/v1/upload/` behind auth middleware. Appends in place, so no
  assembly step and no 2× disk. `MaxSize` 8 GiB, incomplete uploads expire at 7 days.
- `RespectForwardedHeaders = true` so the returned `Location` uses the public host.
- `PreUploadCreateCallback` enforces per-user quota, allowed extensions, and target
  library (from upload metadata). `NotifyCompleteUploads` goroutine moves the file
  into `<library>/<Author>/<Title>/` and triggers a scan of that folder.
- Client: `tus-js-client`, `chunkSize` 50 MiB (under Cloudflare's 100 MB body cap),
  `retryDelays: [0, 1000, 3000, 5000, 10000, 30000]`, default localStorage URL
  storage, `removeFingerprintOnSuccess: true`. Extra metadata: SHA-256 of the first
  1 MiB to guard fingerprint collisions.
- Resume after reload: user re-picks the file; the fingerprint (name+size+mtime+type)
  finds the stored upload URL and `HEAD` returns the server offset. iOS backgrounding
  kills the in-flight chunk only; the UI tells users to keep the app open during
  large uploads.
- `docs/PROXY.md` ships snippets: nginx (`client_max_body_size 0`,
  `proxy_request_buffering off`, `proxy_read_timeout 300s`, forward `X-Forwarded-*`),
  Caddy (`transport http { read_timeout 5m }`, no `request_body max_size` below chunk
  size), Cloudflare (keep chunk < 100 MB, or route uploads over Tailscale only).

### Auth and hardening (public exposure)
- Argon2id hashes, opaque session tokens in HttpOnly SameSite=Lax cookies, 30-day
  sliding expiry, per-user session list with revoke.
- Login rate limit per IP and per username. CSRF: SameSite + required custom header on
  mutating requests. The sendBeacon progress endpoint is a POST alias that validates
  the session cookie and a per-session token in the body.
- Admin-only: user CRUD, library CRUD, rescan, metadata match, server settings.
- Security headers, no directory listing, media and cover endpoints check library
  access. First admin bootstrapped from env on first run.

### PWA shell
- `@vite-pwa/sveltekit` for manifest + service worker. Cache the app shell and covers
  only, never `/media/*`. `display: standalone`, icons, theme colour.
- Call `navigator.storage.persist()` at install. Home-screen apps are exempt from
  Safari's 7-day script-storage purge; quota is up to 60% of disk.

## Phases

### Phase 0 — Prove the risky assumption (get one m4b playing on iPhone)
- `git init`, Go module, SvelteKit scaffold, `go:embed` of the build, Dockerfile,
  compose file, goose migrations, users + sessions, login, env-bootstrapped admin.
- Scanner for one library path with taglib duration. Book list API.
- `http.ServeContent` media route with cookie auth. Bare player with the single
  reused element, Media Session, `audioSession.type='playback'`, and the `src` reset
  before `play()` mitigation.
- **Exit gate:** installed PWA on iPhone plays an m4b, screen locked, audio continues
  10+ minutes, lock-screen skip works, close and reopen after an hour and it plays
  again. If iOS 26 bug 295518 defeats the mitigation, stop and reassess before
  building anything else.

### Phase 1 — Core product
- Library UI: grid, book page, series, authors, search, continue-listening shelf.
- Full player: chapters, multi-file boundaries, speed, sleep timer, bookmarks.
- Journal + heartbeat + foreground reconciliation. Progress store with 409 rule.
  SSE hub with replay. Multi-device test matrix (below).
- tus uploads with progress UI, pause/resume, reload survival, completion → scan.
- Multi-user + admin pages, library access control, session management.
- Covers, metadata edit, Audnexus lookup.
- PWA manifest, service worker, install prompt. Public-exposure hardening.
- `docs/PROXY.md`, `docs/API.md`.

### Phase 2 — Offline
- Download a whole book into OPFS from a worker (`createSyncAccessHandle` is
  worker-only), with quota check and progress UI.
- Service worker serves `/media/*` from OPFS when present, reconstructing 206
  responses for range requests; otherwise passes straight to network.
- Progress writes queue in IndexedDB while offline and flush with original
  `client_listened_at`, so the existing age-corrected rule handles them unchanged.
- Manage-downloads page with sizes and eviction warnings. Optional server-side
  Opus→AAC transcode for old iOS.

### Not planned
- Podcasts, ebooks, OIDC, per-library metadata providers, Audiobookshelf import.

## Execution strategy (frugal-fable routing)

Fable owns decomposition, architecture, shared-file coordination, integration and final
review. Everything else runs in background agents that write to `.frugal-fable/<task>/`
(gitignored) and return only a path, a three-line summary and a confidence level.
Fable reads outputs on demand at review time, never up front.

### Ground rules for every delegated slice
- Handoff packet with zero assumed chat context: objective, repo path, in-scope files,
  out-of-scope files, output location, verification command, stop conditions (code
  doesn't match the brief, a command fails after one retry, needs out-of-scope files).
- Agents never touch shared files without Fable: `go.mod`, migrations, `main.go`
  route table, `web/src/lib/api/types.ts`, `docker-compose.yml`. Fable writes those
  first so agents build against a fixed contract.
- Accept criteria: build and relevant tests pass, and Fable reviews the diff for
  anything Opus-tier or security-relevant.
- Fan-out is capped at ~6 concurrent agents per wave, with a synthesis reserve kept.
- A heartbeat cron (`13,58 * * * *`) is created at the start of each working session
  and deleted when the waves settle, per the owner's standing preference.

### Phase 0 — mostly Fable, tightly coupled
| Slice | Owner | Harness |
| --- | --- | --- |
| Repo skeleton, `go.mod`, migrations, route table, API type contract | Fable | direct |
| SvelteKit scaffold + vite-pwa config + adapter-static + `go:embed` wiring | Sonnet | Agent |
| Scanner with taglib, book list API + tests | Sonnet | Agent |
| Media serve route with cookie auth + range tests | Opus | Agent (security-relevant) |
| Player `machine.ts` with the iOS mitigations | Fable | direct (highest ambiguity) |
| Dockerfile + compose + README quickstart | Sonnet | Agent |
| iPhone exit gate | Owner + Fable | manual |

### Phase 1 — a Workflow per wave, agents outside Fable's context
Wave A (independent, after Fable fixes the API contract):
| Slice | Owner |
| --- | --- |
| Progress store write rule + 409 + `progress_log` + table-driven tests | Opus |
| SSE hub with ring buffer, replay, pings + tests | Opus |
| tus handler, completion hook, `PreUploadCreateCallback` + tests | Sonnet |
| Auth: argon2id, sessions, rate limit, CSRF header check + tests | Opus |
| Admin CRUD handlers (users, libraries, access) + tests | Sonnet |
| Cover extraction and resize + tests | Sonnet |
| Audnexus metadata client + tests against recorded fixtures | Sonnet |

Wave B (frontend, after Wave A ships the endpoints):
| Slice | Owner |
| --- | --- |
| Library grid, book page, series, authors, search, continue shelf | Sonnet |
| Upload page with tus-js-client, resume UI | Sonnet |
| Admin pages | Sonnet |
| Reporter + reconciliation + EventSource client (`reporter.ts`) | Fable |
| Chapters, multi-file boundaries, speed, sleep timer, bookmarks in the player | Opus |
| Playwright smoke suite | Sonnet |

Wave C (verification, Haiku and Sonnet):
| Slice | Owner |
| --- | --- |
| Run `go test`, `npm run check`, Playwright; reduce logs to failures only | Haiku |
| Adversarial review of sync rule and auth against the plan's threat list | Sonnet |
| `docs/PROXY.md`, `docs/API.md` drafted from the route table | Haiku |
| Sync matrix and upload tests on real devices | Owner + Fable |

Fable integrates each wave, resolves conflicting reports, and runs the final review.
No second gap-fill round unless the owner asks.

### Phase 2 — same pattern
OPFS download worker and SW range responder go to Opus (correctness-critical, hard to
test). IndexedDB offline queue and manage-downloads page go to Sonnet. Fable designs the
SW/OPFS interface first and reviews both diffs.

## Verification

- **Go**: `go vet ./... && go test ./...`. Table-driven tests for scanner grouping,
  the progress write rule (including the offline-clobber worked case above, tie
  handling, and the 30-day reject), SSE ring-buffer replay, tus completion hook.
- **Web**: `npm run check && npm run build`. Playwright smoke: log in, open a book,
  press play, assert a progress PUT fires and a 409 is handled by seeking.
- **Media**: `curl -r 0-1 -sI` on a book URL → 206, `Content-Range`, `Accept-Ranges`.
  Unauthenticated → bare 401, never a redirect.
- **iOS gate** (Phase 0 exit) as listed above, repeated on each iOS point release.
- **Sync matrix**: phone + desktop. Play on A, pause, open B: B shows A's position
  within 2 s. Play on B, background A 5 min, resume A: A jumps to B, no clobber, toast
  shown. Airplane-mode A, listen 10 min, reconnect: A wins because its listen is
  newer. Airplane-mode A, do nothing, listen on B, reconnect A: B wins.
- **Upload**: 2 GB m4b over Wi-Fi, reload mid-way, re-pick file, resume completes,
  SHA-256 matches source. Repeat through Caddy and through nginx with
  `proxy_request_buffering off`. Repeat through a Cloudflare tunnel with 50 MiB chunks.

## Sources behind the platform claims

- WebKit 295518 (iOS 26 PWA audio on reopen, NEW): https://bugs.webkit.org/show_bug.cgi?id=295518
- WebKit 261858 (next track in background): https://bugs.webkit.org/show_bug.cgi?id=261858
- WebKit 237878 (Web Audio suspended in background): https://bugs.webkit.org/show_bug.cgi?id=237878
- Safari 26 installability: https://webkit.org/blog/17333/webkit-features-in-safari-26-0/
- Storage quota and 7-day exemption: https://webkit.org/blog/14403/updates-to-storage-policy/ and https://webkit.org/blog/10218/full-third-party-cookie-blocking-and-more/
- Safari range requests: https://philna.sh/blog/2018/10/23/service-workers-beware-safaris-range-request/
- tusd as a package: https://tus.github.io/tusd/advanced-topics/usage-package/
- Cloudflare 100 MB body limit: https://developers.cloudflare.com/workers/platform/limits
- go-taglib: https://github.com/sentriz/go-taglib
