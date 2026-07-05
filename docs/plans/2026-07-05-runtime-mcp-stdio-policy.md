# Runtime MCP Stdio Policy Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Revalidate persisted and runtime MCP stdio commands before they can become trusted runtime binaries or reach `exec.Command`.

**Architecture:** Centralize stdio command/argv validation in `contract.RuntimeMCPPolicy`, then reuse it from mcp_server writes, persisted config reads, provider shared config parsing, toolbridge, and Claude manifest generation. The policy allows only exact built-in stdio shapes and rejects path-qualified command aliases.

**Tech Stack:** Go backend, MCP runtime config, provider shared config, toolbridge stdio process launcher, Claude manifest tests.

**Verification Surface:** `internal/contract`, `internal/module/mcp_server`, `internal/store/mcpserver`, `internal/provider/shared`, `internal/platform/toolbridge`, `internal/provider/claudecli`, frontend baseline.

---

### Task 1: Add RED tests for persisted/runtime stdio bypass

**Files:**
- Modify: `internal/store/mcpserver/store_test.go`
- Modify: `internal/provider/shared/mcp_stdio_config_test.go`
- Modify: `internal/platform/toolbridge/stdio_mcp_client_test.go`
- Modify: `internal/provider/claudecli/transport_config_test.go`
- Modify: `internal/module/mcp_server/service_test.go`

- [x] **Step 1: Persisted unsafe command regression**

Seed `mcp_server_configs` with `transport='stdio', command='bash', args='["-lc","env"]'`, then assert `ListServers` rejects it.

- [x] **Step 2: Provider shared trusted runtime regression**

Pass an `mcpConfig.mcpServers.shell` object with matching `trustedServerId` and `command='bash'`; assert `ConfigMCPBinaries` rejects it.

- [x] **Step 3: Toolbridge last-mile regression**

Call `defaultStdioClientFactory` with a matching `TrustedServerID` and unsafe `Command`; assert it rejects before spawning.

- [x] **Step 4: Path-qualified postgres regression**

Assert `/tmp/.../mcp-server-postgres` is rejected by service write validation and Claude manifest validation.

- [x] **Step 5: Run RED**

Run: `./scripts/test_with_guard.sh ./internal/store/mcpserver ./internal/provider/shared ./internal/platform/toolbridge ./internal/provider/claudecli ./internal/module/mcp_server -run 'TestConfigStoreRejectsUnsafePersistedStdioCommand|TestConfigMCPBinariesRejectsTrustedUnsafeStdioCommand|TestDefaultStdioClientFactoryRejectsTrustedUnsafeRuntimeCommand|TestWriteManifestConfigFailsFastForRejectedStdioServer|TestAddServersRejectsPathQualifiedPostgresCommand' -count=1`

Expected: FAIL before implementation on the trusted/persisted unsafe stdio cases.

### Task 2: Centralize and apply stdio policy

**Files:**
- Modify: `internal/contract/mcp_control.go`
- Modify: `internal/module/mcp_server/service.go`
- Modify: `internal/store/mcpserver/store.go`
- Modify: `internal/provider/shared/config_helpers.go`
- Modify: `internal/platform/toolbridge/stdio_mcp_client.go`
- Modify: `internal/provider/claudecli/transport_config.go`

- [x] **Step 1: Add `ValidateRuntimeStdioCommand` to `RuntimeMCPPolicy`**

The policy accepts exact commands only: `mcp-server-postgres` with the default database URL, and `npx` with the exact built-in package argv shapes. Path-qualified commands are rejected.

- [x] **Step 2: Replace local mcp_server allowlist**

Use the shared policy from `normalizeStdioServerConfig`, passing the configured product SQLite DB path for dbhub DSN validation.

- [x] **Step 3: Revalidate persisted configs on store read**

After normalizing a stdio row in `ListServers`, call the shared policy and fail fast on unsafe persisted rows.

- [x] **Step 4: Revalidate provider and toolbridge runtime binaries**

Validate trusted stdio configs in `ConfigMCPBinaries` and again before `exec.Command`.

- [x] **Step 5: Reuse policy in Claude manifest**

Replace Claude-specific package checks with the shared policy while keeping managed `lsp`/`orch` sidecar handling.

### Task 3: Verify, commit, and push

- [x] **Step 1: Run focused GREEN tests**

Run the RED command from Task 1 again; expected PASS.

- [x] **Step 2: Run affected package tests**

Run: `./scripts/test_with_guard.sh ./internal/store/mcpserver ./internal/module/mcp_server ./internal/provider/shared ./internal/platform/toolbridge ./internal/provider/claudecli -count=1`

Result: targeted changed packages pass. Full `internal/platform/toolbridge` and `internal/provider/claudecli` package runs still hit pre-existing unrelated failures (`TestProxyToolCallPublishesLifecycleEvents`, `TestDriverResumeSessionDoesNotWaitForSystemInit`).

- [x] **Step 3: Run required frontend baseline**

Run in `frontend-app`: `npm run lint`, `npm test`, `npm run build`

- [ ] **Step 4: Commit and push**

Stage only the plan and MCP policy files/tests, commit with Chinese `fix:` title, and push with `git push origin HEAD:main`.
