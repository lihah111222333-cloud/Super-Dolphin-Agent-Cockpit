# Task 4B MCP Quarantine And Authority Evidence

## Scope And Verdict

Task 4B completes only the Codex v3-owned toolbridge path:

`raw tools/list -> strict identity -> class -> Task 4A compile -> quarantine/current-CAS -> surface/call`.

It also repairs the config-owner `TrustedServerID` producer chain through thread
start/resume and turn manifest assembly. It does not implement Claude readiness,
provider-wide executable hardening, generic RPC/admission, release behavior, or
a resident compiler/cache.

Base: `7ccd542bc`. Branch: `codex/reasonix-p0-task4b-mcp-quarantine`.
Verdict: Task 4B lane PASS. This is not a repository-wide PASS and does not
authorize integration merge.

## Frozen Runtime Boundary

- HTTP and stdio `ListTools` decode only the JSON-RPC envelope and tools array;
  every item remains a `json.RawMessage` until identity admission.
- Identity is decoded directly from `MCPTool.RawJSON()`. Duplicate keys,
  unknown fields, missing or non-string names, whitespace names and duplicate
  tool names fail before authority issue or compilation.
- Managed identity requires the unexported in-process marker and the managed
  runtime policy. A production AST guard scans the whole repository and
  requires every `NewManagedMCPBinary` selector/identifier reference to be a
  direct `CallExpr.Fun` inside
  `internal/contract/manifest.go:BuildManifest`; it does not rely on a frozen
  call count. JSON/config round trips, local/package function-value aliases and
  `ExtraBinaries` cannot set the marker.
- Trusted external identity requires the config-owner-produced
  `TrustedServerID`, exact server name and exact HTTP/stdio config parity.
  Name, URL and raw trusted ID cannot independently elevate authority.
- Task 4A `schema.Canonicalize` and the one-shot helper are the only compiler
  path. The helper path is the absolute local artifact
  `<ProjectRoot>/bin/mcp-schema-compiler-helper[.exe]`; there is no PATH,
  relative-path or in-process fallback.
- Managed/first-party schema error revokes the old surface and fails the whole
  generation. Trusted external schema errors quarantine only the bad tools.
- Quarantine commit and surface replacement execute under one owner current-CAS.
  Publish and call both recheck current authority; a stale result performs no
  publish, quarantine write, validator execution or MCP client call.

## Owner State Decision

Task 4B uses owner-aligned process-local current generation and quarantine state,
not cross-process durable state. The implementation and plan no longer claim
durability.

This is fail-closed across restart: surfaces and owner state are both process
memory, so restart exposes no old surface; a fresh owner rejects every old
generation token. A server becomes callable only after a new tools fetch,
strict identity pass, Task 4A compile and exact current-CAS publish. Persisting
quarantine history across restart is therefore not required for this lane.

Authority identity is the current tuple of `CWD`, `ServerID`, `Generation`,
`MembershipDigest`, `ConfigDigest` and managed provenance. Per-tool schema digest
is derived from strict raw membership and canonical schema bytes.

## Fail-First Evidence

The named Task 4B tests were introduced against the missing integration and
failed before the raw/authority/quarantine chain existed. They cover mixed
external and managed lists, all stale fence positions, repair/re-break,
HTTP/stdio parity, helper cancellation/oversize and concurrent executor use.

The real `TrustedServerID` producer chain was mutated by removing its terminal
selector consumption. The dynamic guard failed with producer identity:

```text
chain=trusted-server-id producer=contract.MCPServerConfig field=TrustedServerID: consumer copyTurnMCPServerConfigs uses field 1 times, want at least 2
```

Every actual authority, quarantine and compiled-schema producer was then
mechanically mutated by adding an unconsumed field derived from that producer's
AST. Each mutation failed with the same structured identity, for example:

```text
chain=authority-token producer=MCPToolAuthority field=MutationUnconsumed: consumer internal/module/mcp_server/authority_owner.go: uses field 0 times, want at least 1
```

The managed provenance fixture also injects an illegal `cmd` direct call plus
local and package function-value aliases; all are rejected before GREEN
verification.

## Dynamic Field And Provenance Guards

- `TestTrustedServerIDProducerFieldGuard` derives the field and JSON tag from
  the real `contract.MCPServerConfig` producer, then checks the provider DTO
  and actual HTTP/stdio thread/turn copy functions.
- `TestMCPAuthorityQuarantineCompiledSchemaFieldGuard` reflects all fields of
  the real authority issue, authority token, quarantine commit and canonical
  schema types. It AST-enumerates private owner/surface state and locks aggregate
  current comparison, helper handoff, quarantine CAS and call recheck.
- `TestManagedMCPBinaryProductionOwnerGuard` scans every production Go file;
  every constructor reference must be a direct call in the unique owner
  function. Mutation fixtures prove `cmd` calls and aliases fail.
- `TestTask4BChangedFieldGuardMutationFixtures` derives each real producer's
  fields from AST, inserts an unconsumed field, and requires errors to name the
  chain, producer and field.

No handwritten field list is used as producer truth.

## Behavioral Tests

- `TestTask4BToolsListRawDecodeParity` and non-array rejection prove HTTP/stdio
  raw parity and strict transport boundaries.
- `TestTask4BRawIdentityRejectsAmbiguousObjects` proves duplicate, unknown,
  missing and type-conflicting identity rejection.
- `TestTask4BMixedTrustedExternalQuarantinesOnlyBadTool` proves good external
  tools remain callable while bad tools do not enter surface/catalog/proxy/call.
- `TestTask4BMixedManagedFailsFastAndWithdrawsOldSurface` proves whole-server
  fail-fast and old-surface revocation.
- `TestTask4BAuthorityStaleBeforeCompileAfterSuccessAndPublish` and
  `TestTask4BAuthorityStaleBeforeCallSkipsValidatorAndClient` cover every
  required stale position.
- `TestTask4BRepairThenRebreakUpdatesSurfaceAndQuarantine` proves restore then
  withdrawal.
- `TestTask4BHelperCancelAndOversizeHaveNoQuarantineSideEffect` proves bounded
  failures have no quarantine side effect.
- `TestTask4BSchemaExecutorConcurrentCallsAreRaceFree` plus the race gate
  proves immutable construction and concurrent execution.
- Owner tests prove reserved-name forgery rejection, real `BuildManifest`
  acceptance, external config/membership digest fencing, monotonic generation,
  stale/current CAS behavior and restart fail-closed.

## LSP Evidence

| Evidence class | Result |
| --- | --- |
| Locate | `grep` located raw decode, authority issue/CAS, TrustedServerID producers and constructor calls. |
| Understand | `inspect(definition)` resolved the authority port to `internal/contract/mcp_control.go` and producer types. |
| Impact | `xref(references/call_hierarchy)` confirmed toolbridge, mcp_server and test consumers, including all managed constructor references. |
| Precise read | `file(read_file)` read exact owner functions, thread/turn copies, raw decoder, admission and call fences. |
| Edit/diagnostics | LSP format/diagnostics covered edited Go files; final diagnostics are required to be zero at all severities. |

## Verification

| Gate | Result |
| --- | --- |
| Affected contract/mcp_server/thread/turn/toolbridge via `test_with_guard` | PASS; archtest included |
| Toolbridge and mcp_server with `-race` via `test_with_guard` | PASS |
| Dynamic TrustedServerID, authority/quarantine/schema and managed-caller guards | PASS |
| Production/test freeze, priority SSA and code-size guard | PASS; zero new violations |
| `darwin/linux/windows x amd64/arm64`, `CGO_ENABLED=0`, affected package build | PASS |
| `make codemap-check project-map-check capcontract-check` after generator refresh | PASS |
| `git diff --check` | Pending final pre-commit pass |
| `pre-commit` | Pending final pre-commit pass |
| LSP diagnostics on every changed Go file | PASS; zero diagnostics at every severity |

## Review D01-D19

| Dimension | Task 4B result |
| --- | --- |
| D01 | Toolbridge owns admission/surface/call; mcp_server config owner owns current/quarantine CAS. |
| D02 | Raw ambiguity, missing authority, stale generation, schema and helper failures fail closed. |
| D03 | HTTP/stdio share strict envelope/tools-array raw decode and preserve per-item bytes. |
| D04 | Product LSP behavior is unchanged; required implementation LSP workflow is evidenced above. |
| D05 | Scope is Codex v3 toolbridge only; Claude readiness is excluded. |
| D06 | No orchestration state or generic admission mechanism was added. |
| D07 | No store/sqlc change; process-local owner state and restart fail-closed rationale are explicit. |
| D08 | TrustedServerID is preserved through config owner, thread start/resume and turn assembly. |
| D09 | N/A; no frontend change. |
| D10 | Managed provenance guards every constructor reference and rejects aliases; external name/URL/raw ID cannot self-elevate. |
| D11 | Errors retain server/tool/generation/schema context without exposing config secrets. |
| D12 | Named mixed, stale, parity, repair, resource, restart and concurrency tests pass. |
| D13 | Six-target affected package compilation passes. |
| D14 | Task 4A bounds remain intact; cancel/oversize produce no Task 4B side effect. |
| D15 | N/A; no UI behavior. |
| D16 | Changes stay within raw DTO/transport, config owner, thread/turn propagation, toolbridge and evidence. |
| D17 | Real producer-derived fields and private state are dynamically guarded with mutation RED. |
| D18 | Task 4A remains the sole compiler; no fallback, resident worker or cache was added. |
| D19 | Current authority uses owner generation/config/membership identity; canonical SHA-256 remains schema identity. |

## Residual Risk

- Quarantine history is intentionally not retained across process restart; the
  restart path is fail-closed and recompiles before republishing.
- Cross-compilation proves buildability only. Native Linux/Windows process
  termination behavior remains in the packaging/release verification lanes.
- Lane PASS still requires main-agent review and integration acceptance; this
  branch must not merge integration itself.
