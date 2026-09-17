<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { Library } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import Icon from '$lib/components/Icon.svelte';

	let libraries = $state<Library[]>([]);
	let loading = $state(true);
	let error = $state('');

	let newName = $state('');
	let newPath = $state('');
	let createError = $state('');
	let creating = $state(false);

	let rowBusy = $state<Record<number, boolean>>({});
	let rowMessage = $state<Record<number, string>>({});
	let rowError = $state<Record<number, string>>({});

	$effect(() => {
		if (auth.loaded && auth.user && auth.user.role !== 'admin') {
			void goto('/');
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			libraries = await api.get<Library[]>('/api/v1/libraries');
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function createLibrary(e: SubmitEvent) {
		e.preventDefault();
		createError = '';
		creating = true;
		try {
			await api.post('/api/v1/libraries', { name: newName, path: newPath });
			newName = '';
			newPath = '';
			await load();
		} catch (e) {
			createError = e instanceof ApiError ? e.message : 'Failed to add library';
		} finally {
			creating = false;
		}
	}

	async function rescan(id: number) {
		rowError[id] = '';
		rowMessage[id] = '';
		rowBusy[id] = true;
		try {
			await api.post(`/api/v1/libraries/${id}/scan`);
			rowMessage[id] = 'Scan started';
		} catch (e) {
			rowError[id] = e instanceof ApiError ? e.message : 'Failed to start scan';
		} finally {
			rowBusy[id] = false;
		}
	}

	async function removeLibrary(id: number, name: string) {
		if (!window.confirm(`Delete library "${name}"? This removes its books, files and progress.`)) return;
		rowError[id] = '';
		rowBusy[id] = true;
		try {
			await api.del(`/api/v1/libraries/${id}`);
			await load();
		} catch (e) {
			rowError[id] = e instanceof ApiError ? e.message : 'Failed to delete library';
			rowBusy[id] = false;
		}
	}
</script>

<div class="page">
	<div class="topbar">
		<h1>Libraries</h1>
		<a class="backlink" href="/admin"><Icon name="arrow-left" size={16} /> Users</a>
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if loading}
		<p class="muted">Loading…</p>
	{:else}
		<div class="table-wrap">
			<table>
				<thead>
					<tr>
						<th>Name</th>
						<th>Path</th>
						<th>Books</th>
						<th>Restricted</th>
						<th></th>
					</tr>
				</thead>
				<tbody>
					{#each libraries as lib (lib.id)}
						<tr>
							<td>{lib.name}</td>
							<td class="muted path">{lib.path}</td>
							<td>{lib.book_count}</td>
							<td>{lib.restricted ? 'Yes' : 'No'}</td>
							<td>
								<div class="row-actions">
									<button class="with-icon" onclick={() => rescan(lib.id)} disabled={rowBusy[lib.id]}>
										<Icon name="refresh-cw" size={15} /> Rescan
									</button>
									<button
										class="danger with-icon"
										onclick={() => removeLibrary(lib.id, lib.name)}
										disabled={rowBusy[lib.id]}
									>
										<Icon name="trash-2" size={15} /> Delete
									</button>
								</div>
							</td>
						</tr>
						{#if rowMessage[lib.id] || rowError[lib.id]}
							<tr class="detail-row">
								<td colspan="5">
									{#if rowMessage[lib.id]}<span class="muted">{rowMessage[lib.id]}</span>{/if}
									{#if rowError[lib.id]}<span class="error">{rowError[lib.id]}</span>{/if}
								</td>
							</tr>
						{/if}
					{/each}
					{#if libraries.length === 0}
						<tr><td colspan="5" class="muted">No libraries yet.</td></tr>
					{/if}
				</tbody>
			</table>
		</div>

		<h2>Add library</h2>
		<form class="create-form" onsubmit={createLibrary}>
			<label>
				Name
				<input type="text" bind:value={newName} required />
			</label>
			<label>
				Path
				<input type="text" bind:value={newPath} placeholder="/library" required />
			</label>
			{#if createError}<div class="error">{createError}</div>{/if}
			<button class="primary with-icon" type="submit" disabled={creating}>
				<Icon name="plus" size={16} />
				{creating ? 'Adding…' : 'Add library'}
			</button>
			<p class="muted note">
				Path is on the server (or the container running Storykeeper), e.g. <code>/library</code>, not on this device.
				Adding a library starts a scan immediately.
			</p>
		</form>
	{/if}
</div>

<style>
	h2 {
		font-size: 1.05rem;
		margin: 1.75rem 0 0.75rem;
	}
	.table-wrap {
		overflow-x: auto;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.9rem;
	}
	th {
		text-align: left;
		color: var(--fg-muted);
		font-weight: 500;
		font-size: 0.8rem;
		padding: 0.4rem 0.6rem;
		border-bottom: 1px solid var(--border);
	}
	td {
		padding: 0.5rem 0.6rem;
		border-bottom: 1px solid var(--border);
		vertical-align: top;
	}
	td.path {
		font-family: ui-monospace, monospace;
		font-size: 0.8rem;
	}
	.detail-row td {
		padding-top: 0;
		padding-bottom: 0.6rem;
		font-size: 0.85rem;
		display: flex;
		gap: 0.75rem;
	}
	.row-actions {
		display: flex;
		gap: 0.4rem;
	}
	button.danger {
		border-color: var(--danger);
		color: var(--danger);
	}
	.create-form {
		display: flex;
		flex-wrap: wrap;
		align-items: end;
		gap: 1rem;
		max-width: 40rem;
	}
	.create-form label {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		font-size: 0.9rem;
		color: var(--fg-muted);
	}
	.create-form .error {
		flex-basis: 100%;
	}
	.note {
		flex-basis: 100%;
		font-size: 0.85rem;
		margin: 0;
	}
</style>
