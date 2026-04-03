# Super Agent V3

Migration from go-agent-v2, started 2026-03-19.

## Migration Status

See `migration_checklist.json` for detailed progress.

## Build

```bash
make check   # lint + test
make build   # compile
```

## Architecture

- `internal/platform/*` + `internal/platform/shared/` — Cross-package DRY primitives (former Zone A)
- `internal/*/factory_*.go` — Package-local DRY (Zone B)
- `internal/guards/` — Behavioral & structural guards
