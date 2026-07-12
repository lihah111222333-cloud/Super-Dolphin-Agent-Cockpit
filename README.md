# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**The self-guarding repository for AI-written software.** AI agents implement changes; repository-owned maps, contracts, tests, and gates decide whether those changes are safe enough to keep.

> [!IMPORTANT]
> **Maintainer declaration: 100% AI-written original code and project-authored documentation, human-directed, repository-guarded.** Product code, test code, and project-owned documentation are written or refactored by AI agents. Humans retain ownership of product intent, architecture decisions, credentials, and releases. Authorship does not imply infallibility: every accepted change remains subject to repository-owned evidence and gates. Upstream legal and community texts retain their original attribution.

Super Dolphin Agent is an **AI-native software governance and multi-agent development control plane**. It combines a local desktop runtime, MCP orchestration, multi-language LSP navigation, provider integrations, persistent workflows, and machine-enforced engineering boundaries in one working reference implementation.

The English README is the canonical overview. Translations preserve the same product scope, commands, paths, environment variables, repository identity, and license. Detailed facts live in [Architecture](docs/open-source/ARCHITECTURE.md), [Governance in Action](docs/open-source/GOVERNANCE.md), and the generated [Code Map](docs/doc/codemap/README.md).

<!-- sd:why -->
## Why Super Dolphin Exists

Most agent frameworks optimize task execution. Super Dolphin also governs what a completed task is allowed to change in a long-lived software system.

Its maintenance loop has five stages:

1. **Orient** with generated code maps and capability contracts.
2. **Understand** definitions, references, call hierarchies, and diagnostics through LSP.
3. **Change** a narrow, explicitly owned surface.
4. **Constrain** the diff with AST/SSA rules, dependency boundaries, complexity budgets, and fail-fast contracts.
5. **Prove** the result with focused tests, generated-artifact checks, and change-aware gates.

### Bounded-context maintenance

The repository is designed so routine changes do not require loading the entire codebase into one model context. Generated navigation, narrow contracts, and deterministic failures help an agent find the relevant surface and repair violations quickly.

This is not a guarantee that every change is local. Cross-cutting work still requires broader reference and impact analysis, and all accepted changes remain subject to the relevant tests and review evidence.

### Origin: confronting AI code rot

Super Dolphin began on March 19, 2026 as a clean-slate successor to `go-agent-v2`, a private prototype that combined automated quantitative-trading workflows with multi-agent desktop controls. According to the maintainers' pre-publication records, the prototype worked, but soft constraints alone allowed its architecture to become progressively harder to reason about:

- more than 80 RPC methods accumulated parallel binding, validation, capability, and logging paths;
- lifecycle ownership became distributed across managers and asynchronous side effects;
- a central event handler reached 557 lines;
- manual application assembly exceeded 200 lines.

We call this **AI code rot**: local changes continue to work while global contracts, ownership boundaries, and legibility decay. The private history is not public evidence; the public repository instead exposes the resulting guards, regression fixtures, and reproducible commands.

| V2 failure mode | Super Dolphin response |
|---|---|
| Parallel hand-written RPC paths | Typed requests, one contract surface, explicit middleware and error semantics |
| Distributed lifecycle side effects | Declarative transitions, typed events, and owned lifecycle runners |
| Manual object graphs | `fx` composition with explicit startup and shutdown ownership |
| Business code coupled to adapters | Onion boundaries, module-owned ports, and anti-corruption adapters |
| Giant mixed-abstraction functions | A repository-specific `80 / 4 / 10` function, nesting, and complexity budget |
| Reviewer memory as policy | AST/SSA guards, generated maps, manifests, hooks, and reproducible evidence |

The `80 / 4 / 10` budget is not a universal style rule. It is a ratcheted constraint for this orchestration-heavy repository: default effective function length `<= 80`, nesting `<= 4`, and cyclomatic complexity `<= 10`.

### What the repository enforces

| Layer | Protects against | Repository evidence |
|---|---|---|
| Navigation truth | Editing the wrong subsystem or using stale project knowledge | `docs/doc/codemap`, project map, capability manifest |
| Architecture boundaries | Domain code reaching into stores, providers, UI, or command implementations | Typed backend-boundary registry and AST import evaluation |
| Semantic guards | Ignored errors, silent fallback, unsafe lifecycle paths, and wide service propagation | AST guards and priority SSA analysis |
| Complexity ratchets | New code increasing known structural debt | Function, nesting, complexity, production, and test freeze partitions |
| Acceptance evidence | Treating an agent's “done” status as proof | Focused tests, generated-state checks, Git hooks, and change-aware gates |

### History-backed cases

The maintainers report five pre-publication incidents that now have public regression evidence: wrong-worktree LSP scope, missing provider identity, missing persistent-agent runtime truth, silent asynchronous UI failures, and a type-alias bypass in an architecture guard.

Read the incident/evidence boundary and run every retained proof in [Governance in Action](docs/open-source/GOVERNANCE.md).

### Why this is not another agent framework

| Typical agent framework | Super Dolphin Agent |
|---|---|
| Optimizes task execution | Governs how tasks change a real software system |
| Gives agents more tools and context | Gives agents bounded context and allowed dependency directions |
| Treats a completed run as success | Requires tests, diagnostics, generated-state checks, and Git evidence |
| Relies mainly on prompt discipline | Enforces invariants in code, tests, hooks, and generated manifests |
| Hides missing state behind retries or defaults | Fails fast on missing configuration, identity, ownership, or dependencies |

<!-- sd:architecture -->
## Architecture

```text
frontend-app/             React/Vite desktop UI
        |
cmd/agent-terminal/       Wails host and RPC boundary
        |
internal/app/             composition and anti-corruption adapters
        |
internal/contract/        stable ports and DTOs
        |
internal/module/          business capabilities
   |             |
internal/store/   internal/provider/
SQLite/sqlc       Codex and provider runtime integration

cmd/mcp-lsp/              generic multi-language LSP peer
cmd/mcp-orch/             orchestration, DAG, cron, and agent tools
```

The key dependency rule is inward ownership: modules define the ports they need; adapters implement those ports; platform and provider packages must not import upward into business modules. The backend boundary registry is the single source used to generate the architecture rule map.

See [Architecture](docs/open-source/ARCHITECTURE.md) for component responsibilities, data flow, truth sources, and known scope. Use the generated [Code Map](docs/doc/codemap/README.md) for file-level navigation.

### Current scope

- The desktop application and its repository-specific governance loop are implemented here.
- `make guard` and the related checks govern this repository; they are not advertised as a general scanner for arbitrary repositories.
- The checked-in public-source policy and validation primitives are release-readiness foundations. A complete source-export CLI, sealed receipt workflow, public CI gate, and standalone guard distribution are not yet published capabilities.
- The canonical GitHub URL in this documentation is the publication target. Clone, issue, and private-reporting links become usable only after the repository owner completes the release checklist.
- Codex is required for the current desktop provider flow. Claude is used only by work that explicitly targets its provider integration.

<!-- sd:quick-start -->
## Quick Start

### Prerequisites

- Go 1.25.7
- Node.js 20+ and npm
- OpenAI Codex CLI (`codex`), installed and authenticated
- `gopls`
- `typescript-language-server` and TypeScript 5.9.3

The clone command below targets the canonical public repository and will work after publication. Existing maintainers should use their current authorized checkout until then.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

Run the current desktop development flow:

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

SQLite is created automatically under `SUPER_DOLPHIN_HOME/super-dolphin.db`. Set `SUPER_DOLPHIN_SQLITE_PATH` to use a different local file. PostgreSQL environment variables are not a product-database configuration path.

Build and test:

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

Contributors using linked Git worktrees must build and verify the worktree-local LSP peer before editing. See [Contributing](CONTRIBUTING.md#worktree-and-lsp-readiness) for the exact commands.

<!-- sd:governance-demo -->
## Reproducible Governance Proof

Inspect the gates selected for a known changed file without executing them:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

Run the repository's core governance checks:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

These commands validate architecture rules, guard behavior, generated navigation, project-map drift, and the capability manifest. They apply to this repository and fail on stale truth instead of silently refreshing it. Use explicit `*-refresh` targets only when the owning source has intentionally changed.

## Code Quality

| Metric | Value |
|--------|-------|
| Architecture Tests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Architecture rules | [Generated backend boundary map](docs/doc/codemap/13-archtest-boundaries.md) |
| Test coverage | Recompute from a current test run; no static percentage is claimed |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

<!-- sd:security -->
## Security

Do not commit credentials, provider homes, local databases, logs, user memory, or machine-specific configuration. Missing identity, ownership, configuration, or dependencies must fail closed rather than silently degrade.

Report vulnerabilities through the private process in [Security Policy](SECURITY.md). Do not put exploit details, secrets, trace payloads, or user data in a public issue.

<!-- sd:community -->
## Community and Contribution

Focused issues and pull requests are welcome. Start with:

- [Contributing Guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Roadmap](docs/open-source/ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Release Checklist](docs/open-source/RELEASE_CHECKLIST.md)

AI-assisted contributions are welcome, but the contributor remains responsible for the submitted diff, tests, security, licensing, and evidence. A generated answer or a passing agent run is not a substitute for repository gates.

## License

Licensed under [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for project and third-party attribution guidance.
