// The playback engine. One HTMLAudioElement for the life of the page, created
// on the first user gesture and never recreated; a local journal so position
// survives iOS killing the page; and explicit handling of the known WebKit
// failure modes:
//
//   - WebKit 295518 (iOS 26): after a home-screen PWA is reopened, play()
//     resolves but produces no sound. We re-assign src right before play()
//     whenever the page was hidden in between.
//   - WebKit 261858: when a file ends while backgrounded, the next file does not
//     start. We swap src and call play() synchronously inside `ended`, and on
//     becoming visible we detect "ended but should be playing" and surface a
//     one-tap resume.
//   - Lock-screen pause > ~30 s kills the audio session. The stall watchdog
//     moves us to `suspended` and the UI shows "Tap to resume".
//   - iOS reclaims the media process of a backgrounded page. WebKit then
//     silently re-runs the load algorithm on the element: readyState drops to
//     HAVE_NOTHING, `emptied` fires, and currentTime reads 0 until a seek it
//     queues lands. That 0 is not a position. We never read currentTime from
//     an element that has no metadata, and an `emptied` we did not cause marks
//     the element stale until we re-assign src ourselves. A play() that never
//     reaches `playing` (the same dead pipeline, from the lock screen) is
//     surfaced as `suspended` instead of waiting forever.
//
// Positions are on the book's virtual timeline (sum of preceding file
// durations + offset in the current file), in milliseconds.

import type { BookDetail } from '$lib/api/types';
import { autoRewindMs } from './autorewind';
import { journal } from './journal';
import { mediaSession } from './mediasession';

export type PlayerStatus = 'idle' | 'loading' | 'playing' | 'paused' | 'ended' | 'suspended';

export type PlayerEventKind =
	| 'load'
	| 'play'
	| 'pause'
	| 'seek'
	| 'ratechange'
	| 'filechange'
	| 'ended'
	| 'tick'
	| 'stall'
	| 'hidden'
	| 'visible'
	| 'finished';

export interface PlayerEvent {
	kind: PlayerEventKind;
	bookId: number;
	positionMs: number;
	fileIndex: number;
	playing: boolean;
	/** Only set on 'finished' and 'ended': the flag to persist on the progress record. */
	finished?: boolean;
}

/** Default skip amount; the live amounts are `player.skipBackMs`/`skipForwardMs`. */
export const SKIP_MS = 30_000;
/** Default sleep-timer fade-out. */
export const FADE_MS = 5_000;
const FADE_STEP_MS = 100;
const JOURNAL_EVERY_MS = 5_000;
const STALL_AFTER_MS = 4_000;
const WATCHDOG_MS = 2_000;
/**
 * How long a play() may go without a `playing` event before we call the
 * pipeline dead. Generous, because a cold element fetches the file first; a
 * late `playing` still recovers the state on its own.
 */
const PLAY_TIMEOUT_MS = 10_000;
/**
 * How long a hidden page can sit paused before iOS has torn its audio
 * session down. Past this, the element needs a fresh src before play().
 */
const SESSION_DIES_AFTER_MS = 30_000;

export class PlayerEngine {
	status = $state<PlayerStatus>('idle');
	book = $state<BookDetail | null>(null);
	fileIndex = $state(0);
	positionMs = $state(0);
	durationMs = $state(0);
	/**
	 * The rate actually applied to the element: the book's override when it has
	 * one, otherwise `defaultRate`.
	 */
	rate = $state(1);
	/** The global rate, persisted in localStorage `sk:rate`. */
	defaultRate = $state(1);
	/** This book's rate, from `progress.playback_rate`; null means "use the default". */
	bookRateOverride = $state<number | null>(null);
	/** Skip amounts for the UI buttons and the lock screen. Set from prefs. */
	skipBackMs = $state(SKIP_MS);
	skipForwardMs = $state(SKIP_MS);
	/** Rewind a little when resuming after a pause. The prefs store sets this. */
	autoRewind = $state(true);
	error = $state<string | null>(null);
	/** play() was refused (no gesture) or the session died; UI must offer a tap-to-resume button. */
	needsGesture = $state(false);
	/** Short user-facing message, e.g. "Resumed from iPhone at 2:14:07", with an optional undo. */
	notice = $state<{ text: string; undo?: () => void } | null>(null);

	userId = 0;
	serverSeq = 0;
	serverListenedAt = 0;
	/**
	 * When this device last actually listened, as Date.now(). The reporter sends
	 * it as `client_listened_at` so a report built while paused (a hide/beacon,
	 * say) describes the listen it really is and cannot outrank a newer listen on
	 * another device. Deliberate actions (play, seek, markFinished) refresh it.
	 */
	lastListenedAt = 0;

	private audio: HTMLAudioElement | null = null;
	private fileStarts: number[] = [];
	private wantPlaying = false;
	private reloadBeforePlay = false;
	private pendingSeekSec: number | null = null;
	private lastCurrentTime = -1;
	private lastAdvanceAt = 0;
	private watchdog: ReturnType<typeof setInterval> | null = null;
	private lastJournalAt = 0;
	private hiddenAt = 0;
	/**
	 * When we last actually entered `paused`, as Date.now(), for auto-rewind. Set
	 * only where `status` becomes 'paused' — never for 'suspended', where the
	 * position is already suspect and the listener did not choose to stop.
	 */
	private pausedAt = 0;
	private listeners = new Set<(ev: PlayerEvent) => void>();
	private installed = false;
	private fadeTimer: ReturnType<typeof setInterval> | null = null;
	/**
	 * Set by our own src changes: the `emptied` they queue is ours. Cleared when
	 * that load reaches metadata, so a later `emptied` is known to be WebKit
	 * resetting the element behind our back.
	 */
	private expectEmptied = false;
	/**
	 * The element lost its media without us asking (see the header). Its
	 * currentTime describes nothing until setSrc() gives it a file again, so
	 * position reads are skipped and the tracked positionMs stands.
	 */
	private positionStale = false;
	/** Pending "did play() actually start?" check; see PLAY_TIMEOUT_MS. */
	private playTimer: ReturnType<typeof setTimeout> | null = null;

	readonly currentChapter = $derived.by(() => {
		const b = this.book;
		if (!b || b.chapters.length === 0) return null;
		const pos = this.positionMs;
		let cur = b.chapters[0];
		for (const c of b.chapters) {
			if (c.start_ms <= pos) cur = c;
			else break;
		}
		return cur;
	});

	on(cb: (ev: PlayerEvent) => void): () => void {
		this.listeners.add(cb);
		return () => this.listeners.delete(cb);
	}

	private emit(kind: PlayerEventKind, finished?: boolean): void {
		if (!this.book) return;
		const ev: PlayerEvent = {
			kind,
			bookId: this.book.id,
			positionMs: this.positionMs,
			fileIndex: this.fileIndex,
			playing: this.status === 'playing',
			...(finished !== undefined ? { finished } : {})
		};
		for (const cb of this.listeners) cb(ev);
	}

	/** Install document-level listeners once. Safe to call repeatedly. */
	install(): void {
		if (this.installed || typeof document === 'undefined') return;
		this.installed = true;
		document.addEventListener('visibilitychange', () => {
			if (document.visibilityState === 'hidden') this.onHidden();
			else this.onVisible();
		});
		// Safari can skip visibilitychange on navigation; pagehide is the reliable last word.
		window.addEventListener('pagehide', () => this.onHidden());
		mediaSession.setHandlers({
			play: () => void this.play(),
			pause: () => this.pause(),
			skip: (d) => this.skip(d),
			seekTo: (ms) => this.seekTo(ms),
			prevChapter: () => this.prevChapter(),
			nextChapter: () => this.nextChapter()
		});
	}

	/**
	 * Create the single audio element. MUST first be called synchronously inside a
	 * user gesture (click/tap) so iOS blesses it; every later play()/src swap on
	 * this same element is then allowed without a gesture.
	 */
	ensureAudio(): HTMLAudioElement {
		if (this.audio) return this.audio;
		mediaSession.claimPlaybackSession();
		const a = new Audio();
		a.preload = 'metadata';
		a.setAttribute('playsinline', '');
		(a as unknown as { preservesPitch?: boolean }).preservesPitch = true;
		a.addEventListener('loadedmetadata', () => {
			// Our load got this far, so any `emptied` it owed has been fired.
			this.expectEmptied = false;
			if (this.pendingSeekSec !== null && Math.abs(a.currentTime - this.pendingSeekSec) > 0.5) {
				a.currentTime = this.pendingSeekSec;
			}
			this.pendingSeekSec = null;
		});
		a.addEventListener('emptied', () => {
			// Browsers queue zero, one or two of these for a single src change,
			// so the flag is not consumed here: everything before our load
			// reaches metadata is ours.
			if (this.expectEmptied) return;
			this.onElementReset();
		});
		a.addEventListener('playing', () => {
			this.clearPlayTimer();
			this.status = 'playing';
			this.lastListenedAt = Date.now();
			this.needsGesture = false;
			this.error = null;
			this.lastAdvanceAt = Date.now();
			this.lastCurrentTime = a.currentTime;
			mediaSession.setPlaybackState('playing');
			this.publishPosition();
		});
		a.addEventListener('pause', () => {
			// A pause event we did not ask for (lock screen, headphones unplugged,
			// another app took the session) is still the listener's intent.
			if (!this.book) return;
			if (this.status === 'ended') return;
			// Every browser fires `pause` immediately before `ended`. While we still
			// mean to be playing that is a file boundary, not the listener stopping:
			// clearing wantPlaying here would leave onEnded loading the next file and
			// never starting it. pause() clears wantPlaying first, so a deliberate
			// pause at the end of a file is unaffected.
			if (this.wantPlaying && this.atEndOfFile(a)) return;
			if (this.status !== 'suspended') {
				this.status = 'paused';
				// Auto-rewind measures from here; 'suspended' deliberately does not set it.
				this.pausedAt = Date.now();
			}
			this.wantPlaying = false;
			mediaSession.setPlaybackState('paused');
			this.syncPosition();
			this.writeJournal(true);
			this.emit('pause');
		});
		a.addEventListener('timeupdate', () => {
			if (this.status !== 'playing') return;
			this.syncPosition();
			const now = Date.now();
			this.lastListenedAt = now;
			if (now - this.lastJournalAt >= JOURNAL_EVERY_MS) {
				this.writeJournal(false);
				this.emit('tick');
			}
		});
		a.addEventListener('ratechange', () => {
			this.rate = a.playbackRate;
			this.publishPosition();
		});
		a.addEventListener('ended', () => this.onEnded());
		a.addEventListener('error', () => {
			const code = a.error?.code;
			this.error = code === 4 ? 'This file cannot be played on this device.' : 'Playback error.';
			this.status = 'suspended';
			this.needsGesture = true;
			this.emit('stall');
		});
		this.audio = a;
		this.startWatchdog();
		return a;
	}

	/** Load a book at a position. If autoplay, call from within a user gesture. */
	async load(book: BookDetail, positionMs: number, autoplay: boolean): Promise<void> {
		const a = this.ensureAudio();
		this.book = book;
		this.durationMs = book.duration_ms;
		this.fileStarts = [];
		let acc = 0;
		for (const f of book.files) {
			this.fileStarts.push(acc);
			acc += f.duration_ms;
		}
		if (book.files.length === 0) {
			this.error = 'This book has no audio files.';
			this.status = 'idle';
			return;
		}
		this.error = null;
		this.status = 'loading';
		// A pause on the book we are leaving must not rewind the one we are
		// loading; the position here comes from the server/journal already.
		this.pausedAt = 0;
		// Recomputed on every load, unconditionally, so the previous book's
		// per-book rate can never leak into this one.
		this.bookRateOverride = book.progress?.playback_rate ?? null;
		this.rate = this.bookRateOverride ?? this.defaultRate;
		const { index, offsetMs } = this.locate(positionMs);
		this.fileIndex = index;
		this.positionMs = positionMs;
		this.setSrc(index, offsetMs);
		a.playbackRate = this.rate;
		mediaSession.setBook(book, this.currentChapter?.title);
		this.emit('load');
		if (autoplay) {
			this.lastListenedAt = Date.now();
			// Not the listener pressing play: no auto-rewind.
			await this.play({ auto: true });
		} else {
			this.status = 'paused';
			// A silent restore (relaunch) is not a listen. The position came from
			// the server record, so the last listen is that record's time: any
			// report this player sends before the listener presses play (the
			// prefs load changes the rate, for one) must describe that old
			// listen, or a paused phone could overwrite a newer listen elsewhere.
			this.lastListenedAt = this.serverListenedAt;
			this.publishPosition();
		}
	}

	/**
	 * Start playback. `opts.auto` marks a start we initiated ourselves (the
	 * autoplay inside load(), the recovery in onVisible()) rather than the
	 * listener pressing play: those never auto-rewind, because the listener did
	 * not stop and would not expect the position to move.
	 */
	async play(opts?: { auto?: boolean }): Promise<void> {
		const a = this.ensureAudio();
		if (!this.book) return;
		// Consumed on every play attempt, so a failed start cannot leave a stale
		// pause age behind to rewind twice.
		const pausedFor = this.status === 'paused' && this.pausedAt > 0 ? Date.now() - this.pausedAt : 0;
		this.pausedAt = 0;
		this.lastListenedAt = Date.now();
		// A sleep-timer fade may be in flight; starting again cancels it and
		// restores full volume. Synchronous, so the gesture chain is untouched.
		this.cancelFade();
		this.wantPlaying = true;
		if (this.status === 'ended') {
			this.seekTo(0);
		} else if (!opts?.auto && this.autoRewind && pausedFor > 0) {
			const rewind = autoRewindMs(pausedFor);
			// seekTo after wantPlaying: if the rewind crosses back over a file
			// boundary, seekTo's existing path swaps src and starts playback itself.
			if (rewind > 0) this.seekTo(Math.max(0, this.positionMs - rewind));
		}
		// A play from the lock screen after a long pause: the page is still
		// hidden, so onVisible() has not had the chance to flag the reload, but
		// the audio session is just as dead. Re-assigning src inside this
		// handler is allowed; play() on the stale element would only be silent.
		if (pausedFor > SESSION_DIES_AFTER_MS && typeof document !== 'undefined' && document.hidden) {
			this.reloadBeforePlay = true;
		}
		if (this.reloadBeforePlay) {
			// WebKit 295518 mitigation: a stale element plays silence after reopen.
			this.reloadBeforePlay = false;
			this.setSrc(this.fileIndex, this.positionMs - (this.fileStarts[this.fileIndex] ?? 0));
		}
		// Armed before the await: on a dead pipeline play() never settles, and
		// nothing else would ever notice. `playing` disarms it.
		this.armPlayTimer();
		try {
			await a.play();
			this.needsGesture = false;
		} catch (e) {
			// NotAllowedError: iOS wants a fresh gesture. Surface a tap-to-resume.
			this.clearPlayTimer();
			this.needsGesture = true;
			this.status = 'suspended';
			this.emit('stall');
			void e;
		}
	}

	pause(): void {
		this.clearPlayTimer();
		this.wantPlaying = false;
		this.audio?.pause();
	}

	toggle(): void {
		if (this.status === 'playing') this.pause();
		else void this.play();
	}

	/**
	 * Drop the loaded book and every trace of it, without emitting anything: used
	 * when the session ends or a different user signs in, so the next heartbeat
	 * can never write one user's position into another's history.
	 *
	 * The element itself survives (iOS only ever blesses this one, and a new one
	 * would be silent until the next tap): it is paused and its source cleared.
	 */
	unload(): void {
		this.cancelFade();
		this.clearPlayTimer();
		this.wantPlaying = false;
		// book first: emit() and writeJournal() both no-op without one, so the
		// async `pause` event this triggers stays silent.
		this.book = null;
		const a = this.audio;
		if (a) {
			a.pause();
			this.expectEmptied = true;
			a.removeAttribute('src');
			a.load();
		}
		this.positionStale = false;
		this.fileStarts = [];
		this.fileIndex = 0;
		this.positionMs = 0;
		this.durationMs = 0;
		this.status = 'idle';
		this.serverSeq = 0;
		this.serverListenedAt = 0;
		this.lastListenedAt = 0;
		this.notice = null;
		this.needsGesture = false;
		this.error = null;
		this.reloadBeforePlay = false;
		this.pendingSeekSec = null;
		this.lastCurrentTime = -1;
		this.pausedAt = 0;
		// The next book decides its own rate; the global default stays.
		this.bookRateOverride = null;
		this.rate = this.defaultRate;
		mediaSession.setBook(null);
		mediaSession.setPlaybackState('none');
	}

	seekTo(bookMs: number): void {
		if (!this.book || !this.audio) return;
		// A seek is a deliberate action, so it counts as listening now: the report
		// it triggers must not look like a replay of an old listen.
		this.lastListenedAt = Date.now();
		bookMs = Math.min(Math.max(0, bookMs), this.durationMs);
		const { index, offsetMs } = this.locate(bookMs);
		// Position first: anything that reports to the server on the events
		// below must see the new position, never the one we are leaving.
		this.positionMs = bookMs;
		// A stale element has no media to seek within: give it the file again.
		if (index !== this.fileIndex || this.positionStale) {
			this.fileIndex = index;
			this.setSrc(index, offsetMs);
			if (this.wantPlaying) void this.audio.play().catch(() => (this.needsGesture = true));
		} else {
			this.audio.currentTime = offsetMs / 1000;
		}
		if (this.status === 'ended') this.status = this.wantPlaying ? 'loading' : 'paused';
		this.publishPosition();
		this.writeJournal(true);
		this.emit('seek');
	}

	skip(deltaMs: number): void {
		this.seekTo(this.positionMs + deltaMs);
	}

	/**
	 * Set the playback rate. Without `perBook` this is the global default: it is
	 * persisted and becomes the effective rate unless this book has an override.
	 * With `perBook` it sets this book's override only; the UI is responsible for
	 * PUTting it to the server (the engine never talks to the network).
	 */
	setRate(rate: number, opts?: { perBook?: boolean }): void {
		rate = Math.min(3, Math.max(0.5, rate));
		const before = this.rate;
		if (opts?.perBook) {
			this.bookRateOverride = rate;
			this.applyRate(rate);
		} else {
			this.defaultRate = rate;
			try {
				localStorage.setItem('sk:rate', String(rate));
			} catch {
				/* ignore */
			}
			if (this.bookRateOverride === null) this.applyRate(rate);
		}
		// Only a real change is worth a report; the prefs load re-applies the
		// same rate on every launch.
		if (this.rate !== before) this.emit('ratechange');
	}

	/** Drop this book's rate override and fall back to the global default. */
	clearBookRate(): void {
		this.bookRateOverride = null;
		this.applyRate(this.defaultRate);
		this.emit('ratechange');
	}

	/**
	 * Skip amounts for the UI buttons and, through the media session, the lock
	 * screen (iOS sends no seekOffset, so it needs the number up front).
	 */
	setSkipAmounts(backMs: number, forwardMs: number): void {
		if (backMs > 0) this.skipBackMs = backMs;
		if (forwardMs > 0) this.skipForwardMs = forwardMs;
		mediaSession.setSkipAmounts(this.skipBackMs, this.skipForwardMs);
	}

	prevChapter(): void {
		const b = this.book;
		if (!b) return;
		if (b.chapters.length === 0) {
			this.seekTo(this.fileStarts[Math.max(0, this.fileIndex - 1)] ?? 0);
			return;
		}
		const cur = this.currentChapter;
		if (!cur) return;
		// Within the first 3 s of a chapter, go to the previous one; otherwise restart this one.
		const target =
			this.positionMs - cur.start_ms < 3000 && cur.index > 0 ? b.chapters[cur.index - 1] : cur;
		this.seekTo(target.start_ms);
	}

	nextChapter(): void {
		const b = this.book;
		if (!b) return;
		if (b.chapters.length === 0) {
			const next = this.fileStarts[this.fileIndex + 1];
			if (next !== undefined) this.seekTo(next);
			return;
		}
		const cur = this.currentChapter;
		if (!cur) return;
		const next = b.chapters[cur.index + 1];
		if (next) this.seekTo(next.start_ms);
	}

	/**
	 * Element volume, 0–1. iOS ignores writes to `volume` (the hardware buttons
	 * own it), so the fade below degrades to a plain pause there.
	 */
	setVolume(v: number): void {
		if (this.audio) this.audio.volume = Math.min(1, Math.max(0, v));
	}

	/** Stop any in-flight fade and put the volume back to 1. */
	cancelFade(): void {
		if (this.fadeTimer) {
			clearInterval(this.fadeTimer);
			this.fadeTimer = null;
		}
		this.setVolume(1);
	}

	/** Sleep timer: ramp the volume down over `overMs`, then pause and restore it. */
	fadeOutAndPause(overMs = FADE_MS): void {
		this.cancelFade();
		if (!this.audio || this.status !== 'playing' || overMs <= 0) {
			this.pause();
			this.setVolume(1);
			return;
		}
		const startedAt = Date.now();
		this.fadeTimer = setInterval(() => {
			const t = (Date.now() - startedAt) / overMs;
			if (t >= 1 || this.status !== 'playing') {
				this.cancelFade();
				this.pause();
				return;
			}
			this.setVolume(1 - t);
		}, FADE_STEP_MS);
	}

	/**
	 * Mark the loaded book finished or unfinished. Finishing parks the timeline
	 * (and the element) at the end; unfinishing keeps the position. Either way the
	 * flag reaches the server through the reporter's 'finished' event.
	 */
	markFinished(finished: boolean): void {
		const b = this.book;
		if (!b) return;
		this.lastListenedAt = Date.now();
		if (finished) {
			this.cancelFade();
			this.wantPlaying = false;
			this.audio?.pause();
			const last = Math.max(0, b.files.length - 1);
			this.fileIndex = last;
			const dur = b.files[last]?.duration_ms ?? 0;
			this.setSrc(last, Math.max(0, dur - 500));
			this.positionMs = this.durationMs;
			// Set before the async `pause` event lands: its handler leaves 'ended' alone.
			this.status = 'ended';
			mediaSession.setPlaybackState('paused');
			this.publishPosition();
		} else if (this.status === 'ended') {
			this.status = 'paused';
		}
		this.writeJournal(false);
		this.emit('finished', finished);
	}

	/** Restore the persisted global playback rate (call once at startup). */
	restoreRate(): void {
		try {
			const r = Number(localStorage.getItem('sk:rate'));
			if (r >= 0.5 && r <= 3) {
				this.defaultRate = r;
				if (this.bookRateOverride === null) this.applyRate(r);
			}
		} catch {
			/* ignore */
		}
	}

	dismissNotice(): void {
		this.notice = null;
	}

	// --- internals ---

	/** Put an effective rate on the state and, if it exists, the element. */
	private applyRate(rate: number): void {
		this.rate = rate;
		if (this.audio) this.audio.playbackRate = rate;
	}

	/**
	 * True when this element is sitting at the end of its file: `ended` is set
	 * before the `pause` that precedes it, and the tolerance covers browsers that
	 * stop a hair short of `duration`.
	 */
	private atEndOfFile(a: HTMLAudioElement): boolean {
		return a.ended || (a.duration > 0 && a.currentTime >= a.duration - 0.25);
	}

	private locate(bookMs: number): { index: number; offsetMs: number } {
		const files = this.book?.files ?? [];
		let index = 0;
		for (let i = 0; i < files.length; i++) {
			if (bookMs >= this.fileStarts[i]) index = i;
		}
		const offset = Math.max(0, bookMs - (this.fileStarts[index] ?? 0));
		const dur = files[index]?.duration_ms ?? 0;
		return { index, offsetMs: dur > 0 ? Math.min(offset, Math.max(0, dur - 500)) : offset };
	}

	private setSrc(index: number, offsetMs: number): void {
		const a = this.audio;
		const f = this.book?.files[index];
		if (!a || !f) return;
		const sec = Math.max(0, offsetMs / 1000);
		this.pendingSeekSec = sec;
		// This load is ours, and it gives a reset element its media back.
		this.expectEmptied = true;
		this.positionStale = false;
		// Media fragment gives Safari the start time before it decides how much to buffer.
		a.src = `${f.url}#t=${sec.toFixed(3)}`;
		a.load();
		a.playbackRate = this.rate;
	}

	/**
	 * Pull the position from the element. Only when the element can actually
	 * vouch for it: before metadata (a fresh src, or WebKit's reset after the
	 * media process died) currentTime is 0 regardless of where the listener is,
	 * and writing that through would erase their place in the journal and, with
	 * a fresh timestamp, on the server too. The tracked positionMs stands.
	 */
	private syncPosition(): void {
		const a = this.audio;
		if (!a) return;
		if (!this.positionStale && a.readyState >= HTMLMediaElement.HAVE_METADATA) {
			this.positionMs = (this.fileStarts[this.fileIndex] ?? 0) + a.currentTime * 1000;
		}
		this.publishPosition();
	}

	/**
	 * WebKit emptied the element without us asking: the media process was
	 * reclaimed and the element is being reloaded from scratch. Its position is
	 * meaningless until setSrc() runs again, and anything that was playing has
	 * stopped without a `pause` event, so say so.
	 */
	private onElementReset(): void {
		if (!this.book) return;
		this.positionStale = true;
		this.pendingSeekSec = null;
		this.reloadBeforePlay = true;
		this.clearPlayTimer();
		if (this.status === 'playing' || this.status === 'loading') {
			this.status = 'suspended';
			this.needsGesture = true;
			mediaSession.setPlaybackState('paused');
			this.writeJournal(false);
			this.emit('stall');
		}
	}

	private armPlayTimer(): void {
		this.clearPlayTimer();
		this.playTimer = setTimeout(() => {
			this.playTimer = null;
			if (!this.book || !this.wantPlaying || this.status === 'playing') return;
			// play() was accepted and nothing happened: the pipeline is gone.
			// A fresh src on the next attempt is the only thing that revives it.
			this.reloadBeforePlay = true;
			this.status = 'suspended';
			this.needsGesture = true;
			this.writeJournal(false);
			this.emit('stall');
		}, PLAY_TIMEOUT_MS);
	}

	private clearPlayTimer(): void {
		if (this.playTimer) clearTimeout(this.playTimer);
		this.playTimer = null;
	}

	private publishPosition(): void {
		mediaSession.setPosition(this.durationMs, this.positionMs, this.rate);
		if (this.book && this.book.chapters.length > 0) {
			mediaSession.setBook(this.book, this.currentChapter?.title);
		}
	}

	writeJournal(synced: boolean | null): void {
		if (!this.book || !this.userId) return;
		this.lastJournalAt = Date.now();
		const prev = journal.read(this.userId, this.book.id);
		journal.write(this.userId, {
			bookId: this.book.id,
			fileIndex: this.fileIndex,
			positionMs: Math.round(this.positionMs),
			rate: this.rate,
			playing: this.status === 'playing',
			clientTs: Date.now(),
			serverSeq: this.serverSeq,
			serverListenedAt: this.serverListenedAt,
			synced: synced === null ? (prev?.synced ?? false) : synced
		});
	}

	private onEnded(): void {
		const b = this.book;
		const a = this.audio;
		if (!b || !a) return;
		if (this.fileIndex < b.files.length - 1) {
			// WebKit 261858: must happen synchronously inside the ended handler.
			this.fileIndex += 1;
			this.setSrc(this.fileIndex, 0);
			this.positionMs = this.fileStarts[this.fileIndex] ?? this.positionMs;
			if (this.wantPlaying) void a.play().catch(() => (this.needsGesture = true));
			this.writeJournal(false);
			this.emit('filechange');
			return;
		}
		this.status = 'ended';
		this.wantPlaying = false;
		this.positionMs = this.durationMs;
		mediaSession.setPlaybackState('paused');
		this.writeJournal(false);
		this.emit('ended');
	}

	private startWatchdog(): void {
		if (this.watchdog) return;
		this.watchdog = setInterval(() => {
			const a = this.audio;
			if (!a || this.status !== 'playing') return;
			const now = Date.now();
			if (a.currentTime !== this.lastCurrentTime) {
				this.lastCurrentTime = a.currentTime;
				this.lastAdvanceAt = now;
				return;
			}
			if (now - this.lastAdvanceAt > STALL_AFTER_MS && !a.paused) {
				this.status = 'suspended';
				this.needsGesture = true;
				this.writeJournal(false);
				this.emit('stall');
			}
		}, WATCHDOG_MS);
	}

	private onHidden(): void {
		this.hiddenAt = Date.now();
		if (this.book) {
			this.syncPosition();
			this.writeJournal(null);
		}
		this.emit('hidden');
	}

	private onVisible(): void {
		if (!this.book || !this.audio) {
			this.emit('visible');
			return;
		}
		const a = this.audio;
		this.syncPosition();
		// Anything that was hidden for a while gets a fresh src before the next play():
		// the audio pipeline may have been torn down (WebKit 295518).
		if (Date.now() - this.hiddenAt > SESSION_DIES_AFTER_MS) this.reloadBeforePlay = true;
		this.emit('visible');
		if (this.wantPlaying && (a.paused || a.ended)) {
			// We meant to be playing but are not: either the file ended in the
			// background (WebKit 261858) or the session died. Try once, silently;
			// if iOS refuses, needsGesture shows the tap-to-resume button. Not a
			// listener-initiated start, so it must not rewind.
			void this.play({ auto: true });
		}
	}
}

export const player = new PlayerEngine();
