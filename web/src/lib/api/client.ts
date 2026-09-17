import type { ErrorResponse } from './types';

export const CSRF_HEADER = 'X-Storykeeper';

export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string,
		public body?: unknown
	) {
		super(message);
		this.name = 'ApiError';
	}
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

interface Options {
	keepalive?: boolean;
	signal?: AbortSignal;
}

async function request<T>(method: Method, path: string, body?: unknown, opts: Options = {}): Promise<T> {
	const headers: Record<string, string> = { [CSRF_HEADER]: '1' };
	if (body !== undefined) headers['Content-Type'] = 'application/json';
	const res = await fetch(path, {
		method,
		headers,
		credentials: 'same-origin',
		body: body === undefined ? undefined : JSON.stringify(body),
		keepalive: opts.keepalive,
		signal: opts.signal
	});
	if (res.status === 204) return undefined as T;
	const text = await res.text();
	let data: unknown = null;
	if (text) {
		try {
			data = JSON.parse(text);
		} catch {
			data = null;
		}
	}
	if (!res.ok) {
		const err = (data as ErrorResponse | null)?.error;
		throw new ApiError(res.status, err?.code ?? 'http_error', err?.message ?? res.statusText, data);
	}
	return data as T;
}

export const api = {
	get: <T>(path: string, opts?: Options) => request<T>('GET', path, undefined, opts),
	post: <T>(path: string, body?: unknown, opts?: Options) => request<T>('POST', path, body, opts),
	put: <T>(path: string, body?: unknown, opts?: Options) => request<T>('PUT', path, body, opts),
	patch: <T>(path: string, body?: unknown, opts?: Options) => request<T>('PATCH', path, body, opts),
	del: <T>(path: string, opts?: Options) => request<T>('DELETE', path, undefined, opts)
};
