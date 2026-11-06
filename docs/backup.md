# Backup

The messages live in one SQLite file, `DATA_DIR/pocket-chat.db` (WAL mode); photos, files and voice messages are files in `DATA_DIR/files/`. Take a consistent copy of the database with `pocket-chat backup` (writes `DATA_DIR/backups/pocket-chat-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin), and back up `files/` as it is. Back up that copy, not the live file. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-chat.db` (remove `-wal`/`-shm` files) and `files/`, start.

Every person can also export what they can see: every conversation they are in as JSON (`/api/v1/export.json`), or one conversation as plain text (`/api/v1/conversations/<id>/export.txt`, like a WhatsApp export).
