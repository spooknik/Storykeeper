<script lang="ts">
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';

	const isActive = (href: string) =>
		href === '/' ? page.url.pathname === '/' : page.url.pathname.startsWith(href);
</script>

<nav class="nav">
	<a class="link" class:active={isActive('/')} href="/">Library</a>
	{#if auth.user?.role === 'admin'}
		<a class="link" class:active={isActive('/upload')} href="/upload">Upload</a>
		<a class="link" class:active={isActive('/admin')} href="/admin">Admin</a>
	{/if}
	<a class="link" class:active={isActive('/settings')} href="/settings">Settings</a>
</nav>

<style>
	.nav {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--border);
		overflow-x: auto;
	}
	.link {
		padding: 0.4rem 0.7rem;
		border-radius: var(--radius);
		color: var(--fg-muted);
		font-size: 0.9rem;
		white-space: nowrap;
		flex-shrink: 0;
	}
	.link:hover {
		background: var(--bg-hover);
		color: var(--fg);
	}
	.link.active {
		background: var(--bg-raised);
		color: var(--accent);
		font-weight: 600;
	}
	@media (max-width: 420px) {
		.nav {
			gap: 0.1rem;
			padding: 0.4rem 0.5rem;
		}
		.link {
			padding: 0.35rem 0.5rem;
			font-size: 0.82rem;
		}
	}
</style>
