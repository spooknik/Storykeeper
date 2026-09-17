<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { Library, Role, UserWithLibraries } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import Icon from '$lib/components/Icon.svelte';

	let users = $state<UserWithLibraries[]>([]);
	let libraries = $state<Library[]>([]);
	let loading = $state(true);
	let error = $state('');

	let newUsername = $state('');
	let newPassword = $state('');
	let newRole = $state<Role>('user');
	let createError = $state('');
	let creating = $state(false);

	// Per-row transient UI state, keyed by user id.
	let rolePick = $state<Record<number, Role>>({});
	let passwordDraft = $state<Record<number, string>>({});
	let rowError = $state<Record<number, string>>({});
	let rowBusy = $state<Record<number, boolean>>({});
	let libsOpen = $state<Record<number, boolean>>({});

	$effect(() => {
		if (auth.loaded && auth.user && auth.user.role !== 'admin') {
			void goto('/');
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const [u, l] = await Promise.all([
				api.get<UserWithLibraries[]>('/api/v1/users'),
				api.get<Library[]>('/api/v1/libraries')
			]);
			users = u;
			libraries = l;
			for (const usr of u) {
				rolePick[usr.id] = usr.role;
			}
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to load';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function libraryNames(ids: number[]): string {
		if (ids.length === 0) return 'All libraries';
		return ids.map((id) => libraries.find((l) => l.id === id)?.name ?? `#${id}`).join(', ');
	}

	async function createUser(e: SubmitEvent) {
		e.preventDefault();
		createError = '';
		creating = true;
		try {
			await api.post('/api/v1/users', {
				username: newUsername,
				password: newPassword,
				role: newRole
			});
			newUsername = '';
			newPassword = '';
			newRole = 'user';
			await load();
		} catch (e) {
			createError = e instanceof ApiError ? e.message : 'Failed to create user';
		} finally {
			creating = false;
		}
	}

	async function applyRole(id: number) {
		rowError[id] = '';
		rowBusy[id] = true;
		try {
			await api.patch(`/api/v1/users/${id}`, { role: rolePick[id] });
			await load();
		} catch (e) {
			rowError[id] = e instanceof ApiError ? e.message : 'Failed to update role';
			// Revert the pick to the server's last known value.
			const usr = users.find((u) => u.id === id);
			if (usr) rolePick[id] = usr.role;
		} finally {
			rowBusy[id] = false;
		}
	}

	async function applyPassword(id: number) {
		const pw = passwordDraft[id];
		if (!pw) return;
		rowError[id] = '';
		rowBusy[id] = true;
		try {
			await api.patch(`/api/v1/users/${id}`, { password: pw });
			passwordDraft[id] = '';
		} catch (e) {
			rowError[id] = e instanceof ApiError ? e.message : 'Failed to set password';
		} finally {
			rowBusy[id] = false;
		}
	}

	async function removeUser(id: number, username: string) {
		if (!window.confirm(`Delete user "${username}"? This cannot be undone.`)) return;
		rowError[id] = '';
		rowBusy[id] = true;
		try {
			await api.del(`/api/v1/users/${id}`);
			await load();
		} catch (e) {
			rowError[id] = e instanceof ApiError ? e.message : 'Failed to delete user';
		} finally {
			rowBusy[id] = false;
		}
	}

	function toggleLibsOpen(id: number) {
		libsOpen[id] = !libsOpen[id];
	}

	async function toggleLib(usr: UserWithLibraries, libId: number) {
		const has = usr.library_ids.includes(libId);
		const next = has ? usr.library_ids.filter((x) => x !== libId) : [...usr.library_ids, libId];
		rowError[usr.id] = '';
		rowBusy[usr.id] = true;
		try {
			await api.put(`/api/v1/users/${usr.id}/libraries`, { library_ids: next });
			await load();
			libsOpen[usr.id] = true;
		} catch (e) {
			rowError[usr.id] = e instanceof ApiError ? e.message : 'Failed to update libraries';
		} finally {
			rowBusy[usr.id] = false;
		}
	}
</script>

<div class="page">
	<div class="topbar">
		<h1>Admin</h1>
		<a class="with-icon" href="/admin/libraries">
			<Icon name="library" size={16} /> Manage libraries
		</a>
	</div>

	{#if error}<p class="error">{error}</p>{/if}

	{#if loading}
		<p class="muted">Loading…</p>
	{:else}
		<div class="table-wrap">
			<table>
				<thead>
					<tr>
						<th>Username</th>
						<th>Role</th>
						<th>Libraries</th>
						<th>Password</th>
						<th></th>
					</tr>
				</thead>
				<tbody>
					{#each users as usr (usr.id)}
						<tr>
							<td>{usr.username}</td>
							<td>
								<select bind:value={rolePick[usr.id]} onchange={() => applyRole(usr.id)} disabled={rowBusy[usr.id]}>
									<option value="user">user</option>
									<option value="admin">admin</option>
								</select>
							</td>
							<td>
								<button class="link libs-toggle" onclick={() => toggleLibsOpen(usr.id)}>
									{libraryNames(usr.library_ids)}
								</button>
								{#if libsOpen[usr.id]}
									<div class="libs-panel">
										{#if libraries.length === 0}
											<span class="muted">No libraries yet.</span>
										{/if}
										{#each libraries as lib (lib.id)}
											<label class="lib-check">
												<input
													type="checkbox"
													checked={usr.library_ids.includes(lib.id)}
													disabled={rowBusy[usr.id]}
													onchange={() => toggleLib(usr, lib.id)}
												/>
												{lib.name}
											</label>
										{/each}
										<div class="muted small">Unchecked = visible to everyone.</div>
									</div>
								{/if}
							</td>
							<td>
								<div class="pw-row">
									<input
										type="password"
										placeholder="New password"
										bind:value={passwordDraft[usr.id]}
										autocomplete="new-password"
									/>
									<button onclick={() => applyPassword(usr.id)} disabled={rowBusy[usr.id] || !passwordDraft[usr.id]}>
										Set
									</button>
								</div>
							</td>
							<td>
								<button
									class="danger with-icon"
									onclick={() => removeUser(usr.id, usr.username)}
									disabled={rowBusy[usr.id] || usr.id === auth.user?.id}
								>
									<Icon name="trash-2" size={15} /> Delete
								</button>
							</td>
						</tr>
						{#if rowError[usr.id]}
							<tr class="error-row">
								<td colspan="5" class="error">{rowError[usr.id]}</td>
							</tr>
						{/if}
					{/each}
				</tbody>
			</table>
		</div>

		<h2>Add user</h2>
		<form class="create-form" onsubmit={createUser}>
			<label>
				Username
				<input type="text" bind:value={newUsername} autocomplete="off" required />
			</label>
			<label>
				Password
				<input type="password" bind:value={newPassword} autocomplete="new-password" required />
			</label>
			<label>
				Role
				<select bind:value={newRole}>
					<option value="user">user</option>
					<option value="admin">admin</option>
				</select>
			</label>
			{#if createError}<div class="error">{createError}</div>{/if}
			<button class="primary with-icon" type="submit" disabled={creating}>
				<Icon name="plus" size={16} />
				{creating ? 'Creating…' : 'Create user'}
			</button>
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
	.error-row td {
		padding-top: 0;
		padding-bottom: 0.6rem;
		font-size: 0.85rem;
	}
	.link.libs-toggle {
		background: none;
		border: 0;
		padding: 0;
		color: var(--accent);
		text-align: left;
		border-radius: 0;
	}
	.libs-panel {
		margin-top: 0.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		background: var(--bg-raised);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.6rem;
		min-width: 12rem;
	}
	.lib-check {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.85rem;
	}
	.small {
		font-size: 0.75rem;
	}
	.pw-row {
		display: flex;
		gap: 0.4rem;
	}
	.pw-row input {
		width: 10rem;
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
</style>
