import { expect, test } from '@playwright/test';
import { bookByTitle, CSRF } from './fixtures/api';
import { ADMIN_PASSWORD, ADMIN_USER, BOOKS } from './fixtures/server';
import { resolveBaseURL } from './fixtures/state';

const SEEDED_MS = 3_000;
/** Positions for the staleness test; both inside the 8 s fixture book. */
const FRESH_MS = 5_000;
const STALE_MS = 1_000;
/** Where "another device" is, inside the 9 s two-file fixture book. */
const OTHER_DEVICE_MS = 6_000;
/** A rejected offline listen and the server position that must replace it. */
const OFFLINE_MS = 1_000;
const ADOPTED_MS = 6_000;

test('seeded progress surfaces in the UI and an older listen is refused', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.chaptered.title);

	const now = Date.now();
	const seed = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: SEEDED_MS,
			file_index: 0,
			client_listened_at: now,
			client_now: now,
			base_seq: 0,
			finished: false
		}
	});
	expect(seed.status(), await seed.text()).toBe(200);

	// The library shows it in "Continue listening"...
	await page.goto('/');
	await expect(page.getByRole('heading', { name: /continue listening/i })).toBeVisible();

	// ...and the book page offers to resume rather than to start over.
	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('button', { name: /resume from/i })).toBeVisible();

	// A report describing a listen an hour older must lose to the stored one.
	const older = Date.now();
	const stale = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: 500,
			file_index: 0,
			client_listened_at: older - 60 * 60 * 1000,
			client_now: older,
			base_seq: 0,
			finished: false
		}
	});
	expect(stale.status(), await stale.text()).toBe(409);
	const current = (await stale.json()) as { position_ms: number };
	expect(current.position_ms).toBe(SEEDED_MS);
});

test('a report describing an old listen loses to a newer one', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.chaptered.title);

	// The record another device just wrote.
	const now = Date.now();
	const seed = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: FRESH_MS,
			file_index: 0,
			client_listened_at: now,
			client_now: now,
			base_seq: 0,
			finished: false
		}
	});
	expect(seed.status(), await seed.text()).toBe(200);

	// What a player that has been sitting paused for two hours sends when the
	// page hides: the body is built now, but it describes a two-hour-old listen.
	const late = Date.now();
	const stale = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: STALE_MS,
			file_index: 0,
			client_listened_at: late - 2 * 60 * 60 * 1000,
			client_now: late,
			base_seq: 0,
			finished: false
		}
	});
	expect(stale.status(), await stale.text()).toBe(409);
	expect(((await stale.json()) as { position_ms: number }).position_ms).toBe(FRESH_MS);
});

test('the hide report from a paused player cannot clobber a newer listen', async ({ page }) => {
	// A real pause has to sit for longer than the server's tie window.
	test.slow();
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);

	const start = Date.now();
	const reset = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: { position_ms: 0, file_index: 0, client_listened_at: start, client_now: start, base_seq: 0 }
	});
	expect(reset.status(), await reset.text()).toBe(200);

	// Listen for a moment, then stop.
	await page.goto(`/book/${book.id}`);
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });
	await pauseButton.click();
	await expect(page.getByRole('button', { name: 'Play', exact: true }).last()).toBeVisible();

	// Time passes, and another device listens further along.
	await page.waitForTimeout(4_000);
	const other = Date.now();
	const newer = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: OTHER_DEVICE_MS,
			file_index: 1,
			client_listened_at: other,
			client_now: other,
			base_seq: 0,
			finished: false
		}
	});
	expect(newer.status(), await newer.text()).toBe(200);

	// Leaving the page fires the hide/beacon report. It describes the listen that
	// ended before the other device's, so the server must refuse it.
	// The relaunch also restores the paused player and loads prefs, whose rate
	// change makes the reporter send again. That report must describe the old
	// listen too, not "now". Wait for the restore and the prefs round trip, then
	// give any report it triggers time to land before asking the server.
	await page.goto('/');
	await expect(page.getByRole('heading', { name: /library/i })).toBeVisible();
	// The relaunch restores the paused player; give it (and anything it
	// reports) time to settle before asking the server what stands.
	await expect(page.getByRole('slider', { name: 'Position' })).toBeVisible({ timeout: 15_000 });
	await page.waitForTimeout(1_000);

	const after = await page.request.get(`/api/v1/progress/${book.id}`);
	expect(after.status(), await after.text()).toBe(200);
	expect(((await after.json()) as { position_ms: number }).position_ms).toBe(OTHER_DEVICE_MS);
});

test('a relaunched paused player does not claim a fresh listen', async ({ page, playwright }) => {
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);
	// Another device: its own session, so its writes carry a different device id.
	const other = await playwright.request.newContext({ baseURL: resolveBaseURL() });
	const login = await other.post('/api/v1/auth/login', {
		headers: CSRF,
		data: { username: ADMIN_USER, password: ADMIN_PASSWORD, device_name: 'Other device' }
	});
	expect(login.status(), await login.text()).toBe(200);
	// This device never hears about the other one while the page is open.
	await page.route('**/api/v1/events', (route) => route.abort());

	// Listen for a moment here, then stop.
	await page.goto(`/book/${book.id}`);
	await page.getByRole('button', { name: /play|resume/i }).first().click();
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });
	await pauseButton.click();
	await expect(page.getByRole('button', { name: 'Play', exact: true }).last()).toBeVisible();
	await page.waitForTimeout(3_000);

	// The other device listens further along.
	const otherAt = Date.now();
	const newer = await other.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: OTHER_DEVICE_MS,
			file_index: 1,
			client_listened_at: otherAt,
			client_now: otherAt,
			base_seq: 0,
			finished: false
		}
	});
	expect(newer.status(), await newer.text()).toBe(200);
	await page.waitForTimeout(2_000);

	// Relaunch. The player is restored paused, and loading prefs makes it
	// report. That report must describe the old listen, never the relaunch.
	const reported = page.waitForRequest(
		(r) => r.method() === 'PUT' && r.url().includes(`/api/v1/progress/${book.id}`)
	);
	await page.goto('/');
	await expect(page.getByRole('slider', { name: 'Position' })).toBeVisible({ timeout: 15_000 });
	await page.keyboard.press(']'); // a rate change is reported immediately
	const body = (await reported).postDataJSON() as { client_listened_at: number; client_now: number };
	expect(body.client_now - body.client_listened_at).toBeGreaterThan(1_500);

	const after = await page.request.get(`/api/v1/progress/${book.id}`);
	expect(((await after.json()) as { position_ms: number }).position_ms).toBe(OTHER_DEVICE_MS);
	await other.dispose();
});

test('a journal entry the server refuses stops being the resume position', async ({ page }) => {
	const book = await bookByTitle(page.request, BOOKS.chaptered.title);
	const me = await page.request.get('/api/v1/auth/me');
	expect(me.status(), await me.text()).toBe(200);
	const userId = ((await me.json()) as { user: { id: number } }).user.id;

	// Where the record stands after somebody else's later listen.
	const now = Date.now();
	const seed = await page.request.put(`/api/v1/progress/${book.id}`, {
		headers: CSRF,
		data: {
			position_ms: ADOPTED_MS,
			file_index: 0,
			client_listened_at: now,
			client_now: now,
			base_seq: 0,
			finished: false
		}
	});
	expect(seed.status(), await seed.text()).toBe(200);

	// This device listened two hours ago while offline and never got through.
	await page.goto('/');
	await page.evaluate(
		({ userId: uid, bookId, positionMs }) => {
			localStorage.setItem(
				`sk:journal:${uid}:${bookId}`,
				JSON.stringify({
					bookId,
					fileIndex: 0,
					positionMs,
					rate: 1,
					playing: false,
					clientTs: Date.now() - 2 * 60 * 60 * 1000,
					serverSeq: 0,
					serverListenedAt: 0,
					synced: false
				})
			);
			localStorage.setItem(`sk:last:${uid}`, String(bookId));
		},
		{ userId, bookId: book.id, positionMs: OFFLINE_MS }
	);

	// On the next launch the flush is refused, and what the server says must be
	// what the restored player resumes at.
	await page.reload();
	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect(scrubber).toBeVisible({ timeout: 15_000 });
	await expect(scrubber).toHaveValue(String(ADOPTED_MS), { timeout: 15_000 });

	const entry = await page.evaluate(
		({ userId: uid, bookId }) => localStorage.getItem(`sk:journal:${uid}:${bookId}`),
		{ userId, bookId: book.id }
	);
	expect(entry).toBeTruthy();
	const parsed = JSON.parse(entry!) as { positionMs: number; synced: boolean };
	expect(parsed.synced).toBe(true);
	expect(parsed.positionMs).toBe(ADOPTED_MS);
});
