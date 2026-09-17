// Package web embeds the built SvelteKit app (web/build) into the Go binary.
// Run `npm run build` in ./web before `go build` to include the real UI.
package web

import "embed"

//go:embed all:build
var Build embed.FS
