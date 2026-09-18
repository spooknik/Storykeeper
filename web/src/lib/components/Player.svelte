<script lang="ts">
	import { player } from '$lib/player/machine.svelte';
	import { prefs } from '$lib/player/prefs.svelte';
	import { api, ApiError } from '$lib/api/client';
	import { chapterLabel } from '$lib/player/chapters';
	import { bookmarks } from '$lib/player/bookmarks.svelte';
	import { sleepTimer, SLEEP_MINUTES, type SleepMode } from '$lib/player/sleep.svelte';
	import { journal } from '$lib/player/journal';
	import { fmtTime, joinNames } from '$lib/format';
	import Icon from './Icon.svelte';
	import Sheet from './Sheet.svelte';

	const rates = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3];

	let scrubbing = $state(false);
	let scrubMs = $state(0);
	/** ms under the pointer while hovering the scrub track (non-touch only); null when not hovering. */
	let hoverMs = $state<number | null>(null);
	let sheet = $state<'none' | 'chapters' | 'sleep' | 'speed'>('none');
	let playerHeight = $state(0);
	let chapterList = $state<HTMLElement | null>(null);

	// This app is SSR-off (see +layout.ts), so `window` is always available here:
	// pick the real layout on first render, no flash-of-wrong-layout. Only one of
	// the two bars is ever mounted — not just CSS-hidden — so a plain CSS
	// selector like the rest of the suite uses (".player a.meta") never matches
	// more than one element.
	let isDesktop = $state(window.matchMedia('(min-width: 561px)').matches);
	$effect(() => {
		const mql = window.matchMedia('(min-width: 561px)');
		const onChange = (e: MediaQueryListEvent) => (isDesktop = e.matches);
		mql.addEventListener('change', onChange);
		return () => mql.removeEventListener('change', onChange);
	});

	const backLabel = $derived(`Back ${Math.round(player.skipBackMs / 1000)} seconds`);
	const forwardLabel = $derived(`Forward ${Math.round(player.skipForwardMs / 1000)} seconds`);
	/** The toggle reflects whether the loaded book currently has an override. */
	const perBook = $derived(player.bookRateOverride != null);

	const shownMs = $derived(scrubbing ? scrubMs : player.positionMs);
	const pct = $derived(player.durationMs > 0 ? (shownMs / player.durationMs) * 100 : 0);
	const chapters = $derived(player.book?.chapters ?? []);
	const chaptersTitle = $derived(chapterLabel(chapters));
	const shownChapter = $derived(chapters.length > 0 ? chapterAt(shownMs) : null);

	// The bottom info row's right-hand figure: how much is left, adjusted for
	// the current playback rate (a 2x listener's "remaining" is half as long).
	const remainingMs = $derived(Math.max(0, player.durationMs - shownMs));
	const remainingAdjMs = $derived(player.rate !== 1 ? remainingMs / player.rate : remainingMs);

	// The scrub preview bubble: shows while dragging (scrubbing) or, on
	// hover-capable devices, while the pointer sits over the track.
	const previewMs = $derived(scrubbing ? scrubMs : hoverMs);
	const previewChapter = $derived(previewMs !== null ? chapterAt(previewMs) : null);
	const previewLabel = $derived(
		previewMs !== null ? fmtTime(previewMs) + (previewChapter ? ` · ${previewChapter.title}` : '') : ''
	);
	// Keep the bubble from running off either edge of the bar.
	const previewLeftPct = $derived(previewMs !== null ? Math.min(94, Math.max(6, pctOf(previewMs))) : 0);

	// Keep the bottom padding of .page in step with however tall the bar really is.
	$effect(() => {
		const h = playerHeight;
		if (h > 0) document.documentElement.style.setProperty('--player-height', `${h}px`);
		return () => document.documentElement.style.removeProperty('--player-height');
	});

	// Opening the sheet should land on the chapter being listened to.
	$effect(() => {
		if (sheet !== 'chapters' || !chapterList) return;
		chapterList.querySelector('.active')?.scrollIntoView({ block: 'center' });
	});

	/** The chapter that contains a given book-timeline position, or null with no chapters. */
	function chapterAt(ms: number) {
		if (chapters.length === 0) return null;
		let cur = chapters[0];
		for (const c of chapters) {
			if (c.start_ms <= ms) cur = c;
			else break;
		}
		return cur;
	}

	function pctOf(ms: number): number {
		return player.durationMs > 0 ? (ms / player.durationMs) * 100 : 0;
	}

	function onScrubInput(e: Event) {
		scrubbing = true;
		scrubMs = Number((e.target as HTMLInputElement).value);
	}
	function onScrubChange(e: Event) {
		player.seekTo(Number((e.target as HTMLInputElement).value));
		scrubbing = false;
	}
	/** Hover preview on devices that report a real pointer; ignored for touch. */
	function onScrubPointerMove(e: PointerEvent) {
		if (e.pointerType === 'touch') return;
		const el = e.currentTarget as HTMLInputElement;
		const rect = el.getBoundingClientRect();
		const x = Math.min(Math.max(e.clientX - rect.left, 0), rect.width);
		hoverMs = rect.width > 0 ? (x / rect.width) * player.durationMs : 0;
	}
	function onScrubPointerLeave() {
		hoverMs = null;
	}

	function toast(text: string) {
		const n = { text };
		player.notice = n;
		setTimeout(() => {
			if (player.notice === n) player.notice = null;
		}, 3500);
	}

	async function addBookmark() {
		const b = player.book;
		if (!b) return;
		const pos = Math.min(Math.max(0, Math.round(player.positionMs)), b.duration_ms);
		const note = window.prompt(`Bookmark at ${fmtTime(pos)}. Note (optional):`, '');
		if (note === null) return; // cancelled
		try {
			await bookmarks.create(b.id, pos, note.trim().slice(0, 500));
			toast(`Bookmark saved at ${fmtTime(pos)}`);
		} catch {
			toast('Could not save the bookmark.');
		}
	}

	function pickSleep(mode: SleepMode) {
		sleepTimer.set(mode);
		sheet = 'none';
	}

	function gotoChapter(startMs: number) {
		player.seekTo(startMs);
		sheet = 'none';
	}

	/** "1×", "1.5×", "1.25×" — no trailing zeros. */
	function formatRate(r: number): string {
		return `${Number(r.toFixed(2))}×`;
	}

	/**
	 * Choosing a speed. When the "for this book only" toggle is on, it's a
	 * per-book override: applied locally and PUT to the progress record. When
	 * off, it's the listener's global default, saved through the prefs store
	 * (which applies it to the engine itself).
	 */
	async function pickRate(r: number) {
		const b = player.book;
		if (perBook && b) {
			player.setRate(r, { perBook: true });
			try {
				await api.put(`/api/v1/progress/${b.id}/rate`, { playback_rate: r });
			} catch {
				toast('Could not save this book’s speed.');
			}
		} else {
			try {
				await prefs.save({ playbackRate: r });
			} catch {
				toast('Could not save playback speed.');
			}
		}
	}

	/** Flip the "for this book only" toggle itself, independent of picking a speed. */
	async function togglePerBook() {
		const b = player.book;
		if (!b) return;
		if (perBook) {
			player.clearBookRate();
			try {
				await api.put(`/api/v1/progress/${b.id}/rate`, { playback_rate: null });
			} catch (e) {
				toast(e instanceof ApiError ? e.message : 'Could not clear this book’s speed.');
			}
		} else {
			const r = player.rate;
			player.setRate(r, { perBook: true });
			try {
				await api.put(`/api/v1/progress/${b.id}/rate`, { playback_rate: r });
			} catch (e) {
				toast(e instanceof ApiError ? e.message : 'Could not save this book’s speed.');
			}
		}
	}

	/** Close the bar entirely: stop, drop the loaded book, and forget it was last played. */
	function closePlayer() {
		player.pause();
		player.unload();
		if (player.userId) journal.clearLast(player.userId);
	}
</script>

{#snippet scrubber()}
	<div class="scrub-wrap">
		<input
			class="scrub"
			type="range"
			min="0"
			max={player.durationMs}
			step="1000"
			value={shownMs}
			oninput={onScrubInput}
			onchange={onScrubChange}
			onpointermove={onScrubPointerMove}
			onpointerleave={onScrubPointerLeave}
			aria-label="Position"
		/>
		<div class="ticks" aria-hidden="true">
			{#each chapters.slice(1) as c (c.index)}
				<span class="tick" style:left="{pctOf(c.start_ms)}%"></span>
			{/each}
		</div>
		{#if previewMs !== null}
			<div
				class="scrub-preview"
				data-testid="scrub-preview"
				aria-hidden="true"
				style:left="{previewLeftPct}%"
			>
				{previewLabel}
			</div>
		{/if}
	</div>
{/snippet}

{#snippet transport(size: number, round: boolean)}
	{#if chapters.length > 1}
		<button class="icon-btn" onclick={() => player.prevChapter()} aria-label="Previous chapter">
			<Icon name="skip-back" size={18} />
		</button>
	{/if}
	<button onclick={() => player.skip(-player.skipBackMs)} aria-label={backLabel}>
		<Icon name="rewind-30" size={22} />
	</button>
	{#if player.needsGesture || player.status === 'suspended'}
		<button class="primary big" onclick={() => player.play()}>Tap to resume</button>
	{:else if player.status === 'playing'}
		<button class="primary big {round ? 'round' : ''}" onclick={() => player.pause()} aria-label="Pause">
			<Icon name="pause" size={size} />
		</button>
	{:else if player.status === 'loading'}
		<button class="primary big {round ? 'round' : ''}" disabled aria-label="Loading">
			<Icon name="more-horizontal" size={size} />
		</button>
	{:else}
		<button class="primary big {round ? 'round' : ''}" onclick={() => player.play()} aria-label="Play">
			<Icon name="play" size={size} />
		</button>
	{/if}
	<button onclick={() => player.skip(player.skipForwardMs)} aria-label={forwardLabel}>
		<Icon name="forward-30" size={22} />
	</button>
	{#if chapters.length > 1}
		<button class="icon-btn" onclick={() => player.nextChapter()} aria-label="Next chapter">
			<Icon name="skip-forward" size={18} />
		</button>
	{/if}
{/snippet}

{#if player.book}
	<div class="player" style:--pct="{pct}%" bind:clientHeight={playerHeight}>
		{#if player.notice}
			<div class="notice">
				<span>{player.notice.text}</span>
				{#if player.notice.undo}
					<button onclick={player.notice.undo}>Undo</button>
				{/if}
				<button class="icon-btn" onclick={() => player.dismissNotice()} aria-label="Dismiss">
					<Icon name="x" size={16} />
				</button>
			</div>
		{/if}

		<!-- Desktop / tablet layout: modelled on Audiobookshelf's bottom bar. -->
		{#if isDesktop}
		<div class="desktop-bar">
			<div class="top-desktop">
				<a class="left-desktop meta" href="/book/{player.book.id}">
					{#if player.book.cover_url}
						<img class="cover-img" src="{player.book.cover_url.replace(/\?.*$/, '')}?size=200" alt="" />
					{/if}
					<div class="info-desktop">
						<div class="title-desktop">{player.book.title}</div>
						<div class="sub-desktop">
							{#if player.book.authors.length}
								<span class="with-icon">
									<Icon name="user" size={13} />{joinNames(player.book.authors)}
								</span>
							{/if}
							<span class="with-icon">
								<Icon name="clock" size={13} />{fmtTime(player.durationMs)}
							</span>
						</div>
					</div>
				</a>
				<div class="transport-desktop">
					{@render transport(26, true)}
				</div>
				<div class="right-desktop">
					<button onclick={() => (sheet = 'speed')} aria-haspopup="dialog" aria-label="Speed" class="tool">
						<Icon name="gauge" size={16} />
						<span>{formatRate(player.rate)}</span>
					</button>
					<button
						class="tool"
						class:armed={sleepTimer.armed}
						onclick={() => (sheet = 'sleep')}
						aria-haspopup="dialog"
						aria-label="Sleep timer"
					>
						<Icon name="moon" size={16} />
					</button>
					<button class="tool icon-btn" onclick={addBookmark} aria-label="Bookmark">
						<Icon name="bookmark" size={16} />
					</button>
					{#if chapters.length > 0}
						<button
							class="tool icon-btn"
							onclick={() => (sheet = 'chapters')}
							aria-haspopup="dialog"
							aria-label={chaptersTitle}
							title={chaptersTitle}
						>
							<Icon name="list" size={16} />
						</button>
					{/if}
					<button class="tool icon-btn close" onclick={closePlayer} aria-label="Close player">
						<Icon name="x" size={18} />
					</button>
				</div>
			</div>
			{@render scrubber()}
			<div class="scrubinfo-desktop">
				<span class="elapsed">{fmtTime(shownMs)} / {Math.round(pct)}%</span>
				<span class="chapter-title">{shownChapter?.title ?? ''}</span>
				<span class="remaining" data-testid="remaining">
					-{fmtTime(remainingAdjMs)}
					{#if player.rate !== 1}<span class="muted rate-note"> at {formatRate(player.rate)}</span>{/if}
				</span>
			</div>
		</div>
		{:else}

		<!-- Phone layout: unchanged compact bar. -->
		<div class="mobile-bar">
			{@render scrubber()}
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
					{@render transport(22, false)}
				</div>
			</div>
			<div class="tools">
				{#if chapters.length > 0}
					<button onclick={() => (sheet = 'chapters')} aria-haspopup="dialog">
						<Icon name="list" size={15} />
						{chaptersTitle}
					</button>
				{/if}
				<button
					class:armed={sleepTimer.armed}
					onclick={() => (sheet = 'sleep')}
					aria-haspopup="dialog"
					aria-label="Sleep timer"
				>
					<Icon name="moon" size={15} />
					{sleepTimer.label}
				</button>
				<button onclick={addBookmark}>
					<Icon name="bookmark" size={15} />
					Bookmark
				</button>
				<button onclick={() => (sheet = 'speed')} aria-haspopup="dialog" aria-label="Speed">
					<Icon name="gauge" size={15} />
					{formatRate(player.rate)}
				</button>
			</div>
		</div>
		{/if}

		{#if player.error}
			<div class="error small">{player.error}</div>
		{/if}
	</div>

	{#if sheet === 'chapters'}
		<Sheet title={chaptersTitle} onclose={() => (sheet = 'none')}>
			<ol class="list" bind:this={chapterList}>
				{#each chapters as c (c.index)}
					<li>
						<button
							class="entry"
							class:active={player.currentChapter?.index === c.index}
							onclick={() => gotoChapter(c.start_ms)}
						>
							<span class="label">{c.title}</span>
							<span class="muted">{fmtTime(c.start_ms)}</span>
						</button>
					</li>
				{/each}
			</ol>
		</Sheet>
	{:else if sheet === 'sleep'}
		<Sheet title="Sleep timer" onclose={() => (sheet = 'none')}>
			<ul class="list">
				<li>
					<button class="entry" class:active={!sleepTimer.armed} onclick={() => pickSleep('off')}>
						<span class="label">Off</span>
						{#if prefs.defaultSleepMinutes === 0}<span class="muted small-tag">Default</span>{/if}
					</button>
				</li>
				{#each SLEEP_MINUTES as m (m)}
					<li>
						<button class="entry" class:active={sleepTimer.mode === m} onclick={() => pickSleep(m)}>
							<span class="label">{m} minutes</span>
							{#if prefs.defaultSleepMinutes === m}<span class="muted small-tag">Default</span>{/if}
						</button>
					</li>
				{/each}
				<li>
					<button
						class="entry"
						class:active={sleepTimer.mode === 'chapter'}
						onclick={() => pickSleep('chapter')}
					>
						<span class="label">End of {chaptersTitle === 'Parts' ? 'part' : 'chapter'}</span>
					</button>
				</li>
			</ul>
			<p class="hint muted">The volume fades out over 5 seconds before the pause.</p>
		</Sheet>
	{:else if sheet === 'speed'}
		<Sheet title="Playback speed" onclose={() => (sheet = 'none')}>
			<label class="switch-row">
				<input type="checkbox" checked={perBook} onchange={togglePerBook} />
				<span>For this book only</span>
			</label>
			<ul class="list">
				{#each rates as r (r)}
					<li>
						<button class="entry" class:active={player.rate === r} onclick={() => pickRate(r)}>
							<span class="label">{formatRate(r)}</span>
						</button>
					</li>
				{/each}
			</ul>
		</Sheet>
	{/if}
{/if}

<style>
	.player {
		position: fixed;
		left: 0;
		right: 0;
		/* Above the tab bar when it's present; on desktop --tabbar-height is 0. */
		bottom: var(--tabbar-height, 0px);
		padding: 0 0.75rem calc(var(--safe-bottom) + 0.5rem);
		background: var(--bg-raised);
		border-top: 1px solid var(--border);
		z-index: 10;
	}
	@media (max-width: 560px) {
		.player {
			/* The tab bar owns the safe-area inset at this width; don't double it. */
			padding-bottom: 0.5rem;
		}
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
	.notice .icon-btn {
		min-width: 32px;
		height: 32px;
		padding: 0;
	}

	/* --- shared scrub track (used by both layouts) -------------------------- */
	.scrub-wrap {
		position: relative;
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
	.ticks {
		position: absolute;
		inset: 0;
		pointer-events: none;
	}
	.tick {
		position: absolute;
		top: 50%;
		width: 1px;
		height: 8px;
		background: rgba(0, 0, 0, 0.45);
		transform: translate(-50%, -50%);
	}
	.scrub-preview {
		position: absolute;
		bottom: 100%;
		transform: translateX(-50%);
		margin-bottom: 4px;
		background: #fff;
		color: #111;
		font-size: 0.72rem;
		font-weight: 600;
		line-height: 1;
		padding: 0.3rem 0.55rem;
		border-radius: 999px;
		white-space: nowrap;
		max-width: 220px;
		overflow: hidden;
		text-overflow: ellipsis;
		pointer-events: none;
		box-shadow: 0 2px 6px rgba(0, 0, 0, 0.35);
	}

	/* --- desktop / tablet layout ---------------------------------------------
	   Mounted only when isDesktop is true (see the script block); no CSS
	   display toggle needed here. */
	.desktop-bar {
		padding: 0.5rem 0 0;
	}
	.top-desktop {
		display: grid;
		grid-template-columns: 1fr auto 1fr;
		align-items: center;
		gap: 1rem;
		padding-bottom: 0.5rem;
	}
	/* Element + class beats the plain .meta selector shared with the phone layout below,
	   regardless of source order, so the desktop spacing always wins here. */
	a.left-desktop {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		min-width: 0;
		color: inherit;
	}
	.cover-img {
		width: 64px;
		height: 64px;
		border-radius: 6px;
		object-fit: cover;
		display: block;
		flex-shrink: 0;
	}
	.info-desktop {
		min-width: 0;
	}
	.title-desktop {
		font-weight: 700;
		font-size: 1rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.sub-desktop {
		display: flex;
		align-items: center;
		gap: 0.9rem;
		margin-top: 0.2rem;
		font-size: 0.8rem;
		color: var(--fg-muted);
		white-space: nowrap;
		overflow: hidden;
	}
	.sub-desktop .with-icon {
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.transport-desktop {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		justify-self: center;
	}
	.transport-desktop button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0.45rem 0.6rem;
		color: var(--fg-muted);
	}
	.right-desktop {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		justify-self: end;
	}
	.tool {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.78rem;
		color: var(--fg-muted);
	}
	.tool.armed {
		border-color: var(--accent);
		color: var(--accent);
	}
	.tool.close {
		margin-left: 0.25rem;
	}
	.scrubinfo-desktop {
		display: grid;
		grid-template-columns: 1fr auto 1fr;
		align-items: baseline;
		gap: 0.75rem;
		padding: 0.3rem 0 0.5rem;
		font-size: 0.78rem;
		color: var(--fg-muted);
	}
	.scrubinfo-desktop .chapter-title {
		justify-self: center;
		text-align: center;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.scrubinfo-desktop .remaining {
		justify-self: end;
	}
	.rate-note {
		font-size: 0.72rem;
	}

	/* --- phone layout: unchanged compact bar ---------------------------------
	   Mounted only when isDesktop is false (see the script block). */
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
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0.45rem 0.6rem;
		color: var(--fg-muted);
	}
	.controls button.primary {
		color: var(--accent-fg);
	}
	.big {
		min-width: 48px;
		height: 44px;
		font-size: 1rem;
	}
	.round {
		border-radius: 50%;
		padding: 0;
		width: 56px;
		height: 56px;
		min-width: 56px;
	}
	.tools {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		padding-bottom: 0.4rem;
		overflow-x: auto;
		scrollbar-width: none;
	}
	.tools::-webkit-scrollbar {
		display: none;
	}
	.tools button {
		flex-shrink: 0;
		font-size: 0.78rem;
		padding: 0.3rem 0.55rem;
		white-space: nowrap;
	}
	.tools button {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}
	.tools button.armed {
		border-color: var(--accent);
		color: var(--accent);
	}
	.small {
		font-size: 0.8rem;
		padding-bottom: 0.3rem;
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.list li {
		border-bottom: 1px solid var(--border);
	}
	.list li:last-child {
		border-bottom: 0;
	}
	.entry {
		width: 100%;
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 1rem;
		background: none;
		border: 0;
		border-radius: 0;
		padding: 0.65rem 0.25rem;
		text-align: left;
		font-size: 0.9rem;
	}
	.entry.active {
		color: var(--accent);
		font-weight: 600;
	}
	.entry .label {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.hint {
		font-size: 0.75rem;
		margin: 0.6rem 0 0;
	}
	.switch-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.9rem;
		padding: 0.6rem 0.25rem;
		border-bottom: 1px solid var(--border);
	}
	.switch-row input[type='checkbox'] {
		width: 1.1rem;
		height: 1.1rem;
	}
	.small-tag {
		font-size: 0.7rem;
	}
</style>
