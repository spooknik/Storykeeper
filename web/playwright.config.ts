import { defineConfig, devices } from '@playwright/test';
import { resolveBaseURL, STORAGE_STATE } from './e2e/fixtures/state';

// The base URL lives in e2e/.state/server.json: the first config load reserves a
// free loopback port and records it there, globalSetup starts the real Go binary
// on it, and globalTeardown kills it again. See e2e/fixtures/state.ts.
const baseURL = resolveBaseURL();

export default defineConfig({
	testDir: 'e2e',
	globalSetup: './e2e/fixtures/server.ts',
	globalTeardown: './e2e/fixtures/teardown.ts',
	timeout: 30_000,
	fullyParallel: false,
	workers: 1,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	reporter: [['list'], ['html', { open: 'never' }]],
	use: {
		baseURL,
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure',
		video: 'off'
	},
	projects: [
		{
			name: 'login',
			testMatch: /.*\.setup\.ts/,
			use: { ...devices['Desktop Chrome'] }
		},
		{
			name: 'chromium',
			dependencies: ['login'],
			use: {
				...devices['Desktop Chrome'],
				storageState: STORAGE_STATE,
				launchOptions: {
					// Headless Chromium refuses HTMLMediaElement.play() without a
					// gesture it recognises; the player spec needs it to resolve.
					args: ['--autoplay-policy=no-user-gesture-required', '--mute-audio']
				}
			}
		}
	]
});
