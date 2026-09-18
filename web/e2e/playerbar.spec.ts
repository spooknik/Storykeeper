import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { bookByTitle, resetAllProgress } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// Screenshots land under the repo's frugal-fable work log for this slice.
const SCREEN_DIR = path.join('.frugal-fable', 'v1-1-polish', 's7-playerbar');
fs.mkdirSync(SCREEN_DIR, { recursive: true });

// The fixture books are shared with later specs; leave them untouched.
test.afterEach(async ({ page }) => {
	await resetAllProgress(page.request);
});

test('the desktop player bar surfaces author, remaining time, chapter and a scrub preview', async ({
	page
}) => {
	await page.setViewportSize({ width: 1280, height: 800 });

	// The multi-file (mp3) fixture book: two parts, so it has real chapters.
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);

	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();
	await page.getByRole('button', { name: /play|resume/i }).first().click();

	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect(scrubber).toBeVisible({ timeout: 15_000 });

	const bar = page.locator('.player');
	await expect(bar).toContainText(BOOKS.multiFile.author);

	const remaining = page.getByTestId('remaining');
	await expect(remaining).toBeVisible();
	await expect(remaining).toContainText('-');

	const chapterTitle = bar.locator('.chapter-title');
	await expect(chapterTitle).toBeVisible();

	// No pointer over the track yet: the preview bubble isn't rendered. Scoped to
	// the desktop bar because the (hidden) phone layout renders its own copy of
	// the same scrub track, with its own preview bubble, off-screen via CSS.
	const desktopBar = page.locator('.desktop-bar');
	const preview = desktopBar.getByTestId('scrub-preview');
	await expect(preview).toBeHidden();

	// Hovering the scrub track (a hover-capable pointer) raises the bubble,
	// showing the time it would seek to and, since this book has chapters,
	// which one that time falls in.
	await scrubber.hover();
	await expect(preview).toBeVisible();
	await expect(preview).toContainText(/\d+:\d\d/);

	await page.screenshot({ path: path.join(SCREEN_DIR, 'desktop.png'), fullPage: false });

	// Same bar, phone width: the compact layout, unchanged in structure.
	await page.setViewportSize({ width: 390, height: 844 });
	await expect(scrubber).toBeVisible();
	await page.screenshot({ path: path.join(SCREEN_DIR, 'mobile.png'), fullPage: false });
});
