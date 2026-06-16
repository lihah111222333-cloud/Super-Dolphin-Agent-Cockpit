# Super-Dolphin Full-Test Remediation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-05-22 全量测试报告中的本机环境缺口和阻断项，并重跑报告命令矩阵直到得到可复现结果。

**Architecture:** 先修 Windows 本机工具链与浏览器缓存，再修项目内阻断测试的代码与生成物一致性。所有修复都要 fail-fast，不通过改阈值、跳过 guard 或伪造 E2E 结果来“通过”。

**Tech Stack:** Windows PowerShell, winget/MSYS2, Go 1.25.7, Make, CGO/GCC, Vue/Vite/Vitest/Playwright, PostgreSQL 16, sqlc, codemap generator.

**Plan file target:** `D:\project\Super-Dolphin\docs\ai01-docs\plan\2026-05-22-full-test-remediation-plan.md`

---

## Summary

- 使用 MSYS2 安装 `make` 与 `gcc`，并把 `D:\Configuration\msys64\usr\bin`、`D:\Configuration\msys64\ucrt64\bin` 加入用户 PATH。
- 修复 Playwright Chromium 缓存；同时让 Playwright 配置支持 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`，避免缓存下载失败时无法测试。
- 修前端 size guard 的真实违规，不更新 baseline、不放宽阈值。
- 修 Go Windows path/root 测试：统一路径归一化、修测试 JSON 转义、补 tool scope、对 Windows symlink 权限做精准 skip。
- 修 provider salt Windows 权限模型：POSIX 保持 `0600`，Windows 使用 ACL 校验与本机 salt ACL 修复。
- 刷新并提交 codemap 生成物；修 SQLC 在 Windows `core.autocrlf=true` 下的 EOL drift。
- 修复真实 E2E 包装脚本假阳性，然后重跑全量命令矩阵并生成新的测试报告。

## Key Changes

### Task 1: Save Plan And Prepare Branch

**Files:**
- Create: `docs/审查报告/前端项目资料/plan/2026-05-22-full-test-remediation-plan.md`

- [ ] 保存本计划到目标路径。
- [ ] `git status --short --branch`，确认只有既有 `docs/审查报告/前端项目资料/` 未跟踪内容。
- [ ] 新建分支：`git switch -c codex/full-test-remediation-20260522`。
- [ ] 不改 `.env`，真实启动继续使用覆盖项。

### Task 2: Repair Local Windows Toolchain

**System changes:**
- Install MSYS2 to `D:\Configuration\msys64`.
- Install packages: `make`, `mingw-w64-ucrt-x86_64-gcc`.

**Commands:**
```powershell
winget install --id MSYS2.MSYS2 -e --location D:\Configuration\msys64
D:\Configuration\msys64\usr\bin\bash.exe -lc "pacman -Syu --noconfirm"
D:\Configuration\msys64\usr\bin\bash.exe -lc "pacman -S --needed --noconfirm make mingw-w64-ucrt-x86_64-gcc"
```

- [ ] Add user PATH entries if absent:
  - `D:\Configuration\msys64\usr\bin`
  - `D:\Configuration\msys64\ucrt64\bin`
- [ ] Open a fresh PowerShell and verify:
```powershell
make --version
gcc --version
$env:CGO_ENABLED='1'; go test ./internal/provider/claudecli -race -run TestResolveAbsCWDPreservesAbsolute -count=1
```

### Task 3: Repair Playwright Test Runtime

**Files:**
- Modify: `cmd/agent-terminal/frontend/playwright.config.js`
- Modify: `cmd/agent-terminal/frontend/playwright.real.config.js`
- Modify: `cmd/agent-terminal/frontend/scripts/run-real-e2e.cjs`

**Changes:**
- [ ] Stop stale `playwright install` processes before deleting `C:\Users\ai01\AppData\Local\ms-playwright\__dirlock`.
- [ ] Run `npm run test:e2e:install` from `cmd/agent-terminal/frontend`.
- [ ] Add optional `PLAYWRIGHT_CHROMIUM_EXECUTABLE` support in both Playwright configs via `use.launchOptions.executablePath`.
- [ ] Fix `run-real-e2e.cjs` so `spawnSync` returns failure when `result.error`, `result.signal`, or `result.status === null`; never map null status to 0.
- [ ] Verify:
```powershell
cd D:\project\Super-Dolphin\cmd\agent-terminal\frontend
$env:PLAYWRIGHT_CHROMIUM_EXECUTABLE='C:\Program Files\Google\Chrome\Application\chrome.exe'
npx playwright --version
npx playwright test -c playwright.real.config.js tests/e2e/chat-refresh.real.spec.js --list
```

### Task 4: Fix Frontend Size Guard

**Files:** only files reported by `node scripts/size-guard.cjs`; do not run `guard:update`.

**Required fixes:**
- [ ] Split oversized functions:
  - `vue-app/composables/useAutoScroll.js::useAutoScroll`
  - `vue-app/pages/MemoryCenterPage.js::setup`
  - `vue-app/pages/SystemPromptPage.js::setup`
- [ ] Reduce nesting depth:
  - `vue-app/stores/projects.js::useProjectStore`
  - `vue-app/utils/citation-preview-utils.js::resolveCitationImagePreview`
- [ ] Replace nested ternaries with named variables or small pure helpers in the files listed by the size guard output.

**Verification:**
```powershell
cd D:\project\Super-Dolphin\cmd\agent-terminal\frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
npm run test:e2e -- --list
```

### Task 5: Fix Go Windows Path / Root Tests

**Files:**
- Modify: `internal/util/pathutil/pathutil.go`
- Modify: `cmd/mcp-lsp/runtime_test.go`
- Modify affected LSP tests/callers only where strict tool scope is genuinely missing.

**Changes:**
- [ ] Centralize containment normalization in `internal/util/pathutil.ContainsPath`: clean absolute paths, resolve existing symlinks, normalize Windows drive aliases like `\C:\...` to `C:\...`, and use case-insensitive comparison on Windows.
- [ ] Update duplicate containment helpers in LSP/common code to use the shared helper instead of local `filepath.Rel` variants.
- [ ] Replace hand-built `GO_AGENT_LSP_ROOTS` JSON in tests with `json.Marshal([]string{...})` to avoid invalid `C:\Users` escapes.
- [ ] For tests that call tools directly, pass trusted scope with `common.WithToolScope(ctx, common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})`.
- [ ] For symlink-only tests on Windows, skip only `ERROR_PRIVILEGE_NOT_HELD`; all non-symlink path tests must still run.

**Verification:**
```powershell
go test ./internal/util/pathutil ./internal/mcpserver/common ./cmd/mcp-lsp/... -count=1
```

### Task 6: Fix Provider Salt Permissions On Windows

**Files:**
- Modify: `internal/module/skill/scope_model.go`
- Add: `internal/module/skill/scope_model_permissions_unix.go`
- Add: `internal/module/skill/scope_model_permissions_windows.go`
- Modify: `internal/module/skill/scope_model_test.go`

**Changes:**
- [ ] Move salt permission validation behind platform-specific helpers.
- [ ] Unix helper keeps exact `0600` behavior.
- [ ] Windows helper validates a regular non-empty file and checks DACL equivalence: current owner can read/write; `SYSTEM` and `Administrators` may retain full control; broad principals such as `Everyone`, `Users`, `Authenticated Users`, and `CodexSandboxUsers` must not have read/write access.
- [ ] Add Windows regression test proving a newly created salt passes on Windows without relying on POSIX `Mode().Perm()==0600`.
- [ ] Repair local salt ACL:
```powershell
$salt="$env:USERPROFILE\.super-dolphin\owner_identity.salt"
icacls $salt /inheritance:r
icacls $salt /grant:r "$env:USERDOMAIN\$env:USERNAME:(R,W)" "SYSTEM:(F)" "Administrators:(F)"
icacls $salt /remove "Everyone" "Users" "Authenticated Users" "CodexSandboxUsers"
```

**Verification:**
```powershell
go test ./internal/module/skill -count=1
```

### Task 7: Fix SQLC And Codemap Consistency

**Files:**
- Modify: `.gitattributes`
- Regenerate: `internal/store/sqlc/**`
- Regenerate: `docs/doc/codemap/ai-index.json`
- Regenerate: `docs/doc/codemap/README.md`

**Changes:**
- [ ] Add targeted EOL rule: `internal/store/sqlc/*.go text eol=lf`.
- [ ] Run `git add --renormalize internal/store/sqlc`, then inspect diff; keep only generated SQLC/EOL-normalization changes.
- [ ] Run:
```powershell
make sqlc-generate
make sqlc-verify
make codemap-refresh
make codemap-check
```
- [ ] If SQLC diff includes semantic changes, inspect query/schema source and commit generated output with the matching source reason; do not hand-edit generated SQLC files.

### Task 8: Rerun Full Matrix And Produce New Report

**Startup DB:** `super_agent_v3_ai01_fulltest_20260522_rerun`

**Commands:**
```powershell
git status --short --branch
git rev-parse HEAD
make guard
go vet ./...
make test
make build-plain
make sqlc-verify
make codemap-check
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
npm run test:e2e
npm run test:e2e:real -- --skip-build --base-url=http://127.0.0.1:4511
```

- [ ] Recreate isolated PostgreSQL DB before startup.
- [ ] Start with `run-debug.ps1` menu `1 -> 2` and the same env override pattern from the previous report.
- [ ] Verify:
  - `http://127.0.0.1:4511/metrics` returns 200.
  - `http://localhost:5173` returns 200.
  - `super-agent-debug.exe`, `mcp-orch.exe`, `mcp-lsp.exe` are alive.
  - Codex and Claude minimal provider prompts reach `turn/start` and either pass or report provider/auth failure after the salt gate.
- [ ] Save rerun report to `docs/审查报告/前端项目资料/test/2026-05-22-full-test-rerun-report.md`.

## Test Cases And Scenarios

- Toolchain: `make`, `gcc`, `CGO_ENABLED=1 go test -race` all work from a fresh PowerShell.
- Playwright: Chromium cache install works; system Chrome fallback works through `PLAYWRIGHT_CHROMIUM_EXECUTABLE`.
- Frontend: size guard, Vitest, build, mock E2E, real E2E no longer blocked by global setup.
- Go: LSP path/root tests pass on Windows and reject true outside-root paths.
- Provider: Windows owner salt no longer fails due POSIX mode mismatch; broad ACLs still fail closed.
- Generated state: `make sqlc-verify` and `make codemap-check` pass with clean tracked diffs.
- End-to-end: report command matrix is rerun from clean code state and records every pass/fail with log paths.

## Assumptions

- Use MSYS2 on `D:\Configuration\msys64` as the durable Windows source for `make` and GCC.
- Do not weaken guard thresholds or update frontend size baselines.
- Do not bypass git hooks with `--no-verify`.
- Do not modify `.env`; use process-level startup overrides for isolated DB reruns.
- If real Codex/Claude auth or remote service fails after the salt gate, record it as external/provider dependency failure, not as a project code pass.
