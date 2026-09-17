import { expect, test } from '@playwright/test';
import { bookByTitle } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// .first(): a book with progress is also on the "Continue listening" shelf, so
// its card can legitimately be on the page twice.
test('both scanned books appear as cards', async ({ page }) => {
	await page.goto('/');
	await expect(
		page.getByRole('link', { name: new RegExp(BOOKS.chaptered.title, 'i') }).first()
	).toBeVisible();
	await expect(
		page.getByRole('link', { name: new RegExp(BOOKS.multiFile.title, 'i') }).first()
	).toBeVisible();
});

test('the search box narrows the grid to one book', async ({ page }) => {
	await page.goto('/');
	await expect(
		page.getByRole('link', { name: new RegExp(BOOKS.multiFile.title, 'i') }).first()
	).toBeVisible();

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

test('following the player bar to another book replaces the page', async ({ page }) => {
	const a = await bookByTitle(page.request, BOOKS.chaptered.title);
	const b = await bookByTitle(page.request, BOOKS.multiFile.title);

	// Get book B into the bottom bar, so its link is on every page.
	await page.goto(`/book/${b.id}`);
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	await expect(page.getByRole('slider', { name: 'Position' })).toBeVisible({ timeout: 15_000 });

	// Navigate (client side) to book A...
	await page.getByRole('link', { name: /library/i }).first().click();
	await page
		.getByRole('link', { name: new RegExp(BOOKS.chaptered.title, 'i') })
		.first()
		.click();
	await expect(page).toHaveURL(new RegExp(`/book/${a.id}$`));
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.chaptered.title })).toBeVisible();

	// ...and then straight from book A to book B through the player bar, which
	// keeps this same page component mounted.
	await page.locator('.player a.meta').click();
	await expect(page).toHaveURL(new RegExp(`/book/${b.id}$`));
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.chaptered.title })).toHaveCount(0);
});
