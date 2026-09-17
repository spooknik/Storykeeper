<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';
	import { player } from '$lib/player/machine.svelte';
	import { Reporter } from '$lib/player/reporter';
	import { events } from '$lib/events.svelte';
	import Player from '$lib/components/Player.svelte';

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
		} else {
			reporter?.stop();
			reporter = null;
			unsubEvents?.();
			unsubEvents = null;
			events.stop();
			player.userId = 0;
		}
	});
</script>

{#if !auth.loaded}
	<div class="page muted">Loading…</div>
{:else}
	{@render children()}
	{#if auth.user}
		<Player />
	{/if}
{/if}
