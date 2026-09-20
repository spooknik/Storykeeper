import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// Static export: the Go binary serves everything from web/build (see web/embed.go).
			adapter: adapter({
				pages: 'build',
				assets: 'build',
				fallback: 'index.html',
				precompress: false
			})
		}),

		SvelteKitPWA({
			registerType: 'autoUpdate',
			kit: {
				adapterFallback: 'index.html'
			},
			includeAssets: ['favicon.ico', 'favicon-32.png'],
			manifest: {
				name: 'Storykeeper',
				short_name: 'Storykeeper',
				description: 'Storykeeper',
				display: 'standalone',
				theme_color: '#111111',
				background_color: '#111111',
				start_url: '/',
				scope: '/',
				icons: [
					{ src: 'icon-192.png', sizes: '192x192', type: 'image/png' },
					{ src: 'icon-512.png', sizes: '512x512', type: 'image/png' }
				]
			},
			workbox: {
				navigateFallback: '/index.html',
				// The Go server owns /api/* and /media/* — the service worker must never
				// intercept, precache, or runtime-cache these paths.
				// Standalone diagnostics must open their own HTML, including when the
				// installed app's worker controls the navigation.
				navigateFallbackDenylist: [/^\/api\//, /^\/media\//, /^\/diagnostics\//],
				globIgnores: ['**/api/**', '**/media/**'],
				runtimeCaching: []
			}
		})
	],

	// Talk to the Go backend during `npm run dev`.
	server: {
		proxy: {
			'/api': 'http://localhost:8080',
			'/media': 'http://localhost:8080'
		}
	}
});
