import { expect, test, type Page } from '@playwright/test';
import { CSRF, bookByTitle, resetAllProgress } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// The fixture books are shared with later specs; leave them untouched.
test.afterEach(async ({ page }) => {
	await resetAllProgress(page.request);
});

// These specs exercise the prefs store (web/src/lib/player/prefs.svelte.ts) and
// the settings/player UI built on it. The store isn't wired into +layout.svelte
// yet, so every path here goes through something that calls prefs.load()/save()
// itself: the settings page (onMount and its controls) or Player.svelte's speed
// sheet. Navigation after that stays client-side (link clicks, not page.goto())
// so the in-memory player/prefs singletons — and whatever they just applied —
// survive into the next page, exactly as they would once the layout is wired.

// The admin account (and its books) are shared with every other spec in the
// suite; put the server-side defaults back so a later spec that happens to
// visit Settings or load one of these books never sees this spec's changes.
test.afterEach(async ({ page }) => {
	await page.request.put('/api/v1/me/prefs', { headers: CSRF, data: { skip_back_seconds: 30 } });
	for (const title of [BOOKS.multiFile.title, BOOKS.chaptered.title]) {
		const b = await bookByTitle(page.request, title);
		await page.request.put(`/api/v1/progress/${b.id}/rate`, {
			headers: CSRF,
			data: { playback_rate: null }
		});
	}
});

test('changing the skip-back amount in Settings updates the player button label', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();

	await page.getByLabel('Skip back').selectOption('15');

	await expect
		.poll(async () => (await page.request.get('/api/v1/me/prefs')).json())
		.toMatchObject({ skip_back_seconds: 15 });

	await page.getByLabel('Library').click();
	await page.getByRole('link', { name: BOOKS.multiFile.title }).first().click();
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();
	await page.getByRole('button', { name: /play|resume/i }).first().click();

	await expect(page.getByRole('button', { name: 'Back 15 seconds' })).toBeVisible({ timeout: 15_000 });

	// This book is shared with other specs (e.g. sync.spec.ts) that seed their
	// own controlled progress for it. Pause and wait for that to be flushed
	// before the test ends, so no keepalive/beacon report from a still-"playing"
	// player is left in flight to race a later spec's deliberate write.
	await stopPlaying(page);
});

test('a per-book speed override applies only to that book', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);
	const otherBook = await bookByTitle(page.request, BOOKS.chaptered.title);

	// Clear any override either book kept from a previous run, so this test's
	// assertions are about the change it makes, not stale state.
	for (const id of [book.id, otherBook.id]) {
		await page.request.put(`/api/v1/progress/${id}/rate`, {
			headers: CSRF,
			data: { playback_rate: null }
		});
	}

	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect(scrubber).toBeVisible({ timeout: 15_000 });

	const speedButton = page.getByRole('button', { name: 'Speed' });
	const defaultLabel = (await speedButton.textContent())?.match(/[\d.]+×/)?.[0] ?? '1×';

	await speedButton.click();
	await expect(page.getByRole('dialog', { name: 'Playback speed' })).toBeVisible();
	await page.getByLabel('For this book only').check();
	await page.getByRole('button', { name: '1.5×', exact: true }).click();
	await page.getByRole('button', { name: 'Close', exact: true }).click();

	await expect(speedButton).toContainText('1.5×');
	await expect
		.poll(async () => (await page.request.get(`/api/v1/progress/${book.id}`)).json())
		.toMatchObject({ playback_rate: 1.5 });

	// Opening a different book drops the override: its effective rate falls
	// back to what it was before, and the other book's own record is untouched.
	await page.getByLabel('Library').click();
	await page.getByRole('link', { name: BOOKS.chaptered.title }).first().click();
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.chaptered.title })).toBeVisible();
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	await expect(scrubber).toBeVisible({ timeout: 15_000 });

	await expect(speedButton).toContainText(defaultLabel);

	const otherRes = await page.request.get(`/api/v1/progress/${otherBook.id}`);
	if (otherRes.status() === 200) {
		expect((await otherRes.json()).playback_rate).toBeNull();
	} else {
		expect(otherRes.status()).toBe(404);
	}

	// Same reasoning as the first spec: leave nothing "playing" behind for a
	// later spec's own use of these shared fixture books.
	await stopPlaying(page);
});

/**
 * Pause the loaded book and wait for the pause report to land, so no
 * hide/pagehide beacon is left pending once the page closes at the end of the
 * test. The chaptered (AAC) book sometimes never reaches "playing" in headless
 * Chromium, so this tolerates there being no Pause button to click.
 */
async function stopPlaying(page: Page): Promise<void> {
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	try {
		await pauseButton.waitFor({ state: 'visible', timeout: 5_000 });
	} catch {
		return;
	}
	const paused = page.waitForResponse(
		(res) => res.request().method() === 'PUT' && /\/api\/v1\/progress\/\d+$/.test(new URL(res.url()).pathname),
		{ timeout: 5_000 }
	);
	await pauseButton.click();
	await paused.catch(() => {});
}
