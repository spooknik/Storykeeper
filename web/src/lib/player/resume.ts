import type { BookDetail } from '$lib/api/types';
import { journal } from './journal';

/** Where a restore starts and when that position was last actually listened to. */
export interface StartState {
	positionMs: number;
	/** Client-clock estimate of the listen the position describes; 0 when unknown. */
	listenedAt: number;
}

/**
 * Best known starting state for a book on this device: the newer of the
 * server record and the local journal. An unsynced journal entry always wins
 * because the server has not heard about that listen yet.
 *
 * `listenedAt` travels with the position so a silently restored player can
 * report it honestly: a relaunch is not a listen, and a report that claimed
 * "now" for an old position could overwrite a newer listen from elsewhere.
 */
export function startState(userId: number, b: BookDetail): StartState {
	const server = b.progress && !b.progress.finished ? b.progress : null;
	const local = userId ? journal.read(userId, b.id) : null;
	if (local && !local.synced) return { positionMs: local.positionMs, listenedAt: local.clientTs };
	if (server && local) {
		// serverListenedAt in the journal is the server clock at our last accepted
		// write; a newer server record means someone else listened since.
		if (server.listened_at > local.serverListenedAt + 2000) {
			return { positionMs: server.position_ms, listenedAt: server.listened_at };
		}
		return { positionMs: local.positionMs, listenedAt: local.serverListenedAt };
	}
	if (server) return { positionMs: server.position_ms, listenedAt: server.listened_at };
	if (local) return { positionMs: local.positionMs, listenedAt: local.serverListenedAt || local.clientTs };
	return { positionMs: 0, listenedAt: 0 };
}

export function startPosition(userId: number, b: BookDetail): number {
	return startState(userId, b).positionMs;
}
