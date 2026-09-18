// Server-backed playback preferences: GET/PUT /api/v1/me/prefs. Mirrors
// internal/api/prefs.go's Prefs/PrefsPatch (see .frugal-fable/v1-1-polish/s1-server/NOTES.md).
// This store owns only the *global* defaults; a per-book rate override lives on
// player.bookRateOverride and is written through PUT /api/v1/progress/{id}/rate
// directly by the UI (Player.svelte), never through here.
//
// The engine does no network calls (see s2-engine/NOTES.md), so this store is
// the thing that keeps player.defaultRate / skipBackMs / skipForwardMs /
// autoRewind in sync with the server, in both directions: load() pulls the
// server's values down onto the engine, save() pushes a local change up and
// applies it to the engine optimistically.

import { api } from '$lib/api/client';
import { player } from './machine.svelte';

/** The GET/PUT /api/v1/me/prefs JSON shape, server-side field names. */
interface ServerPrefs {
	playback_rate: number;
	skip_back_seconds: number;
	skip_forward_seconds: number;
	auto_rewind: boolean;
	default_sleep_minutes: number;
}

/** What save() accepts: any subset, camelCase, same fields as the store's own state. */
export interface PrefsPatch {
	playbackRate?: number;
	skipBackSeconds?: number;
	skipForwardSeconds?: number;
	autoRewind?: boolean;
	defaultSleepMinutes?: number;
}

type Snapshot = Required<PrefsPatch>;

const DEFAULTS: Snapshot = {
	playbackRate: 1,
	skipBackSeconds: 30,
	skipForwardSeconds: 30,
	autoRewind: true,
	defaultSleepMinutes: 0
};

class PrefsStore {
	playbackRate = $state(DEFAULTS.playbackRate);
	skipBackSeconds = $state(DEFAULTS.skipBackSeconds);
	skipForwardSeconds = $state(DEFAULTS.skipForwardSeconds);
	autoRewind = $state(DEFAULTS.autoRewind);
	/** For the sleep-timer UI to preselect; the sleep timer itself stays client-only. */
	defaultSleepMinutes = $state(DEFAULTS.defaultSleepMinutes);
	/** True once a GET has succeeded at least once this session. */
	loaded = $state(false);

	// Coalesce concurrent callers (settings page onMount + layout, potentially
	// both calling load() before either resolves) into a single request.
	private inFlight: Promise<void> | null = null;

	/**
	 * Fetch the server's prefs and apply them to both this store and the
	 * engine. On failure, leaves everything at its current value (which, for a
	 * fresh store, is player.restoreRate()'s localStorage defaults) and leaves
	 * `loaded` false so a caller can tell the fetch never actually succeeded.
	 *
	 * Call player.restoreRate() before this, not after: the one-time migration
	 * below reads player.defaultRate to see what localStorage had.
	 */
	async load(): Promise<void> {
		if (this.inFlight) return this.inFlight;
		this.inFlight = this.doLoad();
		try {
			await this.inFlight;
		} finally {
			this.inFlight = null;
		}
	}

	private async doLoad(): Promise<void> {
		// Snapshot before we touch anything: this is the pre-server-prefs
		// localStorage default (set by player.restoreRate() at startup), used
		// only for the one-time migration below.
		const localDefaultRate = player.defaultRate;
		let r: ServerPrefs;
		try {
			r = await api.get<ServerPrefs>('/api/v1/me/prefs');
		} catch {
			// Keep whatever localStorage-derived defaults are already applied;
			// loaded stays false.
			return;
		}
		this.playbackRate = r.playback_rate;
		this.skipBackSeconds = r.skip_back_seconds;
		this.skipForwardSeconds = r.skip_forward_seconds;
		this.autoRewind = r.auto_rewind;
		this.defaultSleepMinutes = r.default_sleep_minutes;
		this.applyToEngine();
		this.loaded = true;

		// One-time migration: this account has never saved a rate server-side
		// (still the untouched default 1.0) but this device's localStorage
		// remembers a different speed from before server-side prefs existed.
		// Push the local value up once so it becomes the account's rate instead
		// of silently reverting to 1.0 the next time prefs load (e.g. on
		// another device, or here after localStorage is cleared). Only fires
		// when the server is still exactly at the default, so it never
		// clobbers a rate the user (or another device) already set on purpose.
		if (r.playback_rate === 1.0 && localDefaultRate !== 1.0) {
			try {
				await this.save({ playbackRate: localDefaultRate });
			} catch {
				// Best-effort; the local rate is still applied to the engine
				// either way, just not persisted yet. A later save() will retry.
			}
		}
	}

	/**
	 * Apply `patch` optimistically (local state + engine), PUT it, and
	 * reconcile with the server's authoritative response. On any failure,
	 * reverts both the local state and the engine to their pre-call values and
	 * rethrows, so the caller (a settings control) can show an error and the UI
	 * doesn't show a value that was never actually saved.
	 */
	async save(patch: PrefsPatch): Promise<void> {
		const prev = this.snapshot();
		this.applyLocal(patch);
		this.applyToEngine();
		const body: Partial<ServerPrefs> = {};
		if (patch.playbackRate !== undefined) body.playback_rate = patch.playbackRate;
		if (patch.skipBackSeconds !== undefined) body.skip_back_seconds = patch.skipBackSeconds;
		if (patch.skipForwardSeconds !== undefined) body.skip_forward_seconds = patch.skipForwardSeconds;
		if (patch.autoRewind !== undefined) body.auto_rewind = patch.autoRewind;
		if (patch.defaultSleepMinutes !== undefined) body.default_sleep_minutes = patch.defaultSleepMinutes;
		try {
			const r = await api.put<ServerPrefs>('/api/v1/me/prefs', body);
			this.playbackRate = r.playback_rate;
			this.skipBackSeconds = r.skip_back_seconds;
			this.skipForwardSeconds = r.skip_forward_seconds;
			this.autoRewind = r.auto_rewind;
			this.defaultSleepMinutes = r.default_sleep_minutes;
			this.applyToEngine();
		} catch (e) {
			this.restore(prev);
			this.applyToEngine();
			throw e;
		}
	}

	/** Back to built-in defaults; does not touch the engine. Call on sign-out. */
	reset(): void {
		this.restore(DEFAULTS);
		this.loaded = false;
	}

	// --- internals ---

	private snapshot(): Snapshot {
		return {
			playbackRate: this.playbackRate,
			skipBackSeconds: this.skipBackSeconds,
			skipForwardSeconds: this.skipForwardSeconds,
			autoRewind: this.autoRewind,
			defaultSleepMinutes: this.defaultSleepMinutes
		};
	}

	private restore(s: Snapshot): void {
		this.playbackRate = s.playbackRate;
		this.skipBackSeconds = s.skipBackSeconds;
		this.skipForwardSeconds = s.skipForwardSeconds;
		this.autoRewind = s.autoRewind;
		this.defaultSleepMinutes = s.defaultSleepMinutes;
	}

	private applyLocal(patch: PrefsPatch): void {
		if (patch.playbackRate !== undefined) this.playbackRate = patch.playbackRate;
		if (patch.skipBackSeconds !== undefined) this.skipBackSeconds = patch.skipBackSeconds;
		if (patch.skipForwardSeconds !== undefined) this.skipForwardSeconds = patch.skipForwardSeconds;
		if (patch.autoRewind !== undefined) this.autoRewind = patch.autoRewind;
		if (patch.defaultSleepMinutes !== undefined) this.defaultSleepMinutes = patch.defaultSleepMinutes;
	}

	/** Push the store's current values onto the engine. Never touches bookRateOverride. */
	private applyToEngine(): void {
		player.setRate(this.playbackRate);
		player.setSkipAmounts(this.skipBackSeconds * 1000, this.skipForwardSeconds * 1000);
		player.autoRewind = this.autoRewind;
	}
}

export const prefs = new PrefsStore();
