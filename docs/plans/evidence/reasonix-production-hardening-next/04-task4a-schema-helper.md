# Task 4A MCP Schema Helper Evidence

## Scope

Task 4A implements only the schema execution boundary under
`internal/platform/toolbridge/schema` and the one-shot
`cmd/mcp-schema-compiler-helper` process. It does not decode raw `tools/list`,
publish surfaces, own authority, define `TrustedServerID`, or implement 4B
quarantine policy.

Base: `77d823c37`. Branch: `codex/reasonix-p0-task4a-schema-helper`.

## Frozen Boundary

- Parent and helper both perform strict JSON parsing, duplicate-key rejection,
  canonicalization, frozen budget scanning, local-reference enforcement and
  digest verification.
- `jsonschema/v6 v6.0.2` compiles the exact canonical bytes with a deny-all URL
  loader. Compile and validate each use one new helper process; there is no
  cache, pool, persistent worker, or background goroutine.
- Parent limits live helpers to 2, semaphore wait to 250 ms, total operation to
  2 seconds, stdout to 64 KiB, stderr to 16 KiB and helper soft memory to
  `GOMEMLIMIT=96MiB`.
- Cancellation and timeout terminate the Unix process group or Windows Job
  Object and always join `Wait`; reap is bounded to 1 second.
- The parent verifies protocol identity, authority generation, schema and
  compiled digests, and invokes the caller-owned fence before launch and after
  success. The hook does not invent an authority source.

## Fail-First Evidence

Before implementation, the named focused tests failed to compile because
`Canonicalize`, `NewClient`, protocol operations and stable diagnostic codes did
not exist.

The dynamic field guard was also proven fail-first by temporarily omitting the
real response producer's `draft` field. The guard failed twice with:

```text
consumer field "draft" is stale for producer schema.protocolResponse
```

After restoring the producer, compile and validate producer/consumer guards
passed. Allowed and required JSON fields are derived from the actual request and
response struct tags and emitted producer objects, not from handwritten field
lists.

## Verification

| Gate | Result |
| --- | --- |
| `go test ./internal/platform/toolbridge/schema -count=1` | PASS |
| Named budget/reference and cancellation/isolation tests | PASS |
| Strict unknown/duplicate/trailing protocol tests | PASS |
| Identity, digest, stdout/stderr cap, non-zero exit, total deadline and stale callback tests | PASS |
| Cancellation/deadline/capacity tests with `-count=10` | PASS |
| Focused code-size, naked-go, sidecar, boundary and timeout-locality archtests | PASS |
| Full `internal/archtest` after exact truth refresh | PASS |
| `test_with_guard --with-race` for schema package | PASS; production/test freeze 0, priority SSA violations 0 |
| `darwin/linux/windows` x `amd64/arm64`, CGO disabled | PASS compile |
| Dependency version/sums and NOTICE attribution | PASS |
| LSP locate/inspect/xref/read and final diagnostics on all changed Go files | PASS; 0 diagnostics at every severity |

The six-target gate proves buildability only. Native Unix process-group behavior
is covered on the host macOS lane; Linux and Windows native Job/process-group
reap tests remain release-lane responsibilities.

## Review D01-D19

| Dimension | Task 4A result |
| --- | --- |
| D01 | Toolbridge schema package owns admission execution; exact command/import boundary registered. |
| D02 | Missing, unknown, duplicate, oversize, stale and process failures fail closed. |
| D03 | Strict one-request/one-response protocol with stable codes. |
| D04 | N/A; no LSP product behavior changed. |
| D05 | N/A; no provider/runtime integration. |
| D06 | N/A; no orchestration state. |
| D07 | N/A; no store/sqlc change. |
| D08 | N/A; no skill/memory/prompt/thread change. |
| D09 | N/A; no frontend change. |
| D10 | Deny-all external loader, no helper network/filesystem schema loading, dual fence hook. |
| D11 | Stable codes and bounded messages with request/server/tool/generation correlation. |
| D12 | Named budget, cancellation, protocol, digest, cap, exit, timeout and stale tests. |
| D13 | Six target helper and package compile passed. |
| D14 | Bytes, values, depth, objects, arrays, refs, regex, concurrency, deadline and reap are bounded. |
| D15 | N/A; no UI behavior. |
| D16 | Changes remain in the authorized Task 4A package, helper, exact arch truth, dependency, maps and evidence. |
| D17 | Dynamic producer-derived request/response field guard passes. |
| D18 | One schema compiler owner; existing toolbridge validators were not modified. |
| D19 | Canonical bytes and SHA-256 are schema identity; authority remains caller-owned through the fence hook. |

## Residual Risk

- `GOMEMLIMIT` is a soft Go memory limit, not a hard RSS guarantee.
- Cross-compilation does not prove Linux or Windows native termination/reaping;
  those checks belong in their packaging lanes.
- Task 4B must provide the real authority source and integrate raw decode,
  quarantine and surface publication without weakening either fence.
