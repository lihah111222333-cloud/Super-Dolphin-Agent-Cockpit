---
name: operating-go-services
description: Use when designing, implementing, reviewing, or debugging Go service runtime behavior, configuration, logging, metrics, traces, health checks, readiness, graceful shutdown, background workers, operational errors, runbooks, alerts, or OpenTelemetry instrumentation.
---

# Operating Go Services

## Overview

Build services so they can be operated under failure. Runtime code must expose useful health, logs, metrics, traces, configuration state, and shutdown behavior without leaking secrets or mixing operational concerns into domain logic.

## Industry Baseline

Use these practices as the default:

- OpenTelemetry for traces, metrics, and logs when instrumentation is needed.
- Structured logging through `internal/platform/logging`, backed by `log/slog` by default.
- `/healthz` for process liveness and `/readyz` for dependency readiness when HTTP is present.
- Context cancellation and deadlines for I/O and background work.
- Twelve-Factor-style environment-aware configuration without committing secrets.
- Prometheus-compatible metrics naming when metrics are exported.

## Runtime Boundaries

- `domain`: no logging, metrics, tracing, environment access, goroutines, or wall-clock access.
- `app`: may accept context and return typed errors/events; avoid direct logging except explicit worker ownership boundaries.
- `adapter`: owns protocol and dependency mapping, boundary logs, retries, and status/error translation.
- `platform`: owns logger, config loader, telemetry setup, server lifecycle, database pools, and metrics exporters.
- `bootstrap`: wires runtime dependencies, starts processes, handles signals, and coordinates graceful shutdown.

## Required Rules

- Log boundary outcomes once. Do not log and return the same internal error repeatedly.
- Include stable fields such as `request_id`, `trace_id`, `operation`, `component`, and safe business identifiers.
- Never log secrets, tokens, passwords, private keys, raw credentials, or sensitive payloads.
- Use context-aware APIs for I/O and long-running work.
- Every background worker must define ownership, start/stop lifecycle, cancellation, retry, and panic recovery policy.
- Startup must validate configuration before serving traffic.
- Readiness must fail when required dependencies are unavailable.
- Graceful shutdown must stop accepting new work, cancel workers, flush telemetry, and close resources.
- Operational runbooks should document common failure signals and recovery steps once the service exists.

## Metrics And Alerts

Prefer low-cardinality metrics:

- Request count, latency, and error count by route group/status class.
- Worker queue depth, processed count, retry count, and failure count.
- Database pool usage and query latency when relevant.
- External dependency latency, timeout, and circuit state when relevant.

Avoid user IDs, emails, order IDs, raw paths, or unbounded labels in metrics.

## Verification

For runtime changes:

```bash
go test ./...
go test -race ./...
make guard-change
```

Before handoff:

```bash
make guard-commit
```

Use focused runtime probes once an executable service exists.

## Common Mistakes

- Adding logs inside domain code.
- Logging full errors at every layer.
- Treating liveness and readiness as the same check.
- Starting goroutines without a cancellation and shutdown path.
- Adding high-cardinality metric labels that make production metrics unusable.
