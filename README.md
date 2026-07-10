# Super Agent v3

Multi-agent orchestration platform for AI-assisted development. Provides session management, tool execution, cron scheduling, and memory systems with real-time event streaming.

> Migration from go-agent-v2, started 2026-03-19.

## Architecture

```
cmd/
├── agent-terminal/      # Wails desktop host + HTTP/RPC bridge; embeds copied frontend-app assets for package runs
├── mcp-orch/            # MCP orchestration peer (agent lifecycle, DAG, cron)
└── mcp-lsp/             # MCP generic multi-language LSP peer (code intelligence)

frontend-app/            # Current React/Vite new UI used by run-new-ui-desktop.sh / .ps1

internal/
├── contract/            # Cross-module interfaces & DTOs
├── module/              # Business logic (turn, prompt, cron, memory, skill)
├── platform/            # Infrastructure (db, rpc, config, runtime safety)
├── provider/            # AI provider adapters (Claude CLI, Codex)
└── store/               # Data access layer (sqlc-backed stores with hand-written wrappers)

pkg/                     # Reusable public libraries
```

## Quick Start

### Prerequisites

- Go 1.25.7
- SQLite is used by the store layer automatically. By default the database is
  created under `SUPER_DOLPHIN_HOME/super-dolphin.db`; set
  `SUPER_DOLPHIN_SQLITE_PATH` to use a different local file.
- Node.js 20+ (for frontend)
- OpenAI Codex CLI (`codex`) installed + authenticated — required for the current new UI desktop provider flow
- Claude Code CLI (`claude`) installed + authenticated — only required for legacy/provider-integration work that explicitly targets Claude

### Clone & Setup

```bash
git clone <repo-url> && cd super-agent-v3
make install-hooks   # Required: enables pre-commit & pre-push checks

# Optional: override the SQLite database path. PostgreSQL env vars are ignored
# for product DB configuration and should not be used for new local setup.
export SUPER_DOLPHIN_SQLITE_PATH="$PWD/.super-dolphin/super-dolphin.db"

# For the current new UI desktop dev flow:
( cd frontend-app && npm install )
# macOS:
./run-new-ui-desktop.sh
# Windows PowerShell:
.\run-new-ui-desktop.ps1

# Optional macOS backend restart-on-change:
SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1 ./run-new-ui-desktop.sh

# Package/embed builds use frontend-app and copy dist into
# cmd/agent-terminal/web-dist for Go embed:
make frontend-app-build
```

First-run side effects (auto, no manual step):
- DB migrations run via `internal/platform/db/module.go:autoMigrate` on startup.
- Old local PostgreSQL data directories are ignored and are not migrated into
  SQLite. To reset local SQLite dev state, stop the app and sidecars, optionally
  back up the database, then remove the `.db`, `.db-wal`, and `.db-shm` files
  together. This discards local dev data and is not a PostgreSQL-to-SQLite
  migration path.
- Runtime canonical skills are managed under project and personal roots:
  `<workspace>/.agents/skills/` for project skills, and
  `~/.super-dolphin/skills/personal/{user,agent,imported}/` for active personal
  skills (`SUPER_DOLPHIN_HOME` can override the home root). `personal/hub` is
  catalog-only and is not scanned, mirrored, or exposed to providers.
- Provider-native skill mirrors are reconciled before provider launch/acquire.
  Project canonical skills live under tracked `<workspace>/.agents/skills/`.
  Codex reads that project canonical root directly; manual-only project skills
  cannot be hidden from Codex native discovery and will block mirror reconcile
  instead of being silently exposed. Legacy `.agent/skills` is not a runtime
  source of truth.
  `<workspace>/.claude/skills/` is optional and validated only when present.
  Personal mirrors live under `~/.claude/skills/` and `~/.agents/skills/` by
  default, or under an explicit provider home `skills/` directory when
  configured.
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
./run-new-ui-desktop.sh      # macOS: run current React/Vite new UI in the desktop host
.\run-new-ui-desktop.ps1     # Windows PowerShell equivalent
SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1 ./run-new-ui-desktop.sh  # macOS: add Go backend restart-on-change
make build-plain           # Build all Go binaries after preparing frontend-app embed assets
make run-plain             # Run the desktop host after preparing frontend-app embed assets
make build-agent-terminal  # Build the desktop UI binary (Wails + Frida)
make build-agent-terminal-plain   # Same without Frida (lighter)
```

### Test

```bash
make test                  # Full guarded suite; prepares frontend embed assets first
./scripts/test_with_guard.sh ./internal/module/ws_test -count=1  # Focused guarded Go package test
make frontend-app-build && go test ./... -count=1  # Direct full Go test after embed assets exist
make frontend-app-build && go test -bench=. ./...  # Run benchmarks after embed assets exist
( cd frontend-app && npm run lint && npm test && npm run build )
```

### Hot Reload Dev Flow

`run-new-ui-desktop.sh` keeps the React UI on the Vite dev server, so frontend
edits use Vite HMR. On macOS, set `SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1` to also
watch backend source paths and restart `cmd/agent-terminal` when Go, SQL, or
runtime config files change.

Useful overrides:

```bash
SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1 SUPER_DOLPHIN_HOT_POLL_INTERVAL=0.5 ./run-new-ui-desktop.sh
SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1 SUPER_DOLPHIN_HOT_WATCH_PATHS="cmd internal pkg go.mod go.sum" ./run-new-ui-desktop.sh
```

### Notes for skill subsystem (post 2026-04-30 P4/P5/P6 cutover)

- Canonical skill truth lives in project `<workspace>/.agents/skills/` plus
  active personal `~/.super-dolphin/skills/personal/{user,agent,imported}/`.
  Optional `.claude/skills` and personal provider mirrors are generated mirror
  targets, not canonical roots. Legacy `.agent/skills` is a historical path and
  must not be edited as the source of truth. `personal/hub` is reserved for
  catalog/marketplace source data and is not a runtime canonical root.
- The legacy `skill_expand_body` / `skill_read_resource` MCP tools are gone; Claude
  and Codex discover skills via provider-native mirrors under project and personal
  provider-native roots (`<workspace>/.claude/skills/`, `<workspace>/.agents/skills/`,
  `~/.claude/skills/`, and `~/.agents/skills/`). `skill_read_section` is no longer
  the production discovery path.
- Earlier feature flags `SUPER_DOLPHIN_NATIVE_FILTER` and `SUPER_DOLPHIN_SKILL_FBSD`
  were removed; provider-native mirrors are the production discovery path.
  The old FBSD/disclosure pipeline and `skill_read_section` implementation were
  physically removed, so those env vars have no effect.

## Code Quality

| Metric | Value |
|--------|-------|
| Test Coverage | Recompute from current test run; no checked-in static baseline |
| Architecture Tests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 311 runnable `Test*` functions across 122 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
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
