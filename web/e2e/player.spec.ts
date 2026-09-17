import { expect, test } from '@playwright/test';
import { bookByTitle, CSRF } from './fixtures/api';
import { BOOKS } from './fixtures/server';

test('playing a book opens the bottom player and reports progress', async ({ page }) => {
	// The mp3 book: Playwright's Chromium decodes mp3 everywhere, AAC not always.
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);

	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();

	// The page's own primary button comes before the player bar in the DOM, so
	// .first() picks it even once the bar renders a Play/Pause of its own.
	const startButton = page.getByRole('button', { name: /play|resume/i }).first();
	await expect(startButton).toBeVisible();

	const progressPut = page.waitForRequest(
		(req) => req.method() === 'PUT' && req.url().includes(`/api/v1/progress/${book.id}`)
	);

	await startButton.click();

	// The bottom player bar: its scrub slider is labelled "Position".
	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect(scrubber).toBeVisible();

	// Playback really started (the bar swaps to a Pause control).
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });

	await progressPut;

	await pauseButton.click();
	await expect(page.getByRole('button', { name: 'Play', exact: true }).last()).toBeVisible();

	const res = await page.request.get(`/api/v1/progress/${book.id}`);
	expect(res.status(), await res.text()).toBe(200);
	const progress = (await res.json()) as {
		position_ms: number;
		device_id: string;
		book_id: number;
	};
	expect(progress.position_ms).toBeGreaterThanOrEqual(0);
	expect(progress.device_id).toBeTruthy();
});

test('playback rolls into the next file at a boundary instead of stopping', async ({ page }) => {
	// Real time passes here: a whole audio file has to play out.
	test.slow();
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);
	const secondPart = BOOKS.multiFile.partTitles[1];
	const boundaryMs = BOOKS.multiFile.partSeconds[0] * 1000;

	// Start from the top whatever an earlier spec left in the record: "now" beats
	// any listen those wrote, so this is always accepted.
	const now = Date.now();
	const reset = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: { position_ms: 0, file_index: 0, client_listened_at: now, client_now: now, base_seq: 0 }
	});
	expect(reset.status(), await reset.text()).toBe(200);

	await page.goto(`/book/${book.id}`);
	await page.getByRole('button', { name: /play|resume/i }).first().click();

	const bar = page.locator('.player');
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });

	// The first part is only a few seconds long, and browsers fire `pause` just
	// before `ended`: this boundary is where playback used to stop dead.
	await expect(bar).toContainText(secondPart, { timeout: 20_000 });
	await expect(pauseButton).toBeVisible();

	// Still moving, not parked at the start of part two.
	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect
		.poll(async () => Number(await scrubber.inputValue()), { timeout: 15_000 })
		.toBeGreaterThan(boundaryMs + 900);
});
