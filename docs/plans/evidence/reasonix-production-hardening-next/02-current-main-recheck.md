# Reasonix Production Hardening - Current Main Recheck

TASK_ID: `TASK_0_CURRENT_MAIN_RECHECK`

DATE: `2026-07-16`

STATUS: `MCP_DECISION_RECHECK_COMPLETE`

SOURCE_HEAD: `3d6fccfc58b904e2c9a6f358285cdee6d6ea7753`

VERDICT: `P0_IMPLEMENTATION_OPEN`

`release_design_complete=true`

`mcp_design_complete=true`

`implementation_design_complete=derived(release_design_complete && mcp_design_complete)=true`

`p0_release_executable=true`

`p0_mcp_executable=true`

`p0_executable=derived(p0_release_executable && p0_mcp_executable)=true`

## Review Object And Scope

| Field | Value |
| --- | --- |
| Checkout | `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-p0-mcp-decision` |
| Branch | `codex/reasonix-p0-mcp-decision` |
| Base/HEAD before this decision | `3d6fccfc58b904e2c9a6f358285cdee6d6ea7753` |
| Historical Task 0 evidence base | `b40867229af8e17916c00393639ccb0fcb4bf6fc` |
| Historical current-main review | `main@1ea371f4e39279703dd2023a94add2dccafbcfa8` |
| Review object | docs-only MCP compiler resource-boundary decision |
| Out of scope | release/Task 1 production code, Task 4 implementation, `go.mod`, `go.sum` |

Write set:

- `docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md`
- `docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md`
- `docs/plans/evidence/reasonix-production-hardening-next/02-current-main-recheck.md`
- `docs/plans/evidence/reasonix-production-hardening-next/03-mcp-compiler-decision.md`

The mandatory pre-commit refresh also owns these mechanical project-map outputs:

- `docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md`
- `docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json`
- `docs/doc/codemap/project-map/AI_PROJECT_MAP.md`
- `docs/doc/codemap/project-map/index/docs-agent.tsv`

They only index the four authored docs changes and are not an expansion into
release or Task 4 production code.

## Current Toolbridge Owner Recheck

All LSP queries used the decision worktree as `work_dir` and were restricted to
`internal/platform/toolbridge`.

| Category | Action / target | Result |
| --- | --- | --- |
| Locate | `grep(text_search)` for `ListTools`, `InputSchema`, `json.RawMessage`, `Compile`; `structure(document_symbol)` on exact files | found transport decode, surface add and current validator; no compiler exists |
| Understand | `inspect(definition|hover)` at HTTP decode and `ListTools` call | HTTP/stdio both resolve to `decodePeerToolsListResult`; client interface owns `ListTools(context.Context)` |
| Impact | `xref(references|call_hierarchy)` on decode, surface add and validator | decode has HTTP/stdio callers; `addSingleMCPToolToSurface` is called by `addMCPToolsToSurface`; validator reaches host/Codex/protocol calls |
| Read | `file(read_file)` on exact functions | confirmed current whole-list typed decode and publish-before-real-compile gap |
| Diagnostics | `file(diagnostics)` on six exact owner files | `No diagnostics found` |

Owner findings:

1. `httpMCPClient.ListTools` and `stdioMCPClient.ListTools` both call
   `types.go:decodePeerToolsListResult`, whose current
   `peerToolsListResult.UnmarshalJSON` decodes the whole array into
   `[]dto.MCPTool`. Raw per-tool deferral therefore lands in those three files.
2. `prepareMCPSurfaceBinaries` fetches tools and
   `addMCPToolsToSurface -> addSingleMCPToolToSurface -> addSurfaceTool`
   publishes them. Schema identity/classification/compile/quarantine must be
   inserted before `addSingleMCPToolToSurface`.
3. `validateToolInputSchema` only enforces unknown fields for strict object
   schemas. It is called from host, Codex surface and protocol paths, but is not
   a JSON Schema compiler and cannot remain a second schema truth.
4. New ownership is `internal/platform/toolbridge/schema/` for pre-scan,
   canonicalization, helper client and result DTO, plus
   `cmd/mcp-schema-compiler-helper/` for isolated compile/validate.

## Compiler Decision Recheck

The official API/source and local temporary module prove:

- candidate `jsonschema/v6@v6.0.2` supports Draft 4/6/7/2019-09/2020-12,
  is Apache-2.0 and has pinned module sums;
- a deny `URLLoader` blocks external fetches, but its `Load(string)` has no
  context;
- `Compiler.Compile(string)` has no context and no documented time/heap cap;
- local cancellation test showed caller cancellation cannot interrupt a blocked
  compile;
- bounded 2,048-property compile took about 20.5-20.8 ms/op and 16.0 MB
  allocations/op on Apple M3 Pro;
- candidate test cross-compiled with CGO disabled for darwin/linux/windows x
  amd64/arm64.

In-process cancellation and hard resource limits are therefore **不可证明**.
`03-mcp-compiler-decision.md` freezes a one-request/one-process helper with
strict budgets, two global live helpers, 2-second operation deadline,
kill plus 1-second reap, six-target packaging, stable codes and
generation/digest fencing. No additional architecture choice remains for Task 4.

## Implementation And Final Review Rule

Each implementation Agent completes its isolated task. The main Agent performs
the only per-task LSP/diff/gate review and immediately merges an accepted task
into `codex/integration-reasonix-p0`; no extra per-task reviewer Agent is added.

After Task 1-4 and Task 5 are complete, freeze the integration exact commit and
start three fresh Agents with no inherited context for Release, MCP/security and
repo-wide integration review. Final P0 completion requires all three to report
`0 P0 / 0 P1`; any repair reruns affected gates and the three-Agent review.

## Blockers

### Release lane

- none.

### MCP lane

- none; `MCP_COMPILER_CANCELLATION_OR_ISOLATION_DECISION_REQUIRED` is cleared.

## Validation Record

| Check | Result |
| --- | --- |
| LSP owner files diagnostics | PASS: no diagnostics |
| candidate local cancellation/loader tests | PASS: 5/5 |
| candidate benchmark | PASS: 3 runs, 20 iterations each |
| candidate six-target cross-compile | PASS |
| candidate `govulncheck` | 0 affected candidate symbols/packages; 23 unreachable local stdlib module advisories reported |
| AI maintenance docs gate | PASS: plan selected only `diff:whitespace`; gate exited 0 |
| `git diff --check` | PASS |
| write-set audit | PASS: four authored docs plus four mandatory generated project-map files; no source/dependency change |

## Current Verdict

`P0_IMPLEMENTATION_OPEN`

Task 1-3 remain open and Task 4 is now open. This status means the implementation
design is executable; it does not claim Task 4 production code exists.
