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
- PostgreSQL (for store layer; provide a reachable instance and `DATABASE_URL`)
- Node.js 20+ (for frontend)
- Claude Code CLI (`claude`) installed + authenticated — required if using Claude provider
- OpenAI Codex CLI (`codex`) installed + authenticated — required if using Codex provider

### Clone & Setup

```bash
git clone <repo-url> && cd super-agent-v3
make install-hooks   # Required: enables pre-commit & pre-push checks

# Provision PostgreSQL and export DATABASE_URL (required for store layer):
export DATABASE_URL="postgres://USER:PASS@127.0.0.1:5432/super_dolphin?sslmode=disable"

# Frontend assets are gitignored; build them so go:embed picks up the bundle:
( cd cmd/agent-terminal/frontend && npm install && npm run build )
```

First-run side effects (auto, no manual step):
- DB migrations run via `internal/platform/db/module.go:autoMigrate` on startup.
- Builtin skills seed into `~/.multi-agent/skills-library/`; cache projects to `~/.multi-agent/skills-cache/`.
- Per-workspace `.claude/skills/` symlinks the shared skill cache (`internal/module/cliadapter`).
- Per-workspace `.claude/settings.json` is rendered with `permissions.deny` (skill name + tool) and `permissions.allow` from active skills' `replaces_native` / `allowed_tools` (P5 nativefilter, default ON post-merge).
- Codex skill manifest renders via FBSD frequency tier when tracker is wired (P6, default ON post-merge).

### Build & Run

```bash
make build-plain           # Build all Go binaries (without Frida)
make run-plain             # Run the cmd/server entry
make build-agent-terminal  # Build the desktop UI binary (Wails + Frida)
make build-agent-terminal-plain   # Same without Frida (lighter)
```

### Test

```bash
make test                  # Full test suite
go test ./... -count=1     # Direct Go test
go test -bench=. ./...     # Run benchmarks
```

### Notes for skill subsystem (post 2026-04-30 P4/P5/P6 cutover)

- The legacy `skill_expand_body` / `skill_read_resource` MCP tools are gone; Claude
  loads skills via native `<workspace>/.claude/skills/` symlinks, Codex calls
  `skill_read_section` host-direct (see `internal/platform/toolbridge/skill_read_section.go`).
- Earlier feature flags `SUPER_DOLPHIN_NATIVE_FILTER` and `SUPER_DOLPHIN_SKILL_FBSD`
  were removed; nativefilter and FBSD are now always-on. Setting these env vars has
  no effect.
- Tunable knobs (still env-driven, **not** gates):
  `SKILL_FBSD_BUDGET`, `SKILL_FBSD_HALF_LIFE_DAYS`, `SKILL_FBSD_FROZEN_DAYS`,
  `SKILL_FBSD_GRACE_DAYS`, `SKILL_FBSD_HOT_CHARS`, `SKILL_FBSD_WARM_CHARS`,
  `SKILL_FBSD_COLD_CHARS`, `SKILL_FBSD_WS_MIN_CALLS`, `SKILL_FBSD_WS_WEIGHT`.

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
