// Reports position to the server and reconciles with other devices.
//
// Rule (most recent listen wins): the server assigns listened_at from
// client_now - client_listened_at, so device clocks never matter. We keep the
// listened_at the server gave our last accepted write; any server record with a
// newer listened_at from a different device is a listen we did not see.
//
// Cadence: every 15 s while playing, immediately on pause/seek/rate/file/ended,
// a keepalive fetch when the page hides, a sendBeacon on pagehide, and a
// reconcile fetch when the page becomes visible.

import { api, ApiError } from '$lib/api/client';
import type { Progress, ProgressReport } from '$lib/api/types';
import { fmtTime } from '$lib/format';
import { journal } from './journal';
import type { PlayerEngine, PlayerEvent } from './machine.svelte';

const HEARTBEAT_MS = 15_000;
const ADOPT_THRESHOLD_MS = 2_000;

export class Reporter {
	private timer: ReturnType<typeof setInterval> | null = null;
	private inflight = false;
	/** A send requested while one was in flight; the newest position wins. */
	private pending: { finished?: boolean } | null = null;
	private lastSentAt = 0;
	private unsubscribe: (() => void) | null = null;

	constructor(
		private player: PlayerEngine,
		private csrfToken: () => string,
		private deviceId: () => string
	) {}

	start(): void {
		if (this.unsubscribe) return;
		this.unsubscribe = this.player.on((ev) => this.onEvent(ev));
		this.timer = setInterval(() => {
			if (this.player.status === 'playing' && Date.now() - this.lastSentAt >= HEARTBEAT_MS - 500) {
				void this.send();
			}
		}, 5_000);
	}

	stop(): void {
		this.unsubscribe?.();
		this.unsubscribe = null;
		if (this.timer) clearInterval(this.timer);
		this.timer = null;
	}

	private onEvent(ev: PlayerEvent): void {
		switch (ev.kind) {
			case 'play':
			case 'pause':
			case 'seek':
			case 'ratechange':
			case 'filechange':
			case 'stall':
				void this.send();
				break;
			case 'ended':
				void this.send(true);
				break;
			case 'finished':
				void this.send(ev.finished);
				break;
			case 'tick':
				if (Date.now() - this.lastSentAt >= HEARTBEAT_MS) void this.send();
				break;
			case 'hidden':
				this.sendOnHide();
				break;
			case 'visible':
				void this.reconcile();
				break;
			case 'load':
				break;
		}
	}

	/**
	 * The report body. `client_listened_at` is when this device last actually
	 * listened, not when the body was built: a hide/beacon report from a player
	 * that has been paused for an hour describes an hour-old listen, and the
	 * server's age correction then keeps it from beating a newer listen
	 * elsewhere. Deliberate actions refresh `lastListenedAt` first, so they still
	 * count as fresh.
	 */
	private report(finished?: boolean): ProgressReport {
		const now = Date.now();
		const listenedAt = Math.min(this.player.lastListenedAt || now, now);
		return {
			position_ms: Math.round(this.player.positionMs),
			file_index: this.player.fileIndex,
			client_listened_at: listenedAt,
			client_now: now,
			base_seq: this.player.serverSeq,
			...(finished !== undefined ? { finished } : {})
		};
	}

	/** Normal write. 409 means another device listened more recently. */
	async send(finished?: boolean): Promise<void> {
		const book = this.player.book;
		if (!book) return;
		if (this.inflight) {
			// Never drop a report: the position may have moved since the one in
			// flight was built. Coalesce into a single follow-up.
			this.pending = { finished: finished ?? this.pending?.finished };
			return;
		}
		this.inflight = true;
		this.lastSentAt = Date.now();
		try {
			const res = await api.put<Progress>(`/api/v1/progress/${book.id}`, this.report(finished));
			this.accept(res);
		} catch (e) {
			if (e instanceof ApiError && e.status === 409) {
				const server = e.body as Progress;
				this.maybeAdopt(server, 'conflict');
			} else if (e instanceof ApiError && (e.status === 501 || e.status === 404)) {
				// Endpoint not built yet (phase 0) or book vanished: journal only.
			} else {
				this.player.writeJournal(false);
			}
		} finally {
			this.inflight = false;
			if (this.pending) {
				const p = this.pending;
				this.pending = null;
				void this.send(p.finished);
			}
		}
	}

	private accept(res: Progress): void {
		this.player.serverSeq = res.seq;
		this.player.serverListenedAt = res.listened_at;
		this.player.writeJournal(true);
	}

	/** Page is hiding: a keepalive fetch survives the page being frozen. */
	private sendOnHide(): void {
		const book = this.player.book;
		if (!book) return;
		const body: ProgressReport = { ...this.report(), csrf_token: this.csrfToken() };
		let sent = false;
		if (typeof navigator.sendBeacon === 'function') {
			try {
				sent = navigator.sendBeacon(
					`/api/v1/progress/${book.id}/beacon`,
					new Blob([JSON.stringify(body)], { type: 'application/json' })
				);
			} catch {
				sent = false;
			}
		}
		if (!sent) {
			void api.put(`/api/v1/progress/${book.id}`, this.report(), { keepalive: true }).catch(() => {});
		}
	}

	/**
	 * Page became visible (or the app launched). Decide whether another device
	 * moved the position while we were away.
	 */
	async reconcile(): Promise<void> {
		const book = this.player.book;
		if (!book) return;
		let server: Progress;
		try {
			server = await api.get<Progress>(`/api/v1/progress/${book.id}`);
		} catch {
			return; // offline, 404, or not built yet
		}
		if (this.player.status === 'playing') {
			// We kept playing in the background: ours is the most recent listen.
			void this.send();
			return;
		}
		this.maybeAdopt(server, 'visible');
	}

	/**
	 * Another device reported progress (arrived over the event stream). If it is
	 * for the book we have loaded and we are not actively playing, move to it.
	 */
	onRemoteProgress(p: Progress): void {
		const book = this.player.book;
		if (!book || p.book_id !== book.id) return;
		if (p.device_id === this.deviceId()) {
			this.accept(p);
			return;
		}
		if (this.player.status === 'playing') return; // ours is newer; the next heartbeat asserts it
		this.maybeAdopt(p, 'remote');
	}

	private maybeAdopt(server: Progress, reason: 'conflict' | 'visible' | 'remote'): void {
		if (server.device_id === this.deviceId()) {
			this.accept(server);
			return;
		}
		const newer = server.listened_at > this.player.serverListenedAt + ADOPT_THRESHOLD_MS;
		if (!newer && reason !== 'conflict') return;
		this.adopt(server);
	}

	/**
	 * Move the loaded player onto a server record and say so. Positions within
	 * the tie window are the same listen, so those only take the seq.
	 */
	private adopt(server: Progress): void {
		const local = Math.round(this.player.positionMs);
		if (Math.abs(server.position_ms - local) < ADOPT_THRESHOLD_MS) {
			this.accept(server);
			return;
		}
		this.player.seekTo(server.position_ms);
		this.player.serverSeq = server.seq;
		this.player.serverListenedAt = server.listened_at;
		this.player.writeJournal(true);
		const from = server.device_name || 'another device';
		this.player.notice = {
			text: `Resumed from ${from} at ${fmtTime(server.position_ms)}`,
			undo: () => {
				this.player.notice = null;
				this.player.seekTo(local);
				void this.send();
			}
		};
	}

	/** Push journal entries the server never accepted (device was offline). */
	async flushUnsynced(userId: number): Promise<void> {
		for (const e of journal.unsynced(userId)) {
			try {
				const now = Date.now();
				const res = await api.put<Progress>(`/api/v1/progress/${e.bookId}`, {
					position_ms: e.positionMs,
					file_index: e.fileIndex,
					client_listened_at: e.clientTs,
					client_now: now,
					base_seq: e.serverSeq
				} satisfies ProgressReport);
				journal.write(userId, { ...e, synced: true, serverSeq: res.seq, serverListenedAt: res.listened_at });
			} catch (err) {
				if (!(err instanceof ApiError) || err.status !== 409) continue;
				// Someone listened after us: their record stands. Adopting it into
				// the journal is what stops the rejected position coming back as the
				// resume position; marking it synced only stops the retries.
				const server = err.body as Progress | null;
				if (!server || typeof server.position_ms !== 'number') {
					journal.write(userId, { ...e, synced: true });
					continue;
				}
				journal.write(userId, {
					...e,
					positionMs: server.position_ms,
					fileIndex: server.file_index,
					serverSeq: server.seq,
					serverListenedAt: server.listened_at,
					clientTs: Date.now(),
					synced: true
				});
				// Already on screen at the stale position: move it, with a toast.
				if (this.player.book?.id === e.bookId) this.adopt(server);
			}
		}
	}
}

/**
 * Mark a book finished (or not) when it is *not* the one loaded in the player,
 * so there is no engine state to move. Same endpoint and body shape the Reporter
 * uses: client timestamps are "now" (this is a deliberate action, not a replayed
 * listen) and base_seq is 0.
 *
 * `durationMs` is the book duration, used as the position when finishing.
 * `positionMs` overrides it — pass the position to keep when un-finishing.
 */
export async function reportFinished(
	bookId: number,
	finished: boolean,
	durationMs: number,
	positionMs?: number
): Promise<Progress> {
	const now = Date.now();
	const pos = positionMs ?? (finished ? durationMs : 0);
	const body: ProgressReport = {
		position_ms: Math.max(0, Math.round(pos)),
		file_index: 0,
		client_listened_at: now,
		client_now: now,
		base_seq: 0,
		finished
	};
	return api.put<Progress>(`/api/v1/progress/${bookId}`, body);
}
