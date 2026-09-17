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
//
// Positions are on the book's virtual timeline (sum of preceding file
// durations + offset in the current file), in milliseconds.

import type { BookDetail } from '$lib/api/types';
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

export const SKIP_MS = 30_000;
/** Default sleep-timer fade-out. */
export const FADE_MS = 5_000;
const FADE_STEP_MS = 100;
const JOURNAL_EVERY_MS = 5_000;
const STALL_AFTER_MS = 4_000;
const WATCHDOG_MS = 2_000;

export class PlayerEngine {
	status = $state<PlayerStatus>('idle');
	book = $state<BookDetail | null>(null);
	fileIndex = $state(0);
	positionMs = $state(0);
	durationMs = $state(0);
	rate = $state(1);
	error = $state<string | null>(null);
	/** play() was refused (no gesture) or the session died; UI must offer a tap-to-resume button. */
	needsGesture = $state(false);
	/** Short user-facing message, e.g. "Resumed from iPhone at 2:14:07", with an optional undo. */
	notice = $state<{ text: string; undo?: () => void } | null>(null);

	userId = 0;
	serverSeq = 0;
	serverListenedAt = 0;

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
	private listeners = new Set<(ev: PlayerEvent) => void>();
	private installed = false;
	private fadeTimer: ReturnType<typeof setInterval> | null = null;

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
			if (this.pendingSeekSec !== null && Math.abs(a.currentTime - this.pendingSeekSec) > 0.5) {
				a.currentTime = this.pendingSeekSec;
			}
			this.pendingSeekSec = null;
		});
		a.addEventListener('playing', () => {
			this.status = 'playing';
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
			if (this.status === 'ended') return;
			if (this.status !== 'suspended') this.status = 'paused';
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
		const { index, offsetMs } = this.locate(positionMs);
		this.fileIndex = index;
		this.positionMs = positionMs;
		this.setSrc(index, offsetMs);
		a.playbackRate = this.rate;
		mediaSession.setBook(book, this.currentChapter?.title);
		this.emit('load');
		if (autoplay) await this.play();
		else {
			this.status = 'paused';
			this.publishPosition();
		}
	}

	async play(): Promise<void> {
		const a = this.ensureAudio();
		if (!this.book) return;
		// A sleep-timer fade may be in flight; starting again cancels it and
		// restores full volume. Synchronous, so the gesture chain is untouched.
		this.cancelFade();
		this.wantPlaying = true;
		if (this.status === 'ended') {
			this.seekTo(0);
		}
		if (this.reloadBeforePlay) {
			// WebKit 295518 mitigation: a stale element plays silence after reopen.
			this.reloadBeforePlay = false;
			this.setSrc(this.fileIndex, this.positionMs - (this.fileStarts[this.fileIndex] ?? 0));
		}
		try {
			await a.play();
			this.needsGesture = false;
		} catch (e) {
			// NotAllowedError: iOS wants a fresh gesture. Surface a tap-to-resume.
			this.needsGesture = true;
			this.status = 'suspended';
			this.emit('stall');
			void e;
		}
	}

	pause(): void {
		this.wantPlaying = false;
		this.audio?.pause();
	}

	toggle(): void {
		if (this.status === 'playing') this.pause();
		else void this.play();
	}

	seekTo(bookMs: number): void {
		if (!this.book || !this.audio) return;
		bookMs = Math.min(Math.max(0, bookMs), this.durationMs);
		const { index, offsetMs } = this.locate(bookMs);
		if (index !== this.fileIndex) {
			this.fileIndex = index;
			this.setSrc(index, offsetMs);
			if (this.wantPlaying) void this.audio.play().catch(() => (this.needsGesture = true));
			this.emit('filechange');
		} else {
			this.audio.currentTime = offsetMs / 1000;
		}
		this.positionMs = bookMs;
		if (this.status === 'ended') this.status = this.wantPlaying ? 'loading' : 'paused';
		this.publishPosition();
		this.writeJournal(true);
		this.emit('seek');
	}

	skip(deltaMs: number): void {
		this.seekTo(this.positionMs + deltaMs);
	}

	setRate(rate: number): void {
		rate = Math.min(3, Math.max(0.5, rate));
		this.rate = rate;
		if (this.audio) this.audio.playbackRate = rate;
		try {
			localStorage.setItem('sk:rate', String(rate));
		} catch {
			/* ignore */
		}
		this.emit('ratechange');
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

	/** Restore the persisted playback rate (call once at startup). */
	restoreRate(): void {
		try {
			const r = Number(localStorage.getItem('sk:rate'));
			if (r >= 0.5 && r <= 3) this.rate = r;
		} catch {
			/* ignore */
		}
	}

	dismissNotice(): void {
		this.notice = null;
	}

	// --- internals ---

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
		// Media fragment gives Safari the start time before it decides how much to buffer.
		a.src = `${f.url}#t=${sec.toFixed(3)}`;
		a.load();
		a.playbackRate = this.rate;
	}

	private syncPosition(): void {
		const a = this.audio;
		if (!a) return;
		this.positionMs = (this.fileStarts[this.fileIndex] ?? 0) + a.currentTime * 1000;
		this.publishPosition();
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
		if (Date.now() - this.hiddenAt > 30_000) this.reloadBeforePlay = true;
		this.emit('visible');
		if (this.wantPlaying && (a.paused || a.ended)) {
			// We meant to be playing but are not: either the file ended in the
			// background (WebKit 261858) or the session died. Try once, silently;
			// if iOS refuses, needsGesture shows the tap-to-resume button.
			void this.play();
		}
	}
}

export const player = new PlayerEngine();
