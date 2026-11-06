# Configuration

Environment variables; every one has a default. Personal settings (chatting, notifications, connections, AI) live in the UI per person — the `OPENAI_*`/`NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Where the app is (links in pushes and in things made from messages, the OIDC callback) |
| `DATA_DIR` | `./data` | Database, files, Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-chat.db` | SQLite file |
| `FILES_DIR` | `$DATA_DIR/files` | Photos, files and voice messages (random file names) |
| `MAX_UPLOAD_BYTES` | `52428800` | Largest file (50 MB; photos are made at most 2400 px and lose their camera data, location included) |
| `MAX_BODY_BYTES` | `67108864` | Largest JSON request (64 MB) |
| `TZ` | – | Time zone for dates, suggestions ("Saturday at 7"), quiet hours and the morning summary |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `NOTIFY_HOUR` | `8` | Default hour of the morning summary (each person can change theirs) |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | – | AI without Pocket AI (any OpenAI-compatible API: Ollama, LM Studio, OpenRouter…) |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | Seed a family that chats (groups, a poll, a to-do list, cards from the apps) into the owner account |

Commands: `pocket-chat backup [-o FILE]`, `pocket-chat reset-password [-email] [-password]`, `pocket-chat version`.

Connections to Pocket Tasks, Calendar, Notes, Split, Wishlist, Places, Voice, Kitchen, Travel, Parcels, Weather, Bookshelf and Watch are made per person under Settings → Connected apps: through Pocket Core (nothing to paste) or directly with the app's URL and an API token. They are used only when that person shares something or makes something from a message.

## Live updates

Open windows get new messages, edits, reactions, typing and who is online over Server-Sent Events (`/api/v1/stream`). A reverse proxy must not buffer it: in Caddy add `flush_interval -1` to `reverse_proxy`, in nginx `proxy_buffering off`.

## Link previews

A web link in a message gets a preview (title, description, picture). Chat fetches only public addresses: loopback, private, link-local and shared (CGNAT/tailnet) addresses are refused at connection time, so a link cannot make the server read your network. Each person can switch previews off (Settings → Chatting).
