<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { Library } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import { UploadQueue, type QueueItem, guessAuthorTitle, fmtBytes, fmtSpeed } from '$lib/upload/queue.svelte';

	const queue = new UploadQueue();

	let libraries = $state<Library[]>([]);
	let libraryId = $state<number | null>(null);
	let loading = $state(true);
	let error = $state('');

	let selectedFiles = $state<File[]>([]);
	let author = $state('');
	let title = $state('');
	let formError = $state('');
	let fileInputEl = $state<HTMLInputElement | undefined>(undefined);

	$effect(() => {
		if (auth.loaded && auth.user?.role !== 'admin') {
			void goto('/');
		}
	});

	async function loadLibraries() {
		loading = true;
		error = '';
		try {
			libraries = await api.get<Library[]>('/api/v1/libraries');
			if (libraries.length > 0) libraryId = libraries[0].id;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to load libraries';
		} finally {
			loading = false;
		}
	}

	onMount(loadLibraries);

	function onFilesChosen(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const list = input.files ? Array.from(input.files) : [];
		selectedFiles = list;
		if (list.length > 0) {
			const guess = guessAuthorTitle(list[0].name);
			author = guess.author;
			title = guess.title;
		}
	}

	function addToQueue(e: SubmitEvent) {
		e.preventDefault();
		formError = '';
		if (selectedFiles.length === 0) {
			formError = 'Choose at least one audio file.';
			return;
		}
		if (libraryId === null) {
			formError = 'Choose a library.';
			return;
		}
		queue.add(selectedFiles, {
			library_id: String(libraryId),
			author: author.trim(),
			title: title.trim()
		});
		selectedFiles = [];
		author = '';
		title = '';
		if (fileInputEl) fileInputEl.value = '';
	}

	function pct(item: QueueItem): number {
		return item.bytesTotal > 0 ? Math.min(100, (item.bytesSent / item.bytesTotal) * 100) : 0;
	}

	// Warn on tab close/reload while uploads are queued or in flight, so users
	// don't lose progress without realizing tus can't upload with the page gone.
	$effect(() => {
		if (!queue.hasActive) return;
		function onBeforeUnload(e: BeforeUnloadEvent) {
			e.preventDefault();
			e.returnValue = '';
		}
		window.addEventListener('beforeunload', onBeforeUnload);
		return () => window.removeEventListener('beforeunload', onBeforeUnload);
	});
</script>

<div class="page">
	<div class="topbar">
		<h1>Upload audiobooks</h1>
		<a href="/">← Library</a>
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if !loading && libraries.length === 0}
		<p class="muted">No libraries yet. Add one from the admin page first.</p>
	{:else}
		<form class="add-form" onsubmit={addToQueue}>
			<label>
				Library
				<select bind:value={libraryId} required>
					{#each libraries as lib (lib.id)}
						<option value={lib.id}>{lib.name}</option>
					{/each}
				</select>
			</label>

			<label class="files">
				Audio files
				<input
					bind:this={fileInputEl}
					type="file"
					multiple
					accept=".m4b,.m4a,.mp3,.ogg,.opus,.flac,.aac,.wav,audio/*"
					onchange={onFilesChosen}
				/>
			</label>

			<label>
				Author
				<input type="text" bind:value={author} placeholder="e.g. Brandon Sanderson" />
			</label>
			<label>
				Title
				<input type="text" bind:value={title} placeholder="e.g. The Way of Kings" />
			</label>

			{#if selectedFiles.length > 0}
				<p class="muted picked">
					{selectedFiles.length} file{selectedFiles.length === 1 ? '' : 's'} selected · {fmtBytes(
						selectedFiles.reduce((s, f) => s + f.size, 0)
					)}
					{#if selectedFiles.length > 1}
						<br />Multiple files are treated as one book and share the Author/Title above.
					{/if}
				</p>
			{/if}

			{#if formError}<div class="error">{formError}</div>{/if}

			<button class="primary" type="submit">Add to upload queue</button>
		</form>

		<p class="muted note">
			Keep this page open while uploading. On iPhone, switching apps pauses uploads; they resume when you come
			back.
		</p>
	{/if}

	{#if queue.items.length > 0}
		<div class="topbar summary-bar">
			<h2>Uploads</h2>
			<span class="muted">
				{queue.doneCount} of {queue.items.length} done · {fmtBytes(queue.sentBytes)} / {fmtBytes(
					queue.totalBytes
				)}
			</span>
		</div>

		{#if queue.allDone}
			<p class="done-banner">
				Done. The library is being scanned; new books appear shortly. <a href="/">Go to library →</a>
			</p>
		{/if}

		<ul class="queue">
			{#each queue.items as item (item.id)}
				<li class="queue-item">
					<div class="row1">
						<span class="name" title={item.file.name}>{item.file.name}</span>
						<span class="muted size">{fmtBytes(item.file.size)}</span>
					</div>
					<div class="bar"><span style:width="{pct(item)}%"></span></div>
					<div class="row2">
						<span class="state" class:error={item.state === 'error'}>
							{#if item.state === 'uploading'}
								Uploading {Math.round(pct(item))}%{#if item.speedBps > 0}
									· {fmtSpeed(item.speedBps)}{/if}
							{:else if item.state === 'queued'}
								Queued
							{:else if item.state === 'paused'}
								Paused {Math.round(pct(item))}%
							{:else if item.state === 'done'}
								Done
							{:else}
								Error{item.errorMessage ? `: ${item.errorMessage}` : ''}
							{/if}
						</span>
						<div class="actions">
							{#if item.state === 'uploading'}
								<button onclick={() => queue.pause(item)}>Pause</button>
							{:else if item.state === 'paused'}
								<button onclick={() => queue.resume(item)}>Resume</button>
							{:else if item.state === 'error'}
								<button onclick={() => queue.retry(item)}>Retry</button>
							{/if}
							<button class="danger" onclick={() => queue.removeItem(item)}>Remove</button>
						</div>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	h2 {
		font-size: 1.05rem;
		margin: 0;
	}
	.add-form {
		display: flex;
		flex-wrap: wrap;
		align-items: end;
		gap: 1rem;
		max-width: 40rem;
	}
	.add-form label {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		font-size: 0.9rem;
		color: var(--fg-muted);
	}
	.add-form .files {
		flex-basis: 100%;
	}
	.add-form input[type='file'] {
		padding: 0.5rem;
	}
	.add-form .picked,
	.add-form .error {
		flex-basis: 100%;
		margin: 0;
		font-size: 0.85rem;
	}
	.note {
		margin-top: 0.9rem;
		font-size: 0.85rem;
		max-width: 40rem;
	}
	.summary-bar {
		margin-top: 1.75rem;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.5rem 1rem;
	}
	.summary-bar span {
		font-size: 0.85rem;
	}
	.done-banner {
		background: var(--bg-raised);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.75rem 1rem;
		margin: 0.75rem 0;
	}
	.queue {
		list-style: none;
		margin: 0.75rem 0 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.queue-item {
		background: var(--bg-raised);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.65rem 0.85rem;
	}
	.row1 {
		display: flex;
		justify-content: space-between;
		gap: 0.75rem;
	}
	.name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.9rem;
	}
	.size {
		flex-shrink: 0;
		font-size: 0.8rem;
	}
	.queue-item .bar {
		height: 6px;
		margin-top: 0.5rem;
	}
	.row2 {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem;
		margin-top: 0.5rem;
	}
	.state {
		font-size: 0.85rem;
		color: var(--fg-muted);
	}
	.state.error {
		color: var(--danger);
	}
	.actions {
		display: flex;
		gap: 0.4rem;
	}
	.actions button {
		padding: 0.35rem 0.7rem;
		font-size: 0.85rem;
	}
	button.danger {
		border-color: var(--danger);
		color: var(--danger);
	}
	@media (max-width: 480px) {
		.add-form {
			max-width: 100%;
		}
		.add-form label {
			flex-basis: 100%;
		}
	}
</style>
