# Reverse proxy setup

Storykeeper speaks plain HTTP on port 8080 and expects a reverse proxy to
terminate TLS. Three things make it different from a typical web app, and every
proxy config below exists to handle them:

1. **Resumable uploads** (tus). Multi-gigabyte audiobooks arrive as 50 MiB `PATCH`
   chunks. The proxy must not buffer request bodies to disk, must not cap body
   size below one chunk, and must allow a couple of minutes per chunk on slow links.
2. **Server-sent events** on `/api/v1/events`. A long-lived response that must be
   streamed, never buffered, and not killed by an idle timeout shorter than the
   20-second ping interval.
3. **Audio range requests** on `/media/*`. Multi-hour responses; Safari sends
   `Range: bytes=0-1` first and needs the `206` passed through unchanged.

Set `SK_SECURE_COOKIE=true` when the proxy serves HTTPS. The server also honours
`X-Forwarded-Proto: https`, so either works.

## Nginx Proxy Manager on TrueNAS (recommended setup)

### 1. DNS

At Cloudflare, create an `A` record for your hostname (for example
`books.example.com`) pointing at your public IP, or a `CNAME` to your DDNS name.
Set the proxy status to **DNS only** (grey cloud). Proxying through Cloudflare
(orange cloud) works but adds a 100 MB request body cap and a 100-second response
timeout, which fights uploads and SSE for no benefit on a home server.

If the box should only be reachable from the LAN or Tailscale, point the record at
the private IP instead. The DNS-01 certificate below does not care.

### 2. Cloudflare API token for DNS-01

Cloudflare dashboard → My Profile → API Tokens → Create Token → template
**Edit zone DNS**. Scope it to the one zone. Copy the token.

### 3. SSL certificate in NPM

NPM → SSL Certificates → Add SSL Certificate → Let's Encrypt:

- Domain names: `books.example.com`
- **Use a DNS Challenge**: on, provider Cloudflare
- Credentials file content: `dns_cloudflare_api_token = <token>`
- Propagation seconds: 30 is plenty for Cloudflare
- Agree to the terms, Save.

### 4. Proxy host in NPM

NPM → Hosts → Proxy Hosts → Add Proxy Host.

**Details tab**

| Field | Value |
| --- | --- |
| Domain names | `books.example.com` |
| Scheme | `http` |
| Forward hostname / IP | TrueNAS LAN IP (or `storykeeper` if NPM shares a Docker network with the stack) |
| Forward port | `8080` |
| Cache assets | off |
| Block common exploits | off (its URL filters can reject tus and range requests) |
| Websockets support | on (harmless, and keeps `Connection: upgrade` handling sane) |

**SSL tab**

| Field | Value |
| --- | --- |
| SSL certificate | the one from step 3 |
| Force SSL | on |
| HTTP/2 support | on |
| HSTS enabled | on |

**Advanced tab** → Custom Nginx Configuration. Paste all of it:

```nginx
# --- Storykeeper: uploads, SSE and audio streaming ---

# tus uploads arrive as 50 MiB PATCH chunks; never buffer them to disk and never cap them.
client_max_body_size 0;
proxy_request_buffering off;

# Stream responses (SSE, audio ranges) straight through.
proxy_buffering off;
proxy_cache off;

# One chunk over a slow uplink can take a while; SSE pings every 20 s.
proxy_connect_timeout 60s;
proxy_send_timeout 600s;
proxy_read_timeout 600s;
send_timeout 600s;

proxy_http_version 1.1;
proxy_set_header Connection "";

# The app derives the Secure cookie flag and upload URLs from these.
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Real-IP $remote_addr;
```

Save. Open `https://books.example.com`, sign in, and on the iPhone use Safari's
Share → **Add to Home Screen** to install the PWA.

### 5. Checks

From any machine:

```bash
# Health through the proxy
curl -sS https://books.example.com/api/v1/health

# Safari's range probe must come back as 206 with Content-Range (log in first and
# reuse the cookie jar)
curl -sS -b cookies.txt -r 0-1 -D - -o /dev/null https://books.example.com/media/books/1/files/0
```

## Plain nginx

Same directives as the NPM advanced block inside a `location / { proxy_pass
http://127.0.0.1:8080; ... }`. Add `proxy_set_header Host $host;`.

## Caddy

```caddyfile
books.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1
        transport http {
            read_timeout 10m
            write_timeout 10m
        }
    }
}
```

Caddy streams bodies and forwards `X-Forwarded-*` by default. Do not add a
`request_body max_size` smaller than 60 MB.

## Traefik

Set `--entrypoints.websecure.transport.respondingTimeouts.readTimeout=600s` and
the matching `writeTimeout`/`idleTimeout` on the entrypoint. Traefik does not
buffer bodies unless the `buffering` middleware is used, so leave it off for this
router.

## Cloudflare Tunnel

Works, with the same caveats as the orange cloud: keep uploads at the default
50 MiB chunk size, and expect SSE to reconnect every 100 seconds (the client
handles this transparently).

## Tailscale only

`tailscale serve --bg 8080` on the host gives you `https://<host>.<tailnet>.ts.net`
with a valid certificate and forwards `X-Forwarded-Proto`. No other configuration
needed. Only devices on your tailnet can reach it.
