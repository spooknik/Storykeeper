# iOS: resuming from headphones after a long background pause

Status: **open, diagnostic ready for device testing** (2026-09-20). The data-loss
half of this story is fixed. Long-pause headphone resume remains unresolved;
the silent-audio workaround is a hypothesis, not a verified fix. A standalone
comparison page is available below; the production player is unchanged.

## The original report

Playing on an iPhone PWA with AirPods. Paused from the AirPods, came back
later, the app had been closed, and on reopening the book was at 0:00.

## What the server log showed

`progress_log` keeps 30 days of every report, accepted or not. For the book in
question on 2026-09-19 (times local, one device the whole way):

| Time | Position | What it was |
| --- | --- | --- |
| 08:17:00 | 3:09:39 | Last normal 15 s heartbeat |
| 08:17:06 | 3:09:46 | AirPods pause |
| 08:17:26 | 3:09:41 | AirPods play: the 5 s auto-rewind report, then **no heartbeats** |
| 08:18:33 | 0:00 | Position 0 accepted as a fresh listen |
| 08:18:39 on | 0:03 | The restored player after the relaunch |

Two separate things happened:

1. The play at 08:17:26 reached the page but produced no audio. iOS had already
   torn the audio session down during the 20 s pause. The element accepted
   `play()` and stayed silent; the engine had no watchdog for that state.
2. On opening the app 67 s later, the engine read `currentTime` from the element
   and got 0, wrote it to the journal, and reported it with a fresh timestamp.
   The server's "most recent listen wins" rule accepted it.

## Why `currentTime` read 0

When iOS reclaims the media (GPU) process of a backgrounded page, WebKit
re-runs the media element load algorithm on the element without the page asking
(`HTMLMediaElement::mediaPlayerReloadAndResumePlaybackIfNeeded` →
`prepareForLoad`). That sets `readyState` to `HAVE_NOTHING`, queues `emptied`
and `timeupdate`, sets `paused` **without** firing `pause`, and resets the
official playback position to 0. A seek back to the old position is queued but
only lands once metadata reloads, which a paused background page may never do.
`MediaPlayerPrivateAVFoundationObjC::currentTime()` returns zero whenever the
player item is gone. Sources:

- [HTMLMediaElement.cpp](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/html/HTMLMediaElement.cpp)
  (`prepareForLoad`, `mediaPlayerReloadAndResumePlaybackIfNeeded`)
- [MediaPlayerPrivateAVFoundationObjC.mm](https://github.com/WebKit/webkit/blob/main/Source/WebCore/platform/graphics/avfoundation/objc/MediaPlayerPrivateAVFoundationObjC.mm)
  (`currentTime()`)
- [RemoteMediaPlayerManager.cpp](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/WebProcess/GPU/media/RemoteMediaPlayerManager.cpp)
  (`gpuProcessConnectionDidClose` triggers the reload)

## What was fixed (shipped)

Commits `bca472c` and `1bce1bb`, all in `web/src/lib/player/machine.svelte.ts`,
tests in `web/e2e/reset.spec.ts`:

- `syncPosition()` only reads `currentTime` when `readyState >= HAVE_METADATA`
  and the element is not marked stale. Otherwise the tracked `positionMs`
  stands. This closes the hidden, visible, pause and tick paths at once.
- An `emptied` the engine did not cause (it flags its own `src` changes) marks
  the element stale until `setSrc()` runs again and arms a reload before the
  next play. If the element was playing, status moves to `suspended` with
  "Tap to resume", because WebKit set `paused` without an event.
- A `play()` that does not reach `playing` within 10 s moves to `suspended`
  instead of hanging with no heartbeats.
- A play arriving while the page is hidden after a pause longer than 30 s
  re-assigns `src` at the tracked position first (a stale element plays
  silence otherwise). Only helps when the play actually reaches the page.

Verified on the phone: after a 10 min listen, a 5 min lock-screen pause and a
relaunch, the position was intact and the app auto-resumed on open.

## What is not fixed: the headphone button after a long pause

Second phone test: pause from the AirPods, wait five minutes, press play on the
AirPods. **The Music app launched.** That means iOS had already dropped the PWA
from the "Now Playing" slot. The button press never reaches the web page, so no
handler on our side runs. Nothing in the app can respond to an event it does
not receive.

Mechanism: a web page has no background audio entitlement. While audio plays,
iOS keeps the page alive and routes lock-screen and headphone controls to it.
Once paused in the background, iOS suspends the page after a short grace period
(observed: fine at ~20 s in the first test, gone by ~5 min in the second; the
exact threshold is iOS's and varies) and releases the media session. From then
on the headphones go to the system default. Native apps keep the slot because
their `AVAudioSession` stays registered while paused.

What does work today, reliably:

- Tapping the player on the lock screen while it is still shown.
- Opening the app. It re-assigns `src` at the tracked position and, if a play
  was requested meanwhile, starts on its own; otherwise "Tap to resume".

## Candidate workaround: silent keepalive

Play a silent audio file while logically paused, to test whether iOS keeps the
controls pointed at the page. Use an **unmuted element at volume 1** with silent
samples: the Audio Session draft excludes muted and zero-volume media elements
from its audible flag. A zero-volume element is a separate comparison, not a
sound basis for assuming the session stays active. Actual iOS behavior still
needs measurement.

Costs and risks:

- Battery drain while "paused", for the page and the audio hardware.
- iOS may still evict the page after some minutes; the win may be partial.
- Must never be reported as listening (no heartbeat, no `lastListenedAt`
  refresh, no journal writes from the silent element).
- Must end on a deliberate stop (close the player bar, sleep timer, finished),
  and on a real `pause` from the lock screen it must **start**, which is the
  odd part: the pause handler has to swap the real file for silence and keep
  the Now Playing metadata as the book, with `playbackState` reported as
  `paused`.
- Risk of the silent element and the real one fighting over the single
  blessed `Audio` instance; iOS only trusts the one created in a gesture.
  Likely needs a second element created in the same gesture and kept idle.
- Should be a settings toggle, default off, with a clear label.

Open questions before building it:

1. Does a silent loop actually hold the Now Playing slot on current iOS, and for
   how long? Needs a throwaway test page before touching the engine.
2. Does iOS 26's `navigator.audioSession` API (already used for `type =
   'playback'`) offer anything for keeping the session registered while
   paused? Worth checking WebKit release notes first.
3. Is the battery cost acceptable for the owner's usage pattern (pauses of
   minutes vs. hours)? A cap, such as stopping the keepalive after 30 minutes
   paused, may be the right shape.

## Standalone diagnostic (2026-09-20)

After building and serving this checkout, open `/diagnostics/ios-resume.html`
on the same HTTPS host as Storykeeper. For local development the page is also
served by Vite. It has a separate manifest and can be added to the Home Screen
as **Resume test** to compare standalone behavior with Safari. If an old
Storykeeper service worker opens the library instead, let the updated worker
activate and reopen the URL.

The page uses synthetic audio, one HTMLAudioElement, and separate local log
storage. It makes no progress API calls and cannot change a book's position.
Its three modes are normal pause, an unmuted silent file at volume 1, and the
same file at volume 0. All request `audioSession.type = 'playback'` when available.
While silence plays, Media Session metadata stays set and `playbackState` is
`paused`, matching the proposed workaround. The silent file lasts ten minutes
and does not loop; the bound is media playback time, not a guaranteed wall-clock
deadline if the media itself stalls. Stop test releases the media source and
metadata immediately.

For each mode:

1. Stop other playback, select the mode, and tap Start tone. Confirm the tone.
2. Lock the phone and pause from AirPods. Wait five minutes without reopening.
3. Press AirPods play. Record whether the tone resumes, Music opens, or nothing
   happens, and whether the lock-screen player remained visible.
4. Reopen the diagnostic, download its log, then tap Stop test. Repeat in the
   next mode. Repeat successful runs before drawing conclusions.

The log includes the user agent, standalone mode, visibility, page reloads,
audio/session events, and the origin of play/pause commands. A remote
`command:play` followed by advancing tone time demonstrates that the command
arrived and playback progressed; confirm audible output separately. Absence
of that event does not distinguish suspension, eviction, or lost routing.
An element pause can also be a system interruption; the diagnostic deliberately
tests starting silence there, so also check that incoming calls and switching
to another audio app are not disrupted before considering production use.

Platform findings:

- The [Audio Session draft](https://www.w3.org/TR/audio-session/#the-audiosession-interface)
  exposes a writable type but read-only state, with no method to pin a paused
  session active. Storykeeper already selects the playback type.
- Its [HTMLMediaElement integration](https://www.w3.org/TR/audio-session/#htmlmediaelement)
  excludes muted/zero-volume elements from the audible flag. The draft is
  guidance for the experiment, not proof of a particular shipped iOS behavior.
- The [Safari 26.0 release notes](https://webkit.org/blog/17333/webkit-features-in-safari-26-0/)
  do not document a new paused-session retention API.

The app launching Music supports the lost-routing explanation above, but does
not by itself prove an exact eviction mechanism or a universal pause timeout.
Desktop automation can validate this diagnostic's controls and logs; only a
real iPhone/AirPods run can answer whether it improves long-pause resume.

Local validation: production build and Svelte checks passed. A Chromium smoke
run exercised all three modes, simulated Media Session play/pause/stop actions,
an element-originated pause, the silent file ending, and logs surviving reload
without creating Storykeeper journal keys. No page errors occurred. This does
not validate iOS command routing or battery use.

## How to check the server log again

From the TrueNAS shell (the app image has no `sqlite3`; the DB is in WAL mode so
a read-only bind fails, and `immutable=1` misses the un-checkpointed tail; copy
first for the full picture):

```
mkdir -p /tmp/skdb && cp /mnt/appdata/applications/storykeeper/storykeeper.db* /tmp/skdb/ && \
docker run --rm -it --user root -v /tmp/skdb:/data keinos/sqlite3 sqlite3 /data/storykeeper.db \
  "SELECT datetime(received_at/1000,'unixepoch','localtime') AS at, book_id, position_ms, accepted, device_id FROM progress_log WHERE user_id = 1 ORDER BY received_at DESC LIMIT 40;"
```

Backward jumps of more than a minute, which is what the original loss looked
like:

```
SELECT datetime(received_at/1000,'unixepoch','localtime') AS at, book_id, prev AS from_ms, position_ms AS to_ms, accepted, device_id
FROM (SELECT *, LAG(position_ms) OVER (PARTITION BY book_id ORDER BY received_at) AS prev FROM progress_log WHERE user_id = 1)
WHERE position_ms < prev - 60000 ORDER BY received_at DESC LIMIT 30;
```
