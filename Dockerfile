# syntax=docker/dockerfile:1

########################################
# Stage 1: build the SvelteKit web app
########################################
FROM node:24-alpine AS web-build
WORKDIR /src/web

# Copy package manifests first for layer caching.
COPY web/package*.json ./
RUN npm ci

# Copy the rest of the web source and build.
COPY web/ ./
RUN npm run build

########################################
# Stage 2: build the Go binary
########################################
FROM golang:1.26-alpine AS go-build
WORKDIR /src

# Copy module files first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source.
COPY . .

# Bring in the built web app so web/embed.go (//go:embed all:build) has
# real assets to embed.
COPY --from=web-build /src/web/build ./web/build

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/storykeeper ./cmd/storykeeper

########################################
# Stage 3: runtime image
########################################
FROM alpine:3.20

RUN apk add --no-cache ffmpeg ca-certificates tzdata \
    && addgroup -g 1000 storykeeper \
    && adduser -D -u 1000 -G storykeeper -h /home/storykeeper storykeeper \
    && mkdir -p /data /library \
    && chown -R storykeeper:storykeeper /data /library

COPY --from=go-build /out/storykeeper /usr/local/bin/storykeeper

USER storykeeper

VOLUME ["/data", "/library"]
EXPOSE 8080

ENV SK_DATA_DIR=/data \
    SK_ADDR=:8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O- http://localhost:8080/api/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/storykeeper"]
