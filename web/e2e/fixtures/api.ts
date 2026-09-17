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
