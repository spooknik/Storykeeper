<script lang="ts">
	import { api } from '$lib/api/client';
	import type { BookList, BookSummary } from '$lib/api/types';
	import { events } from '$lib/events.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import type { IconName } from '$lib/components/Icon.svelte';
	import { fmtDuration, joinNames } from '$lib/format';
	import BookCard from './BookCard.svelte';
	import GroupSection from './GroupSection.svelte';
	import { bySeriesSeq, compareNames, facetHref, groupBooks, seriesCount } from './group';
	import { booksUrl, DEFAULT_QUERY, type FacetFilter } from './query';

	interface Props {
		kind: 'author' | 'series' | 'narrator';
		name: string;
	}

	let { kind, name }: Props = $props();

	const ICONS: Record<Props['kind'], IconName> = {
		author: 'user',
		series: 'layers',
		narrator: 'mic'
	};
	const INDEX: Record<Props['kind'], { href: string; label: string }> = {
		author: { href: '/authors', label: 'Authors' },
		series: { href: '/series', label: 'Series' },
		narrator: { href: '/narrators', label: 'Narrators' }
	};

	let books = $state<BookSummary[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');

	const ordered = $derived(kind === 'series' ? [...books].sort(bySeriesSeq) : books);
	/** An author or narrator with several series reads best split into series sections. */
	const groups = $derived(kind === 'series' ? [] : groupBooks(ordered, 'series'));
	const useGroups = $derived(kind !== 'series' && seriesCount(books) > 1);
	const durationMs = $derived(books.reduce((sum, b) => sum + b.duration_ms, 0));

	/** People credited on these books, for the cross-links under the heading. */
	const relatedAuthors = $derived.by(() => {
		if (kind === 'author') return [];
		const set = new Set<string>();
		for (const b of books) for (const a of b.authors) if (a.trim() !== '') set.add(a.trim());
		return [...set].sort(compareNames).slice(0, 8);
	});

	async function load(facetName: string) {
		loading = true;
		error = '';
		try {
			const facet: FacetFilter =
				kind === 'author'
					? { author: facetName }
					: kind === 'series'
						? { series: facetName }
						: { narrator: facetName };
			const list = await api.get<BookList>(
				booksUrl({ ...DEFAULT_QUERY, sort: 'series', dir: 'asc' }, facet)
			);
			books = list.items;
			total = list.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	let loadedKey = '';
	$effect(() => {
		const key = [name, events.libraryVersion, events.resyncVersion].join('|');
		if (key === loadedKey) return;
		loadedKey = key;
		void load(name);
	});
</script>

<div class="page">
	<p>
		<a class="backlink" href={INDEX[kind].href}>
			<Icon name="arrow-left" size={16} />
			{INDEX[kind].label}
		</a>
	</p>

	<div class="head">
		<h1 class="with-icon"><Icon name={ICONS[kind]} size={22} /> {name}</h1>
		<p class="muted meta">
			{total}
			{total === 1 ? 'book' : 'books'}
			{#if durationMs > 0}· {fmtDuration(durationMs)}{/if}
		</p>
		{#if relatedAuthors.length > 0}
			<p class="muted meta">
				{#each relatedAuthors as a, i (a)}{#if i > 0}, {/if}<a href={facetHref('authors', a)}>{a}</a
					>{/each}
			</p>
		{/if}
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if loading && books.length === 0}
		<p class="muted">Loading…</p>
	{:else if books.length === 0}
		<p class="muted">No books here.</p>
	{:else if useGroups}
		{#each groups as g (g.key)}
			<GroupSection group={g} badges />
		{/each}
	{:else}
		<div class="grid">
			{#each ordered as b (b.id)}
				<BookCard
					book={b}
					badge={kind === 'series' && b.series_seq ? b.series_seq : undefined}
					sub={kind === 'author' ? b.series || fmtDuration(b.duration_ms) : joinNames(b.authors) || fmtDuration(b.duration_ms)}
				/>
			{/each}
		</div>
	{/if}
</div>

<style>
	.head {
		margin-bottom: 1.25rem;
	}
	h1 {
		margin: 0 0 0.35rem;
		font-size: 1.3rem;
		overflow-wrap: anywhere;
	}
	.meta {
		margin: 0.15rem 0;
		font-size: 0.85rem;
	}
</style>
