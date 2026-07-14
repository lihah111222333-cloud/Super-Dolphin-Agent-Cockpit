# 全仓审查真阳风险三 Agent 并行修复执行计划

> 基线：`main@40d907e4a7dd05cf49d5f545dba9cc62033a8474`，且编写时 `HEAD == origin/main`、工作区 clean。
>
> 数据库事实：当前产品数据库唯一实现是 SQLite；`cmd/mcp-orch/sqlc.yaml` 的 `engine: sqlite` 和 SQLite baseline 是权威事实。禁止引入 PostgreSQL、`pq`、PG 方言或 testcontainers-PG。
>
> 本计划只覆盖 2026-07-14 全仓审查经两名独立 agent 交叉复核、主 agent LSP 仲裁后仍成立的 20 项风险：P1 4 项、P2 10 项、P3 6 项。本计划不把旧计划、旧 diagnostics 或默认绿色门禁当作当前缺陷证据。

## 1. 目标与完成定义

三个执行 agent 在三个独立 worktree 中并行完成：

- Agent A：唯一执行权、审批策略、进程身份和 TeamSync runtime 切换。
- Agent B：资源 ownership、补偿事务和严格数据边界。
- Agent C：异步可靠性、降级可见性、前端语义、架构守卫和 SQLite 真值收口。

每项修复必须同时满足：

1. 先有可稳定复现缺陷的 RED 测试，再修改生产代码。
2. owner 层消除根因；高破坏性或易复发问题同时建立上层防御。
3. 禁止默认值、吞错、仅日志、无限重试或扩大豁免制造假绿。
4. 涉及结构化字段生产、持久化或跨层消费时，执行 agent 必须加载“字段守卫”技能。
5. 每个共享符号保留 LSP 定位、理解、影响面、精读和 diagnostics 五类证据。
6. 每个中文修复提交必须同时包含锁定缺陷的测试；禁止 `--no-verify`。
7. 三条 lane 合并后通过全仓门禁、生成物漂移检查和 Git 状态复核，才能声明完成。

## 2. 已撤销项目和非目标

以下内容不得进入三个修复 lane：

- 删除 PostgreSQL 占位测试不等于引入 PG 测试基础设施；当前没有 PG 产品路径。
- 上轮报告的 5 项 Hint/Information 经当前 LSP diagnostics 复查均为 `No diagnostics found`，不得按旧快照修改代码。
- 不修改或重新 freeze baseline，不扩大 allowlist，不手改 sqlc、codemap、project-map 或 `cmd/agent-terminal/web-dist`。
- 不借本计划重构无关 provider、thread、memory、MCP 或前端组件。
- 不把 best-effort trace/RSS probe 的瞬态错误升级为整个桌面进程 fatal；应进入有界重试或 degraded health。

## 3. 并行拓扑与工作树

主 agent 从相同基线创建三个独立 worktree：

```text
Agent A -> codex/audit-remediation-a-safety-20260714
Agent B -> codex/audit-remediation-b-ownership-20260714
Agent C -> codex/audit-remediation-c-async-ui-20260714
```

三个 agent 可以同时启动。每个 agent 只修改本计划声明的独占写集；需要越界时立即停止并输出：

```text
NEEDS_APPROVAL
lane: A|B|C
requested_paths:
  - exact/path
reason: 为什么当前独占写集无法安全完成
risk_if_denied: 拒绝后仍未关闭的风险
```

### 3.1 文件所有权矩阵

| Agent | 独占领域 | 禁止触碰的共享 seam |
|---|---|---|
| A | `internal/module/cron/**`；Codex approval/session 主链；`internal/platform/pidregistry/**`；`internal/module/memory/team/**` | Provider history；thread Stop/Archive/Delete；memory UI RPC；前端；archtest；sqlc/migration |
| B | MCP-LSP middleware/runtime/YAML fallback；provider history；thread scratchpad；sharedfile importer；toolbridge surface；memory merge；insight mapper | Recycler；thread events worker；TeamSync；memory similarity UI；公共 DTO/前端；archtest |
| C | thread AgentLaunched worker；LSP recycler；lifecycle guard；skill debounce；trace bridge；RuntimeToolbar；memory similarity UI；SQLite SSOT guard | `cmd/mcp-lsp/runtime.go`；memory merge；TeamSync；Codex approval/history；sharedfile/toolbridge |

共享 seam 的最终归属：

- `internal/module/memory/ui_rpc.go` 只归 Agent C；Agent B 的 merge sentinel 放在 `ui_rpc_mutations.go` 或新建 `ui_rpc_errors.go`。
- `internal/module/observability/rpc.go` 默认不改；前端 trace 复用现有 `{enabled, recorded, dropped, disabled_reason}` ACK。
- `cmd/mcp-orch/sqlc.yaml` 是只读 SSOT；三个 agent 都不得修改。
- `internal/archtest/**` 只归 Agent C；A/B 只提供 guard 所需 fixture/禁止模式说明。
- codemap、project-map、capability manifest、embed bundle 由主 agent 合并后统一检查或刷新。

### 3.2 合并顺序

三个 lane 并行开发、串行集成：

```text
Agent A review/merge
  -> Agent B rebase + review/merge
    -> Agent C rebase + review/merge
      -> 主 agent 字段/guard/生成物集成
        -> 全仓门禁
```

任何 lane 在前序 lane 合入后必须先 rebase，再复跑自己的 focused gates；不得仅依赖合并前绿色结果。

## 4. 全局执行与证据格式

每个 agent 开始时记录：

```bash
git status --short
git rev-parse HEAD
git rev-parse origin/main
git diff --name-only
```

每个子任务按以下循环执行：

```text
LSP locate/inspect/xref/read
  -> 写 RED 测试并保存失败原因
  -> 最小实现
  -> LSP diagnostics
  -> focused GREEN/race
  -> 自审 diff 与写集
```

每个 agent 交付：

```text
STATE: DONE | BLOCKED
BASE_SHA:
BRANCH:
COMMITS:
WRITE_SET:
RED_EVIDENCE:
GREEN_EVIDENCE:
LSP_EVIDENCE:
RESULT_GATES:
REMAINING_RISKS:
WORKTREE_STATUS:
```

---

## 5. Agent A：唯一执行权与运行时身份安全

### 5.1 独占写集

```text
internal/module/cron/lease_actor.go
internal/module/cron/scheduler_recovery.go
internal/module/cron/*lease*test.go
internal/module/cron/scheduler*_test.go

internal/provider/codexapp/support.go
internal/provider/codexapp/session.go
internal/provider/codexapp/session_approval.go
internal/provider/codexapp/*approval*test.go
internal/provider/codexapp/driver_session_helpers_test.go

internal/platform/pidregistry/pidregistry.go
internal/platform/pidregistry/process_*.go
internal/platform/pidregistry/*_test.go
internal/provider/codexapp/module.go                 # 仅 stale cleanup 结果消费

internal/module/memory/team/team_sync.go
internal/module/memory/team/team_sync_watcher.go
internal/module/memory/team/*_test.go
```

允许新增 `internal/platform/pidregistry/process_identity_{darwin,linux,windows,other}.go` 及同包测试。

### 5.2 A1：Codex resume approval 必须经过远端验证

RED 覆盖：RPC error、非法 JSON、缺 `effective`、错误类型、缺/空/未知 `approvals`，以及合法值。

最小实现：

- `restoreApprovalPolicy` 返回 `error`，所有无法确认策略的情况 fail-fast。
- `finishResumedSession` 传播错误，复用 `finishOrCleanupResumedSession` 清理 session。
- session 增加仅包内运行态 `approvalPolicyVerified`；新 session 只有明确校验 policy 后才置 true。
- approval 主链生成 `ApprovalRequest` 前再次检查 verified；未验证时终止当前 approval/turn。
- 不新增 RPC/DTO/store 字段。

上层防御：resume 入口和 approval request 构造点双层验证，防止未来重构绕过恢复检查。

### 5.3 A2：PID registry 使用稳定进程身份

RED 覆盖：相同 PID/不同 start token、相同 token/不同 executable、TERM 后 PID 复用、identity 读取失败、旧 registry 缺字段、protected PID、完全匹配。

最小实现：

- `ChildInfo` 持久化 `process_start_token` 和 `executable_identity`。
- Linux 使用 `/proc/<pid>/stat` start time 与 `/proc/<pid>/exe`；Darwin 使用稳定创建时间/进程身份；Windows 使用 creation time 与 image identity。
- 不支持的平台明确返回 unsupported，禁止退化为 PID-only。
- 注册时读取身份；TERM 前、grace 后 KILL 前分别复核。
- 旧格式 registry 缺身份时安全跳过，绝不补默认身份后继续 kill。
- detailed cleanup 结果区分 killed、identity mismatch、identity read failure。

字段守卫：本项修改持久化 JSON，必须追踪生产、序列化、旧格式解码、orphan 收集、TERM/KILL 消费和 roundtrip；缺字段必须 fail-safe。

### 5.4 A3：Cron lease 失败终止所有权声明

RED 覆盖：多个 job 中部分 renew 失败、错误聚合包含 job ID、actor 收到 renew error 后退出、旧 token 无法 finalize 新 owner、双 scheduler 不产生两个有效执行。

最小实现：

- `RenewLeases` 遍历收集失败并 `errors.Join`，禁止 Debug 后返回 nil。
- `LeaseActor.Run` 对瞬态 renew error 只允许在当前 lease 安全预算内做有界重试；重试预算必须由 heartbeat interval 与 lease deadline 推导，不能写成无限重试或固定兜底。
- 任一 job 已无法在安全期限内确认续租时，触发该 job active turn 的取消/失租收口，并向 RunGroup 返回聚合错误；不得继续声称仍持有 lease，也不得因一次瞬态 SQLite 错误立即击穿整个 cron 运行组。
- 保留 claim token、active turn 和终态 CAS fencing，不修改 SQLite schema。
- 不靠延长 TTL、第二套锁或 PG 行锁掩盖问题。

上层防御：故障注入测试必须证明失租旧执行不能 publish/finalize；如当前 RunGroup 不能取消对应 active turn，立即报告 blocker，不得只完成错误传播。

### 5.5 A4：TeamSync runtime 切换事务化

RED 覆盖：旧 watcher final flush 失败、旧 watcher 重建失败、新 runtime pull/checksum/watcher 创建失败、并发 StartSession、同 runtime reuse。

最小实现：

- 增加私有 `transitionMu` 串行化 runtime transition。
- 在现有 `mu` 下复制旧 runtime/session/watcher 快照，但等待 flush/网络时不长期持锁。
- `Close(ctx, true)` 失败后不能把已停止的 watcher 指针放回；必须针对旧 root 重建 watcher并恢复旧 owner state。
- rollback 失败与原错误通过 `errors.Join` 返回。
- 新 runtime 完整准备成功后才一次性提交 owner 状态并启动 watcher。
- 不新增跨模块事务框架或 RPC/UI degraded 字段。

### 5.6 Agent A 验证

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -race -run 'Test.*(Resume|ApprovalPolicy|ApprovalRequest)' -count=1
./scripts/test_with_guard.sh ./internal/platform/pidregistry -race -count=1
GOOS=windows GOARCH=amd64 go test -c ./internal/platform/pidregistry -o /tmp/pidregistry-windows.test
./scripts/test_with_guard.sh ./internal/module/cron -race -run 'Test(RenewLeases|LeaseActor|.*Reclaimed.*Lease)' -count=1
./scripts/test_with_guard.sh ./internal/module/memory/team -race -count=1
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/platform/pidregistry ./internal/module/cron ./internal/module/memory/team -count=1
make guard
make build-plain
git diff --check
```

建议提交：approval、PID、Cron、TeamSync 各一个中文修复提交。

---

## 6. Agent B：资源 ownership、补偿事务与严格数据边界

### 6.1 独占写集

```text
cmd/mcp-lsp/middleware/logging.go
cmd/mcp-lsp/middleware/*_test.go
cmd/mcp-lsp/multilsp/manager_symbols_fallback.go
cmd/mcp-lsp/multilsp/*symbols*fallback*_test.go
cmd/mcp-lsp/runtime.go
cmd/mcp-lsp/runtime_test.go

internal/provider/shared/history_decode*.go                 # 可新增
internal/provider/claudecli/session_history*.go
internal/provider/codexapp/session_history*.go

internal/module/thread/scratchpad.go
internal/module/thread/{archive,stop,service,module}.go
internal/module/thread/*scratchpad*_test.go

cmd/mcp-orch/store/sharedfile/importer.go
cmd/mcp-orch/store/sharedfile/importer_test.go

internal/platform/toolbridge/handler_peer_decode.go
internal/platform/toolbridge/*surface*_test.go

internal/module/memory/ui_rpc_mutations.go
internal/module/memory/ui_rpc_errors.go                      # 可新增；不得改 ui_rpc.go
internal/module/memory/*merge*test.go

internal/module/insight/service.go
internal/module/insight/service_test.go
```

### 6.2 B1：MCP-LSP 日志、YAML fallback 与 Close 错误

- 日志 RED：secret 同时进入 request/response/error，输出不得包含 secret、源码、patch、原始路径。
- 日志实现：只保留 tool、bytes、duration、status、稳定 error kind/code；删除 payload compact helper。
- YAML RED：超过 64 KiB 的 value 后仍有合法 key，必须完整返回后续符号与行号。
- YAML 实现：直接遍历已有 `[]string`，删除 join/scanner 二次扫描。
- stdio RED：run nil/error × close nil/error 四格矩阵。
- stdio 实现：`errors.Join(runErr, closeErr)`；runner 已有上层错误出口，不新增 health DTO。

### 6.3 B2：Provider history 与 insight 严格解码

- 新建 history 专用严格 helper；不要改变事件路径使用的宽松 time helper。
- 非空 timestamp 必须满足 RFC3339/RFC3339Nano；metadata 只允许显式约定的空/null或 JSON object。
- Claude/Codex mapper 改为 `([]dto.Message, error)`，错误包含 provider 与 message index；坏记录使整个 read 失败。
- `insight.toSnapshot(s)` 返回 error，只接受 JSON string array；invalid、null、object、number、混合数组全部失败。
- 不新增 degraded 字段；持久化坏数据是失败，不是成功降级。

### 6.4 B3：Scratchpad partial cleanup

- 路径解析和 cleanup helper 全部返回 error。
- Stop/Archive/Delete 状态已变更后 cleanup 失败时，完成必须执行的内存/turn/event 收束，再返回 typed partial-cleanup error。
- 错误不得暴露绝对 scratchpad 路径。
- 本 lane 不新增 RPC 字段；若 UI 需要稳定 partial code，由主 agent 串行加载字段守卫后接线。

### 6.5 B4：Sharedfile durable import

使用 SQLite 临时库和可注入文件系统故障覆盖 temp fsync/close、rename、directory fsync/close、SQLite upsert、rollback remove 双失败。

实现顺序：

```text
path lock
-> snapshot old SQLite/file state
-> stream to same-directory temp
-> temp fsync + close
-> SQLite upsert
-> atomic publish
-> directory fsync + close
-> failure restores SQLite/file and joins all errors
```

保持流式复制，禁止为了复用 helper 把大文件整体读入内存。包内增加只读 reconcile/detect，识别 temp、DB 指向缺失文件和无索引正式文件；接入公共 health 由主 agent串行处理。

### 6.6 B5：MCP client ownership

- 全部 clients 创建成功后，先一次性把 ownership 转给 surface，再执行 backfill/filter/schema。
- 所有错误出口统一 Close surface，并 `errors.Join(primaryErr, closeErr)`。
- `Close` 聚合全部 client 错误；表驱动测试证明任一位置失败时每个 client 恰好关闭一次。

### 6.7 B6：Memory merge 补偿

- delete B 与 rollback A 双失败必须同时保留。
- 用本包 sentinel 标记 durable partial failure；所有路径统一失效相关缓存并发布刷新事件。
- 不在并行阶段新增 RPC result 字段；若产品需要 partial/degraded reason，由主 agent串行走字段守卫。

### 6.8 Agent B 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/middleware ./cmd/mcp-lsp/multilsp ./cmd/mcp-lsp -count=1
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/provider/claudecli ./internal/provider/codexapp -count=1
./scripts/test_with_guard.sh ./internal/module/thread -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/sharedfile -count=1
./scripts/test_with_guard.sh ./internal/platform/toolbridge -count=1
./scripts/test_with_guard.sh ./internal/module/memory ./internal/module/insight -count=1
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./cmd/mcp-orch/store/sharedfile ./internal/provider/... ./internal/module/thread ./internal/platform/toolbridge ./internal/module/memory ./internal/module/insight -race -count=1
make guard
make capcontract-check
make sqlc-verify
make build-plain
git diff --check
```

建议拆为六个中文提交：MCP-LSP、history/insight、scratchpad、sharedfile、toolbridge、memory merge。

---

## 7. Agent C：异步可靠性、降级可见性、前端与治理

### 7.1 独占写集

```text
internal/module/thread/agent_launched_worker*.go
internal/module/thread/events.go
internal/module/thread/events_test.go

frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js
frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.test.js

internal/archtest/lifecycle_onstart_guard_test.go
internal/archtest/runner_actor_guard_test.go
internal/archtest/*lifecycle*fixture*_test.go
internal/archtest/migration_sqlc_guard_test.go

cmd/mcp-lsp/multilsp/recycler.go
cmd/mcp-lsp/multilsp/*recycler*_test.go

frontend-app/src/pages/chat/runtime/RuntimeToolbar.jsx
frontend-app/src/pages/chat/runtime/RuntimePanel.css
frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx
frontend-app/src/shared/styles/ThemePolish.css
frontend-app/src/styles.test.js

internal/module/skill/events.go
internal/module/skill/events_test.go
internal/module/skill/service.go
internal/module/skill/module.go                         # 仅 RunGroup 装配需要

internal/module/memory/ui_rpc.go
internal/module/memory/*ui_rpc*test.go
frontend-app/src/pages/memory/MemoryPage.jsx
frontend-app/src/pages/memory/MemoryPage.test.jsx
frontend-app/src/pages/memory/MemoryPage.css

cmd/mcp-orch/store/taskdag/integration_test.go           # 删除
```

### 7.2 C1：SQLite SSOT 收口

- 在 archtest 动态解析 `cmd/mcp-orch/sqlc.yaml`，断言 engine 只能是 SQLite 且 schema 指向 SQLite baseline。
- 增加 guard，SQLite engine 下禁止 taskdag 活跃测试出现 `TASKDAG_INTEGRATION`、`TestTaskDagPGIntegration`、testcontainers、`requires PG`、`SELECT FOR UPDATE`。
- 先让 guard 因现有文件 RED，再删除陈旧 `integration_test.go`。
- 不修改 sqlc.yaml，不引入 PG。

### 7.3 C2：AgentLaunched worker 失败传播

- processor 返回 error；store/RPC 错误不得被 business no-op 混淆。
- worker 保持单 goroutine和单 timer，有界退避；成功才增加 processed。
- 内部 health 区分 failed/retried/dropped/last error；不默认序列化到 RPC。
- RED 覆盖首次失败后成功、持续失败耗尽、binding store error、Stop 不无限等待和同 key 新事件顺序。

### 7.4 C3：Lifecycle guard 去除永久 Skip

- fixture 覆盖 `OnStart -> helper -> go/safego/ticker/Start/Run/Watch/Serve`，以及允许的同步 setup/root bridge。
- AST helper 最多追踪同包一跳，并输出完整调用路径与行号。
- 删除两个 skeleton Skip；增加 meta-guard 禁止目标 archtest 出现无条件 Skip。
- 不扩大目录级 allowlist。

### 7.5 C4：RSS recycler degraded health

- 单次 probe error 不终止 runner、不把 RSS 当零、不关闭 client。
- 内部 health 记录 total/consecutive failures、last error/time、degraded；成功后恢复。
- 达到阈值后 degraded；如果未来向 RPC 暴露字段，必须另走字段守卫，本 lane默认不扩协议。

### 7.6 C5：Skill debounce 单 RunGroup worker

- 删除每事件一个 `safego.Go(context.Background())`。
- service 持有单 wake channel、单 timer、pending queue；一个阻塞式 `Run(ctx) error` worker完成 reset/flush/cancel。
- 通过 `group:"runners"` 装配；不得在构造器启动 goroutine。
- 用 fake clock、worker start counter 和 1,000-event burst 测试证明只有一个 worker；不得依赖 `runtime.NumGoroutine`。

### 7.7 C6：Frontend trace 使用现有 ACK

- 发送前 `slice` 快照，不 `splice`；合法 ACK 后才从队头移除。
- 严格校验 enabled/recorded/dropped，且 recorded+dropped 必须等于 batch 大小。
- RPC/ACK 失败保留同一批次并用单 timer 有界退避；新事件追加在 retry batch 后。
- disabled ACK 是终态，删除批次并记录 dropped/disabled warning。
- 不修改后端 ACK DTO。ACK 丢失会形成 at-least-once；若未来要求 exactly-once，另开字段守卫任务引入 batch ID 和后端幂等 owner。

### 7.8 C7：RuntimeToolbar 只读语义

- 无行为 button 改为 span/output，保留可访问 label，删除 tab stop 和按钮 hover/focus 样式。
- 测试断言统计不再具有 button role，仍可被辅助技术读取。

### 7.9 C8：Ignored similarity degraded UI

- `LoadIgnored` 失败时设置现有 `SimilarityDegraded`，清空 groups并停止生成错误相似组。
- `MemoryPage` 消费现有 `similarityDegraded`，显示可访问 warning并禁用 similarity 操作；不能显示“无相似记忆”的成功状态。
- 本任务虽复用既有字段，但新增跨层消费者，必须加载字段守卫：验证 Go JSON tag、前端 true/false one-hot、缺字段旧快照兼容和错误类型 fail-fast。

### 7.10 Agent C 验证

```bash
./scripts/test_with_guard.sh ./internal/module/thread -race -count=1
./scripts/test_with_guard.sh ./internal/module/skill -race -count=1
./scripts/test_with_guard.sh ./internal/module/memory -count=1
./scripts/test_with_guard.sh ./cmd/mcp-lsp/multilsp -race -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag/... -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
make sqlc-verify
make guard
make codemap-check
make project-map-check
make capcontract-check
make build-plain
cd frontend-app && npm run lint && npm test && npm run build
git diff --check
```

建议按 SQLite、AgentLaunched、lifecycle、RSS、skill worker、trace、toolbar、similarity 拆为八个中文提交；允许合并 lifecycle+RSS、trace+toolbar，但测试不得延后。

---

## 8. 主 Agent 串行集成任务

三个执行 agent 不得并行处理以下事项，统一由主 agent 在 lane 合入后完成：

1. 审查三个 lane 的实际 diff 是否越界、是否混入用户文件或生成物。
2. 如新增或跨层消费字段，加载“字段守卫”逐一验证：
   - PID registry 持久化 identity 字段。
   - similarity degraded 既有字段的新前端消费者。
   - 任何经批准新增的 partial/degraded/health/RPC 字段。
3. 决定是否把 sharedfile reconcile、RSS health、worker health 接入公共 health/RPC；默认不为了展示而扩大契约。
4. 增加或合并以下上层 guard：SQLite engine SSOT、archtest no-skip、MCP raw payload 禁止日志、Close ownership failure fixture。
5. 处理真实生成物刷新；不得把 hook 产生的无关 drift 混入 lane commit。
6. 串行复核 TeamSync rollback、PID 跨平台、Cron 双 scheduler、trace ACK 丢失的残余风险。

## 9. 全仓完成门禁

三个 lane 合并并完成主 agent 集成后运行：

```bash
make test
make guard
make codemap-check
make project-map-check
make capcontract-check
make sqlc-verify
make build-plain
python3 scripts/validate_super_agent_skills.py

cd frontend-app
npm run lint
npm test
npm run build

git diff --check
git status --short
git diff --stat
```

此外必须：

- 对全部修改源码执行 LSP diagnostics，所有 severity 清零；工具失败记录 blocker。
- `make test` 输出中的 skip、`[no tests to run]` 和 deferred E2E 单独披露，不能汇总为全覆盖。
- 检查三个 lane 的提交 SHA、合并对象、未提交/未跟踪文件和最终 remote 状态。
- 未实际提交时只报告“可提交”；未 push 并核对远端 SHA 时不得报告“已推送”。

## 10. 停机条件

出现以下任一情况，停止对应 lane，不做推测性扩张：

- 基线或 worktree 写集与计划不一致。
- 需要修改其他 agent 独占文件。
- PID 身份在 Darwin/Linux/Windows 只能退化为 PID-only。
- TeamSync 无法在失败后重建旧 watcher。
- Cron 修复无法证明旧执行失租后不再产生副作用。
- 需要新增 migration、PG 依赖或第二个数据库 owner。
- 需要新增跨层字段但未加载字段守卫或无法建立完整消费闭环。
- 任一 LSP severity、focused test、race、guard 或 build 失败且无法在本 lane 安全修复。

## 11. 最终验收清单

- [x] Agent A 四个 P1 全部有 RED、GREEN、race、LSP 和中文提交证据。
- [x] Agent B 九个 P2 全部关闭，资源 ownership 和补偿双失败均有测试。
- [x] Agent C 七个行为风险关闭，陈旧 PG 测试删除且 SQLite guard 生效。
- [x] 20 项真阳逐项映射到提交和测试；6 项撤销内容未被误修。
- [x] 三 lane 的业务源码无写集交叉；生成物交叉由主 agent 统一重建，越界均有审批记录。
- [x] 字段守卫覆盖 PID identity 持久化字段和 similarity degraded 跨层消费。
- [x] 全仓门禁与前端三门禁取得新鲜证据。
- [x] 最终 Git/remote 状态按事实报告。

## 12. 2026-07-14 实际执行记录

### 12.1 分支与合并证据

三条 lane 均从 `40d907e4a7dd05cf49d5f545dba9cc62033a8474` 建立独立 worktree：

| Lane | 分支最终提交 | 主分支合并提交 | 修复提交数 |
|---|---|---|---:|
| A | `2d4cb551207acc312ece93941d5cbcf1d4563407` | `24804666c` | 4 |
| B | `9e9553cd9faa8754a9fc6dcfd83d4b379abada32` | `e9f1c7d40` | 6 |
| C | `a88e82e78885683bdb909c07de09d0c7c47d1e78` | `d83460588` | 7 |

三个 lane 的交集检查只发现 project-map 生成物，没有业务源码交叉。由于三条分支来自同一基线且业务写集互斥，主 agent 批准不做无意义 rebase，改为 A → B → C 串行 `--no-ff` 合并，再以官方生成器统一重建 codemap、project-map 和 capability contract。最终通过 `merge-base --is-ancestor` 确认三个 lane 的最终提交均已进入 `main`。

### 12.2 主 agent 巡查、纠偏与扩写审批

- Agent B 在 RED 前误改生产代码：要求恢复生产文件、取得真实 RED 后再实现。
- Agent A 的 PID 修复最初缺少 TERM/KILL 间 PID 复用注入测试：退回补充私有 process ops 和 TOCTOU 测试后批准。
- Agent A 的 Cron 扩写仅批准到 Cron → Turn 的窄取消边界；`ListJobsClaimedBy` 基础设施错误改为立即返回，只有带类型的逐 job 续租错误进入有界收口。
- Agent B 的 scratchpad 影响面经 LSP 证明包含 `spawn.go`，批准纳入同一 ownership 修复。
- Agent C 的 trace ACK 逻辑修正为接受 `recorded + dropped == batch` 的有效部分丢弃，避免重复重传。
- Agent C 的 skill debounce 增加 cancel 前最终 flush，并把停止健康状态改为严格失败传播。
- Agent C 的 similarity 字段对非布尔值 fail-fast；缺字段仅保留旧快照兼容的 `false`，并加入 Go JSON tag roundtrip。
- PostgreSQL、`pq`、PG 方言和 PG testcontainers 始终是非目标；SQLite SSOT 与 release guard 是唯一数据库落点。

### 12.3 主分支新鲜门禁

以下命令均在三个合并提交进入 `main` 后重新执行：

```text
make test                                                     PASS（全包 -race）
make guard                                                    PASS
make codemap-check                                            PASS
make project-map-check                                        PASS（drift=OK）
make capcontract-check                                        PASS（41 packages）
make sqlc-verify                                              PASS（SQLite root + mcp-orch）
make build-plain                                              PASS
python3 scripts/validate_super_agent_skills.py                PASS
cd frontend-app && npm run lint && npm test && npm run build  PASS
git diff --check                                              PASS
```

前端结果为 154 个 test files、1927 个 tests 全部通过。LSP 对全部变更源码分批 diagnostics；两个 Hint 已修复，跨平台 build-tag 和 SQLite import 的批量诊断噪声经单文件收窄重试后均为 `No diagnostics found`。

披露：全量 Go 输出包含若干 `[no test files]` 包；`internal/e2e/rpc_runtime` 为 `[no tests to run]`，因此不把本次结果表述为 RPC E2E 已覆盖。首次 `make test` 还暴露 `node_modules` 目录存在但缺 `vite` 的环境误判，经 `npm ci` 恢复锁文件依赖后原命令通过。

### 12.4 最终状态边界

- 本次完成的是本地 `main` 集成与提交；未执行 push。
- `origin/main` 在集成时仍为基线 `40d907e4a7dd05cf49d5f545dba9cc62033a8474`。
- 两个 lane 临时 stash 已确认对应内容全部进入各自提交，但为保留可审计恢复点暂不删除。
