import { expect, test } from '@playwright/test';
import { bookByTitle, CSRF } from './fixtures/api';
import { BOOKS } from './fixtures/server';

const SEEDED_MS = 3_000;

test('seeded progress surfaces in the UI and an older listen is refused', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.chaptered.title);

	const now = Date.now();
	const seed = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: SEEDED_MS,
			file_index: 0,
			client_listened_at: now,
			client_now: now,
			base_seq: 0
		}
	});
	expect(seed.status(), await seed.text()).toBe(200);

	// The library shows it in "Continue listening"...
	await page.goto('/');
	await expect(page.getByRole('heading', { name: /continue listening/i })).toBeVisible();

	// ...and the book page offers to resume rather than to start over.
	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('button', { name: /resume from/i })).toBeVisible();

	// A report describing a listen an hour older must lose to the stored one.
	const older = Date.now();
	const stale = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: 500,
			file_index: 0,
			client_listened_at: older - 60 * 60 * 1000,
			client_now: older,
			base_seq: 0
		}
	});
	expect(stale.status(), await stale.text()).toBe(409);
	const current = (await stale.json()) as { position_ms: number };
	expect(current.position_ms).toBe(SEEDED_MS);
});
