# SQLite Data Reset

Task13 switches the product runtime to SQLite. PostgreSQL local data directories from older builds are ignored and are not migrated into SQLite. This page is only a reset guide for local SQLite development data; it is not a PostgreSQL-to-SQLite migration tool.

## Default database path

- Default desktop/dev path: `<SUPER_DOLPHIN_HOME>/super-dolphin.db`.
- Override path: set `SUPER_DOLPHIN_SQLITE_PATH` to an explicit `.db` file path.
- `DATABASE_URL` and `POSTGRES_CONNECTION_STRING` are not product DB configuration sources. If they remain in the shell, startup ignores them for DB selection.

## Reset flow

1. Stop Super Dolphin and all sidecars, including `cmd/agent-terminal`, `cmd/mcp-orch`, `cmd/mcp-lsp`, and provider child processes.
2. Optionally back up the SQLite files:
   - `<db>.db`
   - `<db>.db-wal`
   - `<db>.db-shm`
3. Reset by removing the database, WAL, and shared-memory files together:
   - `super-dolphin.db`
   - `super-dolphin.db-wal`
   - `super-dolphin.db-shm`
4. Alternatively, checkpoint first, then remove only the database file after SQLite has no active writers.
5. Start the app again and let SQLite migrations recreate the schema.

Reset discards local development data. Keep a backup when the local state matters.

## Old PostgreSQL data

Old local PostgreSQL data directories are not read by product startup, package launchers, or provider sidecars. They can be deleted manually after confirming no older development checkout still uses them. Removing old PostgreSQL files does not affect the new SQLite database unless you delete the SQLite files listed above.
