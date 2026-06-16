# MCP LSP Multi Workspace Roots Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow built-in MCP LSP tools to operate on every folder opened in the Super Dolphin app, while keeping workspace roots trusted server metadata rather than model-controlled tool arguments.

**Architecture:** Carry the opened folder set as trusted `_workspaceRoots` metadata from turn/session configuration through Codex/toolbridge/common MCP scope. `cmd/mcp-lsp` resolves relative paths against primary `_cwd`, resolves absolute paths against the longest containing trusted root, and selects LSP manager/cache roots from that resolved workspace root.

**Tech Stack:** Go, `internal/mcpserver/common`, `internal/platform/toolbridge`, Codex app provider, `cmd/mcp-lsp` search/tools/manager/multilsp, guarded Go tests via `./scripts/test_with_guard.sh`.

---

### Task 1: Trusted Workspace Root Metadata

**Files:**
- Modify: `internal/mcpserver/common/scope.go`
- Modify: `internal/mcpserver/common/server.go`
- Test: `internal/mcpserver/common/server_test.go`

- [x] Add `WorkspaceRoots []string` to `common.ToolScope`.
- [x] Decode only private top-level `_workspaceRoots` / `_workspace_roots`; ignore forged root fields in tool arguments.
- [x] Normalize roots by trimming, absolutizing, deduping, and forcing `CWD` to be first.
- [x] Add strict root helpers that fail when trusted roots are absent and include requested path plus allowed roots on rejection.
- [x] Verify with common package tests.

### Task 2: Provider, Manifest, and Toolbridge Propagation

**Files:**
- Modify: `internal/contract/manifest.go`
- Modify: `internal/dto/provider/manifest.go`
- Modify: `internal/module/turn/manifest.go`
- Modify: `internal/platform/toolbridge/*.go`
- Modify: `internal/provider/codexapp/*.go`
- Test: package tests under the same directories.

- [x] Propagate `AdditionalWorkingDirectories` into manifest context.
- [x] Emit `GO_AGENT_LSP_ROOT` and JSON `GO_AGENT_LSP_ROOTS` for the LSP binary.
- [x] Add `WorkspaceRoots` to toolbridge request/scope types and forward `_workspaceRoots` to peer callbacks and stdio MCP calls.
- [x] Enrich Codex tool calls with trusted `_workspaceRoots`, removing public forged aliases.
- [x] Verify with toolbridge, Codex provider, unified manifest, and turn package tests.

### Task 3: LSP File, Search, Diagnostics, Edit, and Exec Roots

**Files:**
- Modify: `internal/sidecar/lsp/search/fileutil.go`
- Modify: `internal/sidecar/lsp/search/searchutil.go`
- Modify: `internal/sidecar/lsp/tools/*.go`
- Modify: `cmd/mcp-lsp/exec/sandbox.go`
- Test: `internal/sidecar/lsp/search/fileutil_roots_test.go` and existing tool/edit tests.

- [x] Add multi-root path resolution where relative paths stay under primary root and absolute paths select the longest containing trusted root.
- [x] Pass root sets through file, grep, diagnostics, and edit handlers.
- [x] Allow `code_run project_cmd` absolute `work_dir` inside any trusted root.
- [x] Preserve fail-fast rejection for paths outside every trusted root and for symlink escapes during edit writes.
- [x] Verify with `cmd/mcp-lsp/...` tests.

### Task 4: LSP Manager and Cache Root Selection

**Files:**
- Modify: `internal/sidecar/lsp/manager/scope.go`
- Modify: `internal/sidecar/lsp/manager/registry.go`
- Modify: `internal/sidecar/lsp/multilsp/*.go`
- Test: `internal/sidecar/lsp/manager/registry_scoped_test.go`
- Test: `internal/sidecar/lsp/multilsp/registry_scope_test.go`

- [x] Add `WorkspaceRoots` to manager and multilsp tool scopes.
- [x] Forward trusted root sets from registry context into the production scoped resolver.
- [x] Select the containing workspace root for absolute targets before adapter root resolution.
- [x] Preserve primary CWD behavior for relative paths and language-only lookups.
- [x] Verify root forwarding and extra-root manager selection with targeted tests.

### Task 5: Review and Verification

**Files:**
- Review all changed files from `git diff --name-only`.

- [ ] Run targeted verification for changed packages.
- [ ] Run `make guard`.
- [ ] Dispatch spec compliance and code quality/security review agents.
- [ ] Fix all confirmed P0/P1 findings and repeat review until none remain.
