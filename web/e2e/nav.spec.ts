import { expect, test } from '@playwright/test';
import { bookByTitle } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// Phone-sized viewport: this is the only width where Nav hides and the tab
// bar (TabBar.svelte) takes over.
test.use({ viewport: { width: 390, height: 844 } });

test('the tab bar replaces Nav on a phone', async ({ page }) => {
	await page.goto('/');
	await expect(page.locator('nav.tabbar')).toBeVisible();
	await expect(page.locator('nav.nav')).toBeHidden();
});

test('tapping Settings in the tab bar lands on /settings', async ({ page }) => {
	await page.goto('/');
	const tabbar = page.locator('nav.tabbar');
	await tabbar.getByRole('link', { name: 'Settings' }).click();
	await expect(page).toHaveURL(/\/settings$/);
	await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
});

test('the Browse tab opens a sheet listing Series and navigates to it', async ({ page }) => {
	await page.goto('/');
	const tabbar = page.locator('nav.tabbar');
	await tabbar.getByRole('button', { name: 'Browse' }).click();

	const sheet = page.getByRole('dialog', { name: 'Browse' });
	await expect(sheet).toBeVisible();
	await expect(sheet.getByRole('button', { name: 'Series' })).toBeVisible();

	await sheet.getByRole('button', { name: 'Series' }).click();
	await expect(page).toHaveURL(/\/series$/);
	await expect(page.getByRole('heading', { level: 1, name: 'Series' })).toBeVisible();
});

test('the Search tab focuses the library search box', async ({ page }) => {
	await page.goto('/');
	const tabbar = page.locator('nav.tabbar');
	await tabbar.getByRole('button', { name: 'Search' }).click();

	const search = page.getByLabel('Search the library');
	await expect(search).toBeFocused();
	// Already on the library page: focused in place, no navigation.
	await expect(page).toHaveURL(/\/$/);
});

test('the Search tab works from another page too', async ({ page }) => {
	await page.goto('/settings');
	await page.locator('nav.tabbar').getByRole('button', { name: 'Search' }).click();

	await expect(page.getByLabel('Search the library')).toBeFocused();
	// The ?focus=1 handoff param is stripped after use so a reload does not refocus.
	await expect(page).toHaveURL(/\/$/);
});

test('the mini player sits above the tab bar, not underneath it', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);

	await page.goto(`/book/${book.id}`);
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	await expect(page.getByRole('slider', { name: 'Position' })).toBeVisible({ timeout: 15_000 });

	const tabbar = page.locator('nav.tabbar');
	const playerBox = await page.locator('.player').boundingBox();
	const tabbarBox = await tabbar.boundingBox();
	expect(playerBox).not.toBeNull();
	expect(tabbarBox).not.toBeNull();
	// The player's bottom edge should land right at (never below) the tab bar's
	// top; allow a couple of sub-pixel rendering units of slack.
	expect(playerBox!.y + playerBox!.height).toBeLessThanOrEqual(tabbarBox!.y + 2);
});
