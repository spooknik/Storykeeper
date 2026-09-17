// Server-sent events client. iOS silently kills the connection when the PWA
// is backgrounded and EventSource may never notice, so we always tear down and
// reconnect on visibilitychange→visible, and treat 45 s without a ping as dead.

import type { Progress } from './api/types';

const PING_TIMEOUT_MS = 45_000;

export interface LibraryEvent {
	library_id: number;
	action: string;
}

class EventClient {
	connected = $state(false);
	/** Latest progress per book seen over the stream, keyed by book id. */
	progress = $state<Record<number, Progress>>({});
	/** Bumped whenever the server says the library changed; pages refetch on it. */
	libraryVersion = $state(0);
	/** Bumped when the server asks us to refetch everything. */
	resyncVersion = $state(0);

	private es: EventSource | null = null;
	private lastEventId = '';
	private lastPingAt = 0;
	private watchdog: ReturnType<typeof setInterval> | null = null;
	private listeners = new Set<(p: Progress) => void>();
	private installed = false;
	private active = false;

	onProgress(cb: (p: Progress) => void): () => void {
		this.listeners.add(cb);
		return () => this.listeners.delete(cb);
	}

	start(): void {
		this.active = true;
		if (!this.installed && typeof document !== 'undefined') {
			this.installed = true;
			document.addEventListener('visibilitychange', () => {
				if (document.visibilityState === 'visible' && this.active) this.reconnect();
			});
			window.addEventListener('online', () => {
				if (this.active) this.reconnect();
			});
		}
		this.connect();
		if (!this.watchdog) {
			this.watchdog = setInterval(() => {
				if (!this.active || !this.es) return;
				if (Date.now() - this.lastPingAt > PING_TIMEOUT_MS) this.reconnect();
			}, 10_000);
		}
	}

	stop(): void {
		this.active = false;
		this.es?.close();
		this.es = null;
		this.connected = false;
		if (this.watchdog) clearInterval(this.watchdog);
		this.watchdog = null;
	}

	private reconnect(): void {
		this.es?.close();
		this.es = null;
		this.connected = false;
		this.connect();
	}

	private connect(): void {
		if (this.es || typeof EventSource === 'undefined') return;
		// EventSource sends Last-Event-ID itself on its own automatic
		// reconnects, but not on a fresh object, so pass it explicitly too.
		const url = this.lastEventId
			? `/api/v1/events?lastEventId=${encodeURIComponent(this.lastEventId)}`
			: '/api/v1/events';
		const es = new EventSource(url, { withCredentials: true });
		this.es = es;
		this.lastPingAt = Date.now();

		es.onopen = () => {
			this.connected = true;
			this.lastPingAt = Date.now();
		};
		es.onerror = () => {
			this.connected = false;
			// Let EventSource retry on its own; the watchdog handles silent death.
		};
		es.addEventListener('progress', (ev) => {
			this.touch(ev as MessageEvent);
			try {
				const p = JSON.parse((ev as MessageEvent).data) as Progress;
				this.progress = { ...this.progress, [p.book_id]: p };
				for (const cb of this.listeners) cb(p);
			} catch {
				/* malformed, ignore */
			}
		});
		es.addEventListener('library', (ev) => {
			this.touch(ev as MessageEvent);
			this.libraryVersion += 1;
		});
		es.addEventListener('resync', (ev) => {
			this.touch(ev as MessageEvent);
			this.progress = {};
			this.resyncVersion += 1;
		});
		// Pings are comments and never surface as events, but any message
		// proves the connection is alive; comments do at least keep the
		// readyState open, so we also refresh lastPingAt on open/each event.
	}

	private touch(ev: MessageEvent): void {
		this.lastPingAt = Date.now();
		if (ev.lastEventId) this.lastEventId = ev.lastEventId;
	}

	/** Called by the reporter when its own write is accepted, so the watchdog knows we are alive. */
	noteAlive(): void {
		this.lastPingAt = Date.now();
	}
}

export const events = new EventClient();
