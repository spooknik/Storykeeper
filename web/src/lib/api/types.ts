// Mirrors internal/api/types.go and docs/API.md. Keep all three in sync.

export type Role = 'admin' | 'user';

export interface User {
	id: number;
	username: string;
	role: Role;
	created_at: number;
}

export interface Session {
	id: number;
	csrf_token: string;
	device_id: string;
	device_name: string;
	created_at: number;
	last_seen_at: number;
	expires_at: number;
}

export interface SessionResponse {
	user: User;
	session: Session;
}

export interface Library {
	id: number;
	name: string;
	path?: string;
	book_count: number;
	restricted: boolean;
	created_at: number;
}

export interface Progress {
	book_id: number;
	position_ms: number;
	duration_ms: number;
	file_index: number;
	seq: number;
	listened_at: number;
	device_id: string;
	device_name: string;
	finished: boolean;
}

export interface ProgressReport {
	position_ms: number;
	file_index: number;
	client_listened_at: number;
	client_now: number;
	base_seq: number;
	finished?: boolean;
	csrf_token?: string;
}

export interface BookSummary {
	id: number;
	library_id: number;
	title: string;
	subtitle: string;
	authors: string[];
	narrators: string[];
	series: string;
	series_seq: string;
	duration_ms: number;
	cover_url: string;
	added_at: number;
	updated_at: number;
	progress?: Progress;
}

export interface BookFile {
	index: number;
	rel_path: string;
	size: number;
	duration_ms: number;
	codec: string;
	bitrate: number;
	url: string;
}

export interface Chapter {
	index: number;
	title: string;
	start_ms: number;
	end_ms: number;
}

export interface Bookmark {
	id: number;
	book_id: number;
	position_ms: number;
	note: string;
	created_at: number;
}

export interface BookDetail extends BookSummary {
	description: string;
	published_year: number | null;
	language: string;
	asin: string;
	isbn: string;
	files: BookFile[];
	chapters: Chapter[];
	bookmarks: Bookmark[];
}

export interface BookList {
	items: BookSummary[];
	total: number;
}

export interface ErrorResponse {
	error: { code: string; message: string };
}
