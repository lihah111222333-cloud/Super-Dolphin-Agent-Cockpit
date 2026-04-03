# P13 两级 DRY 工厂丰满计划

> 生成时间：2026-04-04（v4 — factory.go 豁免版）
> 前提：P0-P12 全部完成，编译/vet/archtest 全绿
> 来源：21 Agent 全仓审查 + 5 接线审查 + 10 文档审查 + 4 互审
> 核心目标：**找重复、合并重复**，不做模块重构
> 执行顺序：**W1 前置清理 → W2+W3 二级 DRY（包内） → W4 一级 DRY（跨包 shared）**

---

## 0. 两级 DRY 定义

```
二级 DRY（先做）：同一包内的重复模式 → 收敛到包内 factory.go
一级 DRY（后做）：跨 2+ 包的相同实现 → 提升到 internal/platform/shared/
```

**为什么先二级后一级**：先把每个包内部收敛干净，形成稳定的 helper 形态，再看哪些 helper 跨包重复、值得提升到 shared。否则直接提升会把未收敛的散落逻辑强行统一。

**抽取铁律**（所有 W2/W3/W4 Agent 必须遵守）：
> **抽取 = 迁移到 factory.go / shared + 删除原处旧实现 + 原调用点改为调用工厂函数**
> 禁止只加新不删旧。每个任务完成后必须用 `lsp_xref(references)` 确认旧函数零残留。
> 旧函数所在文件若因删除变空，则删除该文件。

---

## 1. W1 — 前置清理（守卫违规 + 死代码）

> 优先级：P0 | 依赖：无 | Agent：3

### 1.1 守卫违规（4 文件超 400 行）

| 文件 | 行数 | 修复 |
|------|:----:|------|
| `codexapp/transport.go` | 430 | 先合并 `event_helpers.go`(47行)→`event_map.go` 腾 1 名额，再拆进程生命周期→`transport_process.go` |
| `gopls/transport.go` | 401 | 拆 stdio lifecycle→`transport_conn.go` |
| `uistate/projector_handlers.go` | 418 | 将超限的 handler 模板逻辑迁入 `factory.go`（不受 400 行限制） |
| `uistate/snapshot_helpers.go` | 411 | 将 clone helper 迁入 `factory.go` |

> **注意**：factory.go 不计入包文件数，W2/W3 收敛到 factory.go 不受 15/15 限制。
> 但 W1 新增普通文件（如 `transport_process.go`）仍受限，codexapp 需先合并腾名额。

### 1.2 死代码清理

| 范围 | 处置 |
|------|------|
| legacy factory stub 全目录 | 删除 |
| legacy provider stub + empty helper packages | 删除 |
| 15 个 0 引用符号（rpc.BindEventToNotify/rpc.Registry/rpc.WithCWD/codexapp.NewDriver/approvalPolicyFromThreadConfig/execShell/shellQuote/collectChangedSkillNames/NewPublishDiagnosticsNotification/NewLogMessageNotification/RenderTable/TurnTimeout/NewTypedEmitter/NewProjector + dto alias） | 删除或降 unexported |

### 1.3 预留保留（不动）

| 包 | 原因 |
|-----|------|
| `store/cwdlock` | V2 启动链预留 |
| `store/topologyapproval` | 初始 schema 预建 |
| `taskdag/WorkerLease` + `sqlc/task_ack` | DAG/协作预留 |
| `commandcard`/`prompt` 版本化 | 管理面预留 |
| `store/hookstore` | 契约刻意上提，已接线 |
| `mcp-orch/workspace` module/rpc/event | 休眠适配层，service+tools 已活跃 |

### 1.4 Agent 分配

| Agent | 任务 |
|-------|------|
| W1-A | 守卫违规 4 文件拆分 |
| W1-B | pkg/* 整包删除 |
| W1-C | 0 引用导出符号 + dto alias 清理 |

**验证**：`go build` + `go vet` + `TestCodeSizeGuard` + 全量 archtest

---

## 2. W2 — 二级 DRY 第一批（provider + orchestration 包内收敛）

> 优先级：P1 | 依赖：W1 | Agent：5

### 2.1 codexapp 包内 DRY → `factory.go`

| 重复模式 | 位置 | 收敛到 `factory.go` |
|---------|------|-------------------|
| `withTimeout + callTransport/Call` 模板（17 处同模板） | session.go, driver.go, recovery.go, session_history.go, session_approval.go 等 | `callWithTimeout(ctx, d, method, params)` |
| turn/start 响应解码重复 | session.go:128 + recovery.go:204 | `decodeTurnStartResult()` |
| URL 端口解析重复 | support.go:244 + support.go:264 | `parsePortFromURL()` |
| `ctx == nil` + `ctx.Err()` 守卫重复 | driver.go:63, session.go:131 等 | `checkCtx(ctx)` |
| `boolValue` 跨包近似 | event_map.go:326 (codexapp) + event_map.go:275 (unified) | 各包保留（语义略不同，不强合） |

> 现有 `support.go` 保留不动，新建 `factory.go` 承接收敛后的工厂函数（不受 400 行和文件数限制）。
> **清理规则**：抽取到 `factory.go` 后，必须删除原文件中的旧实现，原调用点改为调用 `factory.go` 中的函数。用 `lsp_grep` 搜索旧函数名确认全仓零残留。


### 2.2 claudecli 包内 DRY → `factory.go`

| 重复模式 | 位置 | 收敛到 `factory.go` |
|---------|------|-------------------|
| `firstNonEmpty` 定义在事件文件但被多处复用 | session_events.go:275 | 迁移到 `factory.go`（W4 统一提升时再迁到 shared） |
| `ctx == nil` + `ctx.Err()` 守卫重复 | driver.go:63, session.go:131, session_config.go:15, history.go:19, thread_identity.go:133 | `checkCtx(ctx)` |

### 2.3 orchestration 包内 DRY → `factory.go`

| 重复模式 | 位置 | 收敛到 `factory.go` |
|---------|------|-------------------|
| eventBus nil 判空 + header 组装模板 | events.go:13-105（多处重复 nil check + resolveEventTime） | `emitEvent(bus, eventType, agentID, fields...)` |
| 启动状态重置/运行时字段赋值 | helpers.go:244, launcher.go:41, launcher.go:142, service_launcher_bridge.go:286 | `resetLaunchState()` |
| stop/reset 流程 | helpers.go:163 + service_launcher_bridge.go:144 | `cleanupAgentState()` |
| report 时间源不一致 | report.go:123/142/153 用 `time.Now()`，其他用 `resolveEventTime` | 统一用 `resolveEventTime` |

### 2.4 unified 包内 DRY

| 重复模式 | 位置 | 收敛方式 |
|---------|------|---------|
| `firstNonEmpty` | ui_tokens.go:142 | 保留（W4 统一提升时再迁移） |

### 2.5 Agent 分配

| Agent | 任务 |
|-------|------|
| W2-A | codexapp 包内 DRY（§2.1） |
| W2-B | claudecli 包内 DRY（§2.2） |
| W2-C | orchestration 包内 DRY（§2.3） |
| W2-D | unified 包内 DRY（§2.4）+ 互审 W2-A/B |
| W2-E | 互审 W2-C + 全量验证 |

> **清理规则**：同 W2。抽取到 `factory.go` 后删除原处旧实现，确认旧函数零残留。

**验证**：受影响包 `go test` + 全量 archtest

---

## 3. W3 — 二级 DRY 第二批（module + platform 包内收敛）

> 优先级：P1 | 依赖：W1 | 可与 W2 并行 | Agent：5

### 3.1 skill 包内 DRY → `factory.go`

| 重复模式 | 位置 | 收敛到 `factory.go` |
|---------|------|-------------------|
| `nextXxxCommandIndex` 6 个同模板函数 | exec_tokenizer_safety.go:57/76/91/112/131, exec_tokenizer.go:336 | 策略表 `wrapperSkipRules` + `skipOptionsAndFindCommand(tokens, rules)` |
| 命令分类表散落 | exec.go:16/23/28/33/37 | 收敛到 `factory.go` 统一分类 map |

### 3.2 uistate 包内 DRY → `factory.go`

| 重复模式 | 位置 | 收敛到 `factory.go` |
|---------|------|-------------------|
| `lock→mutate→sort→patch→emit` 模板（投影层 21 个 apply*） | projector_handlers.go(14) + projector.go(7) | `applyMutation(mutator func(), patchBuilder func())` 通用模板 |
| clone + activity 分类 + 状态归一 | snapshot_helpers.go 全文 | clone 系列 + 状态归一 helper 迁入 `factory.go` |

### 3.3 thread 包内 DRY

| 重复模式 | 位置 | 收敛方式 |
|---------|------|---------|
| `firstNonEmpty` | lifecycle_helpers.go:85 | 保留（W4 统一提升时再迁移） |
| 配置 patch 构造散落 | command.go:179/203/229/265 + lifecycle_helpers.go:101/256 | 收敛到 `lifecycle_helpers.go` 或 `command.go` 单处 |

### 3.4 dashboard 包内 DRY

| 重复模式 | 位置 | 收敛方式 |
|---------|------|---------|
| `firstNonEmpty` | detail.go:60 | 保留（W4 统一提升时再迁移） |
| 视图构造散落 | ui_page.go:39 + service.go:204 + detail.go:22 | 不强合（职责不同），保持现状 |

### 3.5 rpc 包内 DRY

| 重复模式 | 位置 | 收敛方式 |
|---------|------|---------|
| `cloneMap` / `cloneRawMessage` | approval_support.go:228/251 | 保留（W4 提升到 shared/jsonutil） |

### 3.6 bootstrap 包内 DRY

| 重复模式 | 位置 | 收敛方式 |
|---------|------|---------|
| `firstNonEmpty` | env_helpers.go:5（整文件只有这一个函数） | 保留（W4 统一提升时再迁移） |
| 退避逻辑 | reconnect.go:48 + hooks.go:250 | 包内已分文件，保持现状 |

### 3.7 Agent 分配

| Agent | 任务 |
|-------|------|
| W3-A | skill 包内 DRY（§3.1） |
| W3-B | uistate 包内 DRY（§3.2） |
| W3-C | thread + dashboard 包内 DRY（§3.3 + §3.4） |
| W3-D | rpc + bootstrap 标记保留（§3.5 + §3.6）+ 互审 W3-A/B |
| W3-E | 互审 W3-C + 全量验证 |

**验证**：受影响包 `go test` + 全量 archtest

---

## 4. W4 — 一级 DRY（跨包 shared 提升）

> 优先级：P2 | 依赖：W2+W3 完成 | Agent：5

W2/W3 完成后，每个包的 helper 已稳定。此时把跨包重复的 helper **提升到 shared**。

### 4.1 shared/validation.go 重做

**删除**现有 `RequireNonEmpty`（0 消费者）。**新增**：

| 函数 | 来源（W2/W3 后已收敛到各包 helper） | 迁移包数 |
|------|----------------------------------|:--------:|
| `FirstNonEmpty(values ...string) string` | codexapp/support, claudecli/helpers, thread/lifecycle_helpers, dashboard/detail, bootstrap/env_helpers, eventsurface/bind, historyjsonl/history, mcpcontrol/report_handlers, rpc/approval_support, uistate/timeline/projector, unified/ui_tokens | 11 |
| `FirstTrimmed(values ...string) string` | orchestration/rpc_types.go + turn/rpc_helpers.go（当前 2 处） | 2 |
| `ClampLimit(val, min, max, def int) int` | dashboard/service + thread/history（同语义 2 处）+ limit 类近似 8+ 处（见 §4.6） | 10+ |

### 4.2 shared/jsonutil.go 新建

| 函数 | 来源 | 迁移包数 |
|------|------|:--------:|
| `CloneRawMessage(raw json.RawMessage) json.RawMessage` | rpc/approval_support, hooks/registry, gopls/client, mcp-orch/tools | 4 |
| `CloneJSONMap(m map[string]any) map[string]any` | rpc/approval_support (浅), uistate/preferences (深) | 2 |
| `CloneJSONValue(v any) any` | uistate/preferences | 1+ |

### 4.3 shared/pathscope.go 新建

| 函数 | 来源 | 迁移包数 |
|------|------|:--------:|
| `NormalizeRelativePath(path string) string` | workspace/service_helpers, lspgui/service, mcpserver/search/fileutil | 3 |
| `ContainsPath(root, target string) bool` | lspgui/service, mcpserver/search/fileutil, skill/skills_fs | 3 |

**不提升**（R5 审查确认语义不统一）：`ScopeCWD`、`ResolveAbsolutePath`

### 4.4 shared/idgen.go 去重

- 删除 `dto/shared/ids.go` 的 `NewID`
- 迁移 3 个调用点到 `platform/shared.NewID`

### 4.6 审查新发现的跨包重复（R2 补充）

| 函数 | 位置 | 目标 |
|------|------|------|
| `cloneRuntimeConfigMap` (2处完全重复) | thread/history.go:144 + codexapp/support.go:186 | `shared/jsonutil.go` |
| `cloneTime` (3处逐字相同) | orchestration/events.go:174 + mcp-orch/tools/command_tools.go:152 + uistate/state.go:266 | `shared/jsonutil.go` |
| `cloneStrings` (3处近似) | bootstrap/env.go:302 + hooks/registry.go:251 + mcpcontrol/registry_support.go:227 | `shared/jsonutil.go` |
| `filterKeys` (2处近似) | bootstrap/env.go:271 + mcpcontrol/handlers.go:268 | `shared/jsonutil.go` |
| `cloneRaw` (2处近似) | bootstrap/env.go:309 + protocol/codec.go:221 | `shared/jsonutil.go` |
| limit 类 (8+处同模板) | dashboard/service, thread/history, workspace/service, mcp-orch/tools, lsp/tools, bootstrap/env 等 | `shared/validation.go` → `ClampLimit` |

### 4.5 shared/retry.go 补能力

- 补 `Policy` struct + `MaxDelay` + `Jitter` + `OnRetry` callback
- **不统一** `IsTransient`（R5 审查确认 bootstrap 和 codexapp 错误语义不同）

### 4.7 Agent 分配

| Agent | 任务 |
|-------|------|
| W4-A | validation.go 重做：FirstNonEmpty(11处) + FirstTrimmed(2处) + ClampLimit(10+处) |
| W4-B | jsonutil.go 新建：CloneRawMessage(4处) + CloneJSONMap + cloneRuntimeConfigMap(2处) + cloneTime(3处) + cloneStrings(3处) + filterKeys(2处) |
| W4-C | pathscope.go 新建：NormalizeRelativePath + ContainsPath/ensureWithinRoot(4处) |
| W4-D | idgen.go 去重 + 迁移 3 调用点 |
| W4-E | retry.go 补能力 + 互审 W4-A~D |

> **清理规则**：提升到 shared 后，必须删除各包原处的旧实现，原调用点改为 `import shared` + 调用 shared 函数。
> 用 `lsp_grep` 搜索旧函数名确认全仓零残留。旧函数所在文件若变空则删除文件。

**验证**：`go test ./internal/platform/shared/...` + `TestSharedBudget` + 受影响包全量测试 + 全量 archtest

---

## 5. 验收标准

| 维度 | 当前 | W1 后 | W2+W3 后 | W4 后 |
|------|:----:|:-----:|:--------:|:-----:|
| 守卫违规文件 | 4 | **0** | 0 | 0 |
| 死代码/stub | 16 | **0** | 0 | 0 |
| 各包 factory.go | 0 | 0 | **5+** | 5+ |
| 二级 DRY 违规（包内重复） | 40+ 处 | 40+ | **<10** | <10 |
| 一级 DRY 违规（跨包重复） | 30+ 处 | 30+ | 30+ | **<5** |
| shared 行数（raw wc -l） | 86 | 86 | 86 | **~600** |
| archtest 全量 | PASS | PASS | PASS | **PASS** |

**守卫规则**（`guardlib.go` 已更新）：
- `factory.go`：不计入包文件数、不计入包总行数、行数上限 800、单函数 80 行 + CC≤10 照常
- 普通文件：行数上限 400、计入包文件数（≤15）和包总行数（≤4500）

---

## 6. P10 disposition 表

| P10 原项 | P13 处置 | 原因 |
|----------|---------|------|
| Zone A shared 提升 | ✅ W4 | 核心目标（先二级后一级） |
| Zone A stub 清理 | ✅ W1 | 前置清理 |
| 命名漂移 | ⏳ 延后 | 非 DRY 范畴 |
| module.go 纯化 | ⏳ 延后 | 非 DRY 范畴 |
| 缺失文件补建 | ❌ 不做 | 模块重构 |
| rpc.go 边界收紧 | ❌ 不做 | 模块重构 |
| Validate/middleware/WithTx | ⏳ 延后 | 框架演进 |
| TypedEmitter 推广 | ❌ 裁撤 | P10 要推广，但当前 0 运行时消费者、无 recovery，已被 NewEmitter+ResilientSubscribe 替代，W1 删除 |
| truncate | ⏳ 延后 | 已有 2 处（turn/assembler + skill/skills_meta），但语义未统一（字节 vs rune） |
| pagination（limit 部分） | ✅ 已纳入 W4 | ClampLimit 已入 §4.1，limit 类 8+ 处已入 §4.6 |
| pagination（cursor 部分） | ⏳ 延后 | cursor 编解码仅 thread/history 1 处 |
| errors/fileops/hash | ⏳ 延后 | 各仅 1-2 包，不满足 Rule of Two |
| bridge_rpc.go | ❌ 不做 | eventsurface+rpc/push 已替代 |
| archtest identifier_guard | ✅ 已完成 | 已并入 TestCodeSizeGuard/guardlib |
| archtest rule count 文档修正 | ✅ 已完成 | 当前 9 个架构守卫全绿 |
