<script lang="ts">
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';
	import Icon from './Icon.svelte';
	import type { IconName } from './Icon.svelte';

	interface Item {
		href: string;
		label: string;
		icon: IconName;
	}

	const main: Item[] = [
		{ href: '/', label: 'Library', icon: 'library' },
		{ href: '/authors', label: 'Authors', icon: 'users' },
		{ href: '/series', label: 'Series', icon: 'layers' },
		{ href: '/narrators', label: 'Narrators', icon: 'mic' }
	];

	const adminItems: Item[] = [
		{ href: '/upload', label: 'Upload', icon: 'upload' },
		{ href: '/admin', label: 'Admin', icon: 'settings' }
	];

	const settings: Item = { href: '/settings', label: 'Settings', icon: 'user' };

	const isActive = (href: string) =>
		href === '/' ? page.url.pathname === '/' : page.url.pathname.startsWith(href);
</script>

{#snippet link(item: Item)}
	<a
		class="link"
		class:active={isActive(item.href)}
		href={item.href}
		aria-label={item.label}
		aria-current={isActive(item.href) ? 'page' : undefined}
	>
		<Icon name={item.icon} size={18} />
		<span class="label">{item.label}</span>
	</a>
{/snippet}

<nav class="nav">
	{#each main as item (item.href)}{@render link(item)}{/each}
	{#if auth.user?.role === 'admin'}
		{#each adminItems as item (item.href)}{@render link(item)}{/each}
	{/if}
	{@render link(settings)}
</nav>

<style>
	.nav {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--border);
		overflow-x: auto;
		scrollbar-width: none;
	}
	.nav::-webkit-scrollbar {
		display: none;
	}
	.link {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
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
	/* Phones get the fixed bottom tab bar (TabBar.svelte) instead. */
	@media (max-width: 560px) {
		.nav {
			display: none;
		}
	}
</style>
