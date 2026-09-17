// Upload queue: wraps one tus.Upload per file, resumable across reloads via
// tus-js-client's fingerprint-based URL storage, with a small in-app
// scheduler that caps concurrent uploads. See docs/DESIGN.md "Uploads" and
// internal/upload/tus.go for the server side this talks to.

import * as tus from 'tus-js-client';
import { CSRF_HEADER } from '$lib/api/client';

export type UploadState = 'queued' | 'uploading' | 'paused' | 'done' | 'error';

export interface UploadCommonMeta {
	library_id: string;
	author: string;
	title: string;
}

const MAX_CONCURRENT = 2;

const MIME_BY_EXT: Record<string, string> = {
	'.m4b': 'audio/mp4',
	'.m4a': 'audio/mp4',
	'.mp3': 'audio/mpeg',
	'.ogg': 'audio/ogg',
	'.opus': 'audio/opus',
	'.flac': 'audio/flac',
	'.aac': 'audio/aac',
	'.wav': 'audio/wav'
};

function guessFileType(file: File): string {
	if (file.type) return file.type;
	const dot = file.name.lastIndexOf('.');
	const ext = dot >= 0 ? file.name.slice(dot).toLowerCase() : '';
	return MIME_BY_EXT[ext] ?? 'application/octet-stream';
}

/** "Author - Title.mp3" -> {author, title}; otherwise title = filename without extension. */
export function guessAuthorTitle(filename: string): { author: string; title: string } {
	const m = filename.match(/^(.+?) - (.+)\.\w+$/);
	if (m) return { author: m[1].trim(), title: m[2].trim() };
	const dot = filename.lastIndexOf('.');
	return { author: '', title: (dot > 0 ? filename.slice(0, dot) : filename).trim() };
}

export function fmtBytes(n: number): string {
	if (!Number.isFinite(n) || n <= 0) return '0 B';
	const units = ['B', 'KB', 'MB', 'GB', 'TB'];
	const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
	const v = n / 1024 ** i;
	return `${i === 0 ? v.toFixed(0) : v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`;
}

export function fmtSpeed(bytesPerSec: number): string {
	if (!Number.isFinite(bytesPerSec) || bytesPerSec <= 0) return '';
	return `${fmtBytes(bytesPerSec)}/s`;
}

export class QueueItem {
	readonly id = crypto.randomUUID();
	readonly file: File;
	readonly upload: tus.Upload;

	state = $state<UploadState>('queued');
	bytesSent = $state(0);
	bytesTotal = $state(0);
	speedBps = $state(0);
	errorMessage = $state('');

	private readonly onSettle: () => void;
	private started = false;
	private sampleTime = 0;
	private sampleBytes = 0;

	constructor(file: File, meta: UploadCommonMeta, onSettle: () => void) {
		this.file = file;
		this.bytesTotal = file.size;
		this.onSettle = onSettle;

		this.upload = new tus.Upload(file, {
			endpoint: '/api/v1/upload/',
			chunkSize: 50 * 1024 * 1024,
			retryDelays: [0, 1000, 3000, 5000, 10000, 30000],
			headers: { [CSRF_HEADER]: '1' },
			metadata: {
				filename: file.name,
				filetype: guessFileType(file),
				library_id: meta.library_id,
				author: meta.author,
				title: meta.title
			},
			storeFingerprintForResuming: true,
			removeFingerprintOnSuccess: true,
			onProgress: (bytesSent, bytesTotal) => {
				this.bytesSent = bytesSent;
				this.bytesTotal = bytesTotal;
				const now = performance.now();
				if (this.sampleTime === 0) {
					this.sampleTime = now;
					this.sampleBytes = bytesSent;
					return;
				}
				const dt = (now - this.sampleTime) / 1000;
				if (dt >= 0.5) {
					this.speedBps = (bytesSent - this.sampleBytes) / dt;
					this.sampleTime = now;
					this.sampleBytes = bytesSent;
				}
			},
			onSuccess: () => {
				this.state = 'done';
				this.bytesSent = this.bytesTotal;
				this.speedBps = 0;
				this.onSettle();
			},
			onError: (err) => {
				this.state = 'error';
				this.errorMessage = err instanceof Error ? err.message : String(err);
				this.speedBps = 0;
				this.onSettle();
			}
		});
	}

	/**
	 * Called by the queue scheduler when a slot is free. The reload-resume
	 * dance (findPreviousUploads/resumeFromPreviousUpload) only makes sense
	 * before this item's very first start; a paused or retried item reuses
	 * the same Upload instance, which already knows its upload URL.
	 */
	async run(): Promise<void> {
		this.state = 'uploading';
		this.errorMessage = '';
		this.sampleTime = 0;
		if (!this.started) {
			this.started = true;
			try {
				const previous = await this.upload.findPreviousUploads();
				if (previous.length > 0) this.upload.resumeFromPreviousUpload(previous[0]);
			} catch {
				// Best effort: fall through to a fresh upload if lookup fails.
			}
		}
		this.upload.start();
	}

	/** Aborts the in-flight request but keeps the fingerprint, so Resume continues where it left off. */
	pause(): void {
		if (this.state !== 'uploading') return;
		this.state = 'paused';
		this.speedBps = 0;
		void this.upload.abort(false);
		this.onSettle();
	}

	remove(): void {
		void this.upload.abort(false);
	}
}

export class UploadQueue {
	items = $state<QueueItem[]>([]);

	doneCount = $derived(this.items.filter((i) => i.state === 'done').length);
	totalBytes = $derived(this.items.reduce((sum, i) => sum + i.bytesTotal, 0));
	sentBytes = $derived(
		this.items.reduce((sum, i) => sum + (i.state === 'done' ? i.bytesTotal : i.bytesSent), 0)
	);
	hasActive = $derived(this.items.some((i) => i.state === 'queued' || i.state === 'uploading'));
	allDone = $derived(this.items.length > 0 && this.items.every((i) => i.state === 'done'));

	add(files: File[], meta: UploadCommonMeta): void {
		for (const file of files) {
			this.items.push(new QueueItem(file, meta, () => this.pump()));
		}
		this.pump();
	}

	pause(item: QueueItem): void {
		item.pause();
	}

	resume(item: QueueItem): void {
		if (item.state !== 'paused') return;
		item.state = 'queued';
		this.pump();
	}

	retry(item: QueueItem): void {
		if (item.state !== 'error') return;
		item.state = 'queued';
		this.pump();
	}

	removeItem(item: QueueItem): void {
		item.remove();
		this.items = this.items.filter((i) => i !== item);
		this.pump();
	}

	private pump(): void {
		let slots = MAX_CONCURRENT - this.items.filter((i) => i.state === 'uploading').length;
		if (slots <= 0) return;
		for (const item of this.items) {
			if (slots <= 0) break;
			if (item.state === 'queued') {
				slots--;
				void item.run();
			}
		}
	}
}
