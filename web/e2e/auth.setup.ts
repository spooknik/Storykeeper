// Logs in once through the real login form and stores the session cookie so
// every other project starts authenticated.

import { expect, test as setup } from '@playwright/test';
import { ADMIN_PASSWORD, ADMIN_USER } from './fixtures/server';
import { STORAGE_STATE } from './fixtures/state';

setup('authenticate as the bootstrapped admin', async ({ page }) => {
	await page.goto('/login');
	await page.getByLabel('Username').fill(ADMIN_USER);
	await page.getByLabel('Password').fill(ADMIN_PASSWORD);
	await page.getByRole('button', { name: /sign in/i }).click();

	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('heading', { name: /library/i })).toBeVisible();

	await page.context().storageState({ path: STORAGE_STATE });
});
