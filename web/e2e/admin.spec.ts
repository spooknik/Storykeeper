import { expect, test } from '@playwright/test';
import { ADMIN_USER } from './fixtures/server';

test('the admin page lists the bootstrapped admin and can create a user', async ({ page }) => {
	await page.goto('/admin');
	await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible();
	// .first(): the username cell. The role cell next to it exposes its <select>
	// options, so "admin" appears there too.
	await expect(page.getByRole('cell', { name: ADMIN_USER, exact: true }).first()).toBeVisible();

	// Unique per run so a retry does not trip over "username already taken".
	const username = `listener${Date.now().toString().slice(-6)}`;
	const form = page.locator('form').filter({ has: page.getByRole('button', { name: /create user/i }) });
	await form.getByLabel('Username').fill(username);
	await form.getByLabel('Password').fill('listener-pass-123');
	await form.getByRole('button', { name: /create user/i }).click();

	await expect(page.getByRole('cell', { name: username, exact: true })).toBeVisible();

	const res = await page.request.get('/api/v1/users');
	expect(res.status()).toBe(200);
	const users = (await res.json()) as { username: string }[];
	expect(users.map((u) => u.username)).toContain(username);
});

test('settings lists the current session as this device', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Sessions' })).toBeVisible();
	await expect(page.getByText('this device')).toBeVisible();
	await expect(page.getByRole('table')).toContainText(/browser|windows|mac|linux|android|ip(hone|ad)/i);
});
