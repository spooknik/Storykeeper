<script lang="ts">
	import type { BookSummary } from '$lib/api/types';
	import Icon from '$lib/components/Icon.svelte';
	import { fmtDuration, joinNames } from '$lib/format';
	import { isFinished, liveProgress, progressPct } from './progress';

	interface Props {
		book: BookSummary;
		/** Small overlay on the cover, used for the series number. */
		badge?: string;
		/** Overrides the default secondary line (authors, or the duration when unknown). */
		sub?: string;
	}

	let { book, badge, sub }: Props = $props();

	const subLine = $derived(sub ?? (joinNames(book.authors) || fmtDuration(book.duration_ms)));
	const finished = $derived(isFinished(book));
	const started = $derived(liveProgress(book) !== undefined && !finished);
</script>

<a class="card" href="/book/{book.id}">
	<div class="art">
		{#if book.cover_url}
			<img class="cover" src={book.cover_url} alt="" loading="lazy" />
		{:else}
			<div class="cover placeholder"><Icon name="music" size={28} /></div>
		{/if}
		{#if badge}
			<span class="badge">{badge}</span>
		{/if}
		{#if finished}
			<span class="done" title="Finished"><Icon name="check" size={14} strokeWidth={3} /></span>
		{/if}
	</div>
	<div class="title">{book.title}</div>
	<div class="sub">{subLine}</div>
	{#if started}
		<div class="bar"><span style:width="{progressPct(book)}%"></span></div>
	{/if}
</a>

<style>
	.art {
		position: relative;
	}
	.badge,
	.done {
		position: absolute;
		top: 0.35rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 999px;
		background: rgba(17, 17, 17, 0.85);
		border: 1px solid var(--border);
		font-size: 0.7rem;
		font-weight: 700;
		line-height: 1;
	}
	.badge {
		left: 0.35rem;
		min-width: 1.4rem;
		height: 1.4rem;
		padding: 0 0.35rem;
		color: var(--accent);
	}
	.done {
		right: 0.35rem;
		width: 1.4rem;
		height: 1.4rem;
		color: var(--accent);
	}
</style>
