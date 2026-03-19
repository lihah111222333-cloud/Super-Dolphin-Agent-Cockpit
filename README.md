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

- `pkg/factory/` — Cross-package DRY primitives (Zone A)
- `internal/*/factory_*.go` — Package-local DRY (Zone B)
- `internal/guards/` — Behavioral & structural guards
