import { expect, test } from '@playwright/test';
import { bookByTitle, resetAllProgress } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// The fixture books are shared with later specs; leave them untouched.
test.afterEach(async ({ page }) => {
	await resetAllProgress(page.request);
});

// Test-only hook: capture the player's single Audio element on window so a
// spec can read its live playbackRate without reaching into module
// internals (nothing in the app itself exposes this).
async function tapAudioElement(page: import('@playwright/test').Page): Promise<void> {
	await page.addInitScript(() => {
		const OrigAudio = window.Audio;
		class TappedAudio extends OrigAudio {
			constructor(...args: ConstructorParameters<typeof OrigAudio>) {
				super(...args);
				(window as unknown as { __audio: HTMLAudioElement }).__audio = this;
			}
		}
		window.Audio = TappedAudio as unknown as typeof OrigAudio;
	});
}

async function playbackRate(page: import('@playwright/test').Page): Promise<number | undefined> {
	return page.evaluate(
		() => (window as unknown as { __audio?: HTMLAudioElement }).__audio?.playbackRate
	);
}

test('ArrowRight skips forward and ] steps the rate up', async ({ page }) => {
	await tapAudioElement(page);
	// The chaptered book: a single 8s file, so the fixture library needs no
	// second request to have loaded before the shortcuts are exercised.
	const book = await bookByTitle(page.request, BOOKS.chaptered.title);

	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.chaptered.title })).toBeVisible();

	await page.getByRole('button', { name: /play|resume/i }).first().click();

	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect(scrubber).toBeVisible();
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });

	const before = Number(await scrubber.inputValue());
	await page.keyboard.press('ArrowRight');

	// The fixture book is only ~8s long, far shorter than the 30s default skip
	// (skipForwardMs), so the skip clamps at (or within a fraction of a second
	// of) the end of the timeline -- the decoded AAC duration can be a touch
	// shorter than the muxed duration the scrubber's max is built from. That
	// clamp is itself evidence the shortcut called skip(+skipForwardMs), not
	// some smaller native step.
	await expect.poll(async () => Number(await scrubber.inputValue())).toBeGreaterThan(before);
	const after = Number(await scrubber.inputValue());
	const max = Number(await scrubber.getAttribute('max'));
	expect(after).toBeGreaterThan(max - 500);

	await page.keyboard.press(']');
	await expect.poll(() => playbackRate(page)).toBeCloseTo(1.1, 5);
});

test('Space is ignored while focus is on the library search box', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.chaptered.title);
	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.chaptered.title })).toBeVisible();

	await page.getByRole('button', { name: /play|resume/i }).first().click();
	const pauseButton = page.locator('.player').getByRole('button', { name: 'Pause', exact: true });
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });

	// Client-side navigation (not page.goto) so the player bar, and its
	// playing state, survives the route change to the library page.
	await page.getByRole('navigation').getByRole('link', { name: 'Library' }).click();
	await expect(page.getByRole('heading', { name: /library/i })).toBeVisible();
	await expect(pauseButton).toBeVisible();

	const search = page.getByRole('searchbox', { name: 'Search the library' });
	await search.focus();
	await expect(search).toBeFocused();

	await page.keyboard.press(' ');

	// Still playing: Space landed in the search box (isTypingTarget), not the
	// shortcut handler, so the player never toggled.
	await expect(pauseButton).toBeVisible();
	await expect(search).toHaveValue(' ');
});
