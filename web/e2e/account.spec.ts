import { expect, test, type Page } from '@playwright/test';
import { bookByTitle, CSRF } from './fixtures/api';
import { ADMIN_PASSWORD, ADMIN_USER, BOOKS } from './fixtures/server';

// This spec drives the login form itself, so it starts signed out.
test.use({ storageState: { cookies: [], origins: [] } });

/**
 * Fill in the login form that is already on screen. Deliberately no page.goto:
 * a reload would clear the engine by itself and hide exactly what this spec is
 * about.
 */
async function signIn(page: Page, username: string, password: string): Promise<void> {
	await expect(page).toHaveURL(/\/login$/);
	await page.getByLabel('Username').fill(username);
	await page.getByLabel('Password').fill(password);
	await page.getByRole('button', { name: /sign in/i }).click();
	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('heading', { name: /library/i })).toBeVisible();
}

test('signing in as another user leaves nothing of the previous one behind', async ({ page }) => {
	// There is a deliberate wait: the old player's heartbeat gets its chance.
	test.slow();
	const scrubber = page.getByRole('slider', { name: 'Position' });
	// Unique per run so a retry does not trip over "username already taken".
	const second = `switcher${Date.now().toString().slice(-6)}`;
	const password = 'switcher-pass-123';

	await page.goto('/login');
	await signIn(page, ADMIN_USER, ADMIN_PASSWORD);
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);

	const created = await page.request.post('/api/v1/users', {
		headers: CSRF,
		data: { username: second, password, role: 'user' }
	});
	expect(created.status(), await created.text()).toBeLessThan(300);

	// The admin listens, so the engine holds a book, a position and a seq.
	await page.goto(`/book/${book.id}`);
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	await expect(scrubber).toBeVisible({ timeout: 15_000 });

	// Rewind, so there is plenty of audio left to keep the old engine running.
	await page.getByRole('button', { name: 'Back 30 seconds' }).click();

	// Client-side from here on: a reload would tear the engine down anyway.
	await page.getByRole('link', { name: 'Settings' }).first().click();
	await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
	await page.getByRole('button', { name: /sign out/i }).click();

	await signIn(page, second, password);

	// No player bar for a user who has not played anything...
	await expect(scrubber).toHaveCount(0);
	// ...and no heartbeat carrying the admin's position into their history.
	// The reporter's first tick would land within about five seconds.
	await page.waitForTimeout(7_000);
	await expect(scrubber).toHaveCount(0);
	const res = await page.request.get(`/api/v1/progress/${book.id}`);
	expect(res.status(), await res.text()).toBe(404);
});
