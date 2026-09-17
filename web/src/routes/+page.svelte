<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { BookList, BookSummary } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import { fmtDuration, joinNames } from '$lib/format';

	let inProgress = $state<BookSummary[]>([]);
	let books = $state<BookSummary[]>([]);
	let total = $state(0);
	let q = $state('');
	let error = $state('');
	let loading = $state(true);

	async function load() {
		loading = true;
		error = '';
		try {
			const [recent, all] = await Promise.all([
				api.get<BookList>('/api/v1/books?sort=recent&in_progress=1&limit=20'),
				api.get<BookList>(`/api/v1/books?sort=title&limit=200&q=${encodeURIComponent(q)}`)
			]);
			inProgress = recent.items;
			books = all.items;
			total = all.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	let searchTimer: ReturnType<typeof setTimeout>;
	function onSearch() {
		clearTimeout(searchTimer);
		searchTimer = setTimeout(load, 250);
	}

	function pct(b: BookSummary): number {
		if (!b.progress || b.duration_ms === 0) return 0;
		return Math.min(100, (b.progress.position_ms / b.duration_ms) * 100);
	}
</script>

{#snippet card(b: BookSummary)}
	<a class="card" href="/book/{b.id}">
		{#if b.cover_url}
			<img class="cover" src={b.cover_url} alt="" loading="lazy" />
		{:else}
			<div class="cover placeholder">♪</div>
		{/if}
		<div class="title">{b.title}</div>
		<div class="sub">{joinNames(b.authors) || fmtDuration(b.duration_ms)}</div>
		{#if b.progress && !b.progress.finished}
			<div class="bar"><span style:width="{pct(b)}%"></span></div>
		{/if}
	</a>
{/snippet}

<div class="page">
	<div class="topbar">
		<h1>Storykeeper</h1>
		<div class="right">
			<span class="muted small">{auth.user?.username}</span>
			<button onclick={() => auth.logout()}>Sign out</button>
		</div>
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if inProgress.length > 0}
		<h2>Continue listening</h2>
		<div class="grid">
			{#each inProgress as b (b.id)}{@render card(b)}{/each}
		</div>
	{/if}

	<div class="topbar library-head">
		<h2>Library <span class="muted small">{total}</span></h2>
		<input type="search" placeholder="Search" bind:value={q} oninput={onSearch} />
	</div>
	{#if loading && books.length === 0}
		<p class="muted">Loading…</p>
	{:else if books.length === 0}
		<p class="muted">
			No books yet. Add a library folder from the admin page, or wait for the scan to finish.
		</p>
	{:else}
		<div class="grid">
			{#each books as b (b.id)}{@render card(b)}{/each}
		</div>
	{/if}
</div>

<style>
	h2 {
		font-size: 1.05rem;
		margin: 1.25rem 0 0.75rem;
	}
	.right {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.small {
		font-size: 0.85rem;
	}
	.library-head {
		margin-top: 1rem;
	}
	.library-head input {
		max-width: 14rem;
	}
</style>
