<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { searchBox } from '$lib/library/searchbox.svelte';
	import { auth } from '$lib/auth.svelte';
	import Icon from './Icon.svelte';
	import type { IconName } from './Icon.svelte';
	import Sheet from './Sheet.svelte';

	interface Item {
		href: string;
		label: string;
		icon: IconName;
	}

	const browseItems: Item[] = [
		{ href: '/authors', label: 'Authors', icon: 'users' },
		{ href: '/series', label: 'Series', icon: 'layers' },
		{ href: '/narrators', label: 'Narrators', icon: 'mic' }
	];

	const adminItems: Item[] = [
		{ href: '/upload', label: 'Upload', icon: 'upload' },
		{ href: '/admin', label: 'Admin', icon: 'settings' }
	];

	const BROWSE_PATHS = ['/authors', '/series', '/narrators', '/upload', '/admin'];

	let barHeight = $state(0);
	let browseOpen = $state(false);

	const isActive = (href: string) =>
		href === '/' ? page.url.pathname === '/' : page.url.pathname.startsWith(href);

	const browseActive = $derived(BROWSE_PATHS.some((p) => page.url.pathname.startsWith(p)));

	// Keep the bottom padding of .page (and the mini player's offset) in step
	// with however tall the bar really is. On desktop the bar is display:none,
	// so its clientHeight — and this variable — collapse to 0 on their own.
	$effect(() => {
		const h = barHeight;
		document.documentElement.style.setProperty('--tabbar-height', `${h}px`);
		return () => document.documentElement.style.removeProperty('--tabbar-height');
	});

	function go(href: string) {
		browseOpen = false;
		void goto(href);
	}

	/**
	 * Focus the library search box. Done synchronously when the library page is
	 * already showing, because iOS only raises the keyboard for a focus() made
	 * inside the tap itself; otherwise navigate there and let the page focus it.
	 */
	function search() {
		if (page.url.pathname === '/' && searchBox.focus()) return;
		void goto('/?focus=1');
	}
</script>

<nav class="tabbar" bind:clientHeight={barHeight}>
	<a class="tab" class:active={isActive('/')} href="/" aria-current={isActive('/') ? 'page' : undefined}>
		<Icon name="library" size={20} />
		<span>Library</span>
	</a>
	<button
		type="button"
		class="tab"
		class:active={browseActive}
		onclick={() => (browseOpen = true)}
		aria-haspopup="dialog"
		aria-expanded={browseOpen}
	>
		<Icon name="layout-grid" size={20} />
		<span>Browse</span>
	</button>
	<button type="button" class="tab" onclick={search} aria-label="Search">
		<Icon name="search" size={20} />
		<span>Search</span>
	</button>
	<a
		class="tab"
		class:active={isActive('/settings')}
		href="/settings"
		aria-current={isActive('/settings') ? 'page' : undefined}
	>
		<Icon name="user" size={20} />
		<span>Settings</span>
	</a>
</nav>

{#if browseOpen}
	<Sheet title="Browse" onclose={() => (browseOpen = false)}>
		<ul class="list">
			{#each browseItems as item (item.href)}
				<li>
					<button class="entry" class:active={isActive(item.href)} onclick={() => go(item.href)}>
						<Icon name={item.icon} size={18} />
						<span class="label">{item.label}</span>
					</button>
				</li>
			{/each}
			{#if auth.user?.role === 'admin'}
				{#each adminItems as item (item.href)}
					<li>
						<button class="entry" class:active={isActive(item.href)} onclick={() => go(item.href)}>
							<Icon name={item.icon} size={18} />
							<span class="label">{item.label}</span>
						</button>
					</li>
				{/each}
			{/if}
		</ul>
	</Sheet>
{/if}

<style>
	.tabbar {
		display: none;
		position: fixed;
		left: 0;
		right: 0;
		bottom: 0;
		z-index: 11;
		background: var(--bg-raised);
		border-top: 1px solid var(--border);
		padding-bottom: var(--safe-bottom);
	}
	@media (max-width: 560px) {
		.tabbar {
			display: flex;
			align-items: stretch;
		}
	}
	.tab {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 0.2rem;
		padding: 0.4rem 0.25rem 0.3rem;
		color: var(--fg-muted);
		font-size: 0.68rem;
		border-radius: 0;
	}
	.tab.active {
		color: var(--accent);
		font-weight: 600;
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.list li {
		border-bottom: 1px solid var(--border);
	}
	.list li:last-child {
		border-bottom: 0;
	}
	.entry {
		width: 100%;
		display: flex;
		align-items: center;
		gap: 0.6rem;
		background: none;
		border: 0;
		border-radius: 0;
		padding: 0.7rem 0.25rem;
		text-align: left;
		font-size: 0.9rem;
		color: var(--fg);
	}
	.entry.active {
		color: var(--accent);
		font-weight: 600;
	}
	.entry .label {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
</style>
