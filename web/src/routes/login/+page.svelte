<script lang="ts">
	import { goto } from '$app/navigation';
	import { auth } from '$lib/auth.svelte';
	import { ApiError } from '$lib/api/client';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		busy = true;
		try {
			await auth.login(username, password);
			await goto('/', { replaceState: true });
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not reach the server.';
		} finally {
			busy = false;
		}
	}
</script>

<div class="page login">
	<h1>Storykeeper</h1>
	<form onsubmit={submit}>
		<label>
			Username
			<input type="text" bind:value={username} autocomplete="username" autocapitalize="off" required />
		</label>
		<label>
			Password
			<input type="password" bind:value={password} autocomplete="current-password" required />
		</label>
		{#if error}<div class="error">{error}</div>{/if}
		<button class="primary" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
	</form>
</div>

<style>
	.login {
		max-width: 22rem;
		padding-top: 15vh;
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 0.9rem;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		font-size: 0.9rem;
		color: var(--fg-muted);
	}
</style>
