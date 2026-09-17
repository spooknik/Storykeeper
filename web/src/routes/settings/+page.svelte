<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { SessionListItem } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import { player } from '$lib/player/machine.svelte';

	let sessions = $state<SessionListItem[]>([]);
	let loading = $state(true);
	let error = $state('');
	let revoking = $state<Record<number, boolean>>({});

	async function load() {
		loading = true;
		error = '';
		try {
			sessions = await api.get<SessionListItem[]>('/api/v1/sessions');
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to load sessions';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function revoke(id: number) {
		error = '';
		revoking[id] = true;
		try {
			await api.del(`/api/v1/sessions/${id}`);
			await load();
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Failed to revoke session';
		} finally {
			revoking[id] = false;
		}
	}

	async function signOut() {
		await auth.logout();
		await goto('/login', { replaceState: true });
	}

	function onRateInput(e: Event) {
		const v = Number((e.currentTarget as HTMLInputElement).value);
		player.setRate(v);
	}
</script>

<div class="page">
	<div class="topbar">
		<h1>Settings</h1>
	</div>

	<section>
		<h2>Account</h2>
		<div class="account">
			<div><span class="muted">Username</span> {auth.user?.username}</div>
			<div><span class="muted">Role</span> {auth.user?.role}</div>
		</div>
		<button class="danger" onclick={signOut}>Sign out</button>
	</section>

	<section>
		<h2>Playback speed</h2>
		<div class="rate-row">
			<input
				type="range"
				min="0.5"
				max="3"
				step="0.1"
				value={player.rate}
				oninput={onRateInput}
			/>
			<span class="rate-value">{player.rate.toFixed(1)}×</span>
		</div>
		<p class="muted small">Applies immediately and becomes the default for new books.</p>
	</section>

	<section>
		<h2>Sessions</h2>
		{#if error}<p class="error">{error}</p>{/if}
		{#if loading}
			<p class="muted">Loading…</p>
		{:else}
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th>Device</th>
							<th>Created</th>
							<th>Last seen</th>
							<th></th>
						</tr>
					</thead>
					<tbody>
						{#each sessions as s (s.id)}
							<tr>
								<td>
									{s.device_name}
									{#if s.current}<span class="badge">this device</span>{/if}
								</td>
								<td class="muted">{new Date(s.created_at).toLocaleString()}</td>
								<td class="muted">{new Date(s.last_seen_at).toLocaleString()}</td>
								<td>
									{#if !s.current}
										<button onclick={() => revoke(s.id)} disabled={revoking[s.id]}>Revoke</button>
									{/if}
								</td>
							</tr>
						{/each}
						{#if sessions.length === 0}
							<tr><td colspan="4" class="muted">No sessions.</td></tr>
						{/if}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
</div>

<style>
	section {
		margin-bottom: 2rem;
	}
	h2 {
		font-size: 1.05rem;
		margin: 0 0 0.75rem;
	}
	.account {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		margin-bottom: 0.9rem;
		font-size: 0.95rem;
	}
	.account .muted {
		display: inline-block;
		width: 5.5rem;
	}
	button.danger {
		border-color: var(--danger);
		color: var(--danger);
	}
	.rate-row {
		display: flex;
		align-items: center;
		gap: 1rem;
		max-width: 24rem;
	}
	.rate-row input[type='range'] {
		flex: 1;
	}
	.rate-value {
		min-width: 3rem;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.small {
		font-size: 0.85rem;
		margin-top: 0.4rem;
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
	}
	.badge {
		margin-left: 0.5rem;
		font-size: 0.7rem;
		color: var(--accent);
		border: 1px solid var(--accent);
		border-radius: 999px;
		padding: 0.1rem 0.5rem;
	}
</style>
