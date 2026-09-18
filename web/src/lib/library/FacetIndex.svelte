<script lang="ts" generics="T extends Facet">
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { Facet } from '$lib/api/types';
	import { events } from '$lib/events.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import type { IconName } from '$lib/components/Icon.svelte';
	import { compareNames, facetHref, letterOf } from './group';

	interface Props {
		title: string;
		/** API path returning `T[]` (a `Facet[]` subtype). */
		endpoint: string;
		/** Route segment the entries link into. */
		base: 'authors' | 'series' | 'narrators';
		icon: IconName;
		/** Plural noun used in the empty state, e.g. "authors". */
		noun: string;
		/** Optional extra per-item content rendered after the count, e.g. a progress badge. */
		detail?: Snippet<[T]>;
	}

	let { title, endpoint, base, icon, noun, detail }: Props = $props();

	let items = $state<T[]>([]);
	let filter = $state('');
	let loading = $state(true);
	let error = $state('');

	const shown = $derived.by(() => {
		const needle = filter.trim().toLowerCase();
		const list = needle === '' ? items : items.filter((f) => f.name.toLowerCase().includes(needle));
		return [...list].sort((a, b) => compareNames(a.name, b.name));
	});

	const sections = $derived.by(() => {
		const map = new Map<string, T[]>();
		for (const f of shown) {
			const l = letterOf(f.name);
			const bucket = map.get(l);
			if (bucket) bucket.push(f);
			else map.set(l, [f]);
		}
		return [...map.entries()]
			.map(([letter, entries]) => ({ letter, entries, id: anchorId(letter) }))
			.sort((a, b) => {
				if ((a.letter === '#') !== (b.letter === '#')) return a.letter === '#' ? 1 : -1;
				return a.letter < b.letter ? -1 : 1;
			});
	});

	function anchorId(letter: string): string {
		return letter === '#' ? 'letter-other' : `letter-${letter}`;
	}

	async function load() {
		loading = true;
		error = '';
		try {
			items = await api.get<T[]>(endpoint);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	let seenLibraryVersion = events.libraryVersion;
	let seenResync = events.resyncVersion;
	$effect(() => {
		if (events.libraryVersion !== seenLibraryVersion || events.resyncVersion !== seenResync) {
			seenLibraryVersion = events.libraryVersion;
			seenResync = events.resyncVersion;
			void load();
		}
	});
</script>

<div class="page">
	<p><a class="backlink" href="/"><Icon name="arrow-left" size={16} /> Library</a></p>

	<div class="topbar">
		<h1 class="with-icon"><Icon name={icon} size={20} /> {title}</h1>
		<span class="muted total">{items.length}</span>
	</div>

	<div class="toolbar">
		<div class="searchbox">
			<Icon name="search" size={16} />
			<input type="search" placeholder="Filter {noun}" aria-label="Filter {noun}" bind:value={filter} />
			{#if filter !== ''}
				<button class="icon-btn" onclick={() => (filter = '')} aria-label="Clear filter">
					<Icon name="x" size={16} />
				</button>
			{/if}
		</div>
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if loading && items.length === 0}
		<p class="muted">Loading…</p>
	{:else if shown.length === 0}
		<p class="muted">
			{#if items.length === 0}No {noun} yet.{:else}No {noun} match that filter.{/if}
		</p>
	{:else}
		{#if sections.length > 1}
			<ul class="letter-index">
				{#each sections as s (s.letter)}
					<li><a href="#{s.id}">{s.letter}</a></li>
				{/each}
			</ul>
		{/if}
		{#each sections as s (s.letter)}
			<h2 class="letter-head" id={s.id}>{s.letter}</h2>
			<ul class="facet-list">
				{#each s.entries as f (f.name)}
					<li>
						<a href={facetHref(base, f.name)}>
							<Icon name={icon} size={16} class="row-icon" />
							<span class="name">{f.name}</span>
							<span class="count">{f.book_count}</span>
							{#if detail}<span class="detail">{@render detail(f)}</span>{/if}
						</a>
					</li>
				{/each}
			</ul>
		{/each}
	{/if}
</div>

<style>
	h1 {
		margin: 0;
		font-size: 1.25rem;
	}
	.total {
		font-size: 0.9rem;
	}
	:global(.facet-list .row-icon) {
		color: var(--fg-muted);
	}
	.detail {
		margin-left: 0.5rem;
		font-size: 0.8rem;
		color: var(--fg-muted);
	}
</style>
