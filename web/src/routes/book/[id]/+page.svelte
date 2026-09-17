<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { BookDetail } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import { fmtDuration, fmtTime, joinNames } from '$lib/format';
	import { player } from '$lib/player/machine.svelte';
	import { reportFinished } from '$lib/player/reporter';
	import { chaptersAreParts as titlesAreParts, chapterLabel } from '$lib/player/chapters';
	import { bookmarks as bookmarkStore } from '$lib/player/bookmarks.svelte';
	import { startPosition as resumePosition } from '$lib/player/resume';

	let book = $state<BookDetail | null>(null);
	let error = $state('');
	let busy = $state(false);
	/** Set optimistically while the progress write for this page is in flight. */
	let finishedOverride = $state<boolean | null>(null);
	let seenBookmarkVersion = 0;

	const isCurrent = $derived(book !== null && player.book?.id === book.id);
	const finished = $derived(finishedOverride ?? book?.progress?.finished ?? false);

	async function load() {
		try {
			book = await api.get<BookDetail>(`/api/v1/books/${page.params.id}`);
			finishedOverride = null;
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
		void player.load(book, resumePosition(auth.user?.id ?? 0, book), true);
	}

	function jump(ms: number) {
		if (!book) return;
		if (isCurrent) player.seekTo(ms);
		else void player.load(book, ms, true);
	}

	const shownPos = $derived(isCurrent ? player.positionMs : book ? resumePosition(auth.user?.id ?? 0, book) : 0);

	const partsOnly = $derived(book !== null && titlesAreParts(book.chapters));
	const chapterHeading = $derived(book ? chapterLabel(book.chapters) : 'Chapters');
	let showParts = $state(false);

	// --- finished ---

	async function toggleFinished() {
		const b = book;
		if (!b || busy) return;
		const next = !finished;
		busy = true;
		error = '';
		finishedOverride = next;
		try {
			if (isCurrent) {
				// The engine moves the timeline and the reporter does the PUT.
				player.markFinished(next);
			} else {
				// Un-finishing keeps whatever position the record already holds.
				const pos = next ? b.duration_ms : (b.progress?.position_ms ?? shownPos);
				await reportFinished(b.id, next, b.duration_ms, pos);
				await load();
			}
		} catch (e) {
			finishedOverride = null;
			error = e instanceof Error ? e.message : 'Could not update this book.';
		} finally {
			busy = false;
		}
	}

	// --- bookmarks ---

	async function refreshBookmarks() {
		const b = book;
		if (!b) return;
		try {
			b.bookmarks = await bookmarkStore.list(b.id);
		} catch {
			/* leave the list as it is */
		}
	}

	// The player can add a bookmark while this page is open; the store bumps a
	// counter and we refetch so BookDetail.bookmarks stays the single source.
	$effect(() => {
		const v = bookmarkStore.version;
		if (v === seenBookmarkVersion) return;
		seenBookmarkVersion = v;
		void refreshBookmarks();
	});

	async function removeBookmark(id: number) {
		if (busy) return;
		busy = true;
		error = '';
		try {
			await bookmarkStore.remove(id);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not delete the bookmark.';
		} finally {
			busy = false;
		}
	}
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
						{:else if shownPos > 0 && !finished}
							Resume from {fmtTime(shownPos)}
						{:else}
							Play
						{/if}
					</button>
					<button onclick={toggleFinished} disabled={busy}>
						{finished ? 'Mark unfinished' : 'Mark finished'}
					</button>
					{#if finished}<span class="muted">Finished</span>{/if}
					{#if auth.user?.role === 'admin'}
						<a class="btn" href="/book/{book.id}/edit">Edit</a>
					{/if}
				</div>
			</div>
		</div>

		{#if book.description}
			<p class="desc">{book.description}</p>
		{/if}

		<h2>Bookmarks</h2>
		{#if book.bookmarks.length === 0}
			<p class="muted">
				No bookmarks yet. Use the Bookmark button in the player to save the spot you are at.
			</p>
		{:else}
			<ul class="chapters">
				{#each book.bookmarks as bm (bm.id)}
					<li>
						<button class="link" onclick={() => jump(bm.position_ms)}>
							<span class="note">{bm.note || 'Bookmark'}</span>
							<span class="muted">{fmtTime(bm.position_ms)}</span>
						</button>
						<button
							class="del"
							onclick={() => removeBookmark(bm.id)}
							disabled={busy}
							aria-label="Delete bookmark at {fmtTime(bm.position_ms)}">✕</button
						>
					</li>
				{/each}
			</ul>
		{/if}

		{#if book.chapters.length > 0 && partsOnly && !showParts}
			<h2>Parts</h2>
			<p class="muted">
				This book has no chapter markers, only {book.chapters.length} audio parts of about
				{fmtDuration(book.duration_ms / book.chapters.length)} each.
				<button class="inline" onclick={() => (showParts = true)}>Show parts</button>
			</p>
		{:else if book.chapters.length > 0}
			<h2>{chapterHeading}</h2>
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
		flex-wrap: wrap;
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
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.chapters li.active .link {
		color: var(--accent);
	}
	.inline {
		padding: 0.25rem 0.6rem;
		margin-left: 0.4rem;
		font-size: 0.85rem;
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
	.note {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.del {
		flex-shrink: 0;
		background: none;
		border: 0;
		color: var(--fg-muted);
		padding: 0.5rem 0.4rem;
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
