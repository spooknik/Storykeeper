// Sleep timer. Lives entirely in memory: a reload, or a cold start after iOS
// kills the page, starts with no timer armed — which is what a listener who has
// fallen asleep wants anyway.
//
// The countdown only runs while the engine is actually playing, so pausing to
// answer the door does not eat the timer. "End of chapter" watches
// `player.currentChapter` (or the file index, for books with no chapters) and
// fires when it changes or the book ends.

import { FADE_MS, player, type PlayerEngine } from './machine.svelte';

/** Minutes, or one of the two special modes. */
export type SleepMode = 'off' | 'chapter' | number;

export const SLEEP_MINUTES = [15, 30, 45, 60] as const;

const TICK_MS = 500;

export class SleepTimer {
	mode = $state<SleepMode>('off');
	/** Time left for a minute-based timer; 0 for 'off' and 'chapter'. */
	remainingMs = $state(0);

	private timer: ReturnType<typeof setInterval> | null = null;
	private lastTickAt = 0;
	private markIndex = 0;

	constructor(private engine: PlayerEngine) {}

	get armed(): boolean {
		return this.mode !== 'off';
	}

	/** Arm (or re-arm, or with 'off' cancel) the timer. */
	set(mode: SleepMode): void {
		this.clear();
		if (mode === 'off') return;
		this.engine.cancelFade();
		this.mode = mode;
		this.markIndex = this.mark();
		this.remainingMs = mode === 'chapter' ? 0 : mode * 60_000;
		this.lastTickAt = Date.now();
		this.timer = setInterval(() => this.tick(), TICK_MS);
	}

	cancel(): void {
		this.clear();
	}

	/** Short label for the player button, e.g. "24:59" or "Chapter". */
	get label(): string {
		if (this.mode === 'off') return 'Sleep';
		if (this.mode === 'chapter') return 'Chapter';
		const total = Math.ceil(this.remainingMs / 1000);
		return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`;
	}

	// --- internals ---

	/** The unit "end of chapter" watches: a chapter index, or a file index. */
	private mark(): number {
		const b = this.engine.book;
		if (!b) return 0;
		return b.chapters.length > 0 ? (this.engine.currentChapter?.index ?? 0) : this.engine.fileIndex;
	}

	private tick(): void {
		const now = Date.now();
		const dt = now - this.lastTickAt;
		this.lastTickAt = now;
		if (!this.engine.book) {
			this.clear();
			return;
		}
		if (this.mode === 'chapter') {
			if (this.engine.status === 'ended') {
				this.fire();
				return;
			}
			const cur = this.mark();
			// While paused, a manual seek re-baselines instead of firing.
			if (this.engine.status !== 'playing') this.markIndex = cur;
			else if (cur !== this.markIndex) this.fire();
			return;
		}
		if (this.engine.status !== 'playing') return;
		this.remainingMs = Math.max(0, this.remainingMs - dt);
		if (this.remainingMs === 0) this.fire();
	}

	private fire(): void {
		this.clear();
		this.engine.fadeOutAndPause(FADE_MS);
		this.engine.notice = { text: 'Sleep timer — pausing' };
	}

	private clear(): void {
		if (this.timer) clearInterval(this.timer);
		this.timer = null;
		this.mode = 'off';
		this.remainingMs = 0;
		this.markIndex = 0;
	}
}

export const sleepTimer = new SleepTimer(player);
