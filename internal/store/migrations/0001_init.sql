CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- ---------- Pocket Chat ----------
--
-- A conversation is shared by its members; nobody else (an admin neither)
-- can see it. Every per-person row (membership, stars, what you made from a
-- message, uploads) carries the person's user_id, and every query starts
-- from the caller's membership.

-- A conversation: a chat with one person (direct), a group, or your own
-- space (self, "Me": notes to yourself, things to file later).
CREATE TABLE conversations (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  kind            TEXT NOT NULL DEFAULT 'group',     -- direct | group | self
  title           TEXT NOT NULL DEFAULT '',          -- groups; direct/self show the other person / "Me"
  about           TEXT NOT NULL DEFAULT '',
  emoji           TEXT NOT NULL DEFAULT '',
  color           TEXT NOT NULL DEFAULT '',
  direct_key      TEXT UNIQUE,                       -- "d:<low id>:<high id>" or "s:<id>": one each
  created_by      INTEGER REFERENCES users(id) ON DELETE SET NULL,
  ai              INTEGER NOT NULL DEFAULT 1,        -- Pocket AI answers when someone writes @ai
  smart           INTEGER NOT NULL DEFAULT 1,        -- suggest "add to calendar", "split this"… under messages
  last_message_id INTEGER NOT NULL DEFAULT 0,
  last_message_at TEXT,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Who is in a conversation, and each person's own state in it.
CREATE TABLE members (
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role            TEXT NOT NULL DEFAULT 'member',    -- owner | member
  last_read_id    INTEGER NOT NULL DEFAULT 0,
  notify          TEXT NOT NULL DEFAULT 'default',   -- default | all | mentions | off
  muted_until     TEXT,
  pinned          INTEGER NOT NULL DEFAULT 0,
  archived        INTEGER NOT NULL DEFAULT 0,
  draft           TEXT NOT NULL DEFAULT '',
  joined_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, conversation_id)
);
CREATE INDEX members_conversation ON members(conversation_id);

-- A message. user_id is who wrote it; NULL for Pocket AI, another app or
-- the conversation itself (system lines), with author_name to show.
CREATE TABLE messages (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  user_id         INTEGER REFERENCES users(id) ON DELETE SET NULL,
  kind            TEXT NOT NULL DEFAULT 'text',      -- text | card | poll | todo | voice | ai | app | system
  body            TEXT NOT NULL DEFAULT '',
  data            TEXT NOT NULL DEFAULT '{}',        -- card, poll, to-do list, link preview, hints, AI notes
  reply_to        INTEGER REFERENCES messages(id) ON DELETE SET NULL,
  mentions        TEXT NOT NULL DEFAULT '',          -- " 3 7 ai " (user ids, ai)
  author_name     TEXT NOT NULL DEFAULT '',
  source          TEXT NOT NULL DEFAULT '',          -- app slug that posted it, "import" for a WhatsApp import
  source_ref      TEXT NOT NULL DEFAULT '',
  client_id       TEXT NOT NULL DEFAULT '',          -- the sender's id for a retry-safe send / a re-import
  search          TEXT NOT NULL DEFAULT '',          -- lower case, accents folded
  pinned_at       TEXT,
  pinned_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
  edited_at       TEXT,
  deleted_at      TEXT,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX messages_conversation ON messages(conversation_id, id);
CREATE INDEX messages_user ON messages(user_id, id);
CREATE UNIQUE INDEX messages_client ON messages(conversation_id, client_id) WHERE client_id != '';

-- Photos, files and voice messages (the files live in DATA_DIR/files).
CREATE TABLE attachments (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  message_id      INTEGER REFERENCES messages(id) ON DELETE CASCADE,
  file            TEXT NOT NULL UNIQUE,
  name            TEXT NOT NULL DEFAULT '',
  mime            TEXT NOT NULL DEFAULT '',
  bytes           INTEGER NOT NULL DEFAULT 0,
  width           INTEGER NOT NULL DEFAULT 0,
  height          INTEGER NOT NULL DEFAULT 0,
  duration_ms     INTEGER NOT NULL DEFAULT 0,
  transcript      TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX attachments_user ON attachments(user_id, created_at);
CREATE INDEX attachments_message ON attachments(message_id);
CREATE INDEX attachments_conversation ON attachments(conversation_id, id);

CREATE TABLE reactions (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  emoji      TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX reactions_user ON reactions(user_id);

-- Poll votes (the poll itself is a message).
CREATE TABLE votes (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  option     INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (message_id, user_id, option)
);
CREATE INDEX votes_user ON votes(user_id);

-- Messages a person keeps (Saved), only for them.
CREATE TABLE stars (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, message_id)
);

-- What a person made from a message in their other apps (a task, an
-- event, a note, a split expense…), only for them.
CREATE TABLE made (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  target     TEXT NOT NULL,                          -- task | event | note | expense | wish | place
  app        TEXT NOT NULL,
  ref        TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  url        TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX made_user ON made(user_id, message_id);
