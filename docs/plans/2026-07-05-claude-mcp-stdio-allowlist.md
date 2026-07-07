# Claude MCP Stdio Allowlist Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Claude MCP manifest stdio config from launching arbitrary local commands whose basename merely starts with `mcp-`.

**Architecture:** Keep the existing single validation boundary in `internal/provider/claudecli/transport_config.go`. Replace prefix trust with exact command-family trust for managed sidecars plus the existing explicit global/npx MCP package allowlist.

**Tech Stack:** Go provider code, manifest DTOs, provider unit tests, provider e2e manifest test.

**Verification Surface:** `internal/provider/claudecli`, `internal/provider/e2e`, frontend baseline lint/test/build, git diff checks.

---

### Task 1: Lock the regression with failing tests

**Files:**
- Modify: `internal/provider/claudecli/transport_config_test.go`
- Modify: `internal/provider/e2e/claude_mcp_test.go`

- [x] **Step 1: Add unit regression coverage**

Add a table-driven rejected-stdio test that includes `run-anything`, `/tmp/mcp-evil`, `/tmp/mcp-evil.exe`, and `/tmp/mcp-evil.cmd`.

- [x] **Step 2: Add e2e regression coverage**

Extend `TestClaudeMCPManifest_RejectsUnmanagedStdioServer_E2E` so the third-party server command is `/tmp/claude-e2e/bin/mcp-evil`.

- [x] **Step 3: Run RED tests**

Run: `./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/e2e -run 'TestWriteManifestConfigFailsFastForRejectedStdioServer|TestClaudeMCPManifest_RejectsUnmanagedStdioServer_E2E' -count=1`

Expected: FAIL because `/tmp/.../mcp-evil` is still allowed by the `mcp-` prefix rule.

### Task 2: Tighten the allowlist

**Files:**
- Modify: `internal/provider/claudecli/transport_config.go`

- [x] **Step 1: Replace prefix trust with exact command allowlist**

Keep basename normalization for `.exe` and `.cmd`, but only allow managed basenames `mcp-lsp` and `mcp-orch`. Keep `mcp-server-postgres` as an explicit global stdio command and keep current `npx` package checks.

- [x] **Step 2: Run focused GREEN tests**

Run: `./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/e2e -run 'TestWriteManifestConfig|TestClaudeMCPManifest' -count=1`

Expected: PASS.

### Task 3: Validate and publish

**Files:**
- Review staged diff for the files above and this plan.

- [ ] **Step 1: Run package verification**

Run: `./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/e2e -count=1`

Expected: PASS.

- [ ] **Step 2: Run required frontend baseline**

Run in `frontend-app`: `npm run lint`, `npm test`, `npm run build`

Expected: PASS.

- [ ] **Step 3: Commit and push to remote main**

Stage only the plan and Claude MCP files, commit with a Chinese `fix:` message, then run `git push origin HEAD:main`.
