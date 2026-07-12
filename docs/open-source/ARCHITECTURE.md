# Architecture

This document describes the public architecture of Super Dolphin Agent. It is an orientation guide, not a duplicate source of generated package or symbol facts. Use the [code map](../doc/codemap/README.md) for current file-level navigation and the [architecture contracts](../%E5%A5%91%E7%BA%A6/README.md) for enforceable conventions.

## Design Goal

Super Dolphin Agent is built for a development model in which AI agents write and refactor all original product code, test code, and project-authored documentation while humans retain product, security, credential, and release authority. Upstream legal and community texts retain their original attribution. The repository therefore has to provide three things at the same time:

1. a runtime that can execute and observe agent work;
2. a codebase that can be understood in bounded slices;
3. deterministic evidence that a change respected the repository's contracts.

The architecture favors narrow ports, explicit ownership, fail-fast errors, generated navigation, and executable boundary rules.

## System Overview

```text
                           +----------------------+
                           |  frontend-app        |
                           |  React / Vite UI     |
                           +----------+-----------+
                                      |
                           +----------v-----------+
                           | cmd/agent-terminal   |
                           | Wails host + RPC     |
                           +----------+-----------+
                                      |
                           +----------v-----------+
                           | internal/app         |
                           | composition/adapters |
                           +----------+-----------+
                                      |
                           +----------v-----------+
                           | internal/contract    |
                           | ports and DTOs       |
                           +----+------------+----+
                                |            |
                    +-----------v--+      +--v----------------+
                    | internal/    |      | internal/provider |
                    | module       |      | agent runtimes     |
                    +------+-------+      +--------------------+
                           |
                    +------v-------+
                    | internal/    |
                    | store        |
                    | SQLite/sqlc  |
                    +--------------+

  cmd/mcp-lsp  <---- code intelligence and worktree-scoped LSP
  cmd/mcp-orch <---- agent, DAG, cron, and orchestration tools
```

## Component Responsibilities

| Surface | Responsibility | Must not own |
|---|---|---|
| `frontend-app` | Current React/Vite user interface | Backend lifecycle, database handles, provider processes |
| `cmd/agent-terminal` | Desktop entrypoint, Wails host, frontend embedding, RPC boundary | Business rules that belong to modules |
| `internal/app` | Dependency injection and anti-corruption adapters | New product capabilities or persistence schemas |
| `internal/contract` | Stable cross-module ports, events, and DTO-facing contracts | Imports from modules, providers, stores, UI, or commands |
| `internal/module` | Product capabilities such as thread, turn, cron, memory, skill, and prompt behavior | Direct store implementation or database ownership |
| `internal/platform` | Shared runtime infrastructure such as RPC, configuration, events, process safety, and observability | Product-module or store dependencies |
| `internal/provider` | Codex and provider-specific runtime/transport integration | Product database ownership or direct store imports |
| `internal/store` | Persistence adapters and sqlc-backed storage | Business policy that belongs to a module |
| `cmd/mcp-lsp` | Generic multi-language LSP peer with workspace-scoped tools | Reusing state or binaries from a sibling worktree |
| `cmd/mcp-orch` | MCP tools for agent lifecycle, workflows, DAGs, and scheduling | UI state or provider-private persistence shortcuts |

## Dependency Direction

Business modules own the ports they consume. Store, provider, and runtime adapters implement or bridge those ports from `internal/app`. This keeps product behavior independent from a particular database, provider process, or UI host.

The intended direction is:

```text
entrypoints -> app composition -> contract-owned ports <- modules
                                      ^
                                      |
                         store/provider/platform adapters
```

The diagram is conceptual. The machine-readable source is the typed backend-boundary registry used by `internal/archtest`; its generated view is [13-archtest-boundaries.md](../doc/codemap/13-archtest-boundaries.md).

## Runtime Flows

### Desktop request

1. The React UI invokes a typed backend bridge.
2. The desktop RPC layer validates and translates the request.
3. A module or contract-facing application adapter owns the business operation.
4. Persistence and provider work occurs through injected ports.
5. Typed events and RPC responses update the UI.

Missing capability, identity, ownership, or configuration is an error. The runtime must not manufacture success by silently selecting a legacy path or empty default.

### Agent tool request

1. A provider session requests an allowed tool.
2. The toolbridge validates session and runtime ownership.
3. MCP peers execute code-intelligence or orchestration work inside the trusted workspace.
4. Results are returned with protocol-level errors preserved.
5. Relevant traces and evidence remain available to the local runtime and UI.

Worktree scope is part of the trust boundary. An LSP result from a sibling checkout is considered unsafe even when the result looks syntactically valid.

### Persistent workflow

Workflow and automation state is stored in SQLite. Runtime actors use explicit lifecycle ownership, optimistic concurrency, leases, and recovery rules rather than relying on an in-memory task alone. A persistent agent or DAG must not be reported as recoverable unless the required durable identity and runtime state exist.

## Repository Governance Loop

```text
generated map -> scoped inspection -> narrow change -> deterministic guards
      ^                                                   |
      |                                                   v
      +--------- refreshed truth <- accepted evidence <- tests
```

The loop is composed of:

- generated code maps and capability manifests for orientation;
- LSP definition, reference, hierarchy, read, and diagnostic evidence;
- AST rules for syntax and dependency invariants;
- SSA rules for value propagation and call-path invariants;
- repository-specific complexity and debt ratchets;
- change-aware gate planning;
- pre-commit and pre-push checks.

See [Governance in Action](GOVERNANCE.md) for concrete incidents and retained regression proofs.

## Sources of Truth

| Fact | Canonical source | Derived view |
|---|---|---|
| Backend dependency boundaries | `internal/archtest` typed registry | `docs/doc/codemap/13-archtest-boundaries.md` |
| File-level navigation | Tracked repository tree and project-map configuration | `docs/doc/codemap/project-map` |
| Go capability inventory | Source symbols and capability-contract generator | `docs/doc/codemap/capability-contract` |
| Runtime ports and DTOs | `internal/contract` | Contract and code-map documentation |
| Persistence shape | `migrations`, `sql`, and generated sqlc code | Store code map and SQL contracts |
| Public-source inclusion policy | `release/open-source-policy.json` | Future sealed public-source receipt |

Generated files must be refreshed through their owning generator. A stale generated file is a failed check, not permission to edit the output by hand.

## Failure Semantics

Super Dolphin follows a fail-fast contract:

- invalid or missing configuration blocks the affected operation;
- missing provider or persistent-agent identity does not fall back to shared ownership;
- swallowed asynchronous UI errors are rejected by frontend guards;
- dependency and lifecycle violations fail tests near the changed surface;
- a green status must name the command and evidence that produced it.

This reduces false completion. It does not eliminate defects, replace security review, or prove every possible runtime behavior.

## How to Navigate the Repository

Start with the smallest relevant map:

- [Code-map index](../doc/codemap/README.md)
- [Terminal and React UI](../doc/codemap/01-terminal-ui.md)
- [MCP orchestration](../doc/codemap/02-mcp-orch.md)
- [MCP LSP](../doc/codemap/03-mcp-lsp-ida.md)
- [Application and contracts](../doc/codemap/04-app-contract.md)
- [Business modules](../doc/codemap/07-module.md)
- [Platform](../doc/codemap/08-platform.md)
- [Providers](../doc/codemap/09-provider.md)
- [Stores and SQL](../doc/codemap/10-store.md)

Repository-local AI instructions are defined in [`AGENTS.md`](../../AGENTS.md). Contributors should also read [`CONTRIBUTING.md`](../../CONTRIBUTING.md).

## Current Scope and Limits

- The architecture and governance rules are tailored to this repository. A standalone general-purpose guard package is not currently published.
- Provider behavior depends on the installed provider CLI and its authenticated environment.
- Multi-language LSP depth depends on the language server available for each language.
- The checked-in public-source policy and validation primitives do not yet constitute an end-to-end public export, sealed receipt, or public release.
- A passing guard suite is evidence for encoded rules, not proof that the software is defect-free.
