import { expect, test, type Page } from '@playwright/test';
import { bookByTitle, resetAllProgress, resetProgress } from './fixtures/api';
import { BOOKS } from './fixtures/server';

// The fixture books are shared with later specs; leave them untouched.
test.afterEach(async ({ page }) => {
	await resetAllProgress(page.request);
});

// Test-only hook, copied from keyboard.spec.ts: subclass window.Audio so the
// app's single element is reachable as window.__audio. Extended with an
// opt-in play() hang: when window.__hangPlay is set (by a script added
// *before* this one, order does not matter -- the flag is only read once the
// element is actually constructed, on the first Play click) the tapped
// element's play() never resolves and never fires `playing`, exactly what a
// dead WebKit media pipeline looks like from the engine's side.
async function tapAudioElement(page: Page): Promise<void> {
	await page.addInitScript(() => {
		const OrigAudio = window.Audio;
		class TappedAudio extends OrigAudio {
			constructor(...args: ConstructorParameters<typeof OrigAudio>) {
				super(...args);
				(window as unknown as { __audio: HTMLAudioElement }).__audio = this;
				if ((window as unknown as { __hangPlay?: boolean }).__hangPlay) {
					this.play = () => new Promise<void>(() => {});
				}
			}
		}
		window.Audio = TappedAudio as unknown as typeof OrigAudio;
	});
}

test('a browser-side element reset keeps the position out of the journal and the server', async ({
	page
}) => {
	await tapAudioElement(page);
	const book = await bookByTitle(page.request, BOOKS.multiFile.title);
	await resetProgress(page.request, book.id);

	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();

	await page
		.getByRole('button', { name: /play|resume/i })
		.first()
		.click();

	const scrubber = page.getByRole('slider', { name: 'Position' });
	const pauseButton = page.getByRole('button', { name: 'Pause', exact: true }).last();
	await expect(pauseButton).toBeVisible({ timeout: 15_000 });

	// Let real time pass into the first (3 s) part, but stop polling well
	// before the file boundary at 3000 ms so the position stays simple.
	await expect
		.poll(async () => Number(await scrubber.inputValue()), { timeout: 2_500, intervals: [100] })
		.toBeGreaterThan(1500);

	await pauseButton.click();
	await expect(page.getByRole('button', { name: 'Play', exact: true }).last()).toBeVisible();

	// The <input type=range step=1000> scrubber snaps whatever it is given to
	// the nearest 1000 ms (WHATWG value sanitization applies step-snapping to
	// range inputs even on a programmatic set), so it is only useful here for
	// "did the displayed value move" -- not as the precise tracked position.
	const scrubBefore = await scrubber.inputValue();

	const userId = (
		(await (await page.request.get('/api/v1/auth/me')).json()) as { user: { id: number } }
	).user.id;

	async function readJournalPositionMs(): Promise<number | null> {
		const raw = await page.evaluate(
			({ uid, bookId }) => localStorage.getItem(`sk:journal:${uid}:${bookId}`),
			{ uid: userId, bookId: book.id }
		);
		return raw ? (JSON.parse(raw) as { positionMs: number }).positionMs : null;
	}

	// The journal's own record is the precise, unsnapped tracked position (it
	// stores Math.round(positionMs), same rounding the server reports use), so
	// it -- not the scrubber -- is what the journal/network/server assertions
	// below compare against.
	const posBefore = await readJournalPositionMs();
	if (posBefore === null) throw new Error('journal entry missing after pause');
	expect(posBefore).toBeGreaterThan(1000);

	// Collect every progress write from here on: none of them, across the
	// simulated reset below, may describe position 0.
	const bodies: Array<{ position_ms?: number }> = [];
	page.on('request', (req) => {
		if (!req.url().includes(`/api/v1/progress/${book.id}`)) return;
		if (req.method() !== 'PUT' && req.method() !== 'POST') return;
		try {
			const data = req.postDataJSON() as { position_ms?: number } | null;
			if (data) bodies.push(data);
		} catch {
			// Non-JSON body (shouldn't happen for this endpoint): ignore.
		}
	});

	// Simulate WebKit reclaiming the media process behind the app's back:
	// readyState collapses to HAVE_NOTHING, currentTime reads 0, and `emptied`
	// fires without the engine having asked for it. Then cycle visibility,
	// which is the path that used to trust that 0 and write it through.
	await page.evaluate(() => {
		const a = (window as unknown as { __audio: HTMLAudioElement }).__audio;
		Object.defineProperty(a, 'readyState', { configurable: true, get: () => 0 });
		Object.defineProperty(a, 'currentTime', { configurable: true, get: () => 0, set: () => {} });
		a.dispatchEvent(new Event('emptied'));
		Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
		document.dispatchEvent(new Event('visibilitychange'));
		Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' });
		document.dispatchEvent(new Event('visibilitychange'));
	});

	// Settle, then check the tracked position survived intact everywhere: the
	// UI, the journal, every request the reset triggered, and the server.
	await expect.poll(async () => scrubber.inputValue(), { timeout: 5_000 }).toBe(scrubBefore);

	await expect.poll(readJournalPositionMs, { timeout: 5_000 }).toBe(posBefore);

	for (const body of bodies) {
		expect(body.position_ms, JSON.stringify(body)).not.toBe(0);
		expect(body.position_ms ?? -1, JSON.stringify(body)).toBeGreaterThanOrEqual(posBefore - 100);
	}

	const serverRes = await page.request.get(`/api/v1/progress/${book.id}`);
	expect(serverRes.status(), await serverRes.text()).toBe(200);
	const serverProgress = (await serverRes.json()) as { position_ms: number };
	expect(serverProgress.position_ms).toBeGreaterThanOrEqual(posBefore - 100);

	// Remove the overrides so the element can really play again: they were
	// defined configurable on the instance, so `delete` restores the
	// prototype's real getters.
	await page.evaluate(() => {
		const a = (window as unknown as { __audio: Record<string, unknown> }).__audio;
		delete a.currentTime;
		delete a.readyState;
		delete (document as unknown as Record<string, unknown>).visibilityState;
	});

	await page.getByRole('button', { name: 'Play', exact: true }).last().click();

	// The engine reloads src at the tracked position rather than trusting the
	// (still-attached) stale element.
	const src = await page.evaluate(
		() => (window as unknown as { __audio: HTMLAudioElement }).__audio.src
	);
	expect(src).toContain('#t=');
	const fragment = /#t=([0-9.]+)/.exec(src);
	expect(fragment, src).toBeTruthy();
	const fragmentSeconds = Number(fragment![1]);
	expect(Math.abs(fragmentSeconds - posBefore / 1000)).toBeLessThan(0.3);

	await expect(page.getByRole('button', { name: 'Pause', exact: true }).last()).toBeVisible({
		timeout: 15_000
	});
	await expect
		.poll(async () => Number(await scrubber.inputValue()), { timeout: 10_000 })
		.toBeGreaterThan(Number(scrubBefore));
});

test('a play() that never starts is surfaced as Tap to resume instead of hanging', async ({ page }) => {
	await tapAudioElement(page);
	await page.addInitScript(() => {
		(window as unknown as { __hangPlay: boolean }).__hangPlay = true;
	});

	const book = await bookByTitle(page.request, BOOKS.multiFile.title);
	await resetProgress(page.request, book.id);

	await page.goto(`/book/${book.id}`);
	await expect(page.getByRole('heading', { level: 1, name: BOOKS.multiFile.title })).toBeVisible();

	await page
		.getByRole('button', { name: /play|resume/i })
		.first()
		.click();

	// play() is hung, so `playing` never fires: the engine's 10 s play-timeout
	// watchdog is the only thing that can move it out of "loading" forever.
	const resumeButton = page.getByRole('button', { name: 'Tap to resume' });
	await expect(resumeButton).toBeVisible({ timeout: 15_000 });

	// Restore real playback: delete the own-property override so play() falls
	// back to the prototype's real (native) implementation.
	await page.evaluate(() => {
		const a = (window as unknown as { __audio: Record<string, unknown> }).__audio;
		delete a.play;
	});

	await resumeButton.click();

	await expect(page.getByRole('button', { name: 'Pause', exact: true }).last()).toBeVisible({
		timeout: 15_000
	});

	const scrubber = page.getByRole('slider', { name: 'Position' });
	await expect
		.poll(async () => Number(await scrubber.inputValue()), { timeout: 10_000 })
		.toBeGreaterThan(500);
});
