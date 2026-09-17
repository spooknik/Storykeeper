<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { BookDetail, BookEditRequest } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';

	const bookId = $derived(page.params.id);

	let book = $state<BookDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let saving = $state(false);

	let title = $state('');
	let subtitle = $state('');
	let authors = $state('');
	let narrators = $state('');
	let series = $state('');
	let seriesSeq = $state('');
	let publishedYear = $state('');
	let language = $state('');
	let asin = $state('');
	let isbn = $state('');
	let description = $state('');

	$effect(() => {
		if (auth.loaded && auth.user && auth.user.role !== 'admin') {
			void goto('/');
		}
	});

	function fillForm(b: BookDetail) {
		title = b.title;
		subtitle = b.subtitle;
		authors = b.authors.join('\n');
		narrators = b.narrators.join('\n');
		series = b.series;
		seriesSeq = b.series_seq;
		publishedYear = b.published_year != null ? String(b.published_year) : '';
		language = b.language;
		asin = b.asin;
		isbn = b.isbn;
		description = b.description;
	}

	async function load() {
		loading = true;
		error = '';
		try {
			book = await api.get<BookDetail>(`/api/v1/books/${bookId}`);
			fillForm(book);
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function linesToList(v: string): string[] {
		return v
			.split('\n')
			.map((s) => s.trim())
			.filter((s) => s.length > 0);
	}

	function sameList(a: string[], b: string[]): boolean {
		return a.length === b.length && a.every((v, i) => v === b[i]);
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!book) return;
		error = '';
		saving = true;
		try {
			const body: BookEditRequest = {};

			if (title.trim() !== book.title) body.title = title.trim();
			if (subtitle !== book.subtitle) body.subtitle = subtitle;
			if (series !== book.series) body.series = series;
			if (seriesSeq !== book.series_seq) body.series_seq = seriesSeq;
			if (language !== book.language) body.language = language;
			if (asin !== book.asin) body.asin = asin;
			if (isbn !== book.isbn) body.isbn = isbn;
			if (description !== book.description) body.description = description;

			const authorList = linesToList(authors);
			if (!sameList(authorList, book.authors)) body.authors = authorList;

			const narratorList = linesToList(narrators);
			if (!sameList(narratorList, book.narrators)) body.narrators = narratorList;

			const year = publishedYear.trim() === '' ? null : Number(publishedYear.trim());
			if (year !== book.published_year && !(year === null && book.published_year === null)) {
				if (year !== null) body.published_year = year;
			}

			if (Object.keys(body).length > 0) {
				await api.patch<BookDetail>(`/api/v1/books/${bookId}`, body);
			}
			await goto(`/book/${bookId}`);
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to save';
		} finally {
			saving = false;
		}
	}
</script>

<div class="page">
	<p><a href="/book/{bookId}">← Back</a></p>
	<div class="topbar">
		<h1>Edit metadata</h1>
	</div>

	<p class="muted note">
		Edits are locked against future rescans: once you save, the scanner will no longer overwrite
		these fields from the file tags.
	</p>

	{#if loading}
		<p class="muted">Loading…</p>
	{:else if !book}
		<p class="error">{error}</p>
	{:else}
		<form class="edit-form" onsubmit={save}>
			<label>
				Title
				<input type="text" bind:value={title} required />
			</label>
			<label>
				Subtitle
				<input type="text" bind:value={subtitle} />
			</label>
			<div class="two-col">
				<label>
					Authors <span class="muted small">(one per line)</span>
					<textarea rows="4" bind:value={authors}></textarea>
				</label>
				<label>
					Narrators <span class="muted small">(one per line)</span>
					<textarea rows="4" bind:value={narrators}></textarea>
				</label>
			</div>
			<div class="two-col">
				<label>
					Series
					<input type="text" bind:value={series} />
				</label>
				<label>
					Series #
					<input type="text" bind:value={seriesSeq} />
				</label>
			</div>
			<div class="two-col">
				<label>
					Published year
					<input type="number" bind:value={publishedYear} />
				</label>
				<label>
					Language
					<input type="text" bind:value={language} />
				</label>
			</div>
			<div class="two-col">
				<label>
					ASIN
					<input type="text" bind:value={asin} />
				</label>
				<label>
					ISBN
					<input type="text" bind:value={isbn} />
				</label>
			</div>
			<label>
				Description
				<textarea rows="8" bind:value={description}></textarea>
			</label>

			{#if error}<div class="error">{error}</div>{/if}

			<div class="actions">
				<button class="primary" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
				<a href="/book/{bookId}">Cancel</a>
			</div>
		</form>
	{/if}
</div>

<style>
	.note {
		max-width: 40rem;
		margin: -0.25rem 0 1.25rem;
	}
	.edit-form {
		display: flex;
		flex-direction: column;
		gap: 1rem;
		max-width: 40rem;
	}
	.edit-form label {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		font-size: 0.9rem;
		color: var(--fg-muted);
	}
	.edit-form input,
	.edit-form textarea {
		color: var(--fg);
	}
	textarea {
		font: inherit;
		background: var(--bg-raised);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.55rem 0.7rem;
		resize: vertical;
	}
	.two-col {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1rem;
	}
	.small {
		font-size: 0.78rem;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 1rem;
		margin-top: 0.5rem;
	}
	@media (max-width: 520px) {
		.two-col {
			grid-template-columns: 1fr;
		}
	}
</style>
