import { expect, test } from '@playwright/test';
import { ADMIN_PASSWORD, ADMIN_USER } from './fixtures/server';

// These run against a signed-out browser, so the stored session is dropped.
test.use({ storageState: { cookies: [], origins: [] } });

test('a wrong password is refused with a visible error', async ({ page }) => {
	await page.goto('/login');
	await page.getByLabel('Username').fill(ADMIN_USER);
	await page.getByLabel('Password').fill('definitely-not-the-password');
	await page.getByRole('button', { name: /sign in/i }).click();

	await expect(page.getByText(/invalid username or password/i)).toBeVisible();
	await expect(page).toHaveURL(/\/login$/);
});

test('a correct password lands on the library and /auth/me knows the user', async ({ page }) => {
	await page.goto('/login');
	await page.getByLabel('Username').fill(ADMIN_USER);
	await page.getByLabel('Password').fill(ADMIN_PASSWORD);
	await page.getByRole('button', { name: /sign in/i }).click();

	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('heading', { name: /library/i })).toBeVisible();

	const res = await page.request.get('/api/v1/auth/me');
	expect(res.status()).toBe(200);
	const body = (await res.json()) as {
		user: { username: string; role: string };
		session: { device_id: string };
	};
	expect(body.user.username).toBe(ADMIN_USER);
	expect(body.user.role).toBe('admin');
	expect(body.session.device_id).not.toBe('');
});
