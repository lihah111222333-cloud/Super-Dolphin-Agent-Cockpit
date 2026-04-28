# Super Agent v3

Multi-agent orchestration platform for AI-assisted development. Provides session management, tool execution, cron scheduling, and memory systems with real-time event streaming.

> Migration from go-agent-v2, started 2026-03-19.

## Architecture

```
cmd/
├── agent-terminal/      # Frontend + HTTP server (Vue.js SPA)
├── mcp-orch/            # MCP orchestration peer (agent lifecycle, DAG, cron)
└── mcp-lsp/             # MCP LSP peer (gopls integration, code intelligence)

internal/
├── contract/            # Cross-module interfaces & DTOs
├── module/              # Business logic (turn, prompt, cron, memory, skill)
├── platform/            # Infrastructure (db, rpc, config, runtime safety)
├── provider/            # AI provider adapters (Claude CLI, Codex)
└── store/               # Data access layer (sqlc-generated)

pkg/                     # Reusable public libraries
```

## Quick Start

### Prerequisites

- Go 1.23+
- PostgreSQL (for store layer)
- Node.js 20+ (for frontend)

### Clone & Setup

```bash
git clone <repo-url> && cd super-agent-v3
make install-hooks   # Required: enables pre-commit & pre-push checks
```

### Build & Run

```bash
make build-plain           # Build all (without Frida)
make run-plain             # Run server
make build-agent-terminal  # Build terminal UI
```

### Test

```bash
make test                  # Full test suite
go test ./... -count=1     # Direct Go test
go test -bench=. ./...     # Run benchmarks
```

## Code Quality

| Metric | Value |
|--------|-------|
| Test Coverage | 67.4% |
| Architecture Tests | 50+ (internal/archtest) |
| Linter | golangci-lint (see .golangci.yml) |
| CI | GitHub Actions (see .github/workflows/ci.yml) |

### Git Hooks

`make install-hooks` sets `core.hooksPath` to `.githooks`, enabling automatic pre-commit and pre-push checks. Bypass with `--no-verify` only in emergencies — violations must be fixed retroactively.

## Code Map

Full code map: [`docs/doc/codemap/README.md`](docs/doc/codemap/README.md). Key sections:

- [Terminal Entry & UI Layer](docs/doc/codemap/01-terminal-ui.md)
- [MCP Orchestration](docs/doc/codemap/02-mcp-orch.md)
- [App Core & Contract Layer](docs/doc/codemap/04-app-contract.md)
- [Business Modules](docs/doc/codemap/07-module.md)
- [Platform Infrastructure](docs/doc/codemap/08-platform.md)
- [Provider Integration](docs/doc/codemap/09-provider.md)

## License

Proprietary — Anthropic, Inc.
