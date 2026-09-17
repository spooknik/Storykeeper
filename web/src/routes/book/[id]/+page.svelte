<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { BookDetail } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import { fmtDuration, fmtTime, joinNames } from '$lib/format';
	import { player } from '$lib/player/machine.svelte';
	import { journal } from '$lib/player/journal';

	let book = $state<BookDetail | null>(null);
	let error = $state('');

	const isCurrent = $derived(book !== null && player.book?.id === book.id);

	/** Best known starting position: the newer of the server record and the local journal. */
	function startPosition(b: BookDetail): number {
		const server = b.progress && !b.progress.finished ? b.progress : null;
		const local = auth.user ? journal.read(auth.user.id, b.id) : null;
		if (local && !local.synced) return local.positionMs;
		if (server && local) {
			// Both present: prefer the one written later. serverListenedAt in the
			// journal is the server clock at our last accepted write, so if the
			// server record is newer than that, someone else listened since.
			return b.progress!.listened_at > local.serverListenedAt + 2000 ? server.position_ms : local.positionMs;
		}
		return server?.position_ms ?? local?.positionMs ?? 0;
	}

	async function load() {
		try {
			book = await api.get<BookDetail>(`/api/v1/books/${page.params.id}`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load';
		}
	}

	onMount(load);

	function play() {
		if (!book) return;
		if (isCurrent) {
			player.toggle();
			return;
		}
		if (book.progress) {
			player.serverSeq = book.progress.seq;
			player.serverListenedAt = book.progress.listened_at;
		} else {
			player.serverSeq = 0;
			player.serverListenedAt = 0;
		}
		// Must stay synchronous up to play(): this click is the gesture iOS needs.
		void player.load(book, startPosition(book), true);
	}

	function jump(ms: number) {
		if (!book) return;
		if (isCurrent) player.seekTo(ms);
		else void player.load(book, ms, true);
	}

	const shownPos = $derived(
		isCurrent ? player.positionMs : (book ? startPosition(book) : 0)
	);
</script>

<div class="page">
	<p><a href="/">← Library</a></p>
	{#if error}<p class="error">{error}</p>{/if}
	{#if book}
		<div class="head">
			{#if book.cover_url}
				<img class="cover big" src={book.cover_url} alt="" />
			{:else}
				<div class="cover big placeholder">♪</div>
			{/if}
			<div class="info">
				<h1>{book.title}</h1>
				{#if book.subtitle}<div class="muted">{book.subtitle}</div>{/if}
				{#if book.authors.length}<div>by {joinNames(book.authors)}</div>{/if}
				{#if book.narrators.length}<div class="muted">read by {joinNames(book.narrators)}</div>{/if}
				{#if book.series}<div class="muted">{book.series}{book.series_seq ? ` #${book.series_seq}` : ''}</div>{/if}
				<div class="muted">
					{fmtDuration(book.duration_ms)}
					{#if book.files.length > 1}· {book.files.length} files{/if}
					{#if book.published_year}· {book.published_year}{/if}
				</div>
				<div class="actions">
					<button class="primary" onclick={play}>
						{#if isCurrent && player.status === 'playing'}
							Pause
						{:else if shownPos > 0 && !(book.progress?.finished)}
							Resume from {fmtTime(shownPos)}
						{:else}
							Play
						{/if}
					</button>
					{#if book.progress?.finished}<span class="muted">Finished</span>{/if}
				</div>
			</div>
		</div>

		{#if book.description}
			<p class="desc">{book.description}</p>
		{/if}

		{#if book.chapters.length > 0}
			<h2>Chapters</h2>
			<ol class="chapters">
				{#each book.chapters as c (c.index)}
					<li class:active={isCurrent && player.currentChapter?.index === c.index}>
						<button class="link" onclick={() => jump(c.start_ms)}>
							<span>{c.title}</span>
							<span class="muted">{fmtTime(c.start_ms)}</span>
						</button>
					</li>
				{/each}
			</ol>
		{/if}
	{:else if !error}
		<p class="muted">Loading…</p>
	{/if}
</div>

<style>
	.head {
		display: flex;
		gap: 1.25rem;
		align-items: flex-start;
	}
	.cover.big {
		width: 180px;
		height: 180px;
		flex-shrink: 0;
	}
	.info {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	h1 {
		margin: 0;
		font-size: 1.4rem;
	}
	h2 {
		font-size: 1.05rem;
		margin: 1.5rem 0 0.5rem;
	}
	.actions {
		margin-top: 0.75rem;
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.desc {
		white-space: pre-line;
		line-height: 1.45;
		color: var(--fg);
		margin-top: 1.25rem;
	}
	.chapters {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	.chapters li {
		border-bottom: 1px solid var(--border);
	}
	.chapters li.active .link {
		color: var(--accent);
	}
	.link {
		width: 100%;
		display: flex;
		justify-content: space-between;
		gap: 1rem;
		background: none;
		border: 0;
		border-radius: 0;
		padding: 0.65rem 0.25rem;
		text-align: left;
	}
	@media (max-width: 520px) {
		.head {
			flex-direction: column;
			align-items: center;
			text-align: center;
		}
		.info {
			align-items: center;
		}
		.actions {
			justify-content: center;
		}
	}
</style>
