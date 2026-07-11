# Generated Artifacts LaunchAgent Refresh Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 安装一个 macOS LaunchAgent，每 300 秒刷新 super-agent-v3 capability manifest 和 wjboot-v2 AI project map。

**Architecture:** 一个仓库脚本同时承担 `install`、`uninstall`、`status`、`run-once` 四个命令。LaunchAgent 只负责周期调度；实际生成继续调用两个仓库各自的 `scripts/refresh_generated_artifacts.sh`，并使用 Git 公共目录互斥锁防止同一任务重入。

**Tech Stack:** Bash 3.2、macOS launchd/launchctl、Go 标准库测试、现有生成物刷新脚本。

**Verification Surface:** `go test ./scripts` 聚焦测试、Shell 语法、LSP diagnostics、super-agent-v3 capcontract/project-map/codemap checks、wjboot-v2 project-map 严格刷新、launchctl 实机状态。

---

## 文件结构

- Create: `scripts/generated_artifacts_auto_refresh.sh` — CLI、互斥锁、双仓刷新、plist 生成和 launchctl 生命周期。
- Create: `scripts/generated_artifacts_auto_refresh_guard_test.go` — 真实执行脚本，用临时仓库和最小外部命令替身验证行为。
- Modify: `docs/superpowers/specs/2026-07-10-capcontract-launchd-refresh-design.md` — 记录扩展后的双仓范围。
- Create: `docs/plans/2026-07-10-generated-artifacts-launchd-refresh.md` — 本实现计划。
- Refresh: `docs/doc/codemap/**` — 由仓库生成器根据新增脚本和文档更新。

### Task 1: 用失败测试锁定 CLI 与双仓刷新契约

**Files:**
- Create: `scripts/generated_artifacts_auto_refresh_guard_test.go`
- Test: `scripts/generated_artifacts_auto_refresh_guard_test.go`

- [ ] **Step 1: 创建临时双仓 fixture**

测试 helper 创建 `super-agent-v3/scripts`、`wjboot-v2/scripts`、临时 HOME 和 fake-bin，初始化 super fixture Git 仓库，并把两个刷新入口实现为记录参数的可执行脚本。测试只替换外部 `launchctl`；被测管理脚本始终真实执行。

- [ ] **Step 2: 编写 run-once 失败测试**

断言尚不存在的管理脚本执行后必须记录以下两次调用，顺序固定：

```text
capcontract --repo <super-root>
project-map --repo <wjboot-root>
```

同时覆盖缺失 `--wjboot-repo`、刷新入口不存在、首仓刷新失败阻止第二仓执行、活跃锁拒绝重入、陈旧锁可回收。

- [ ] **Step 3: 编写 LaunchAgent 生命周期失败测试**

执行 `install` 后断言 plist 包含：

```text
com.super-agent-v3.generated-artifacts-refresh
StartInterval = 300
RunAtLoad = true
run-once
--wjboot-repo
StandardOutPath
StandardErrorPath
```

再验证重复 `install` 会卸载旧任务后重新 bootstrap，`status` 使用 GUI domain 查询，`uninstall` 只删除目标 plist。

- [ ] **Step 4: 运行 RED**

Run: `go test ./scripts -run '^TestGeneratedArtifactsAutoRefresh' -count=1`

Expected: FAIL，原因是 `scripts/generated_artifacts_auto_refresh.sh` 尚不存在。

### Task 2: 实现最小 LaunchAgent 管理脚本

**Files:**
- Create: `scripts/generated_artifacts_auto_refresh.sh`
- Test: `scripts/generated_artifacts_auto_refresh_guard_test.go`

- [ ] **Step 1: 实现参数与路径校验**

脚本启用 `set -euo pipefail`，只接受四个命令和 `--wjboot-repo PATH`；对 super repo、wjboot repo、两边刷新脚本、HOME、launchctl 和 GUI domain 做明确校验，任何缺失都返回非零。

- [ ] **Step 2: 实现 run-once 与互斥锁**

锁目录位于 `git rev-parse --path-format=absolute --git-common-dir` 返回路径下。锁中 PID 活跃时拒绝执行；PID 不存在时删除陈旧锁。持锁后依次执行：

```text
scripts/refresh_generated_artifacts.sh capcontract --repo <super-root>
scripts/refresh_generated_artifacts.sh project-map --repo <wjboot-root>
```

- [ ] **Step 3: 实现 plist 与 launchctl 生命周期**

`install` 创建 `~/Library/LaunchAgents` 和 `~/Library/Logs/super-agent-v3`，写入临时 plist 后原子替换目标文件，按 `print -> bootout -> bootstrap -> kickstart` 管理 GUI domain。`status` 返回实际加载状态；`uninstall` bootout 后只删除目标 plist，保留日志。

- [ ] **Step 4: 运行 GREEN**

Run: `go test ./scripts -run '^TestGeneratedArtifactsAutoRefresh' -count=1`

Expected: PASS，且输出无 warning/hint。

- [ ] **Step 5: 运行脚本静态检查**

Run: `bash -n scripts/generated_artifacts_auto_refresh.sh`

Expected: exit 0。

### Task 3: 刷新生成物并完成仓库验证

**Files:**
- Refresh: `docs/doc/codemap/**`
- Refresh: `docs/doc/codemap/capability-contract/capability_manifest.json`
- Preserve: `/Users/mima0000/Desktop/wj/wjboot-v2/docs/guide/index/backend-engine.tsv`

- [ ] **Step 1: 验证 wjboot-v2 既有地图改动**

在隔离的 detached worktree 运行 `bash scripts/refresh_generated_artifacts.sh project-map`，与当前 `backend-engine.tsv` 比较；只有生成器输出一致时才允许定时任务接管当前地图，差异不一致则停止并报告 blocker。

- [ ] **Step 2: 运行聚焦及脚本包测试**

Run: `go test ./scripts -run '^TestGeneratedArtifactsAutoRefresh' -count=1`

Run: `go test -short ./scripts`

Expected: 两条命令均 PASS。

- [ ] **Step 3: 刷新并检查 super-agent-v3 生成物**

Run: `scripts/refresh_generated_artifacts.sh capcontract`

Run: `make capcontract-check && make project-map-refresh && make project-map-check && make codemap-check`

Expected: 全部 exit 0。

- [ ] **Step 4: 运行 LSP diagnostics 与差异检查**

对 `scripts/generated_artifacts_auto_refresh_guard_test.go` 运行 `file(diagnostics)`；随后运行 `git diff --check` 并检查精确 diff，确认没有秘密、临时路径或无关文件。

### Task 4: 安装并实机验收 LaunchAgent

**Files:**
- Create outside repo: `~/Library/LaunchAgents/com.super-agent-v3.generated-artifacts-refresh.plist`
- Create outside repo: `~/Library/Logs/super-agent-v3/generated-artifacts-refresh*.log`

- [ ] **Step 1: 安装并触发立即刷新**

Run: `bash scripts/generated_artifacts_auto_refresh.sh install --wjboot-repo /Users/mima0000/Desktop/wj/wjboot-v2`

Expected: bootstrap 和 kickstart 成功，命令 exit 0。

- [ ] **Step 2: 验证加载状态**

Run: `bash scripts/generated_artifacts_auto_refresh.sh status --wjboot-repo /Users/mima0000/Desktop/wj/wjboot-v2`

Run: `launchctl print gui/$(id -u)/com.super-agent-v3.generated-artifacts-refresh`

Expected: 两条命令均显示 loaded/running 或最近成功退出状态，interval 为 300。

- [ ] **Step 3: 验证真实生成结果和日志**

Run: `make capcontract-check`

Run in wjboot-v2: `bash scripts/refresh_generated_artifacts.sh project-map`

Expected: 两仓刷新成功；日志包含两个刷新入口的成功输出，没有 error/warning。

- [ ] **Step 4: 保持后台任务已安装**

再次运行 `status`，确认 plist 存在且 LaunchAgent 已加载。本任务不自动暂存、提交或推送仓库文件。
