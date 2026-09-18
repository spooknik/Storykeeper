import { expect, test } from '@playwright/test';
import { CSRF, bookByTitle, resetAllProgress } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// The fixture books are shared with later specs; leave them untouched.
test.afterEach(async ({ page }) => {
	await resetAllProgress(page.request);
});

// Neither fixture book carries series metadata by default (see fixtures/server.ts),
// so this spec turns the two of them into a two-book series through the admin
// metadata PATCH, in series order: the chaptered book first, the multi-file book
// second.
const SERIES_NAME = 'E2E Series';

test('series badge and "Next up" reflect per-user progress', async ({ page }) => {
	const first = await bookByTitle(page.request, BOOKS.chaptered.title);
	const second = await bookByTitle(page.request, BOOKS.multiFile.title);

	for (const [book, seq] of [
		[first, '1'],
		[second, '2']
	] as const) {
		const patch = await page.request.patch(`/api/v1/books/${book.id}`, {
			headers: CSRF,
			data: { series: SERIES_NAME, series_seq: seq }
		});
		expect(patch.status(), await patch.text()).toBe(200);
	}

	// Mark the first book (by series order) finished via the progress API.
	const now = Date.now();
	const finish = await page.request.put(`/api/v1/progress/${first.id}`, {
		headers: CSRF,
		data: {
			position_ms: 4000,
			file_index: 0,
			client_listened_at: now,
			client_now: now,
			base_seq: 0,
			finished: true
		}
	});
	expect(finish.status(), await finish.text()).toBe(200);

	// The series list shows "1 of 2 finished".
	await page.goto('/series');
	const seriesLink = page.getByRole('link', { name: new RegExp(SERIES_NAME) });
	await expect(seriesLink).toBeVisible();
	await expect(seriesLink).toContainText('1 of 2 finished');

	// The series page's "Next up" is the second book, the first one still unfinished.
	await seriesLink.click();
	await expect(page).toHaveURL(new RegExp(`/series/${encodeURIComponent(SERIES_NAME)}$`));
	await expect(page.getByRole('heading', { name: 'Next up' })).toBeVisible();

	const nextUpCard = page.locator('.next-up-card');
	await expect(nextUpCard).toContainText(BOOKS.multiFile.title);
	await expect(nextUpCard).toHaveAttribute('href', `/book/${second.id}`);
});
