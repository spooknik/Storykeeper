<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		title: string;
		onclose: () => void;
		children: Snippet;
	}

	let { title, onclose, children }: Props = $props();
</script>

<svelte:window
	onkeydown={(e: KeyboardEvent) => {
		if (e.key === 'Escape') onclose();
	}}
/>

<button class="backdrop" aria-label="Close {title}" onclick={onclose}></button>
<div class="sheet" role="dialog" aria-modal="true" aria-label={title}>
	<div class="head">
		<h2>{title}</h2>
		<button class="close" onclick={onclose} aria-label="Close">✕</button>
	</div>
	<div class="body">
		{@render children()}
	</div>
</div>

<style>
	.backdrop {
		position: fixed;
		inset: 0;
		z-index: 20;
		background: rgba(0, 0, 0, 0.55);
		border: 0;
		border-radius: 0;
		padding: 0;
	}
	.sheet {
		position: fixed;
		left: 0;
		right: 0;
		bottom: 0;
		z-index: 21;
		display: flex;
		flex-direction: column;
		max-height: 72vh;
		background: var(--bg-raised);
		border-top: 1px solid var(--border);
		border-radius: var(--radius) var(--radius) 0 0;
		padding-bottom: var(--safe-bottom);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.6rem 0.75rem;
		border-bottom: 1px solid var(--border);
	}
	.head h2 {
		margin: 0;
		font-size: 0.95rem;
	}
	.close {
		padding: 0.35rem 0.6rem;
	}
	.body {
		overflow-y: auto;
		-webkit-overflow-scrolling: touch;
		padding: 0.25rem 0.75rem 0.75rem;
	}
	@media (min-width: 720px) {
		.sheet {
			left: 50%;
			right: auto;
			transform: translateX(-50%);
			width: 420px;
			border-radius: var(--radius) var(--radius) 0 0;
		}
	}
</style>
