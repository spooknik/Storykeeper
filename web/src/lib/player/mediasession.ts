// Lock-screen / hardware controls via the Media Session API. On iOS these are
// the only controls the listener has while the phone is locked, and the skip
// buttons are what people actually use for audiobooks, so seekbackward and
// seekforward are mapped to a skip amount rather than previous/next track.
//
// iOS never sends `details.seekOffset`, so the amount has to come from here.
// The engine pushes the user's configured amounts in with setSkipAmounts(); the
// handlers read the module state at call time, so changing them later takes
// effect without re-installing handlers.

import type { BookDetail } from '$lib/api/types';

export interface MediaSessionActions {
	play: () => void;
	pause: () => void;
	skip: (deltaMs: number) => void;
	seekTo: (positionMs: number) => void;
	prevChapter: () => void;
	nextChapter: () => void;
}

const SKIP_MS = 30_000;

let skipBackMs = SKIP_MS;
let skipForwardMs = SKIP_MS;

function coverArtwork(book: BookDetail): MediaImage[] {
	if (!book.cover_url) return [];
	const base = book.cover_url.replace(/\?.*$/, '');
	return [
		{ src: `${base}?size=600`, sizes: '600x600', type: 'image/jpeg' },
		{ src: `${base}?size=200`, sizes: '200x200', type: 'image/jpeg' }
	];
}

export const mediaSession = {
	supported(): boolean {
		return typeof navigator !== 'undefined' && 'mediaSession' in navigator;
	},

	/** Ask iOS for a real playback session so audio ignores the ring/silent switch. */
	claimPlaybackSession(): void {
		const nav = navigator as unknown as { audioSession?: { type: string } };
		try {
			if (nav.audioSession) nav.audioSession.type = 'playback';
		} catch {
			/* not supported */
		}
	},

	setBook(book: BookDetail | null, chapterTitle?: string): void {
		if (!this.supported()) return;
		if (!book) {
			navigator.mediaSession.metadata = null;
			return;
		}
		navigator.mediaSession.metadata = new MediaMetadata({
			title: chapterTitle ? `${chapterTitle} · ${book.title}` : book.title,
			artist: book.authors.join(', '),
			album: book.series || book.title,
			artwork: coverArtwork(book)
		});
	},

	setHandlers(a: MediaSessionActions): void {
		if (!this.supported()) return;
		const ms = navigator.mediaSession;
		const set = (action: MediaSessionAction, handler: MediaSessionActionHandler | null) => {
			try {
				ms.setActionHandler(action, handler);
			} catch {
				/* action not supported on this platform */
			}
		};
		set('play', () => a.play());
		set('pause', () => a.pause());
		set('seekbackward', (d) => a.skip(-((d.seekOffset ?? skipBackMs / 1000) * 1000)));
		set('seekforward', (d) => a.skip((d.seekOffset ?? skipForwardMs / 1000) * 1000));
		set('seekto', (d) => {
			if (typeof d.seekTime === 'number') a.seekTo(d.seekTime * 1000);
		});
		set('previoustrack', () => a.prevChapter());
		set('nexttrack', () => a.nextChapter());
		set('stop', () => a.pause());
	},

	/**
	 * The amounts the lock-screen skip buttons use when the platform does not
	 * tell us one (iOS). Called by the engine's setSkipAmounts().
	 */
	setSkipAmounts(backMs: number, forwardMs: number): void {
		if (backMs > 0) skipBackMs = backMs;
		if (forwardMs > 0) skipForwardMs = forwardMs;
	},

	setPlaybackState(state: 'playing' | 'paused' | 'none'): void {
		if (!this.supported()) return;
		try {
			navigator.mediaSession.playbackState = state;
		} catch {
			/* ignore */
		}
	},

	/** Position on the whole-book timeline so the lock-screen scrubber is meaningful. */
	setPosition(durationMs: number, positionMs: number, rate: number): void {
		if (!this.supported() || !('setPositionState' in navigator.mediaSession)) return;
		if (!(durationMs > 0)) return;
		try {
			navigator.mediaSession.setPositionState({
				duration: durationMs / 1000,
				position: Math.min(Math.max(0, positionMs), durationMs) / 1000,
				playbackRate: rate
			});
		} catch {
			/* invalid state, ignore */
		}
	}
};
