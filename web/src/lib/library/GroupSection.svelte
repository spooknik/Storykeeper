<script lang="ts">
	import Icon from '$lib/components/Icon.svelte';
	import BookCard from './BookCard.svelte';
	import type { BookGroup } from './group';

	interface Props {
		group: BookGroup;
		/** Show the series number as a badge on each cover. */
		badges?: boolean;
	}

	let { group, badges = false }: Props = $props();

	/** Sections open by default; collapsing is per instance and keyed by group name. */
	let open = $state(true);
	const bodyId = $derived(`group-${group.key.replace(/\W+/g, '-').toLowerCase()}`);
</script>

<section class="group">
	<div class="group-head">
		<button
			class="toggle"
			onclick={() => (open = !open)}
			aria-expanded={open}
			aria-controls={bodyId}
			aria-label={open ? `Collapse ${group.name}` : `Expand ${group.name}`}
		>
			<Icon name={open ? 'chevron-down' : 'chevron-right'} size={18} />
		</button>
		{#if group.href}
			<a class="name" href={group.href}>{group.name}</a>
		{:else}
			<span class="name muted">{group.name}</span>
		{/if}
		<span class="count muted">{group.count}</span>
	</div>
	{#if open}
		<div class="grid" id={bodyId}>
			{#each group.books as b (b.id)}
				<BookCard book={b} badge={badges && b.series_seq ? b.series_seq : undefined} />
			{/each}
		</div>
	{/if}
</section>

<style>
	.group {
		margin-top: 1.5rem;
	}
	.group-head {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		margin-bottom: 0.75rem;
		border-bottom: 1px solid var(--border);
		padding-bottom: 0.4rem;
	}
	.toggle {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		padding: 0.25rem;
		color: var(--fg-muted);
		flex-shrink: 0;
	}
	.name {
		font-weight: 600;
		font-size: 1rem;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.count {
		font-size: 0.8rem;
		flex-shrink: 0;
		margin-left: auto;
	}
</style>
