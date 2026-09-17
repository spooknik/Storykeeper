<script lang="ts">
	import { player, SKIP_MS } from '$lib/player/machine.svelte';
	import { fmtTime } from '$lib/format';

	const rates = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3];
	let scrubbing = $state(false);
	let scrubMs = $state(0);

	const shownMs = $derived(scrubbing ? scrubMs : player.positionMs);
	const pct = $derived(player.durationMs > 0 ? (shownMs / player.durationMs) * 100 : 0);

	function onScrubInput(e: Event) {
		scrubbing = true;
		scrubMs = Number((e.target as HTMLInputElement).value);
	}
	function onScrubChange(e: Event) {
		player.seekTo(Number((e.target as HTMLInputElement).value));
		scrubbing = false;
	}
</script>

{#if player.book}
	<div class="player" style:--pct="{pct}%">
		{#if player.notice}
			<div class="notice">
				<span>{player.notice.text}</span>
				{#if player.notice.undo}
					<button onclick={player.notice.undo}>Undo</button>
				{/if}
				<button onclick={() => player.dismissNotice()} aria-label="Dismiss">✕</button>
			</div>
		{/if}
		<input
			class="scrub"
			type="range"
			min="0"
			max={player.durationMs}
			step="1000"
			value={shownMs}
			oninput={onScrubInput}
			onchange={onScrubChange}
			aria-label="Position"
		/>
		<div class="row">
			<a class="meta" href="/book/{player.book.id}">
				{#if player.book.cover_url}
					<img src="{player.book.cover_url.replace(/\?.*$/, '')}?size=200" alt="" />
				{/if}
				<div class="text">
					<div class="title">{player.book.title}</div>
					<div class="sub">
						{#if player.currentChapter}{player.currentChapter.title} ·{/if}
						{fmtTime(shownMs)} / {fmtTime(player.durationMs)}
					</div>
				</div>
			</a>
			<div class="controls">
				<button onclick={() => player.skip(-SKIP_MS)} aria-label="Back 30 seconds">−30</button>
				{#if player.needsGesture || player.status === 'suspended'}
					<button class="primary big" onclick={() => player.play()}>Tap to resume</button>
				{:else if player.status === 'playing'}
					<button class="primary big" onclick={() => player.pause()} aria-label="Pause">❚❚</button>
				{:else if player.status === 'loading'}
					<button class="primary big" disabled aria-label="Loading">…</button>
				{:else}
					<button class="primary big" onclick={() => player.play()} aria-label="Play">▶</button>
				{/if}
				<button onclick={() => player.skip(SKIP_MS)} aria-label="Forward 30 seconds">+30</button>
				<select
					value={player.rate}
					onchange={(e) => player.setRate(Number((e.target as HTMLSelectElement).value))}
					aria-label="Speed"
				>
					{#each rates as r (r)}
						<option value={r}>{r}×</option>
					{/each}
				</select>
			</div>
		</div>
		{#if player.error}
			<div class="error small">{player.error}</div>
		{/if}
	</div>
{/if}

<style>
	.player {
		position: fixed;
		left: 0;
		right: 0;
		bottom: 0;
		padding: 0 0.75rem calc(var(--safe-bottom) + 0.5rem);
		background: var(--bg-raised);
		border-top: 1px solid var(--border);
		z-index: 10;
	}
	.notice {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.5rem 0;
		font-size: 0.85rem;
	}
	.notice span {
		flex: 1;
	}
	.notice button {
		padding: 0.3rem 0.6rem;
	}
	.scrub {
		width: 100%;
		margin: 0;
		height: 24px;
		-webkit-appearance: none;
		appearance: none;
		background: transparent;
	}
	.scrub::-webkit-slider-runnable-track {
		height: 4px;
		border-radius: 2px;
		background: linear-gradient(to right, var(--accent) var(--pct), var(--border) var(--pct));
	}
	.scrub::-webkit-slider-thumb {
		-webkit-appearance: none;
		width: 16px;
		height: 16px;
		margin-top: -6px;
		border-radius: 50%;
		background: var(--accent);
	}
	.row {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		min-height: 56px;
	}
	.meta {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		min-width: 0;
		flex: 1;
		color: inherit;
	}
	.meta img {
		width: 44px;
		height: 44px;
		border-radius: 6px;
		object-fit: cover;
		flex-shrink: 0;
	}
	.text {
		min-width: 0;
	}
	.title {
		font-weight: 600;
		font-size: 0.9rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.sub {
		font-size: 0.75rem;
		color: var(--fg-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.controls {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		flex-shrink: 0;
	}
	.controls button {
		padding: 0.45rem 0.6rem;
	}
	.big {
		min-width: 48px;
		height: 44px;
		font-size: 1rem;
	}
	.small {
		font-size: 0.8rem;
		padding-bottom: 0.3rem;
	}
	@media (max-width: 480px) {
		.controls select {
			display: none;
		}
	}
</style>
