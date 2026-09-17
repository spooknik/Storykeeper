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

	$effect(() => {
		const user = auth.user;
		const session = auth.session;
		if (user && session) {
			player.userId = user.id;
			reporter?.stop();
			reporter = new Reporter(
				player,
				() => auth.session?.csrf_token ?? '',
				() => auth.session?.device_id ?? ''
			);
			reporter.start();
			void reporter.flushUnsynced(user.id);
			unsubEvents?.();
			const r = reporter;
			unsubEvents = events.onProgress((p) => r.onRemoteProgress(p));
			events.start();
			void restoreLastBook(user.id);
		} else {
			reporter?.stop();
			reporter = null;
			unsubEvents?.();
			unsubEvents = null;
			events.stop();
			player.userId = 0;
		}
	});

	/**
	 * After a relaunch (iOS kills backgrounded PWAs freely) put the last book
	 * back into the player bar, paused at the best known position. Creating the
	 * audio element here is fine; iOS only needs the first play() to come from a
	 * tap, and that tap will be on this same element.
	 */
	async function restoreLastBook(userId: number) {
		if (player.book) return;
		const id = journal.lastBook(userId);
		if (!id) return;
		try {
			const book = await api.get<BookDetail>(`/api/v1/books/${id}`);
			if (player.book || book.progress?.finished) return;
			if (book.progress) {
				player.serverSeq = book.progress.seq;
				player.serverListenedAt = book.progress.listened_at;
			}
			await player.load(book, startPosition(userId, book), false);
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
