// Small API helpers shared by the specs. They use the page's own request
// context, so they carry the same session cookie as the browser.

import { expect, type APIRequestContext } from '@playwright/test';

export interface BookSummary {
	id: number;
	title: string;
	duration_ms: number;
}

export async function listBooks(request: APIRequestContext): Promise<BookSummary[]> {
	const res = await request.get('/api/v1/books');
	expect(res.status(), await res.text()).toBe(200);
	return ((await res.json()) as { items: BookSummary[] }).items;
}

export async function bookByTitle(request: APIRequestContext, title: string): Promise<BookSummary> {
	const books = await listBooks(request);
	const found = books.find((b) => b.title === title);
	expect(found, `book ${title} not in ${JSON.stringify(books.map((b) => b.title))}`).toBeTruthy();
	return found!;
}

/** The CSRF header every mutating /api/ request must carry. */
export const CSRF = { 'X-Storykeeper': '1' };

/**
 * Put a fixture book back to "never listened": position 0, not finished, no
 * per-book rate. The fixture books are seconds long, so any spec that plays
 * one tends to finish it, and `finished` is carried forward by later reports
 * unless a report says otherwise.
 */
export async function resetProgress(request: APIRequestContext, bookId: number): Promise<void> {
	const now = Date.now();
	const res = await request.put(`/api/v1/progress/${bookId}`, {
		headers: CSRF,
		data: {
			position_ms: 0,
			file_index: 0,
			client_listened_at: now,
			client_now: now,
			base_seq: 0,
			finished: false
		}
	});
	expect(res.status(), await res.text()).toBe(200);
	const rate = await request.put(`/api/v1/progress/${bookId}/rate`, {
		headers: CSRF,
		data: { playback_rate: null }
	});
	expect(rate.status(), await rate.text()).toBe(200);
}

/** Reset every fixture book; for specs that play or finish them. */
export async function resetAllProgress(request: APIRequestContext): Promise<void> {
	for (const b of await listBooks(request)) await resetProgress(request, b.id);
}
