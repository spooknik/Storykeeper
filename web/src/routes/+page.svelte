<script lang="ts">
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { BookList, BookSummary } from '$lib/api/types';
	import { events } from '$lib/events.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import type { IconName } from '$lib/components/Icon.svelte';
	import BookCard from '$lib/library/BookCard.svelte';
	import GroupSection from '$lib/library/GroupSection.svelte';
	import { groupBooks } from '$lib/library/group';
	import {
		booksUrl,
		defaultDir,
		FILTERS,
		GROUPS,
		isPlainView,
		parseQuery,
		SORTS,
		toSearch,
		type FilterKey,
		type GroupBy,
		type LibraryQuery
	} from '$lib/library/query';
	import type { BookSort } from '$lib/api/types';

	const FILTER_ICONS: Record<FilterKey, IconName> = {
		all: 'layout-grid',
		'in-progress': 'circle-dot',
		'not-started': 'circle',
		finished: 'check-circle'
	};

	let inProgress = $state<BookSummary[]>([]);
	let books = $state<BookSummary[]>([]);
	let total = $state(0);
	let error = $state('');
	let loading = $state(true);

	const query = $derived(parseQuery(page.url));
	const grouped = $derived(groupBooks(books, query.group));
	const showShelf = $derived(isPlainView(query));

	/** Local mirror of the search box so typing stays smooth while the URL catches up. */
	let qInput = $state('');
	let syncedQ = '';
	let searchTimer: ReturnType<typeof setTimeout>;
	let searchInput = $state<HTMLInputElement | null>(null);

	$effect(() => {
		const urlQ = query.q;
		if (urlQ !== syncedQ) {
			syncedQ = urlQ;
			qInput = urlQ;
		}
	});

	// The mobile tab bar's Search tab links here with ?focus=1 so tapping it
	// jumps straight into the search box instead of just landing on the page.
	$effect(() => {
		if (page.url.searchParams.get('focus') !== '1') return;
		searchInput?.focus();
		const url = new URL(page.url);
		url.searchParams.delete('focus');
		replaceState(url, {});
	});

	function update(patch: Partial<LibraryQuery>) {
		const next: LibraryQuery = { ...query, ...patch };
		void goto(`/${toSearch(next)}`, { replaceState: true, keepFocus: true, noScroll: true });
	}

	function onSearchInput() {
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			syncedQ = qInput;
			update({ q: qInput });
		}, 250);
	}

	function clearSearch() {
		clearTimeout(searchTimer);
		qInput = '';
		syncedQ = '';
		update({ q: '' });
	}

	function onSort(e: Event) {
		const sort = (e.currentTarget as HTMLSelectElement).value as BookSort;
		update({ sort, dir: defaultDir(sort) });
	}

	function onGroup(e: Event) {
		update({ group: (e.currentTarget as HTMLSelectElement).value as GroupBy });
	}

	async function load(q: LibraryQuery) {
		loading = true;
		error = '';
		try {
			const wantShelf = isPlainView(q);
			const [shelf, list] = await Promise.all([
				wantShelf
					? api.get<BookList>('/api/v1/books?sort=recent&dir=desc&in_progress=1&limit=20')
					: Promise.resolve(null),
				api.get<BookList>(booksUrl(q))
			]);
			inProgress = shelf ? shelf.items : [];
			books = list.items;
			total = list.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	// One fetch per distinct view. Live progress events update the bars in place;
	// only a library change or a resync from the server forces a refetch.
	let loadedKey = '';
	$effect(() => {
		const key = [
			query.sort,
			query.dir,
			query.filter,
			query.q.trim(),
			events.libraryVersion,
			events.resyncVersion
		].join('|');
		if (key === loadedKey) return;
		loadedKey = key;
		void load(query);
	});
</script>

<div class="page">
	<div class="topbar">
		<h1>Library <span class="muted count">{total}</span></h1>
	</div>

	<div class="toolbar">
		<div class="searchbox">
			<Icon name="search" size={16} />
			<input
				type="search"
				placeholder="Search titles, authors, series"
				aria-label="Search the library"
				bind:value={qInput}
				bind:this={searchInput}
				oninput={onSearchInput}
			/>
			{#if qInput !== ''}
				<button class="icon-btn clear" onclick={clearSearch} aria-label="Clear search">
					<Icon name="x" size={16} />
				</button>
			{/if}
		</div>

		<div class="controls">
			<div class="field">
				<Icon name="arrow-up-down" size={16} />
				<select value={query.sort} onchange={onSort} aria-label="Sort by">
					{#each SORTS as s (s.value)}
						<option value={s.value}>{s.label}</option>
					{/each}
				</select>
			</div>
			<button
				class="icon-btn"
				onclick={() => update({ dir: query.dir === 'asc' ? 'desc' : 'asc' })}
				aria-label={query.dir === 'asc' ? 'Sorted ascending, switch to descending' : 'Sorted descending, switch to ascending'}
			>
				<Icon name={query.dir === 'asc' ? 'arrow-up' : 'arrow-down'} size={18} />
			</button>
			<div class="field">
				<Icon name="layers" size={16} />
				<select value={query.group} onchange={onGroup} aria-label="Group by">
					{#each GROUPS as g (g.value)}
						<option value={g.value}>{g.label}</option>
					{/each}
				</select>
			</div>
		</div>

		<div class="chips" role="group" aria-label="Filter">
			<Icon name="filter" size={16} class="chips-icon" />
			{#each FILTERS as f (f.value)}
				<button
					class="chip"
					class:on={query.filter === f.value}
					aria-pressed={query.filter === f.value}
					onclick={() => update({ filter: f.value })}
				>
					<Icon name={FILTER_ICONS[f.value]} size={14} />
					{f.label}
				</button>
			{/each}
		</div>
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if showShelf && inProgress.length > 0}
		<h2>Continue listening</h2>
		<div class="grid">
			{#each inProgress as b (b.id)}
				<BookCard book={b} />
			{/each}
		</div>
	{/if}

	{#if loading && books.length === 0}
		<p class="muted">Loading…</p>
	{:else if books.length === 0}
		<p class="muted">
			{#if query.q.trim() !== ''}
				Nothing matches “{query.q}”.
			{:else if query.filter !== 'all'}
				No books in this filter yet.
			{:else}
				No books yet. Add a library folder from the admin page, or wait for the scan to finish.
			{/if}
		</p>
	{:else}
		{#if showShelf && inProgress.length > 0}
			<h2>All books</h2>
		{/if}
		{#if total > books.length}
			<p class="muted note">
				Showing the first {books.length} of {total} books. Narrow the view with search or a filter
				to see the rest.
			</p>
		{/if}
		{#if query.group === 'none'}
			<div class="grid">
				{#each books as b (b.id)}
					<BookCard book={b} badge={query.sort === 'series' && b.series_seq ? b.series_seq : undefined} />
				{/each}
			</div>
		{:else}
			{#each grouped as g (g.key)}
				<GroupSection group={g} badges={query.group === 'series'} />
			{/each}
		{/if}
	{/if}
</div>

<style>
	h1 .count {
		font-size: 0.9rem;
		font-weight: 400;
	}
	h2 {
		font-size: 1.05rem;
		margin: 1.5rem 0 0.75rem;
	}
	.note {
		font-size: 0.85rem;
		margin: 0 0 0.75rem;
	}
	:global(.toolbar .chips-icon) {
		color: var(--fg-muted);
		flex-shrink: 0;
	}
</style>
