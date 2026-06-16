---
name: writing-go-code
description: Use when writing, editing, refactoring, reviewing, testing, documenting, or generating Go source code, Go tests, package comments, exported APIs, error handling, concurrency code, domain logic, application use cases, adapters, or platform code in this repository.
---

# Writing Go Code

## Overview

Write Go that is small, explicit, documented, testable, and aligned with the repository architecture. This skill owns coding style and implementation discipline; architecture boundaries belong to `designing-go-architecture`, and executable validation belongs to `guarding-go-projects`.

## Required Companion Skills

- Use `designing-go-architecture` when adding packages, changing dependencies, or touching domain/app/adapter/platform boundaries.
- Use `guarding-go-projects` after every Go code edit and before commit, merge, or release claims.

## Coding Workflow

1. Read nearby package docs, tests, interfaces, and existing naming before editing.
2. Keep the change in the owning package. Do not move logic across architecture layers to make code easier locally.
3. Write or update tests with the behavior change.
4. Add comments where they carry design intent, domain invariants, exported API meaning, concurrency rules, or non-obvious error handling.
5. Keep functions short and single-purpose. Extract only when the extracted name improves the reader's understanding.
6. Return explicit errors with context. Do not panic outside startup, tests, or impossible invariant violations.
7. Run `make guard-change` after edits.

## Comment Policy

Comments are required, but they must explain useful information. Do not add noise comments that restate obvious syntax.

Required comments:

- Every package needs a package comment, usually in `doc.go`, describing ownership and forbidden responsibilities.
- Every exported type, function, method, const, and var needs a Go doc comment starting with the exported identifier.
- Domain entities and value objects need comments for invariants and valid states.
- Use cases need comments for transaction boundaries, idempotency, permissions, and side effects.
- Ports need comments explaining why the application layer owns the interface and what implementations must guarantee.
- Concurrency code needs comments for goroutine lifetime, cancellation, ownership, locking order, and channel close rules.
- Security-sensitive code needs comments for trust boundaries, validation assumptions, and secret handling.

Avoid comments like `// increment i`, `// set name`, or `// return error`. Prefer comments that explain why the code exists or what contract it protects.

## Implementation Rules

- Prefer simple structs and functions over clever abstractions.
- Accept `context.Context` as the first parameter for operations that can block, call I/O, or cross process boundaries.
- Keep domain code pure: no HTTP, SQL, logging, config, env, time.Now, randomness, goroutines, or external clients.
- Keep application code responsible for orchestration, transactions, policy checks, and port calls.
- Keep adapters responsible for protocol/persistence mapping only.
- Return errors through internal layers; log them only at process/protocol boundaries or background worker ownership boundaries.
- Use `fmt.Errorf("operation: %w", err)` when adding context to an error. Do not use `errors.New(fmt.Sprintf(...))` or `%v` for wrapped errors.
- Use the project logger abstraction from `internal/platform/logging`; default concrete implementation should wrap standard `log/slog`.
- Do not call `fmt.Print*`, `log.Print*`, `log.Fatal*`, `slog.*`, `panic`, or `os.Exit` in domain/app code.
- Prefer constructor functions when invariants must be checked.
- Keep interfaces small and define them in the consumer package.
- Prefer table-driven tests for rule matrices and focused tests for use cases.

For the detailed standards, read `references/go-code-standards.md`.

## Completion Rule

Before claiming Go code is complete, run:

```bash
make guard-change
```

If the change is being committed or handed off, run:

```bash
make guard-commit
```
