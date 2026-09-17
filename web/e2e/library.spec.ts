import { expect, test } from '@playwright/test';
import { BOOKS } from './fixtures/server';

test('both scanned books appear as cards', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('link', { name: new RegExp(BOOKS.chaptered.title, 'i') })).toBeVisible();
	await expect(page.getByRole('link', { name: new RegExp(BOOKS.multiFile.title, 'i') })).toBeVisible();
});

test('the search box narrows the grid to one book', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('link', { name: new RegExp(BOOKS.multiFile.title, 'i') })).toBeVisible();

	await page.getByPlaceholder('Search').fill('Clockwork');

	// The query is debounced and re-fetched from the server.
	await expect(page.getByRole('link', { name: new RegExp(BOOKS.multiFile.title, 'i') })).toHaveCount(0);
	await expect(
		page.getByRole('link', { name: new RegExp(BOOKS.chaptered.title, 'i') }).first()
	).toBeVisible();
});

test('opening a card shows the book page with its title and chapters', async ({ page }) => {
	await page.goto('/');
	await page
		.getByRole('link', { name: new RegExp(BOOKS.chaptered.title, 'i') })
		.first()
		.click();

	await expect(page).toHaveURL(/\/book\/\d+$/);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.chaptered.title })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Chapters' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'The Gate Opens' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'The Gate Closes' })).toBeVisible();
});
