<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { SessionListItem } from '$lib/api/types';
	import { auth } from '$lib/auth.svelte';
	import { prefs } from '$lib/player/prefs.svelte';
	import Icon from '$lib/components/Icon.svelte';

	const SKIP_OPTIONS = [10, 15, 30, 45, 60];
	const SLEEP_OPTIONS = [0, 15, 30, 45, 60];

	let sessions = $state<SessionListItem[]>([]);
	let loading = $state(true);
	let error = $state('');
	let revoking = $state<Record<number, boolean>>({});
	let prefsError = $state('');

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

	onMount(() => {
		load();
		// The layout will also call this on login; safe to call again here,
		// prefs.load() coalesces concurrent calls into one request.
		if (!prefs.loaded) void prefs.load();
	});

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

	async function savePrefs(patch: Parameters<typeof prefs.save>[0]) {
		prefsError = '';
		try {
			await prefs.save(patch);
		} catch (e) {
			prefsError = e instanceof ApiError ? e.message : 'Failed to save preference';
		}
	}

	function onRateInput(e: Event) {
		const v = Number((e.currentTarget as HTMLInputElement).value);
		void savePrefs({ playbackRate: v });
	}

	function onSkipBackChange(e: Event) {
		void savePrefs({ skipBackSeconds: Number((e.currentTarget as HTMLSelectElement).value) });
	}

	function onSkipForwardChange(e: Event) {
		void savePrefs({ skipForwardSeconds: Number((e.currentTarget as HTMLSelectElement).value) });
	}

	function onAutoRewindChange(e: Event) {
		void savePrefs({ autoRewind: (e.currentTarget as HTMLInputElement).checked });
	}

	function onSleepChange(e: Event) {
		void savePrefs({ defaultSleepMinutes: Number((e.currentTarget as HTMLSelectElement).value) });
	}
</script>

<div class="page">
	<div class="topbar">
		<h1>Settings</h1>
	</div>

	<section>
		<h2 class="with-icon"><Icon name="user" size={17} /> Account</h2>
		<div class="account">
			<div><span class="muted">Username</span> {auth.user?.username}</div>
			<div><span class="muted">Role</span> {auth.user?.role}</div>
		</div>
		<button class="danger with-icon" onclick={signOut}>
			<Icon name="log-out" size={16} /> Sign out
		</button>
	</section>

	<section>
		<h2 class="with-icon"><Icon name="gauge" size={17} /> Playback speed</h2>
		<div class="rate-row">
			<input
				type="range"
				min="0.5"
				max="3"
				step="0.1"
				value={prefs.playbackRate}
				oninput={onRateInput}
			/>
			<span class="rate-value">{prefs.playbackRate.toFixed(1)}×</span>
		</div>
		<p class="muted small">Applies immediately and becomes the default for new books.</p>
	</section>

	<section>
		<h2 class="with-icon"><Icon name="rewind-30" size={17} /> Skip amounts</h2>
		<div class="prefs-grid">
			<div class="prefs-field">
				<span class="muted">Back</span>
				<select value={prefs.skipBackSeconds} onchange={onSkipBackChange} aria-label="Skip back">
					{#each SKIP_OPTIONS as s (s)}
						<option value={s}>{s}s</option>
					{/each}
				</select>
			</div>
			<div class="prefs-field">
				<span class="muted">Forward</span>
				<select
					value={prefs.skipForwardSeconds}
					onchange={onSkipForwardChange}
					aria-label="Skip forward"
				>
					{#each SKIP_OPTIONS as s (s)}
						<option value={s}>{s}s</option>
					{/each}
				</select>
			</div>
		</div>
	</section>

	<section>
		<h2 class="with-icon"><Icon name="rotate-ccw" size={17} /> Auto-rewind</h2>
		<label class="switch-row">
			<input type="checkbox" checked={prefs.autoRewind} onchange={onAutoRewindChange} />
			<span>Auto-rewind on resume</span>
		</label>
		<p class="muted small">Rewind a few seconds when resuming after a pause.</p>
	</section>

	<section>
		<h2 class="with-icon"><Icon name="moon" size={17} /> Default sleep timer</h2>
		<select value={prefs.defaultSleepMinutes} onchange={onSleepChange} aria-label="Default sleep timer">
			{#each SLEEP_OPTIONS as m (m)}
				<option value={m}>{m === 0 ? 'Off' : `${m} minutes`}</option>
			{/each}
		</select>
	</section>

	{#if prefsError}<p class="error">{prefsError}</p>{/if}

	<section>
		<h2 class="with-icon"><Icon name="users" size={17} /> Sessions</h2>
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
										<button class="with-icon" onclick={() => revoke(s.id)} disabled={revoking[s.id]}>
											<Icon name="log-out" size={14} /> Revoke
										</button>
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
	.prefs-grid {
		display: flex;
		gap: 1.5rem;
		flex-wrap: wrap;
	}
	.prefs-field {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		font-size: 0.85rem;
	}
	.switch-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.95rem;
	}
	.switch-row input[type='checkbox'] {
		width: 1.1rem;
		height: 1.1rem;
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
		/* Inline-block so the pill wraps as one unit onto its own line on
		   narrow screens instead of breaking mid-pill and clipping. */
		display: inline-block;
		white-space: nowrap;
		vertical-align: middle;
		line-height: 1.4;
		margin-left: 0.5rem;
		font-size: 0.7rem;
		color: var(--accent);
		border: 1px solid var(--accent);
		border-radius: 999px;
		padding: 0.1rem 0.5rem;
	}
</style>
