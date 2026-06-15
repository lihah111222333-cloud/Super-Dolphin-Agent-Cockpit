# macOS Clean VM Packaged App Checklist

## VM baseline

- macOS arm64 VM.
- No `DATABASE_URL` or `POSTGRES_CONNECTION_STRING` in shell or launch
  environment.
- No PostgreSQL server installed or running.
- No Docker Desktop required.
- No `~/.codex` directory.
- No `codex` binary on `PATH`.
- No `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL` or
  `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN` in the shell or launch
  environment; the packaged app must load bundled relay settings.
- No Super Dolphin app data directory:
  - `~/Library/Application Support/Super Dolphin`

## Install

1. Copy `dist/package/macos/Super Dolphin.dmg` to the VM.
2. Mount the DMG.
3. Drag `Super Dolphin.app` to `/Applications`.
4. Launch `/Applications/Super Dolphin.app` by double-clicking.

## Expected first launch behavior

- App creates `~/Library/Application Support/Super Dolphin`.
- App creates the SQLite database at
  `~/Library/Application Support/Super Dolphin/super-dolphin.db`.
- App runs SQLite migrations to the binary's required schema version.
- App prepares app-managed Codex home.
- App writes Codex relay config from bundled app resources, not user shell
  variables. The bundled bootstrap token is readable from the package and must
  be a public/bootstrap credential, not a secret or privileged API key.
- App does not require Docker.
- App does not require user-provided database environment variables.
- App does not create PostgreSQL runtime artifacts such as `postgres/`,
  `pg_ctl`, `initdb`, `postgres.bki`, or `logs/postgres.log`.

## Scripted preflight

Before recording the manual GUI acceptance, run:

```bash
docs/scripts/macos_release_smoke.sh blockers
```

This command must exit 0 inside the target VM before the clean-VM acceptance can
be considered complete. A non-zero exit is a release blocker; keep the generated
log under `docs/reviews/smoke-logs/2026-05-28/` with the release packet.

## Acceptance test

1. Open the app.
2. Create a Codex conversation.
3. Send: `Say hello from packaged Super Dolphin.`
4. Receive a Codex response.
5. Quit the app.
6. Reopen the app.
7. Confirm the previous conversation is visible or the app can create a second
   Codex conversation without recreating the SQLite database.

## Failure evidence to collect

Run these commands in Terminal and save output:

```bash
ls -la "$HOME/Library/Application Support/Super Dolphin"
find "$HOME/Library/Application Support/Super Dolphin" -maxdepth 4 -type f | sort | sed -n '1,120p'
find "$HOME/Library/Application Support/Super Dolphin" -maxdepth 4 \( -iname '*postgres*' -o -name pg_ctl -o -name initdb -o -name postgres.bki \) -print
ps aux | grep -E 'postgres|agent-terminal|mcp-orch|mcp-lsp|codex' | grep -v grep
log show --predicate 'process == "agent-terminal"' --last 10m
```

If `sqlite3` is available, capture the schema list:

```bash
sqlite3 "$HOME/Library/Application Support/Super Dolphin/super-dolphin.db" '.tables'
```
