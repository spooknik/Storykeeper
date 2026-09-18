// Desktop keyboard shortcuts for the audiobook player. Installed once from
// +layout.svelte's onMount, alongside player.install().
//
//   Space              play/pause
//   ArrowLeft/Right    skip back/forward (player.skipBackMs/skipForwardMs)
//   Shift+Arrow        previous/next chapter
//   [ / ]              step the effective rate down/up by 0.1 (0.5-3.0)
//   b                  bookmark the current position
//
// Shortcuts are ignored while a modifier key is held, while focus is on a
// typing target, while nothing is loaded, when the event was already
// consumed (e.g. Sheet.svelte's own Escape handler runs first in capture
// order and may preventDefault), and Escape itself is never intercepted here
// so sheets/dialogs keep sole ownership of it.

import { player } from './player/machine.svelte';
import { bookmarks } from './player/bookmarks.svelte';

/** True when typing into this element would insert text, so single-key shortcuts must not fire. */
export function isTypingTarget(el: Element | null): boolean {
	if (!el) return false;
	const tag = el.tagName;
	if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
	return el instanceof HTMLElement && el.isContentEditable;
}

const RATE_STEP = 0.1;
const RATE_MIN = 0.5;
const RATE_MAX = 3.0;

/** Round to one decimal place, avoiding float drift from repeated +/- 0.1 steps. */
function round1(n: number): number {
	return Math.round(n * 10) / 10;
}

function stepRate(delta: number): void {
	const next = Math.min(RATE_MAX, Math.max(RATE_MIN, round1(player.rate + delta)));
	if (player.bookRateOverride != null) {
		player.setRate(next, { perBook: true });
	} else {
		player.setRate(next);
	}
}

/** Mirrors the toast pattern in Player.svelte, without importing that file. */
function notice(text: string): void {
	const n = { text };
	player.notice = n;
	setTimeout(() => {
		if (player.notice === n) player.notice = null;
	}, 3500);
}

function addBookmark(): void {
	const b = player.book;
	if (!b) return;
	void bookmarks
		.create(b.id, Math.round(player.positionMs), '')
		.then(() => notice('Bookmark saved'))
		.catch(() => notice('Could not save the bookmark.'));
}

/**
 * Add the window keydown listener for player shortcuts. Returns a remover;
 * call it on teardown (the layout's onMount is async, so it cannot rely on
 * Svelte's synchronous "return a function from onMount" cleanup and instead
 * calls this from onDestroy).
 */
export function installPlayerShortcuts(): () => void {
	function onKeydown(event: KeyboardEvent): void {
		if (event.defaultPrevented) return;
		if (event.metaKey || event.ctrlKey || event.altKey) return;
		if (event.key === 'Escape') return;
		if (isTypingTarget(document.activeElement)) return;
		if (!player.book) return;

		switch (event.key) {
			case ' ':
			case 'Spacebar':
				event.preventDefault();
				player.toggle();
				break;
			case 'ArrowLeft':
				if (event.shiftKey) player.prevChapter();
				else player.skip(-player.skipBackMs);
				break;
			case 'ArrowRight':
				if (event.shiftKey) player.nextChapter();
				else player.skip(player.skipForwardMs);
				break;
			case '[':
				stepRate(-RATE_STEP);
				break;
			case ']':
				stepRate(RATE_STEP);
				break;
			case 'b':
			case 'B':
				addBookmark();
				break;
			default:
				return;
		}
	}

	window.addEventListener('keydown', onKeydown);
	return () => window.removeEventListener('keydown', onKeydown);
}
