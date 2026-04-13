# Super Agent V3

Migration from go-agent-v2, started 2026-03-19.

## Migration Status

See `migration_checklist.json` for detailed progress.

## Build

```bash
make check   # lint + test
make build   # compile
```

## Guarded Go commands

To ensure repository-wide code guards always run before package tests/builds/vet:

```bash
source scripts/activate_guard_env.sh
go test ./internal/provider/claudecli   # will be blocked with a clear error + replacement command
```

Preferred explicit entrypoint:

```bash
./scripts/go_with_guard.sh test ./internal/provider/claudecli -count=1
./scripts/go_with_guard.sh build ./...
./scripts/go_with_guard.sh vet ./...
```

Or start a guarded subshell:

```bash
make guard-shell
```

## Architecture

- `internal/platform/*` + `internal/platform/shared/` — Cross-package DRY primitives (former Zone A)
- `internal/*/factory_*.go` — Package-local DRY (Zone B)
- `internal/guards/` — Behavioral & structural guards
