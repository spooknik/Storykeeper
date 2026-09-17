.PHONY: web build run dev-web test docker clean

# Build the SvelteKit app into web/build (embedded by the Go binary).
web:
	cd web && npm ci && npm run build

# Build the storykeeper binary. Depends on the web build so the binary
# always embeds a real UI.
build: web
	go build -trimpath -o ./storykeeper ./cmd/storykeeper

# Run the server from source without building a binary. Uses a local
# ./data directory instead of the container default of /data.
run:
	SK_DATA_DIR=./data go run ./cmd/storykeeper

# Run the SvelteKit dev server (hot reload) against the API.
dev-web:
	cd web && npm run dev

# Run Go and web checks.
test:
	go vet ./...
	go test ./...
	cd web && npm run check

# Build the container image locally.
docker:
	docker compose -f docker-compose.yml -f docker-compose.build.yml build

# Remove local build artifacts.
clean:
	rm -f ./storykeeper ./storykeeper.exe
	rm -rf ./web/build ./web/.svelte-kit
