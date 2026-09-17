<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';
	import { player } from '$lib/player/machine.svelte';
	import { Reporter } from '$lib/player/reporter';
	import { events } from '$lib/events.svelte';
	import { journal } from '$lib/player/journal';
	import { startPosition } from '$lib/player/resume';
	import { api } from '$lib/api/client';
	import type { BookDetail } from '$lib/api/types';
	import Player from '$lib/components/Player.svelte';
	import Nav from '$lib/components/Nav.svelte';

	let { children } = $props();
	let reporter: Reporter | null = null;
	let unsubEvents: (() => void) | null = null;
	/** The user everything below is currently wired up for; 0 when signed out. */
	let wiredUserId = 0;
	/**
	 * Bumped on every sign-in and sign-out. A restore is only allowed to finish
	 * while its token is still the current one, so a slow restore started for one
	 * user can never land in another user's player.
	 */
	let sessionToken = 0;

	onMount(async () => {
		player.install();
		player.restoreRate();
		await auth.load();
	});

	$effect(() => {
		if (!auth.loaded) return;
		const onLogin = page.url.pathname === '/login';
		if (!auth.user && !onLogin) {
			goto('/login', { replaceState: true });
		} else if (auth.user && onLogin) {
			goto('/', { replaceState: true });
		}
	});

	/**
	 * Tear every per-user thing down. Signing out or switching accounts must
	 * leave nothing of the previous user behind: a player still holding their
	 * book would have the next heartbeat write their position into the new
	 * user's history.
	 */
	function teardown() {
		sessionToken += 1;
		reporter?.stop();
		reporter = null;
		unsubEvents?.();
		unsubEvents = null;
		events.stop();
		events.progress = {};
		player.unload();
		player.userId = 0;
		wiredUserId = 0;
	}

	$effect(() => {
		const user = auth.user;
		const session = auth.session;
		const id = user?.id ?? 0;
		// Signed out, or a different account signed in on this device.
		if (id !== wiredUserId) teardown();
		if (!user || !session) return;
		if (wiredUserId === user.id) return; // already wired for this user
		wiredUserId = user.id;
		const token = sessionToken;
		player.userId = user.id;
		const r = new Reporter(
			player,
			() => auth.session?.csrf_token ?? '',
			() => auth.session?.device_id ?? ''
		);
		reporter = r;
		r.start();
		unsubEvents = events.onProgress((p) => r.onRemoteProgress(p));
		events.start();
		void (async () => {
			// Flush first: a rejected offline entry is reconciled against the server
			// there, so the restore below sees the position that actually stands.
			await r.flushUnsynced(user.id);
			if (token !== sessionToken) return;
			await restoreLastBook(user.id, token);
		})();
	});

	/**
	 * After a relaunch (iOS kills backgrounded PWAs freely) put the last book
	 * back into the player bar, paused at the best known position. Creating the
	 * audio element here is fine; iOS only needs the first play() to come from a
	 * tap, and that tap will be on this same element.
	 */
	async function restoreLastBook(userId: number, token: number) {
		if (player.book) return;
		const id = journal.lastBook(userId);
		if (!id) return;
		try {
			const book = await api.get<BookDetail>(`/api/v1/books/${id}`);
			if (token !== sessionToken || player.book || book.progress?.finished) return;
			if (book.progress) {
				player.serverSeq = book.progress.seq;
				player.serverListenedAt = book.progress.listened_at;
			}
			await player.load(book, startPosition(userId, book), false);
			// The session can end while the book is being fetched or loaded.
			if (token !== sessionToken) player.unload();
		} catch {
			/* book gone or offline: nothing to restore */
		}
	}
</script>

{#if !auth.loaded}
	<div class="page muted">Loading…</div>
{:else}
	{#if auth.user}
		<Nav />
	{/if}
	{@render children()}
	{#if auth.user}
		<Player />
	{/if}
{/if}
