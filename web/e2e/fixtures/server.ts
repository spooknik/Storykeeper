// Playwright global setup: everything the smoke suite needs to talk to a real
// Storykeeper.
//
//  1. build the SvelteKit app when web/build/index.html is missing,
//  2. generate a tiny audiobook library with ffmpeg in a temp dir,
//  3. `go build` the server binary into that temp dir,
//  4. start it on the reserved port with a temp data dir and a known admin,
//  5. wait for /api/v1/health and for the startup scan to find both books,
//  6. record pid + temp dirs in e2e/.state/server.json for the teardown.
//
// Nothing here touches the developer's own ./data or ./library directories.

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { repoRoot, resolveBaseURL, webDir, writeState, type ServerState } from './state';

export const ADMIN_USER = 'admin';
export const ADMIN_PASSWORD = 'e2epass123';

/** Titles the fixture library is expected to produce, used by the specs. */
export const BOOKS = {
	/** Single-file m4b with two named chapters. */
	chaptered: { title: 'The Clockwork Gate', author: 'Ada Vance' },
	/** Two-file mp3 book. Chromium can always decode mp3, so it is the one the
	 *  player spec actually plays. */
	multiFile: { title: 'Echoes of Tomorrow', author: 'Bram Sol' }
} as const;

const isWindows = process.platform === 'win32';

/**
 * Run a command to completion. `shell` is only needed for npm, which is a .cmd
 * shim on Windows; using it for ffmpeg would mangle arguments containing spaces.
 */
function run(cmd: string, args: string[], cwd: string, shell = false): void {
	const res = spawnSync(cmd, args, { cwd, encoding: 'utf8', shell });
	if (res.status !== 0) {
		throw new Error(
			`${cmd} ${args.join(' ')} failed (${res.status})\n${res.stdout ?? ''}\n${res.stderr ?? ''}`
		);
	}
}

function haveFfmpeg(): boolean {
	const probe = spawnSync('ffmpeg', ['-version'], { encoding: 'utf8' });
	return probe.status === 0;
}

// --- fixture library ---------------------------------------------------------

/**
 * One single-file m4b with real chapter markers, and one two-file mp3 book in
 * its own folder. Chapter titles deliberately avoid bare numbers, which the UI
 * treats as synthesised "parts" rather than chapters.
 */
function generateLibrary(libDir: string): void {
	fs.mkdirSync(libDir, { recursive: true });

	// Book 1: root-level single file -> a single-file book keyed by its filename.
	const meta = path.join(libDir, 'chapters.ffmeta');
	fs.writeFileSync(
		meta,
		[
			';FFMETADATA1',
			`album=${BOOKS.chaptered.title}`,
			`artist=${BOOKS.chaptered.author}`,
			`album_artist=${BOOKS.chaptered.author}`,
			'composer=Nora Reed',
			'',
			'[CHAPTER]',
			'TIMEBASE=1/1000',
			'START=0',
			'END=4000',
			'title=The Gate Opens',
			'',
			'[CHAPTER]',
			'TIMEBASE=1/1000',
			'START=4000',
			'END=8000',
			'title=The Gate Closes',
			''
		].join('\n')
	);
	run(
		'ffmpeg',
		[
			'-y', '-v', 'error',
			'-f', 'lavfi', '-i', 'sine=frequency=440:duration=8',
			'-i', 'chapters.ffmeta',
			'-map_metadata', '1', '-map_chapters', '1',
			'-c:a', 'aac', '-b:a', '48k',
			'The Clockwork Gate.m4b'
		],
		libDir
	);
	fs.rmSync(meta, { force: true });

	// Book 2: a folder with two mp3 parts.
	const folder = path.join(libDir, 'Echoes of Tomorrow');
	fs.mkdirSync(folder, { recursive: true });
	for (const [index, name] of ['01 - Part One.mp3', '02 - Part Two.mp3'].entries()) {
		run(
			'ffmpeg',
			[
				'-y', '-v', 'error',
				'-f', 'lavfi', '-i', `sine=frequency=${330 + index * 110}:duration=6`,
				'-c:a', 'libmp3lame', '-b:a', '64k',
				'-metadata', `album=${BOOKS.multiFile.title}`,
				'-metadata', `artist=${BOOKS.multiFile.author}`,
				'-metadata', `album_artist=${BOOKS.multiFile.author}`,
				'-metadata', `title=${name.slice(5, -4)}`,
				'-metadata', `track=${index + 1}`,
				name
			],
			folder
		);
	}
}

// --- server ------------------------------------------------------------------

async function waitFor(what: string, check: () => Promise<boolean>, timeoutMs = 90_000): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	let lastErr: unknown = null;
	while (Date.now() < deadline) {
		try {
			if (await check()) return;
		} catch (e) {
			lastErr = e;
		}
		await new Promise((r) => setTimeout(r, 250));
	}
	throw new Error(`timed out waiting for ${what}${lastErr ? `: ${String(lastErr)}` : ''}`);
}

/** Log in over the API and return a Cookie header value. */
async function apiLogin(baseURL: string): Promise<string> {
	const res = await fetch(`${baseURL}/api/v1/auth/login`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', 'X-Storykeeper': '1' },
		body: JSON.stringify({
			username: ADMIN_USER,
			password: ADMIN_PASSWORD,
			device_name: 'e2e-setup'
		})
	});
	if (!res.ok) throw new Error(`setup login failed: ${res.status} ${await res.text()}`);
	return res.headers
		.getSetCookie()
		.map((c) => c.split(';')[0])
		.join('; ');
}

export default async function globalSetup(): Promise<void> {
	const web = webDir();
	const root = repoRoot();
	const baseURL = resolveBaseURL();
	const port = new URL(baseURL).port;

	// 1. web build (embedded by the Go binary through web/embed.go)
	if (!fs.existsSync(path.join(web, 'build', 'index.html'))) {
		console.log('[e2e] web/build missing, running npm run build');
		run('npm', ['run', 'build'], web, true);
	}
	// The bundler wipes web/build, and the repo keeps a .gitkeep there.
	fs.writeFileSync(path.join(web, 'build', '.gitkeep'), '');

	const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'storykeeper-e2e-'));
	const libDir = path.join(tmpDir, 'library');
	const dataDir = path.join(tmpDir, 'data');
	fs.mkdirSync(dataDir, { recursive: true });

	// Recorded before anything can fail, so globalTeardown always knows what to
	// remove even when the fixture generation or the go build blows up.
	const state: ServerState = { baseURL, pid: 0, tmpDir };
	writeState(state);

	// 2. fixture library
	if (!haveFfmpeg()) {
		state.skipReason =
			'ffmpeg is not on PATH: the e2e fixture library cannot be generated. ' +
			'Install ffmpeg (https://ffmpeg.org/download.html) and re-run `npm run test:e2e`.';
		writeState(state);
		throw new Error(state.skipReason);
	}
	generateLibrary(libDir);

	// 3. the real binary
	const binary = path.join(tmpDir, isWindows ? 'storykeeper.exe' : 'storykeeper');
	console.log('[e2e] go build ./cmd/storykeeper');
	run('go', ['build', '-o', binary, './cmd/storykeeper'], root);

	// 4. start it
	const child = spawn(binary, [], {
		cwd: tmpDir,
		env: {
			...process.env,
			SK_ADDR: `127.0.0.1:${port}`,
			SK_DATA_DIR: dataDir,
			SK_LIBRARY: libDir,
			SK_ADMIN_USER: ADMIN_USER,
			SK_ADMIN_PASSWORD: ADMIN_PASSWORD,
			SK_SECURE_COOKIE: 'false'
		},
		stdio: ['ignore', 'pipe', 'pipe'],
		windowsHide: true
	});
	const log = fs.createWriteStream(path.join(tmpDir, 'server.log'));
	child.stdout?.pipe(log);
	child.stderr?.pipe(log);
	child.on('exit', (code) => console.log(`[e2e] server exited with ${code}`));

	state.pid = child.pid ?? 0;
	writeState(state);

	// 5. health, then the startup scan
	await waitFor('GET /api/v1/health', async () => {
		const res = await fetch(`${baseURL}/api/v1/health`);
		return res.ok;
	});
	const cookie = await apiLogin(baseURL);
	await waitFor('the startup library scan to find both fixture books', async () => {
		const res = await fetch(`${baseURL}/api/v1/books`, { headers: { Cookie: cookie } });
		if (!res.ok) return false;
		const body = (await res.json()) as { items: { title: string }[] };
		const titles = body.items.map((b) => b.title);
		return titles.includes(BOOKS.chaptered.title) && titles.includes(BOOKS.multiFile.title);
	});

	child.unref();
	console.log(`[e2e] storykeeper ready on ${baseURL} (pid ${state.pid})`);
}
