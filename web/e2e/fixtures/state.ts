// Shared paths and the on-disk state file that links the Playwright config,
// the global setup and the global teardown.
//
// The config is loaded before globalSetup runs (and again inside every worker
// process), so the base URL cannot simply be "written by setup, read by
// config". Instead the config reserves a free port once, records it in the
// state file, and exports the URL through SK_E2E_BASE_URL so re-loads of the
// config (workers) reuse the very same value. globalSetup then starts the real
// server on that port and fills in the pid and temp dirs for the teardown.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

/** Absolute path of the `web` directory, regardless of where npx was run. */
export function webDir(): string {
	const cwd = process.cwd();
	if (fs.existsSync(path.join(cwd, 'e2e', 'fixtures', 'state.ts'))) return cwd;
	if (fs.existsSync(path.join(cwd, 'web', 'e2e', 'fixtures', 'state.ts'))) return path.join(cwd, 'web');
	return cwd;
}

/** Absolute path of the repository root (the parent of `web`). */
export function repoRoot(): string {
	return path.dirname(webDir());
}

export const STATE_DIR = path.join(webDir(), 'e2e', '.state');
export const STATE_FILE = path.join(STATE_DIR, 'server.json');
export const STORAGE_STATE = path.join(STATE_DIR, 'storage.json');

export interface ServerState {
	baseURL: string;
	/** pid of the storykeeper process; 0 until globalSetup has started it. */
	pid: number;
	/** Temp directory holding the generated library, data dir and binary. */
	tmpDir: string;
	/** Set when the fixture library could not be generated (no ffmpeg). */
	skipReason?: string;
}

export function readState(): ServerState | null {
	try {
		return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8')) as ServerState;
	} catch {
		return null;
	}
}

export function writeState(state: ServerState): void {
	fs.mkdirSync(STATE_DIR, { recursive: true });
	fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));
}

export function clearState(): void {
	try {
		fs.rmSync(STATE_FILE, { force: true });
	} catch {
		/* nothing to remove */
	}
}

/** Ask the OS for an unused loopback port, synchronously. */
function freePortSync(): number {
	const script =
		"const net=require('node:net');const s=net.createServer();" +
		"s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>process.stdout.write(String(p)))});";
	const out = execFileSync(process.execPath, ['-e', script], { encoding: 'utf8' }).trim();
	const port = Number(out);
	if (!Number.isInteger(port) || port <= 0) throw new Error(`could not reserve a port (got ${JSON.stringify(out)})`);
	return port;
}

/**
 * The base URL for this run. Stable across config re-loads: the first caller
 * reserves a port and records it, everybody else reuses it.
 */
export function resolveBaseURL(): string {
	if (process.env.SK_E2E_BASE_URL) return process.env.SK_E2E_BASE_URL;
	const existing = readState();
	if (existing?.baseURL && existing.pid > 0) {
		// A worker re-loading the config while the server is already running.
		process.env.SK_E2E_BASE_URL = existing.baseURL;
		return existing.baseURL;
	}
	const baseURL = `http://127.0.0.1:${freePortSync()}`;
	process.env.SK_E2E_BASE_URL = baseURL;
	writeState({ baseURL, pid: 0, tmpDir: '' });
	return baseURL;
}
