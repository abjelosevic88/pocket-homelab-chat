# API

JSON over `/api/v1`, with a session cookie or `Authorization: Bearer <token>` (Settings → API & shortcuts). Errors are `{"error": "…"}`. Every person sees only the conversations they are in; anything else answers 404, for admins too.

## Conversations

| Call | What |
|---|---|
| `GET /conversations` | Yours (your own space "Me" is made on first call): `name` as you see it, members, `unread`, `mentioned`, `last`, your `notify`, `muted_until`, `pinned`, `archived`, `draft`, and who is `online` |
| `POST /conversations` | `{kind: "direct", with: <user id>}` (one per pair), `{kind: "group", title, about, emoji, color, members: [ids]}` or `{kind: "self"}` |
| `GET/PUT/DELETE /conversations/{id}` | PUT: `title, about, emoji, color` (groups), `ai`, `smart`. DELETE: a group, for everyone (its owner) |
| `PUT /conversations/{id}/me` | Your own switches: `notify (default|all|mentions|off), muted_until (RFC 3339 or ""), pinned, archived, draft` |
| `POST /conversations/{id}/read` | `{upto: <message id>}` (or everything) |
| `POST /conversations/{id}/typing` | Tells the others you are writing (at most every 3 s) |
| `POST /conversations/{id}/members` · `PUT/DELETE …/members/{uid}` | Add people to a group · make an owner (`{role}`) · remove someone (owner) or leave (yourself) |
| `GET /conversations/{id}/pinned`, `GET …/shared?type=photos|files|links|cards|polls|todos|voice` | Pinned messages; what was shared |
| `GET /conversations/{id}/export.txt` | The conversation as text |
| `GET /people` | Everyone on this box, with `online` and `seen` |

## Messages

| Call | What |
|---|---|
| `GET /conversations/{id}/messages?before=&after=&around=&limit=` | Oldest first; with attachments, reactions, votes, the quoted message, and your own `starred` and `made` |
| `POST /conversations/{id}/messages` | `{text, reply_to, attachments: [ids], client_id}` — `client_id` makes a retry safe. Also `{kind: "poll", poll: {question, options, multi}}`, `{kind: "todo", todo: {title, items: [{text, assignee}]}}`, `{kind: "voice", attachments: [id]}`, or a `card` |
| `POST /conversations/{id}/upload` | multipart `file` (+ `voice=1`, `duration_ms`) → an attachment for your next message there |
| `GET /files/{file}` | A file, for the people in its conversation (`?download=1`) |
| `PUT/DELETE /messages/{id}` | Change your text / delete for everyone (yours, or any in a group you own) |
| `POST /messages/{id}/react` `{emoji}` · `/vote` `{options}` · `/close` `{closed}` · `/todo` `{op: toggle|add|remove|assign, item, text, assignee}` · `/pin` `{on}` · `/star` `{on}` | |
| `POST /messages/{id}/make` | `{target: task|event|note|expense|wish|place, title, notes, date, time, minutes, location, amount, currency, group, paid_by, members, url, kind, city, address}` → made in your own app |
| `POST /messages/{id}/fill` `{target, ai}` | What "Make…" would fill in (rules; `ai: true` asks your AI) |
| `POST /messages/{id}/translate` `{language}` | |
| `GET /search?q=&conversation=&from=&kind=&since=&until=` | `{query, results}`, accents ignored |
| `GET /saved`, `GET /made`, `GET /unread` | |
| `POST /send` | `{to: "me" | email | group name | id, text, card}` — for scripts, Siri Shortcuts, Pocket Flows |

## AI

`GET /conversations/{id}/catchup`, `GET /catchup` (everything unread) → `{summary, for_you, plans, count, unread}`; `POST /conversations/{id}/replies` → `{replies}`; `POST /conversations/{id}/note` `{title, content}` keeps text in Pocket Notes. These answer only the person who asks; Pocket AI never writes into a conversation.

## The other apps

`GET /share/apps`, `GET /share/{app}?q=` (items with their card), `POST /conversations/{id}/share` `{app, id, text}`; `GET /make/options`, `GET /make/split-groups`.

## Live

`GET /stream` — Server-Sent Events: `message`, `message_update`, `read`, `typing`, `conversation`, `presence`.

## For other apps (Pocket Core links)

| Route | Scope |
|---|---|
| `GET /summary` — unread, conversations waiting, who mentioned you, today | `chat:read` |
| `GET /day/YYYY-MM-DD` — messages per conversation, plans spotted, cards shared, photos (Pocket Day) | `chat:read` |
| `GET /conversations`, `/conversations/{id}`, `…/messages`, `…/pinned`, `…/catchup`, `/search`, `/unread`, `/people`, `/saved` | `chat:read` |
| `POST /send`, `POST /conversations/{id}/messages` — shown as from that app; it may change or delete only its own | `chat:write` |

Events on the Pocket Core bus: `chat.message_sent` (for the sender), `chat.mentioned` (for the person named).

## Box

`GET /healthz`, `/readyz`, `/metrics`; `GET /export.json`; `POST /import/whatsapp` (multipart `file` .txt/.zip, `preview=1`, `people` = JSON name → user id, `conversation` or `title`); `GET /backup` (admin).
