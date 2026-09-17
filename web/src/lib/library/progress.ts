// Overlays the live SSE progress stream on the books a page has already fetched.

import type { BookSummary, Progress } from '$lib/api/types';
import { events } from '$lib/events.svelte';

/** Newest known progress for a book: the live event when it is not older than the fetched one. */
export function liveProgress(b: BookSummary): Progress | undefined {
	const live = events.progress[b.id];
	if (live && (!b.progress || live.seq >= b.progress.seq)) return live;
	return b.progress;
}

/** 0 to 100 for the card progress bar. */
export function progressPct(b: BookSummary): number {
	const p = liveProgress(b);
	if (!p || b.duration_ms === 0) return 0;
	return Math.min(100, (p.position_ms / b.duration_ms) * 100);
}

export function isFinished(b: BookSummary): boolean {
	return liveProgress(b)?.finished ?? false;
}
