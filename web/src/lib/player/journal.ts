// Synchronous localStorage journal of the player's position. Written every few
// seconds while playing and on every state change so that when iOS kills the
// page (or the audio session dies on the lock screen) we can rebuild exactly
// where the listener was. Never rely on in-memory state for position.

export interface JournalEntry {
	bookId: number;
	fileIndex: number;
	positionMs: number;
	rate: number;
	playing: boolean;
	clientTs: number; // Date.now() when written
	serverSeq: number; // last seq the server acknowledged for this book
	serverListenedAt: number; // listened_at the server assigned to our last accepted write
	synced: boolean; // false while this entry has not been accepted by the server
}

const PREFIX = 'sk:journal:';
const LAST = 'sk:last:';

function key(userId: number, bookId: number): string {
	return `${PREFIX}${userId}:${bookId}`;
}

function safeGet(k: string): string | null {
	try {
		return localStorage.getItem(k);
	} catch {
		return null;
	}
}

function safeSet(k: string, v: string): void {
	try {
		localStorage.setItem(k, v);
	} catch {
		/* private mode / quota: journal is best effort */
	}
}

export const journal = {
	read(userId: number, bookId: number): JournalEntry | null {
		const raw = safeGet(key(userId, bookId));
		if (!raw) return null;
		try {
			const e = JSON.parse(raw) as JournalEntry;
			return typeof e.positionMs === 'number' ? e : null;
		} catch {
			return null;
		}
	},

	write(userId: number, entry: JournalEntry): void {
		safeSet(key(userId, entry.bookId), JSON.stringify(entry));
		safeSet(`${LAST}${userId}`, String(entry.bookId));
	},

	/** The book the user was last playing on this device, for restoring the mini player. */
	lastBook(userId: number): number | null {
		const v = safeGet(`${LAST}${userId}`);
		const n = v ? Number(v) : NaN;
		return Number.isFinite(n) ? n : null;
	},

	/** Entries not yet accepted by the server (device was offline). */
	unsynced(userId: number): JournalEntry[] {
		const out: JournalEntry[] = [];
		try {
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				if (!k || !k.startsWith(`${PREFIX}${userId}:`)) continue;
				const raw = localStorage.getItem(k);
				if (!raw) continue;
				const e = JSON.parse(raw) as JournalEntry;
				if (e && e.synced === false) out.push(e);
			}
		} catch {
			/* ignore */
		}
		return out;
	}
};
