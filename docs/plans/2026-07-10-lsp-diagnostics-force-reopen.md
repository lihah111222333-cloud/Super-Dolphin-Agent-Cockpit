# LSP Diagnostics Force Reopen Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes only when persistent orchestration records are needed. Steps use checkbox syntax for tracking.

**Goal:** 让带显式文件路径的 `file(diagnostics)` 在读取结果前强制执行文档关闭与重开，并只返回重开后的诊断。

**Architecture:** 工具层只负责声明“显式诊断需要重开”；真实的文档版本、scope、诊断 epoch、缓存和 `didClose`/`didOpen` 生命周期由 manager 维护。无路径诊断保持现有有界刷新，不重启整个语言服务器。

**Tech Stack:** Go, stdio MCP, LSP, fake language-server binary E2E harness.

**Verification Surface:** `./cmd/mcp-lsp`, `./cmd/mcp-lsp/tools`, `./cmd/mcp-lsp/manager`, `./cmd/mcp-lsp/multilsp`, LSP diagnostics, `git diff --check`.

---

### Task 1: Lock the stale-symbol bug with a binary E2E

**Files:**
- Modify: `cmd/mcp-lsp/lsp_binary_multilang_diagnostics_e2e_test.go`

- [x] Extend the fake server to retain the text received by `textDocument/didOpen`, ignore `didChange`, and clear state on `didClose`.
- [x] Add `TestMcpLSPBinaryDiagnosticsReopensChangedFileBeforeReturning_E2E`: diagnose a file containing `staleName`, rewrite it to `freshName`, diagnose it again, and require the second response to contain only `freshName`.
- [x] Run `go test -tags=e2e ./cmd/mcp-lsp -run TestMcpLSPBinaryDiagnosticsReopensChangedFileBeforeReturning_E2E -count=1 -v`.
- [ ] Confirm RED because the second diagnostic still contains `staleName` after the current `didChange`-only refresh.

### Task 2: Add a diagnostics-specific reopen boundary

**Files:**
- Modify: `cmd/mcp-lsp/manager/manager.go`
- Modify: `cmd/mcp-lsp/manager/registry.go`
- Modify: `cmd/mcp-lsp/manager/scope.go`
- Modify: `cmd/mcp-lsp/multilsp/manager.go`
- Modify: `cmd/mcp-lsp/multilsp/manager_lifecycle.go`
- Modify: `cmd/mcp-lsp/tools/tool_diagnostics.go`
- Test: same-package manager/tool lifecycle tests selected by compiler impact.

- [x] Add narrow `DiagnosticDocumentReopener` / `DiagnosticsReopenRegistry` capabilities and a resolved-scope wrapper without widening every generic manager test double.
- [x] In multilsp, serialize diagnostic reopen operations, read the current disk snapshot, advance the document diagnostic epoch, remove the old scoped snapshot, and send `didClose` followed by `didOpen` with the next cached version.
- [x] Persist the reopened snapshot/version and bootstrap state only after both notifications succeed; return errors immediately without stale fallback.
- [x] Call the new method for explicit `file_path`/`file_paths` targets after normal bootstrap and before `WaitDiagnosticsStable`; do not force-reopen the unscoped all-open-documents request.
- [x] Run focused package tests and repair only compile/test fallout caused by the new narrow contract.

### Task 3: Prove GREEN and inspect the change

**Files:**
- Verify all files changed by Tasks 1-2.

- [x] Run the E2E command from Task 1 and require PASS.
- [x] Run `./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools ./cmd/mcp-lsp/manager ./cmd/mcp-lsp/multilsp -count=1`.
- [x] Run `./scripts/test_with_guard.sh ./cmd/mcp-lsp -count=1`.
- [x] Run LSP diagnostics for every changed Go file and treat every severity as actionable.
- [x] Run `gofmt`, `git diff --check`, inspect the exact diff, and confirm the original main-worktree dirty files remain outside this worktree.
- [x] Do not stage, commit, push, or delete the worktree unless the user explicitly requests it.

## Replay evidence

- Replayed without conflict onto `main` at `ae9282e40964d14c292029940d14fce176ab8e87`.
- Focused package tests, the top-level `cmd/mcp-lsp` tests, and the binary E2E passed after replay.
- LSP diagnostics returned no findings for all changed Go files; `gofmt -d` and `git diff --check` were clean.
- The historical RED run was not repeated during replay because the implementation was already present; the unchecked RED item is retained rather than claiming evidence that was not freshly observed.
