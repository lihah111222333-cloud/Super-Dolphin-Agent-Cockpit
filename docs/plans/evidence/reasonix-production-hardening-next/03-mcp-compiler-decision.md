# Reasonix P0 MCP Schema Compiler Decision

TASK_ID: `TASK_0_MCP_COMPILER_DECISION`

DATE: `2026-07-16`

REVIEW_OBJECT: `codex/reasonix-p0-mcp-decision@3d6fccfc58b904e2c9a6f358285cdee6d6ea7753`

STATUS: `DONE_WITH_EVIDENCE`

DECISION: `ONE_SHOT_LOCAL_HELPER_PROCESS`

`mcp_design_complete=true`

`p0_mcp_executable=true`

## 1. Decision

Task 4 必须使用独立本地 helper `cmd/mcp-schema-compiler-helper`。每次
`compile` 或 `validate` 请求启动一个新进程，处理一条请求后退出；禁止把
compiler 放进 desktop 进程，禁止常驻 worker、进程池内 cache、后台 goroutine
或失败后进程内重试。

候选 `github.com/santhosh-tekuri/jsonschema/v6@v6.0.2` 可作为 helper
内部 compiler，但不能作为进程内 compiler。其 `Compile(loc string)` 和
`URLLoader.Load(url string)` 均没有 `context.Context`，源码编译循环也没有
可中断检查或公开的 CPU/heap hard limit。调用方 context cancellation 不能证明
可停止编译；该能力结论为 **不可证明**，因此停止继续比较并采用进程边界。

## 2. Candidate Facts

| Item | Evidence | Verdict |
| --- | --- | --- |
| Version | GitHub latest release 与 Go module 均为 `v6.0.2`，tag commit `29cbed948d24a04700eb94436416b18a07953b71` | pin exact version |
| Module sums | module `h1:KRzFb2m7YtdldCEkzs6KqmJw4nqEVZGK7IN2kJkjTuQ=`; go.mod `h1:JXeL+ps8p7/KNMjDQk3TCwPpBy0wYklyWTfbkIzdIFU=` | record in Task 4 dependency change |
| License | Apache-2.0 | compatible subject to existing NOTICE/SBOM owner |
| Drafts | Draft 4, 6, 7, 2019-09, 2020-12 | P0 freezes Draft 2020-12; exact built-in `$schema` URI may select another supported draft |
| Loader | `UseLoader(URLLoader)`; `URLLoader.Load(string)` has no context | install deny loader in helper |
| Compile API | `Compile(string) (*Schema, error)`; no context/deadline argument | in-process cancellation unavailable |
| Resource cap | no documented per-compile time, allocation, node, depth, ref or regex cap | hard bound **不可证明** |
| Vulnerabilities | `govulncheck@v1.6.0 ./...`: 0 symbol/package vulnerabilities in the candidate test; scanner separately reported 23 unreachable local stdlib module advisories | no candidate advisory found by this scan; not a future-safety guarantee |

Primary sources:

- [v6.0.2 release](https://github.com/santhosh-tekuri/jsonschema/releases/tag/v6.0.2)
- [v6.0.2 package API](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6@v6.0.2)
- [compiler source](https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.2/compiler.go)
- [loader source](https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.2/loader.go)
- [Apache-2.0 license](https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.2/LICENSE)

## 3. Local Reproduction

Temporary module: `/tmp/reasonix-mcp-schema-candidate`; it is outside the repo
and does not change repository `go.mod` or `go.sum`.

| Check | Command | Result |
| --- | --- | --- |
| local compile, deny external loader, non-cancellation | `go test -count=5 -run 'Test(LocalCompileAndExternalReferenceDeny|ContextCancellationCannotInterruptCompile)$' -v` | PASS 5/5; cancelled caller context did not stop blocked `Compile` |
| bounded-shape compile benchmark | `go test -run '^$' -bench '^BenchmarkCompile2048Properties$' -benchtime=20x -count=3 -benchmem` | 20.46-20.78 ms/op, about 16.0 MB allocations/op on Apple M3 Pro |
| vulnerability query | `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 -show verbose ./...` | candidate affected 0; 23 unreachable stdlib module advisories reported |
| six-target compile | `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go test -c .` | PASS for darwin/linux/windows x amd64/arm64 |

Temporary source SHA-256 is
`eff72404c398e94d0fbe8731ee3b91ee36ca1b952e85e156f4632a9956ac25bd`;
`go.mod` is
`fdca3b036ac54b5b115b14950e60c4d9abd5cdc657a4aee71f3e3b3b13bce271`.
This temporary test is evidence, not a production test or committed dependency.

## 4. Frozen Pre-scan And Canonicalization

Parent-side pre-scan runs before helper launch and fails closed. The helper repeats
the same checks before calling the library. Neither side may silently truncate.

| Budget or rule | Frozen value |
| --- | --- |
| raw input schema | max 256 KiB |
| canonical schema | max 256 KiB |
| decoded JSON values | max 8,192 |
| nesting depth | max 64 |
| object members | max 4,096 total; max 2,048 in one object |
| array elements | max 4,096 total; max 2,048 in one array |
| reference keywords | max 256 total |
| regex-bearing strings | max 128; max 4 KiB each; max 32 KiB cumulative |
| one JSON string | max 16 KiB; all JSON strings max 192 KiB |
| one numeric lexeme | max 128 bytes |
| root | JSON object with effective `type: "object"`; boolean schemas and non-object roots rejected |

The scanner rejects duplicate object keys and wrong types for recognized
structural keywords before canonicalization. The top-level `type` must exist
and be exactly the string `"object"`. `$ref`, `$dynamicRef` and
`$recursiveRef` may only be exactly `#` or begin with `#/` as local JSON
Pointer references. Input `$id`/legacy `id`, custom `$schema`, `$vocabulary`,
unknown external schemes and relative document references are rejected.
`$schema` is optional; if present it must exactly match a built-in URI for the
five supported drafts. The helper installs a deny-all URL loader as defense in
depth and performs no network or filesystem reads.

Canonical bytes use deterministic object-key ordering, preserved array order,
JSON number lexemes, UTF-8 validation and no insignificant whitespace.
`schema_digest = sha256(canonical_schema)`; this digest is the only schema
identity consumed by catalog, surface, quarantine and call validation.

## 5. Frozen Helper Protocol

Protocol ID: `reasonix.mcp-schema-helper/v1`. Transport is one strict JSON object
on stdin followed by EOF and one strict JSON object on stdout followed by process
exit. Unknown/missing/duplicate fields, stdout prefix/suffix bytes, multiple
responses and non-zero exit after a success envelope are protocol violations.
Logs may use stderr only.

Request fields:

`protocol, operation, request_id, server_id, tool_name, authority_generation,
schema_digest, draft, canonical_schema`; `arguments` is required only for
`validate`. `operation` is exactly `compile` or `validate`.

Response fields:

`protocol, operation, request_id, server_id, tool_name, authority_generation,
draft, schema_digest, compiled_digest, ok, code, message`; `validate` also
returns `arguments_valid`. The helper echoes every request identity field.
On success, `compiled_digest` must equal
`sha256(canonical_schema)`; an error response leaves it empty.

The helper does not return or persist a compiled object. `compile` attests that
the exact canonical bytes compile. `validate` recompiles those exact bytes and
validates the supplied arguments in the same one-shot process. This is slower
than a cache but keeps cancellation, generation fencing and cleanup mechanical.

| Boundary | Frozen value |
| --- | --- |
| full stdin envelope | max 384 KiB |
| arguments | max 64 KiB |
| stdout | max 64 KiB |
| stderr captured by parent | max 16 KiB |
| returned message | max 4 KiB |
| global live helpers | max 2 per desktop process |
| semaphore wait | max 250 ms; no unbounded queue |
| compile/validate wall deadline | 2 seconds from spawn request through stdout EOF and normal exit |
| parent cancellation | immediate termination path |
| kill-to-`Wait` reap deadline | 1 second |
| helper Go soft memory limit | `GOMEMLIMIT=96MiB`; not reported as a hard RSS guarantee |

On Unix the child is placed in a new process group; timeout/cancel sends
`SIGKILL` to that group and the parent must call `Wait`. On Windows it is
assigned to a Job Object with `KILL_ON_JOB_CLOSE`; timeout/cancel terminates the
job and waits for the process handle. A reap timeout is fatal for that MCP server
generation: no surface publish, no client call, and `MCP_SCHEMA_REAP_FAILED`.

## 6. Generation And Digest Fence

Before launch, the parent reads config-owner current authority and binds
`server_id + authority_generation + membership + schema_digest`. After a
successful helper exit and before any publish/quarantine write/call, it reads
current authority again. The result is accepted only when:

1. response protocol/request/server/tool/generation exactly match the request;
2. response `schema_digest` and `compiled_digest` both equal the request
   digest and a parent recomputation;
3. server is still enabled, current and contains the same tool membership;
4. compile result belongs to the exact current generation.

Mismatch returns `MCP_SCHEMA_GENERATION_STALE` or
`MCP_SCHEMA_DIGEST_MISMATCH` and has zero publish, quarantine-write and client
side effects. Helper success never creates authority.

## 7. Stable Error Codes

| Code | Meaning |
| --- | --- |
| `MCP_SCHEMA_INVALID_ENVELOPE` | malformed, duplicate, unknown or missing protocol field |
| `MCP_SCHEMA_INPUT_TOO_LARGE` | schema/request/arguments input cap exceeded |
| `MCP_SCHEMA_OUTPUT_TOO_LARGE` | stdout/stderr/message cap exceeded |
| `MCP_SCHEMA_BUDGET_EXCEEDED` | node/depth/member/ref/regex/string/number budget exceeded |
| `MCP_SCHEMA_EXTERNAL_REF_FORBIDDEN` | non-local reference, ID, custom metaschema or vocabulary |
| `MCP_SCHEMA_DRAFT_UNSUPPORTED` | draft URI outside frozen set |
| `MCP_SCHEMA_ROOT_NOT_OBJECT` | root contract is not an object schema |
| `MCP_SCHEMA_COMPILE_FAILED` | bounded input is not a valid schema |
| `MCP_SCHEMA_ARGUMENT_INVALID` | call arguments fail the compiled schema |
| `MCP_SCHEMA_CAPACITY_EXHAUSTED` | global semaphore not acquired in 250 ms |
| `MCP_SCHEMA_PROCESS_START_FAILED` | helper could not start or enter process boundary |
| `MCP_SCHEMA_TIMEOUT` | 2-second operation deadline exceeded |
| `MCP_SCHEMA_CANCELLED` | caller cancelled and child was terminated/reaped |
| `MCP_SCHEMA_PROCESS_EXITED` | child exited abnormally or without one response |
| `MCP_SCHEMA_PROTOCOL_VIOLATION` | response framing or echoed identity invalid |
| `MCP_SCHEMA_GENERATION_STALE` | config-owner generation/membership changed |
| `MCP_SCHEMA_DIGEST_MISMATCH` | canonical digest mismatch |
| `MCP_SCHEMA_REAP_FAILED` | killed child was not reaped within one second |

Codes are API contract; message text is diagnostic only. Managed/first-party
maps every non-argument code to whole-server fail-fast. Trusted external maps
tool-specific schema failures to per-tool quarantine, but process boundary,
authority, generation, digest, protocol or reap failures fail the server
generation and never preserve a stale surface.

## 8. Build And Packaging Contract

The helper is a CGO-disabled Go binary built from the same commit and Go toolchain
as the desktop artifact for `darwin/{amd64,arm64}`,
`linux/{amd64,arm64}`, and `windows/{amd64,arm64}`. Packaging includes only
the helper matching the host artifact tuple. A release/package/startup parity
gate verifies helper presence, executable bit where applicable, SHA-256,
protocol version, app commit, Go version and target tuple. Missing or mixed
helper bytes disable MCP schema admission; the app must not fall back to
in-process compile.

Task 4 must add native runtime kill/reap tests on macOS, Linux and Windows
packaging lanes. Cross-compilation alone proves buildability, not OS process
reaping behavior.

## 9. LSP Owner Evidence

All queries used
`work_dir=/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-p0-mcp-decision`
and were restricted to `internal/platform/toolbridge`.

| Category | Evidence |
| --- | --- |
| Locate | `grep(text_search)` for `ListTools`, `InputSchema`, `json.RawMessage`, `Compile`; `structure(document_symbol)` on HTTP/stdio/types/handler files |
| Understand | `inspect(definition|hover)` resolved HTTP `decodePeerToolsListResult` to `types.go:157` and confirmed its signature |
| Impact | `xref(references)` found both HTTP and stdio decode callers; `addSingleMCPToolToSurface` is called by `addMCPToolsToSurface`; `validateToolInputSchema` has host, Codex call and protocol callers |
| Read | `file(read_file)` read both `ListTools`, `decodePeerToolsListResult`, `prepareMCPSurfaceBinaries`, `addSingleMCPToolToSurface` and `validateToolInputSchema` |
| Diagnostics | `file(diagnostics)` on six exact owner files returned `No diagnostics found` |

Owner verdict: raw tools/list decode lands in
`http_mcp_client.go`/`stdio_mcp_client.go` -> `types.go`; schema admission
must occur after identity/classification and before
`addSingleMCPToolToSurface`. New compiler/process ownership is
`internal/platform/toolbridge/schema/` plus
`cmd/mcp-schema-compiler-helper/`; existing `validateToolInputSchema` is not a
compiler and must not become a second schema truth.

## 10. Review Coverage

| Dimension | Coverage |
| --- | --- |
| D01 | Applied: toolbridge owns admission; helper is a local execution boundary |
| D02 | Applied: every missing/unknown/oversize/stale condition fails closed |
| D03 | Applied: strict one-request/one-response helper protocol and stable codes |
| D04 | N/A: no LSP product behavior changes; LSP was used only for owner evidence |
| D05 | N/A: no provider/runtime implementation in this task |
| D06 | N/A: no orchestration state change |
| D07 | N/A: no store/sqlc change |
| D08 | N/A: no skill/memory/prompt/thread implementation |
| D09 | N/A: no frontend change |
| D10 | Applied: deny external loader, no network/file access, authority fence |
| D11 | Applied: stable codes and request/server/tool/generation correlation |
| D12 | Applied: named cancellation, budget, generation, build and reap tests frozen |
| D13 | Applied: six target build and package parity contract |
| D14 | Applied: bytes/nodes/depth/refs/regex/concurrency/deadline/kill/reap bounds |
| D15 | N/A: no user-facing UI behavior |
| D16 | Applied: exact branch/base/write set and no dependency/source edits |
| D17 | Applied: Task 4 must dynamically guard new protocol/result/authority fields |
| D18 | Applied: one schema owner; existing validator cannot fork schema truth |
| D19 | Applied: canonical bytes/digest and config-owner generation are SSOT |

## 11. Residual Risks And Gate

- `GOMEMLIMIT` is a Go soft limit, not a cross-platform hard RSS cap. Process
  isolation, strict input/concurrency bounds and kill/reap protect the desktop
  process; Task 4 must not claim a numeric hard RSS guarantee.
- The local cancellation proof uses a blocking loader, not an exhaustive set of
  adversarial schemas. It is sufficient because the public API itself lacks a
  context path.
- The vulnerability result is point-in-time. Task 4 dependency acceptance and
  release gates must rerun the repository's current vulnerability policy.
- Windows Job Object and Unix process-group behavior require native tests; the
  six-target check here proves compile only.

Decision blocker ledger: none. This document clears
`MCP_COMPILER_CANCELLATION_OR_ISOLATION_DECISION_REQUIRED`,
sets `mcp_design_complete=true` and `p0_mcp_executable=true`, and opens Task 4
after this commit is accepted by the main Agent.
