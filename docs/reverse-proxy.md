# Reverse proxy

Set `BASE_URL` to the public address and `SECURE_COOKIES=true` behind HTTPS — browsers only let a page use the microphone (voice messages) over HTTPS. See [deploy/reverse-proxy/Caddyfile](../deploy/reverse-proxy/Caddyfile).

- **Live updates** come over Server-Sent Events (`/api/v1/stream`): the proxy must pass them through unbuffered (Caddy `flush_interval -1`, nginx `proxy_buffering off; proxy_read_timeout 1h;`). Chat sends a ping every 25 seconds.
- **Uploads**: allow request bodies of at least `MAX_UPLOAD_BYTES` (50 MB by default), more for big WhatsApp exports with media.
- `/api/*` is reachable for Homepage widgets, Shortcuts and linked apps; they send their own bearer token.
