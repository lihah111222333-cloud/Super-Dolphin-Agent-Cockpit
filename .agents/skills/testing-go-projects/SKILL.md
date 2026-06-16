---
name: testing-go-projects
description: Use when designing, writing, reviewing, expanding, or debugging Go tests, test strategy, coverage policy, unit tests, integration tests, contract tests, fuzz tests, benchmarks, race tests, test fixtures, mocks, or CI test gates in this repository.
---

# Testing Go Projects

## Overview

Test behavior at the smallest stable boundary that proves the risk. Prefer fast unit and use-case tests first, then add adapter integration, contract, race, fuzz, benchmark, or end-to-end tests when the change crosses process, persistence, concurrency, or public API boundaries.

This skill owns test strategy. `writing-go-code` owns implementation style, `designing-go-architecture` owns package boundaries, and `guarding-go-projects` owns executable gates.

## Industry Baseline

Use these standards and practices as the baseline:

- Go `testing` package, table-driven tests, `t.Run`, `t.Helper`, `t.Cleanup`, and deterministic fixtures.
- Go fuzzing for parser, validator, codec, and boundary-input logic.
- Go race detector for concurrent code and lifecycle code.
- Contract tests for OpenAPI, gRPC/protobuf, AsyncAPI, webhooks, and event schemas.
- Test pyramid: many unit tests, focused integration tests, few end-to-end tests.
- Coverage as a risk signal, not a substitute for meaningful assertions.

## Test Selection Matrix

| Change Type | Required Test |
| --- | --- |
| Domain rule or value object | Table-driven unit test in `domain` |
| Application use case | Use-case test with fake ports, idempotency and error paths |
| Repository adapter | Integration test against real or containerized database when possible |
| HTTP/RPC adapter | Handler/transport test plus API contract validation |
| Event consumer/producer | Schema compatibility and idempotency tests |
| Concurrency, worker, shutdown | Race test, cancellation test, goroutine lifetime assertions |
| Parser, validator, codec | Unit tests plus fuzz test for malformed input |
| Performance-sensitive path | Benchmark with allocation checks when relevant |
| Bug fix | Regression test that fails before the fix |

## Required Rules

- Write tests in the package that owns the behavior. Use `_test` package only when proving public API behavior from outside the package.
- Prefer explicit fakes over broad mocking frameworks. Define test doubles near the test unless reused by many packages.
- Tests must not depend on wall-clock time, random order, external network, or real secrets.
- Use `context.WithTimeout` in tests that can block.
- Use `t.TempDir` for filesystem tests and `t.Setenv` for environment tests.
- Keep fixtures small. Store large golden files under `testdata/`.
- Do not assert on log text unless log output is the behavior being tested.
- Do not add coverage-only tests with no meaningful assertions.
- Do not make unit tests import adapters to avoid writing a fake port.

## Coverage Policy

- New domain and app logic should normally have direct tests.
- Adapters should test mapping, error translation, retries, idempotency, and resource cleanup.
- A global coverage threshold may be enabled with `GO_GUARD_COVERAGE_MIN`.
- Coverage exceptions must be documented in the task result or package docs.

## Verification

Run the smallest useful command first, then the project guard:

```bash
go test ./internal/<context>/...
go test -race ./...
make guard-change
```

Before commit or handoff:

```bash
make guard-commit
```

## Common Mistakes

- Testing implementation details instead of observable behavior.
- Reusing production adapters in application tests, which hides boundary violations.
- Skipping error paths, cancellation paths, and idempotency.
- Making integration tests silently depend on a developer's local machine.
- Treating coverage percentage as proof of correctness.
