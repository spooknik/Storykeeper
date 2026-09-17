// Library browsing state. The URL query string is the single source of truth so
// reloads, deep links and back navigation all restore the same view.

import type { BookSort, SortDir } from '$lib/api/types';

export type GroupBy = 'none' | 'author' | 'series';
export type FilterKey = 'all' | 'in-progress' | 'not-started' | 'finished';

export interface LibraryQuery {
	sort: BookSort;
	dir: SortDir;
	group: GroupBy;
	filter: FilterKey;
	q: string;
}

export const SORTS: { value: BookSort; label: string }[] = [
	{ value: 'title', label: 'Title' },
	{ value: 'author', label: 'Author' },
	{ value: 'series', label: 'Series' },
	{ value: 'recent', label: 'Recently listened' },
	{ value: 'added', label: 'Recently added' },
	{ value: 'duration', label: 'Duration' }
];

export const GROUPS: { value: GroupBy; label: string }[] = [
	{ value: 'none', label: 'No grouping' },
	{ value: 'author', label: 'By author' },
	{ value: 'series', label: 'By series' }
];

export const FILTERS: { value: FilterKey; label: string }[] = [
	{ value: 'all', label: 'All' },
	{ value: 'in-progress', label: 'In progress' },
	{ value: 'not-started', label: 'Not started' },
	{ value: 'finished', label: 'Finished' }
];

const SORT_VALUES = SORTS.map((s) => s.value);
const GROUP_VALUES = GROUPS.map((g) => g.value);
const FILTER_VALUES = FILTERS.map((f) => f.value);

/** How many books one page of the library fetches; grouping happens over this set. */
export const PAGE_LIMIT = 500;

export const DEFAULT_QUERY: LibraryQuery = {
	sort: 'title',
	dir: 'asc',
	group: 'none',
	filter: 'all',
	q: ''
};

/** Newest-first and longest-first read better as the opening direction for these sorts. */
export function defaultDir(sort: BookSort): SortDir {
	return sort === 'added' || sort === 'recent' || sort === 'duration' ? 'desc' : 'asc';
}

function pick<T extends string>(raw: string | null, allowed: readonly T[], fallback: T): T {
	return raw !== null && (allowed as readonly string[]).includes(raw) ? (raw as T) : fallback;
}

export function parseQuery(url: URL): LibraryQuery {
	const p = url.searchParams;
	const sort = pick<BookSort>(p.get('sort'), SORT_VALUES, DEFAULT_QUERY.sort);
	return {
		sort,
		dir: pick<SortDir>(p.get('dir'), ['asc', 'desc'], defaultDir(sort)),
		group: pick<GroupBy>(p.get('group'), GROUP_VALUES, DEFAULT_QUERY.group),
		filter: pick<FilterKey>(p.get('filter'), FILTER_VALUES, DEFAULT_QUERY.filter),
		q: p.get('q') ?? ''
	};
}

/** Serialises back to a search string, omitting defaults so a plain view keeps a plain URL. */
export function toSearch(q: LibraryQuery): string {
	const p = new URLSearchParams();
	if (q.sort !== DEFAULT_QUERY.sort) p.set('sort', q.sort);
	if (q.dir !== defaultDir(q.sort)) p.set('dir', q.dir);
	if (q.group !== DEFAULT_QUERY.group) p.set('group', q.group);
	if (q.filter !== DEFAULT_QUERY.filter) p.set('filter', q.filter);
	if (q.q.trim() !== '') p.set('q', q.q);
	const s = p.toString();
	return s === '' ? '' : `?${s}`;
}

/** True when the view is the plain, unfiltered library. */
export function isPlainView(q: LibraryQuery): boolean {
	return q.filter === 'all' && q.q.trim() === '';
}

export interface FacetFilter {
	author?: string;
	series?: string;
	narrator?: string;
}

/** Builds the /api/v1/books query string for a view. */
export function booksUrl(q: LibraryQuery, facet: FacetFilter = {}, limit = PAGE_LIMIT): string {
	const p = new URLSearchParams();
	p.set('sort', q.sort);
	p.set('dir', q.dir);
	p.set('limit', String(limit));
	if (q.q.trim() !== '') p.set('q', q.q.trim());
	if (q.filter === 'in-progress') p.set('in_progress', '1');
	else if (q.filter === 'not-started') p.set('not_started', '1');
	else if (q.filter === 'finished') p.set('finished', '1');
	if (facet.author) p.set('author', facet.author);
	if (facet.series) p.set('series', facet.series);
	if (facet.narrator) p.set('narrator', facet.narrator);
	return `/api/v1/books?${p.toString()}`;
}
