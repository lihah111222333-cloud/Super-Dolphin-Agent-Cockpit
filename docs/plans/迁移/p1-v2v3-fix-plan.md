# V2↔V3 P1 核心功能缺失修复计划

> 日期：2026-03-25
> 来源：docs/plans/迁移/v2v3-recheck-final.md §4
> 总项数：30 项 P1 + 12 项 P2 = 42 项
> 估计工作量：38-55 人日
> 执行方式：按根因聚合，Codex Agent 并行修复 + 互审

---

## 0. 执行原则

1. **按根因聚合**，不逐项零散修复
2. **每批修复后 1:N 互审 + archtest 全绿**方可进入下一批
3. **Agent 拉起规范**：通过 `orchestration_launch_agent(provider="codex")`，初始 prompt 必含 LSP 文档链接
4. **V2 参考路径**：`/Users/mima0000/Desktop/wj/go-agent-v2/`
5. **守卫标准**：文件≤400行，函数≤80行，CC≤10，包非测试文件≤15

---

## 1. 第一批：根因 B + C（orchestration 终态 + provider 进程）

### 预计：7 项，15-20 人日，5 个 Agent 并行

| Agent | 任务 | 涉及模块 | 工作量 | 依赖 |
|-------|------|---------|--------|------|
| **B1** | interrupt→complete 闭环 | turn-lifecycle | 中 | 无 |
| **B2** | StopAllAgents/archive/delete 统一停机 | orch-agent | 中 | 无 |
| **B3** | claude reconnect/reinitialize | provider-claude | 中 | 无 |
| **B4** | codex 进程启动/停止（probe/Setpgid/stderr/orphan） | provider-codex | 大 | 无 |
| **B5** | 通用 approval 阻塞语义（tool approval→awaiting 状态） | orch-statemachine | 中 | B1 |

### B1: interrupt→complete 闭环

**问题**：orchestration 只订阅 `TurnStarted/TurnCompleted`，`TurnInterrupted` 不被消费，导致 interrupt 后 orchestration 不知道 turn 已结束。

**V2 行为**：client lifecycle 消费 `turn_complete/turn_aborted/idle/error/shutdown_complete` 等多种终态。

**修复方案**：
1. 读 V2 `internal/executor/client_lifecycle.go` 理解终态事件消费
2. 在 V3 `cmd/mcp-orch/orchestration/module.go` 的事件订阅中增加 `TurnInterrupted`
3. 在 `turn_lifecycle.go` 中对 `TurnInterrupted` 做等价处理：
   - 标记 turn 为终态
   - 清理 active turn 绑定
   - 发送合成的 `TurnCompleted{status:"interrupted"}` 或直接处理 interrupted 终态
4. 补测试：模拟 interrupt 后 orchestration 正确感知终态

**验证**：`go test ./cmd/mcp-orch/orchestration/...`

### B2: StopAllAgents/archive/delete 统一停机

**问题**：`StopAllAgents` 提前 removeSession+publishAgentStopped；`archive/delete` 只 close session 不走 `StopAgent()/waitForProcessExit()`。

**V2 行为**：统一走停机链路。

**修复方案**：
1. 读 V2 `internal/service/agent_manager.go` 的 StopAll/Archive/Delete 链路
2. 让 `StopAllAgents` 遍历调用 `StopAgent()` 而不是直接 removeSession
3. 让 `archive/delete` 先调 `StopAgent()` 等待进程退出，再做 archive/delete
4. 补测试

**验证**：`go test ./cmd/mcp-orch/orchestration/...`

### B3: claude reconnect/reinitialize

**问题**：`restartIfNeededLocked()` 只换 transport，不重建 `threadReady/threadReadyOnce`，不重新等待 `system:init`。

**V2 行为**：`Kill + --resume`，每次重启重置 `readyCh/turnActive` 等，等待 `session_configured` 写 thread/session。

**修复方案**：
1. 读 V2 `pkg/agentsdk/claude/driver.go` 的 restart 链路
2. 在 V3 `internal/provider/claudecli/driver.go` 的 `restartIfNeededLocked()` 中：
   - 重建 `threadReady` channel / `threadReadyOnce`
   - 等待新 transport 的 `system:init` 事件
   - 恢复 ready 状态后再放行后续操作
3. 补测试：模拟 restart 后 thread ready 重建

**验证**：`go test ./internal/provider/claudecli/...`

### B4: codex 进程启动/停止

**问题**：缺 readiness probe、`Setpgid`、stderr collector、orphan cleanup；stop 只有 `Interrupt+1s Kill`。

**V2 行为**：完整的进程管理（readiness probe、process group、stderr 收集、orphan 清理、fresh-client recovery）。

**修复方案**：
1. 读 V2 `pkg/agentsdk/codex/process.go` 理解完整进程管理
2. 在 V3 `internal/provider/codexapp/` 中逐项补齐：
   - `Setpgid: true` 在 `cmd.SysProcAttr` 中设置
   - readiness probe：启动后检测 stdout 或健康检查端口
   - stderr collector：启动 goroutine 收集 stderr 到日志
   - orphan cleanup：在 fx.OnStop 或 shutdown 时扫描并杀死孤儿进程
   - stop 改进：先 SIGTERM→等待 graceful period→SIGKILL
3. 补测试

**验证**：`go test ./internal/provider/codexapp/...`

### B5: 通用 approval 阻塞语义

**问题**：只有 `kind==request_user_input` 才驱动 `awaiting_user_input`；普通 tool approval 不填 Kind，不驱动等待态。

**修复方案**：
1. 读 V2 approval 事件驱动状态变更的逻辑
2. 在状态机中让 tool approval 也驱动等待态（如 `awaiting_tool_approval`），或补 Kind 映射
3. 依赖 B1 的终态感知才能正确恢复

**验证**：`go test ./internal/platform/statemachine/... ./cmd/mcp-orch/orchestration/...`

---

## 2. 第二批：根因 D（RPC 返回体系统迁移）

### 预计：10 项，10-15 人日，5 个 Agent 并行

| Agent | 任务 | 涉及模块 | 工作量 |
|-------|------|---------|--------|
| **D1** | config/read 补全字段 | thread-config | 中 |
| **D2** | thread/messages 分页+结构+compaction+离线 | thread-messages | 中×4 |
| **D3** | turn/interrupt 返回 envelope | turn-lifecycle | 小 |
| **D4** | claude turn finish payload | provider-claude | 中 |
| **D5** | store-db thread/read + workspace DTO | store-db | 小×2 |

### 方法论：V2 RPC Golden 测试集

在开始逐项修复前，先建立 golden 测试框架：
1. 从 V2 提取关键 RPC 方法的请求/响应 JSON schema
2. 在 V3 建立 `internal/platform/rpc/golden_test.go` 作为回归基准
3. 每修一个返回体，同时更新 golden 测试
4. 这样后续修复不会反复回归

---

## 3. 第三批：根因 A + E（session 解耦 + approval guard）

### 预计：4 项核心 + 附带，8-10 人日

| Agent | 任务 | 涉及模块 | 工作量 |
|-------|------|---------|--------|
| **A1** | thread-scoped config/state resolver | thread-config + thread-messages | 中 |
| **A2** | uistate preferences flat map + side effects | uistate | 中 |
| **A3** | approval live replay | approval | 中 |
| **A4** | uistate 事件投影链（normalize/lifecycle/overlay） | uistate | 大 |

### 核心修复：session 解耦

在 session 之上恢复一层 thread-scoped config/state resolver：
- `thread/config/get` 不依赖 active session 即可读取
- `thread/messages` 的历史读取解耦 session
- `ui/sidebar/get` 的 `agentMetaById` 有独立于 session 的真相源

---

## 4. 第四批：dashboard + wails + 次优先

### 预计：13 项，8-12 人日

| Agent | 任务 | 工作量 |
|-------|------|--------|
| **F1** | dashboard agentStatus 独立读模型 + status filter | 中 |
| **F2** | dashboard 日志面（audit/bus store 注入）+ DAG 面 | 大 |
| **F3** | wails desktop API（ui/log, windowBootstrap, LSP helper） | 中 |
| **F4** | wails 默认 middleware 链恢复 | 中 |
| **F5** | thread-start provider 选择 + 返回体 + fork | 小~中 |
| **F6** | workspace dry-run + merge 补偿 | 中 |
| **F7** | WSHandler 接线 + execute-time ready wait | 小 |

---

## 5. P2 协议收缩（12 项，可与 P1 同批修复）

多数 P2 项与对应 P1 项同源，修 P1 时顺带对齐：

| P2 | 关联 P1 | 同批修复 |
|----|---------|---------|
| thread/start envelope | P1 thread-start 返回体 | 第四批 F5 |
| config 语义变更 | P1 config/read | 第二批 D1 |
| messages before 类型 | P1 分页机制 | 第二批 D2 |
| turn payload 缩水 | P1 interrupt envelope | 第二批 D3 |
| dashboard 收窄 | P1 dashboard | 第四批 F1+F2 |
| uistate 缩水 | P1 preferences + projector | 第三批 A2+A4 |
| manifest env | P1 WSHandler | 第四批 F7 |
| claude translator | P1 turn finish | 第二批 D4 |
| rpc-push method | 独立补 | 第二批附带 |
| raw passthrough | 评估是否恢复 | 第二批附带 |
| bus naming | 兼容桥 | 第二批附带 |
| workspace tool DTO | P1 store DTO | 第二批 D5 |

---

## 6. 验收标准

每批修复完成后必须满足：
1. `go build ./internal/... ./cmd/mcp-orch/...` ✅
2. 相关包测试全绿 ✅
3. `go test -run "TestCodeSizeGuard|TestDependencyDirection|TestTimeoutLocality" ./internal/archtest/...` ✅
4. `lsp_file diagnostics` 无编译错误 ✅
5. 1:N 互审通过（每项至少 2 个 Agent 交叉审查） ✅

---

## 7. 时间线

| 周 | 批次 | 根因 | 项数 | Agent 数 |
|----|------|------|------|---------|
| 第 2 周 | 第一批 | B + C | 7 | 5 |
| 第 3 周 | 第二批 | D | 10 | 5 |
| 第 3-4 周 | 第三批 | A + E | 4+ | 4 |
| 第 4 周 | 第四批 | 余项 | 13 | 7 |
| **合计** | | | **34+** | **~21** |

P2 项随对应 P1 同批消化，不单独排期。
P3 设计演进（7 项）为可接受差异，不修复。
