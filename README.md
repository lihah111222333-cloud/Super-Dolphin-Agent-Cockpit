# Super Agent v3

Multi-agent orchestration platform for AI-assisted development. Provides session management, tool execution, cron scheduling, and memory systems with real-time event streaming.

> Migration from go-agent-v2, started 2026-03-19.

## Architecture

```
cmd/
├── agent-terminal/      # Frontend + HTTP server (Vue.js SPA)
├── mcp-orch/            # MCP orchestration peer (agent lifecycle, DAG, cron)
└── mcp-lsp/             # MCP generic multi-language LSP peer (code intelligence)

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
- Runtime canonical skills are managed under project and personal roots:
  `<workspace>/.agent/skills/` for project skills, and
  `~/.super-dolphin/skills/personal/{user,agent,imported,hub}/` for personal
  skills (`SUPER_DOLPHIN_HOME` can override the home root).
- Provider-native skill mirrors are reconciled before provider launch/acquire:
  project mirrors live under `<workspace>/.claude/skills/` and `<workspace>/.codex/skills/`,
  with provider-home mirrors under `~/.super-dolphin/providers/{claude,codex}/skills/`
  by default or an explicit provider home `skills/` directory.
- Legacy `.claude/settings.json` nativefilter deny entries are not written or cleared during provider launch; skill visibility now comes from provider-native mirrors, not settings injection.

### Optional: Codex Fast Mode

If you use the Codex provider, you can opt into Codex's server-side fast tier (~1.5× speedup, 2–2.5× credit cost). The toggle lives in your **personal** `~/.codex/config.toml` (not in this repo), so each developer enables it independently.

Prerequisite: `codex login` with a ChatGPT account. API-key auth has no fast credits.

Add this line to the top level of `~/.codex/config.toml` (not inside any `[section]`):

```toml
service_tier = "fast"
```

Verify it's wired through:

```bash
RUST_LOG=codex=debug codex exec --json <<< "hi" 2>&1 | grep service_tier
# Expect: service_tier: Some(Fast)
```

Notes:
- Whether the upstream model provider honors `service_tier` depends on the provider. The codex CLI side will always send it; the speedup is upstream-dependent.
- To disable, delete the line and restart any long-running codex subprocess.
- See https://developers.openai.com/codex/speed for tier semantics.

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

- Canonical skill truth lives in project `<workspace>/.agent/skills/` plus
  personal `~/.super-dolphin/skills/personal/{user,agent,imported,hub}/`.
  Provider-native mirror directories are generated, ignored, and not committed.
- The legacy `skill_expand_body` / `skill_read_resource` MCP tools are gone; Claude
  and Codex discover skills via provider-native mirrors under project and provider
  homes (`<workspace>/.claude/skills/`, `<workspace>/.codex/skills/`, and provider
  home `skills`). `skill_read_section` is no longer the production discovery path.
- Earlier feature flags `SUPER_DOLPHIN_NATIVE_FILTER` and `SUPER_DOLPHIN_SKILL_FBSD`
  were removed; provider-native mirrors are the production discovery path.
  The old FBSD/disclosure pipeline and `skill_read_section` implementation were
  physically removed, so those env vars have no effect.

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
