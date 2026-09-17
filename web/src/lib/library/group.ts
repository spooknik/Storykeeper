// Client-side grouping over one fetched page of books.

import type { BookSummary } from '$lib/api/types';
import type { GroupBy } from './query';

export const NO_SERIES = 'No series';
export const NO_AUTHOR = 'Unknown author';

export interface BookGroup {
	/** Stable key for each blocks and for remembering the collapsed state. */
	key: string;
	name: string;
	count: number;
	/** Author or series page for this group, or null for the catch-all bucket. */
	href: string | null;
	books: BookSummary[];
}

/** "2" to 2, "1.5" to 1.5, "Book 3" to 3, "" to Infinity so unnumbered entries sort last. */
export function seqNum(seq: string): number {
	const m = /-?\d+(\.\d+)?/.exec(seq ?? '');
	return m ? Number(m[0]) : Number.POSITIVE_INFINITY;
}

export function firstAuthor(b: BookSummary): string {
	return b.authors.find((a) => a.trim() !== '') ?? '';
}

const collator = new Intl.Collator(undefined, { sensitivity: 'base', numeric: true });

export function compareNames(a: string, b: string): number {
	return collator.compare(a, b);
}

export function byTitle(a: BookSummary, b: BookSummary): number {
	return collator.compare(a.title, b.title);
}

/** Series order: numeric series_seq first, then title. */
export function bySeriesSeq(a: BookSummary, b: BookSummary): number {
	const d = seqNum(a.series_seq) - seqNum(b.series_seq);
	if (d !== 0 && Number.isFinite(d)) return d;
	return byTitle(a, b);
}

export function facetHref(kind: 'authors' | 'series' | 'narrators', name: string): string {
	return `/${kind}/${encodeURIComponent(name)}`;
}

/**
 * Buckets books by first author or by series, keeping the order the server sent
 * inside each bucket except for series, which are put into reading order. The
 * catch-all bucket (No series / Unknown author) always sorts last.
 */
export function groupBooks(books: BookSummary[], by: GroupBy): BookGroup[] {
	if (by === 'none') return [];
	const placeholder = by === 'series' ? NO_SERIES : NO_AUTHOR;
	const buckets = new Map<string, BookSummary[]>();
	for (const b of books) {
		const raw = by === 'series' ? b.series.trim() : firstAuthor(b).trim();
		const name = raw === '' ? placeholder : raw;
		const list = buckets.get(name);
		if (list) list.push(b);
		else buckets.set(name, [b]);
	}
	const groups: BookGroup[] = [];
	for (const [name, list] of buckets) {
		const real = name !== placeholder;
		if (by === 'series' && real) list.sort(bySeriesSeq);
		groups.push({
			key: name,
			name,
			count: list.length,
			href: real ? facetHref(by === 'series' ? 'series' : 'authors', name) : null,
			books: list
		});
	}
	groups.sort((a, b) => {
		if ((a.href === null) !== (b.href === null)) return a.href === null ? 1 : -1;
		return collator.compare(a.name, b.name);
	});
	return groups;
}

/** How many distinct named series appear in a set of books. */
export function seriesCount(books: BookSummary[]): number {
	const seen = new Set<string>();
	for (const b of books) if (b.series.trim() !== '') seen.add(b.series.trim());
	return seen.size;
}

/** Buckets facet names under their first letter; anything not A to Z lands in "#". */
export function letterOf(name: string): string {
	const c = name.trim().charAt(0).toUpperCase();
	return c >= 'A' && c <= 'Z' ? c : '#';
}
