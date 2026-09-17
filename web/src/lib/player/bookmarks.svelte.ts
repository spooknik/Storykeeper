// Bookmark API access shared by the player and the book page. `version` is
// bumped after every write so an open book page can refetch a list that the
// player just changed; BookDetail.bookmarks stays the display source.

import { api } from '$lib/api/client';
import type { Bookmark } from '$lib/api/types';

class BookmarkStore {
	/** Increments on every successful create/delete. */
	version = $state(0);

	list(bookId: number): Promise<Bookmark[]> {
		return api.get<Bookmark[]>(`/api/v1/books/${bookId}/bookmarks`);
	}

	async create(bookId: number, positionMs: number, note: string): Promise<Bookmark> {
		const bm = await api.post<Bookmark>(`/api/v1/books/${bookId}/bookmarks`, {
			position_ms: Math.max(0, Math.round(positionMs)),
			note
		});
		this.version += 1;
		return bm;
	}

	async remove(id: number): Promise<void> {
		await api.del(`/api/v1/bookmarks/${id}`);
		this.version += 1;
	}
}

export const bookmarks = new BookmarkStore();
