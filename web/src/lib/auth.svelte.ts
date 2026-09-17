import { api, ApiError } from './api/client';
import type { Session, SessionResponse, User } from './api/types';

function guessDeviceName(): string {
	if (typeof navigator === 'undefined') return 'unknown';
	const ua = navigator.userAgent;
	let device = 'Browser';
	if (/iPhone/.test(ua)) device = 'iPhone';
	else if (/iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) device = 'iPad';
	else if (/Android/.test(ua)) device = 'Android';
	else if (/Macintosh/.test(ua)) device = 'Mac';
	else if (/Windows/.test(ua)) device = 'Windows PC';
	else if (/Linux/.test(ua)) device = 'Linux';
	const standalone =
		(typeof window !== 'undefined' && window.matchMedia?.('(display-mode: standalone)').matches) ||
		(navigator as unknown as { standalone?: boolean }).standalone === true;
	return standalone ? `${device} app` : device;
}

class AuthStore {
	user = $state<User | null>(null);
	session = $state<Session | null>(null);
	loaded = $state(false);

	async load(): Promise<void> {
		try {
			const r = await api.get<SessionResponse>('/api/v1/auth/me');
			this.user = r.user;
			this.session = r.session;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				this.user = null;
				this.session = null;
			} else {
				throw e;
			}
		} finally {
			this.loaded = true;
		}
	}

	async login(username: string, password: string): Promise<void> {
		const r = await api.post<SessionResponse>('/api/v1/auth/login', {
			username,
			password,
			device_name: guessDeviceName()
		});
		this.user = r.user;
		this.session = r.session;
		this.loaded = true;
	}

	async logout(): Promise<void> {
		try {
			await api.post('/api/v1/auth/logout');
		} finally {
			this.user = null;
			this.session = null;
		}
	}

	/** Called by the API layer when any request comes back 401 after we thought we were logged in. */
	invalidate(): void {
		this.user = null;
		this.session = null;
	}
}

export const auth = new AuthStore();
