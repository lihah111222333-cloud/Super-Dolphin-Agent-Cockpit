---
name: designing-api-contracts
description: Use when designing, generating, reviewing, changing, or validating REST APIs, OpenAPI specs, gRPC or protobuf contracts, AsyncAPI event contracts, request and response DTOs, error codes, pagination, idempotency, versioning, webhooks, or backward compatibility.
---

# Designing API Contracts

## Overview

Design external contracts before implementation. A contract is a compatibility promise, so schema, status codes, error codes, pagination, idempotency, authentication, and versioning must be explicit before handlers or clients depend on them.

## Industry Baseline

Use these standards by protocol:

- REST/HTTP: OpenAPI 3.1, JSON Schema, RFC 7807-style problem details when suitable.
- RPC: Protocol Buffers with gRPC service definitions.
- Events and webhooks: AsyncAPI, CloudEvents-style envelopes when useful.
- Versioning: SemVer for public client packages and explicit API compatibility rules.
- Security: OWASP API Security Top 10 and least-privilege authentication scopes.

## Contract Location

Use this layout unless the repository establishes a stronger convention:

```text
api/
  openapi/
  proto/
  asyncapi/
docs/api/
internal/<context>/adapter/http/     # transport DTOs and mapping
internal/<context>/adapter/event/    # event DTOs and mapping
```

Do not place transport DTOs in `domain` or `app`. Domain models are not wire contracts.

## Required Design Rules

- Define the contract before writing or changing handlers.
- Keep request/response DTOs near the adapter that owns the protocol.
- Map transport DTOs to app commands/queries; do not pass framework request objects into app or domain code.
- Every endpoint or method must define authentication, authorization, idempotency, timeout, and error behavior.
- Use stable machine-readable error codes. Do not make clients parse human messages.
- Use cursor pagination for changing collections unless offset pagination is explicitly acceptable.
- Include request IDs or trace IDs in boundary errors and logs.
- Treat enum additions, field removals, type changes, and required-field changes as compatibility decisions.
- Generated clients, server stubs, docs, or mocks must be reproducible through `make generate` or a documented generator command.

## Compatibility Rules

| Change | Default Policy |
| --- | --- |
| Add optional response field | Usually backward compatible |
| Add required request field | Breaking |
| Remove or rename field | Breaking |
| Change field type or meaning | Breaking |
| Add enum value | Compatibility review required |
| Change status code or error code | Compatibility review required |
| Add endpoint | Usually backward compatible |
| Remove endpoint | Breaking |

## Verification

- Validate OpenAPI, protobuf, or AsyncAPI syntax with the chosen project tool.
- Run generated artifact drift checks through `make guard-commit`.
- Add contract tests for public handlers, clients, or event payloads.
- Run `make guard-change` after implementation and `make guard-commit` before handoff.

## Common Mistakes

- Starting from handler code and writing the contract afterward.
- Exposing database or domain structs directly as API JSON.
- Returning inconsistent error shapes across endpoints.
- Mixing transport validation with domain invariants.
- Changing a public contract without a migration or compatibility note.
