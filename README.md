# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

> [!IMPORTANT]
> **🤖 100% AI-Written & Guarded**
> This entire repository—including all Go backend logic, the React frontend, the AST/SSA compiler-level guardrails, and this documentation—was written and refactored **exclusively by AI agents** under human architectural guidance. It serves as the live proof of concept for the Self-Guarding Repository pattern.

**AI-native software governance and multi-agent development control plane.**

Super Dolphin Agent is built for software projects maintained primarily by AI agents. It combines multi-agent sessions, tool execution, MCP orchestration, multi-language LSP, scheduling, memory, provider-native skills, real-time event streaming, and machine-enforced engineering boundaries in one desktop control plane.

The English README is the canonical source. Translations preserve the same product meaning, commands, paths, environment variables, rule IDs, and license identity.

## What it actually does

Super Dolphin Agent is not a chatbot, a cursor rules template, or a prompt wrapper. It is a **local desktop runtime environment and compiler-grade governance firewall** designed to let AI agents autonomously develop and maintain software without turning the codebase into a mess.

It solves the **"Black Box AI Entropy"** problem by splitting the system into three coordinated parts:

1. **The Local Control Center (Desktop App)**: A Wails-based desktop interface (`cmd/agent-terminal`) that lets you run and view multi-agent sessions, monitor tool executions, schedule natural-language cron jobs, manage sqlite-backed vector memory, and stream AI workspace logs in real time.
2. **The Code Intelligence Engine (LSP & MCP Sidecars)**:
   - **LSP Sidecar (`cmd/mcp-lsp`)**: A generic multi-language Language Server Protocol sidecar that indexes your repository and feeds precise, structured code definitions, references, and type hierarchies to the AI, replacing fragile textual search.
   - **Orchestration Sidecar (`cmd/mcp-orch`)**: Coordinates model context protocols (MCP) and manages tool execution DAGs, ensuring the AI can read and write files through a secure, structured interface rather than arbitrary bash scripts.
3. **The Immune System (AST/SSA Unit Tests)**: Embedded directly into your Go test suite (`internal/archtest`), it compiles modified code into Go's compiler-level Static Single Assignment (SSA) intermediate representation to run data-flow checks, blocking common AI anti-patterns (swallowing errors, introducing deadlocks, or breaching onion-architecture boundaries) before they can be committed to Git.

### A Production-Grade Reference (Learn Anti-Corruption)

Super Dolphin Agent is a production-grade multi-agent orchestration system. While built for real-world production workloads, it serves as a gold-standard reference repository for developers to learn:

1. **How to solve the pain of Vibe Coding (Immune Software Engineering)**: It provides a complete, production-verified blueprint for protecting a codebase from AI-driven entropy. It shows how to write AST rules, build SSA call graphs, and run auto-shrinking quality ratchets inside standard Go unit tests.
2. **Production-grade Multi-Agent Architecture**: The codebase itself is a clean, dependency-injected (`fx`), contract-first (`internal/contract`) implementation of a multi-agent control plane. It contains clear reference patterns for:
   - Starting, stopping, and recovering concurrent agent worker goroutines.
   - Spawning stdio MCP sidecar processes and translating raw JSON-RPC to typed Go structures.
   - Preserving thread history in project-local JSONL databases.
   - Implementing SQLite-based vector memory search.

<!-- sd:why -->
## AI-Maintained by Design

“AI-maintained” does not mean unreviewed code or an AI that must understand the entire repository at once. It means AI is the primary implementation workforce while the repository supplies the orientation, constraints, and proof needed to keep every change narrow and auditable. Humans still own product intent, high-impact decisions, credentials, and releases.

The maintenance loop is designed around bounded context:

1. **Orient** with generated code maps and a file-level AI project map.
2. **Understand** public behavior through capability contracts and explicit architecture conventions.
3. **Change** a small surface using LSP definitions, references, call hierarchies, and diagnostics.
4. **Constrain** the change with AST rules, SSA rules, dependency boundaries, complexity limits, and fail-fast policies.
5. **Prove** the result with focused tests, generated-artifact checks, and change-aware gates before it can be committed or pushed.

This replaces the fragile assumption that either a human or an AI must keep the whole codebase in memory.

### The Clean AI Loop: No "Full-Repo Scanning" Required

In traditional setups, developers feel forced to feed the entire codebase into the AI agent's context window. This blows the token budget, causes context saturation, and increases hallucinations.

Super Dolphin's self-guarding architecture creates a **clean, localized code modification loop** that operates under the "Zero-Knowledge" principle:
*   **Narrow Context Only**: Because the repository has compiler-enforced interfaces, clear boundary rules, and auto-updating project maps, an AI agent only needs to load the target file and its immediate contract interfaces.
*   **The Repository Guides the Agent**: If the AI attempts to violate architectural rules or introduce technical debt, the AST/SSA static gate will immediately block it and provide precise, compiler-grade diagnostics. 
*   **Automatic Self-Healing**: The agent reads the compiler diagnostic output, self-corrects the code in-place, and tries again. 

This means **the AI never needs to read the entire project** to make safe, production-grade changes. The codebase itself acts as the deterministic coordinator.


### Origin: Confronting AI Code Rot

Super Dolphin Agent began on March 19, 2026 as a clean-slate migration from `go-agent-v2`, our proprietary, closed-source prototype that combined automated quantitative trading with multi-agent desktop controls. The prototype had proved the product's value: agent sessions, tools, providers, events, recovery, and the desktop experience all worked. However, it was built entirely by AI agents using only *soft constraints* (Markdown guidelines and system prompts). 

Without hard, repository-enforced guardrails, successful local additions gradually destroyed global legibility. Over time, more than 80 RPC methods accumulated hand-written binding, validation, capability checks, logging, and parallel registration paths. Lifecycle control was scattered across multiple manager files, with asynchronous recovery side effects and an implicit side state machine. A central event handler grew to 557 lines; manual application assembly exceeded 200 lines. The code still ran, but answering "where is the authoritative behavior?" became progressively harder.

This is what we define as **AI Code Rot (or AI Entropy)**: not bad developers and not necessarily broken runtime output, but a system whose local parts still function while its global contracts, dependency boundaries, and code quality degrade. Fast AI iteration amplifies this failure mode because every plausible local patch can add another hidden path, eventually locking the repository in a state where AI agents hit context limits and can no longer make safe changes.

The original V3 decision rejected in-place surgery on the ~83,000-line prototype. The old system remained as behavioral evidence while capabilities were migrated function by function into explicit contracts. Super Dolphin is the result of turning those lessons into a public, open-source paradigm where the repository itself acts as the immune system:

| V2 entropy | Super Dolphin response |
|---|---|
| Hand-written RPC paths and scattered cross-cutting logic | typed requests, one contract surface, explicit middleware and error semantics |
| Lifecycle transitions and side effects spread across files | declarative state transitions, typed events, and owned lifecycle runners |
| Manual `New()` / `Close()` object graphs | `fx` composition with explicit startup and shutdown ownership |
| Business modules coupled to storage and adapters | onion boundaries, module-owned ports, and anti-corruption adapters |
| Giant mixed-abstraction functions | composed methods plus the `80 / 4 / 10` function, nesting, and complexity budget |
| Conventions remembered by reviewers | AST/SSA guards, maps, manifests, hooks, and reproducible evidence |

V2 is therefore not hidden history. It is the failure model against which Super Dolphin's governance is designed.

### Engineering Anti-Corruption

AI can produce code quickly; without hard boundaries, it can also produce architectural drift quickly. Super Dolphin treats this drift as **AI code rot** and converts it into machine-visible failures near the point where it is introduced.

| Anti-corruption layer | What it prevents | Repository evidence |
|---|---|---|
| Navigation truth | Editing the wrong subsystem or relying on stale mental models | `docs/doc/codemap`, project map, capability manifest |
| Architecture boundaries | Domain code importing stores, providers, UI, or command implementations | typed backend-boundary registry and AST import evaluation |
| Semantic guards | Ignored errors, silent fallback, unsafe lifecycle patterns, and wide orchestration seams | AST guards plus priority SSA analysis |
| Complexity budget | Giant functions that mix business flow, infrastructure, protocol, and persistence details | default effective function length `<= 80`, nesting `<= 4`, cyclomatic complexity `<= 10` |
| Ratcheted debt | A new change making known debt worse or “washing” debt by declaring a fresh baseline | production/test freeze partitions that reject regressions and shrink as code improves |
| Reproducible gates | A green claim without maps, tests, generated artifacts, or exact evidence | pre-commit/pre-push hooks and change-aware AI maintenance gates |

The 80-line limit is not a claim that every system should use the same number. It reflects this repository's orchestration-heavy workload, where composed methods and single-level abstractions are safer than monolithic procedures. The deeper rule is: **keep policy visible, details behind narrow interfaces, and exceptions explicit and measurable.**

### Why This Is Not Another Agent Framework

| Typical agent framework | Super Dolphin Agent |
|---|---|
| Optimizes task execution | Governs how tasks change a real software system |
| Gives agents more tools and context | Gives agents bounded context, capability contracts, and allowed dependency directions |
| Treats a completed run as success | Requires tests, diagnostics, generated-state checks, and Git evidence |
| Relies mainly on prompt discipline | Enforces invariants in code, tests, hooks, and generated manifests |
| Hides failures behind retries or defaults | Fails fast on missing configuration, invalid state, or broken dependencies |

```text
intent
  -> code map + capability contract
  -> scoped AI change through LSP/MCP
  -> AST/SSA/architecture guards
  -> focused tests + generated artifact checks
  -> reviewable evidence
  -> accepted commit
```

<!-- sd:architecture -->
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

<!-- sd:quick-start -->
## Quick Start

### Prerequisites

- Go 1.25.7
- SQLite is used by the store layer automatically. By default the database is
  created under `SUPER_DOLPHIN_HOME/super-dolphin.db`; set
  `SUPER_DOLPHIN_SQLITE_PATH` to use a different local file.
- Node.js 20+ (for frontend)
- OpenAI Codex CLI (`codex`) installed + authenticated — required for the current new UI desktop provider flow
- `gopls` (`go install golang.org/x/tools/gopls@latest`) for Codex Go navigation
- TypeScript language-server companions (`npm install -g typescript-language-server typescript@5.9.3`) for Codex JS/TS navigation
- Claude Code CLI (`claude`) installed + authenticated — only required for legacy/provider-integration work that explicitly targets Claude

### Clone & Setup

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
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

### Codex Git Worktree LSP Setup

After entering each newly created linked worktree, prepare and verify its local
LSP sidecar before starting implementation:

```bash
# Unix/macOS convenience target; on Windows use the Go command below directly.
make codex-worktree-ready
# Equivalent on every platform:
go run ./cmd/codex-worktree-setup ready
go run ./cmd/codex-worktree-setup verify
```

`ready` rebuilds `bin/mcp-lsp` from the current worktree and writes the managed
server block to this worktree's `.codex/config.toml`; both are ignored local
artifacts. It never reuses a binary or configuration from another checkout and
does not modify the global `~/.codex/config.toml`. Both commands fail fast when
required paths, language servers, configuration, or MCP tools are invalid.
`verify` also performs real `file(diagnostics)` calls against one Go file and
one frontend JavaScript file, so an executable-looking but unusable language
server fails readiness instead of producing a false PASS.

After `ready` and `verify` pass, start a **new Codex task** so Codex loads the
worktree-local MCP server, then confirm it is enabled:

```bash
codex mcp get lsp
codex mcp list
```

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

<!-- sd:governance-demo -->
## Reproducible Governance Proof

Inspect the exact gates selected for a change without executing them:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --base HEAD
```

Run the core anti-corruption surfaces directly:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

These checks validate architecture rules, AST/SSA guard behavior, generated code navigation, project-map drift, and the capability-contract manifest. They fail instead of silently refreshing stale truth. Use the explicit `*-refresh` targets only when the generated source of truth is intentionally being updated.

## Code Quality

| Metric | Value |
|--------|-------|
| Test Coverage | Recompute from current test run; no checked-in static baseline |
| Architecture Tests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Linter | golangci-lint (see .golangci.yml) |
| CI | GitHub Actions (see .github/workflows/ci.yml) |

### Git Hooks

`make install-hooks` sets `core.hooksPath` to `.githooks`, enabling automatic pre-commit and pre-push checks. Do not use `--no-verify` to turn a failed gate into a green claim. If the hook environment itself is blocked, record the exact blocker and repair or rerun the same gate explicitly.

## Code Map

Full code map: [`docs/doc/codemap/README.md`](docs/doc/codemap/README.md). Key sections:

- [Terminal Entry & UI Layer](docs/doc/codemap/01-terminal-ui.md)
- [MCP Orchestration](docs/doc/codemap/02-mcp-orch.md)
- [App Core & Contract Layer](docs/doc/codemap/04-app-contract.md)
- [Business Modules](docs/doc/codemap/07-module.md)
- [Platform Infrastructure](docs/doc/codemap/08-platform.md)
- [Provider Integration](docs/doc/codemap/09-provider.md)

<!-- sd:security -->
## Security

- Never commit credentials, provider homes, local databases, logs, user memory, or machine-specific configuration.
- Runtime configuration and missing dependencies follow the [fail-fast contract](docs/%E5%A5%91%E7%BA%A6/fail-fast-convention.md); silent fallback is treated as a defect.
- The public-source exporter reads committed Git objects through a default-deny policy and excludes private plans, archives, run evidence, local workspaces, and untracked files.
- Report sensitive vulnerabilities privately to the repository owner; do not include exploit details, secrets, or user data in a public issue.

<!-- sd:community -->
## Community and Contribution

Issues and focused pull requests are welcome. Keep changes small, preserve module boundaries, add same-commit regression tests for fixes, and run the gates that match the changed surface. Architecture decisions should be expressed as contracts and executable guards rather than prompt-only conventions.

Useful starting points:

- [Code map](docs/doc/codemap/README.md)
- [Architecture contracts](docs/%E5%A5%91%E7%BA%A6/README.md)
- [Project agent instructions](AGENTS.md)
- [Apache License 2.0](LICENSE)

## License

Licensed under the [Apache License 2.0](LICENSE).
