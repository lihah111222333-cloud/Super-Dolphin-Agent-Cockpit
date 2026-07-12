# Roadmap

This roadmap separates implemented repository behavior from release-readiness work and future direction. It intentionally contains no delivery dates or release promises.

## Implemented in the Development Repository

- Wails desktop host with the current React/Vite frontend.
- Codex-oriented provider runtime plus explicit provider integration surfaces.
- MCP orchestration and generic multi-language LSP peers.
- SQLite-backed threads, turns, memory, automation, and workflow state.
- Persistent DAG execution with explicit lifecycle and recovery behavior.
- Provider-native skill discovery and mirror validation.
- Generated code maps and capability-contract inventory.
- Repository-specific AST/SSA architecture guards, complexity budgets, and debt ratchets.
- Change-aware local gates and Git hooks.
- Apache-2.0 project identity and six-language README entrypoints.

Implemented does not mean universally supported. Platform, provider, and language-server prerequisites are documented in the [README](../../README.md) and [Architecture](ARCHITECTURE.md).

## Being Validated for an Open-Source Release

- Complete community, contribution, support, security, and conduct documentation.
- A deterministic public-source candidate built from committed Git objects under a default-deny policy.
- Public-profile code maps that cannot reference private development material.
- A sealed, reproducible export receipt and independent verification command.
- A minimal public CI workflow with pinned dependencies and least privilege.
- Clean-machine onboarding evidence for supported desktop development flows.
- Reproducible governance demonstrations that invoke the same rule engine used by the repository.

These items are release gates. They must not be described as shipped capabilities until their commands and checks exist and pass.

## Future Direction

- Make selected governance rules easier to adopt outside this repository without requiring the desktop application.
- Publish a versioned corpus of minimized AI code-rot cases, including misses and false positives.
- Produce auditable per-release maintenance receipts covering agent attribution, gate results, overrides, and unresolved blockers.
- Improve provider parity while preserving provider identity and ownership boundaries.
- Expand verified language-server readiness beyond the currently probed languages.
- Improve contributor-facing diagnostics and PR evidence without weakening local fail-fast gates.

Future direction is exploratory. Inclusion here is not a compatibility, schedule, or support commitment.

## How Priorities Are Chosen

Priority favors work that:

1. prevents false success or loss of ownership;
2. reduces the context required for a safe change;
3. turns a discovered bypass into a durable regression fixture;
4. improves reproducibility for external contributors;
5. preserves one source of truth instead of adding a parallel declaration.

Feature requests should describe the concrete problem, affected workflow, governance impact, and evidence that would prove the outcome. See [Contributing](../../CONTRIBUTING.md) and [Support](../../SUPPORT.md).
