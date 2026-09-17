import type { BookDetail } from '$lib/api/types';
import { journal } from './journal';

/**
 * Best known starting position for a book on this device: the newer of the
 * server record and the local journal. An unsynced journal entry always wins
 * because the server has not heard about that listen yet.
 */
export function startPosition(userId: number, b: BookDetail): number {
	const server = b.progress && !b.progress.finished ? b.progress : null;
	const local = userId ? journal.read(userId, b.id) : null;
	if (local && !local.synced) return local.positionMs;
	if (server && local) {
		// serverListenedAt in the journal is the server clock at our last accepted
		// write; a newer server record means someone else listened since.
		return server.listened_at > local.serverListenedAt + 2000 ? server.position_ms : local.positionMs;
	}
	return server?.position_ms ?? local?.positionMs ?? 0;
}
