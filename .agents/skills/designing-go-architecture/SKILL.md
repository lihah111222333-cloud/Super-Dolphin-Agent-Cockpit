---
name: designing-go-architecture
description: Use when designing or generating Go project architecture, package layout, bounded contexts, clean architecture, hexagonal architecture, onion architecture, dependency boundaries, import-cycle prevention, or AI-readable package responsibilities for a new or refactored Go codebase.
---

# Designing Go Architecture

## Overview

Design Go projects as a modular monolith by default: domain rules at the center, application use cases around them, adapters at the edge, and composition isolated in bootstrap. Optimize for simple imports, explicit package ownership, and code that another AI agent can safely extend.

## Default Architecture

Use this baseline unless the repository already has a stronger convention:

```text
cmd/
  api/                       # main.go only: start, stop, signal handling
internal/
  bootstrap/                 # dependency assembly; may import every internal module
  platform/                  # config, logging, db, tx, http server, metrics
  <bounded-context>/
    domain/                  # entities, value objects, domain services, invariants
    app/                     # use cases, commands, queries, transactions
      port/                  # small interfaces required by app use cases
    adapter/
      http/                  # handlers, request/response mapping
      postgres/              # repository implementations
      redis/                 # cache/lock implementations
      event/                 # message consumers/producers
api/                         # OpenAPI, protobuf, public schemas
configs/
migrations/
docs/architecture/
```

The dependency direction is:

```text
bootstrap -> adapter -> app -> domain
bootstrap -> platform
adapter -> platform
app -> app/port
```

`domain` must not import `app`, `adapter`, `platform`, database drivers, HTTP frameworks, queues, or configuration packages.

## Workflow

1. Inspect existing `go.mod`, `cmd/`, `internal/`, docs, and tests before proposing structure.
2. Identify bounded contexts from business language, not from transport or database tables.
3. Write `docs/architecture/package-map.md` before broad implementation. Include each package's responsibility, allowed imports, forbidden imports, and public entry points.
4. Add `doc.go` to every non-trivial package. State what the package owns and what it must not do.
5. Define ports in the consumer side (`internal/<context>/app/port`), not in adapters and not in a shared dumping ground.
6. Keep use cases small and command/query oriented. Put transaction boundaries in `app`, not handlers or repositories.
7. Keep adapters boring: translate protocols, call app services, implement ports, map persistence records.
8. Run the project guard before claiming any Go code or architecture change is valid.

For the full dependency matrix, anti-patterns, and package ownership rules, read `references/go-architecture-rules.md`.

## Design Rules

- Prefer one Go module and one deployable process until the user explicitly needs independent deployment.
- Prefer `internal/` for application code. Use top-level `pkg/` only for stable public libraries intended for external import.
- Do not create `common`, `utils`, `shared`, `models`, or `types` packages as a shortcut. Name packages by responsibility.
- Do not let handlers access repositories directly. Handler -> app use case -> port -> adapter implementation.
- Do not let one bounded context import another context's `adapter` or persistence package.
- Do not introduce framework types into `domain` or `app` APIs.
- Do not add an interface with only one vague method set before a real consumer needs it.
- Do not centralize all DTOs in one package. Keep request/response DTOs near the transport adapter and persistence models near the persistence adapter.
- Do not use import aliases to hide boundary violations.

## Verification

When Go code exists, run:

```bash
make guard-change
```

**Required companion skill:** Use `guarding-go-projects` for every Go code change, commit, merge, or release gate.

If the checker reports violations, fix package ownership instead of suppressing the result. If a real use case seems to require a forbidden import, update `docs/architecture/package-map.md` first and make the dependency direction explicit.
