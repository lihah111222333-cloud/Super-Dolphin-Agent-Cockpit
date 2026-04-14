# Super Agent V3

Migration from go-agent-v2, started 2026-03-19.

## Migration Status

- This repository is still in the go-agent-v2 → V3 migration window.
- Scope summary from `migration_checklist.json`: `700+` V2 guards reviewed, `~150-200` must-migrate guards retained, V3 target size `≤35,000` LOC.
- The near-term focus is provider convergence + prompt/memory system migration.
- See `migration_checklist.json` for detailed batch-by-batch status.

## Build

```bash
make test        # unit/integration tests
make vet         # go vet under guard
make build       # full build
make build-plain # plain build path when CGO/frida stack is not needed
```

## Quick Start / Run

```bash
make run                       # main app entrypoint
make run-plain                 # plain runtime path
make build-agent-terminal      # build terminal binary
make run-agent-terminal-debug  # terminal debug mode
make mcp                       # start MCP entrypoint
```

## Environment Notes

- `make build` is the default guarded/full build path.
- `make build-plain` / `make run-plain` are the fallback paths when you do not need the CGO/frida stack.
- If you are working on the CGO/frida path itself, check `make setup-cgo` first.
- Guarded commands are expected in normal development flow; use an unguarded Go invocation only when you intentionally need to debug the guard layer.

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

Navigation:
- code map index: `docs/doc/codemap/ai-index.json`
- terminal / UI path: `docs/doc/codemap/01-terminal-ui.md`
- orchestration / MCP path: `docs/doc/codemap/02-mcp-orch.md`
