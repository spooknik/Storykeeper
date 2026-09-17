// Playwright global teardown: stop the server started by fixtures/server.ts and
// remove its temp directories.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import { clearState, readState } from './state';

function killTree(pid: number): void {
	if (!pid) return;
	if (process.platform === 'win32') {
		// The Go binary is spawned through a shell-less spawn, but Windows still
		// needs the whole tree gone or the port stays bound.
		spawnSync('taskkill', ['/PID', String(pid), '/T', '/F'], { stdio: 'ignore' });
		return;
	}
	try {
		process.kill(pid, 'SIGTERM');
	} catch {
		/* already gone */
	}
}

export default async function globalTeardown(): Promise<void> {
	const state = readState();
	if (!state) return;
	killTree(state.pid);
	// Give Windows a moment to release the sqlite file handles before rmSync.
	await new Promise((r) => setTimeout(r, 500));
	if (state.tmpDir) {
		try {
			fs.rmSync(state.tmpDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
		} catch (e) {
			console.warn(`[e2e] could not remove ${state.tmpDir}: ${String(e)}`);
		}
	}
	clearState();
}
