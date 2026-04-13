# P18 Phase 4.5：Provider 归一前置解耦

> 预计：3-5 天 | 依赖：Phase 3 | 必须在 Phase 4 之前完成
> 来源：30 个 agent Claude/Codex 归一审查

---

## 背景

Phase 4 的两阶段装配无法直接落地，因为 V3 当前存在 3 个结构性阻塞：

1. **Prompt/BaseInstructions 语义污染**：`start_session.go:20` 把 system prompt 回填到 thread 元数据
2. **Provider wire format 差异**：codex 是结构化双字段，claude 是单字符串拼接
3. **Resume 不持久化 instructions**：跨进程恢复会丢 prompt

---

## 任务 1：语义解耦（推荐方案 C）

### 当前污染链路

```text
BaseInstructions
  → start_session.go:20  req.Prompt = FirstNonEmpty(req.Prompt, req.BaseInstructions)
    → lifecycle.go:56    launchAgent(..., req.Prompt, ...)  // launch name 被污染
    → lifecycle.go:80    state.Prompt = req.Prompt          // thread store 被污染
    → lifecycle.go:116   Resume 继续用 state.Prompt         // resume 被污染
    → lifecycle_helpers.go:146  toRef() fallback Prompt     // UI 展示被污染
```

### 解耦方案

**目标语义：**
- `Name`：线程展示名（launch name / store / resume / UI）
- `BaseInstructions`：System Prompt（只给 provider）
- `DeveloperInstructions`：System Context tail（只给 provider）
- `Prompt`：废弃兼容字段，不再作为 lifecycle 主字段

**代码改法：**

| 文件 | 改动 |
|------|------|
| `start_session.go:18-24` | 删除 `req.Prompt = FirstNonEmpty(req.Prompt, req.BaseInstructions)`；改为显式派生 `displayName := FirstNonEmpty(req.Name, req.Prompt)`，仅用于 lifecycle 展示 |
| `start_session.go:146-152` | `Instructions` 只取 `req.BaseInstructions`；兼容 fallback 落在 provider DTO `dto.StartSessionRequest.Instructions` 映射层（承接 legacy 映射），不再从 displayName 回写 |
| `lifecycle.go:56` | `launchAgent(...)` 使用 `displayName`，不再直接吃 `req.BaseInstructions` |
| `lifecycle.go:80` | 兼容期先把 `displayName` 持久化到现有 display-name 槽位；若后续新增 `Name` 字段，再做 schema/backfill |
| `lifecycle.go:116,141,192,219,245,275` | Resume/Fork/Recover 统一读取 persisted displayName，而不是 system prompt 文本 |
| `lifecycle_helpers.go:146-152` | `toRef()` 只展示 persisted displayName/compat fallback，不展示 instructions |
| `service.go:131-138` | `SetName()` 只改 display name，不碰 instructions |
| `rpc_types.go:76-88` | legacy `prompt` 优先作为 displayName fallback；仅显式 `instructions/baseInstructions` 进入 provider prompt |
| `binding.go:59-67` | Wails 旧入口未传 `Name` 时继续走 `displayName := FirstNonEmpty(name, prompt)` 兼容 |
| `*_test.go` | 补 `binding_test` / `rpc_types_test` / `service_handlers_test` / `resume/fork/recover/toRef` 回归 |

### 兼容迁移策略

- 第一阶段不强制立刻给 thread store 加 `Name` 字段；先把现有展示槽位收敛为 **display name only** 语义
- 旧 desktop/RPC 调用方若未提供 `Name`，必须继续从 `Prompt` 派生 `displayName`，避免 launch name 变空
- legacy `prompt` 不再双向污染 instructions；只允许单向兼容映射

---

## 任务 2：PromptAssemblyService 接口定义

```go
package prompt

type AssemblyService interface {
    // 会话启动时组装
    AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error)
    // 每轮 turn 前组装
    AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error)
    // 缓存失效
    Invalidate(ctx context.Context, reason InvalidateReason) error
}

type StartInput struct {
    ThreadID              string
    Name                  string
    Prompt                string // legacy display-name fallback
    BaseInstructions      string
    DeveloperInstructions string
    Provider              string
    CWD                   string
    Language              string
}

type TurnInput struct {
    ThreadID    string
    Provider    string
    UserText    string
    SkillPrompt string
    Attachments []string
    CurrentDate string
}

type InvalidateReason string

const (
    InvalidateClear          InvalidateReason = "clear"
    InvalidateCompact        InvalidateReason = "compact"
    InvalidateWorktree       InvalidateReason = "worktree"
    InvalidateResumeRestore  InvalidateReason = "resume_restore"
    InvalidateProviderSwitch InvalidateReason = "provider_switch"
)

type StartAssembly struct {
    DisplayName          string
    BaseInstructions     string // → provider system prompt
    DeveloperInstructions string // → provider system tail
}

type TurnAssembly struct {
    UserContextText string // → 前置 synthetic text input
}

type PromptAssemblySnapshot struct {
    DisplayName           string
    BaseInstructions      string
    DeveloperInstructions string
    Provider              string
    Version               int
    Hash                  string // 结构化产物摘要，用于校验/去重/恢复时匹配
}
```

**放置位置**：`internal/module/prompt/assembly.go`
**不放**：provider 内部

### 职责边界

| 组件 | 负责 | 不负责 |
|------|------|--------|
| `PromptRegistry` | section 注册、system prompt 渲染、cache | provider DTO 映射 |
| `PromptContext` | `gitStatus` / `claudeMd` / currentDate 等运行时上下文聚合 | lifecycle 兼容字段迁移 |
| `PromptAssemblyService` | 调 `PromptRegistry + PromptContext` 产出 `StartAssembly/TurnAssembly/Snapshot` | provider transport 细节、把 displayName 回写成 instructions |
| `thread/start_session` | 在 provider DTO 前注入 assembly 产物 | 直接拼装 prompt 内容 |
| provider | 消费 assembly 产物，按各自 wire format 落地 | 再次计算 UserContext/SystemContext |

---

## 任务 3：Codex 链路改造

### thread/start（阶段 A）
- 注入点：`start_session.go:146-152`（provider DTO 前）
- 产物映射：
  - `StartAssembly.BaseInstructions` → `dto.Instructions` → `threadStartParams.baseInstructions`
  - `StartAssembly.DeveloperInstructions` → `Config.developerInstructions` → `threadStartParams.developerInstructions`
- **codex wire format**：`baseInstructions + developerInstructions`（独立字段，无 userContext）

### turn/start（阶段 B）
- 注入点：`session_turn.go:76-85`（turnInputsFromRequest 前）
- 产物映射：`TurnAssembly.UserContextText` → 前置 synthetic text input（在 skill prompt 之后）
- **顺序**：skill prompt → 前置 synthetic text input（UserContext） → user inputs
- 组装责任在 `PromptAssemblyService` / thread turn 入口；provider 只接收合成后的 synthetic input，不在 provider 内部重新计算 UserContext
- 参考先例：skill prompt 已有同类注入（`module.go:231-248`）

### Resume
- codex `thread/resume` 只带 threadID/cwd/model（`driver.go:203-214`）
- 若坚持“resume 不重注入”，必须把 `PromptAssemblySnapshot` 作为 durability contract 持久化，并保证 app-server / runtime snapshot 不会丢 `BaseInstructions/DeveloperInstructions`
- 若要支持 cold resume / recovery replay，则统一从 `PromptAssemblySnapshot` 重新 hydrate，不保留半吊子的字段回填

---

## 任务 4：Claude CLI 链路改造

### Launch（阶段 A）
- 注入点：`start_session.go:146-152`（与 codex 共享）
- 最终落点：`transport_config.go:129-147` composeLaunchSystemPrompt()
- **claude wire format**：全拼成单个 `--system-prompt`

### turn（阶段 B）
- 注入点：`session_turn.go:173-202` `prepareTurnLocked()`
- 实现形态：**不新增 provider DTO 字段**；在 `prepareTurnLocked()` 临时拼 `userContextPrefix + buildTurnText(req)`
- `steer` 复用同一 helper，避免普通 turn / steer 顺序分叉
- **不在** `buildTurnText()` 内部改（避免 steer 重复注入）
- **顺序**：UserContext → 附件提示 → user text → skill section → output_schema

### Restart/Recovery
- claude restart 用 `s.instructions`（`session_log_watcher_integration.go:231-236`）
- `PromptAssemblySnapshot` 持久化到 thread/session runtime snapshot；codex/claude 共用同一快照结构，恢复后再做 provider-specific 映射
- snapshot 以**结构化字段 + hash/version/provider** 为主，不长期持久化完整拼装后的 provider 最终字符串，避免安全泄露与 provider 串味

---

## 任务 5：缓存失效补充

Phase 3 缓存失效点需补充：
- **Provider 切换**：codex↔claude 时必须清空 section cache（cache key 不含 provider）
- **auto-compact**：落在 V3 的 compact cleanup hook；自动 compact 完成后立即触发 invalidate
- **REPL partial compact**：落在 turn loop partial-compact cleanup hook；partial compact 成功后立即触发 invalidate

触发点统一放在：
- `start_session` 解析 provider 后、构造 provider DTO 前
- `/resume` 恢复后发现 persisted provider 与当前 provider 不一致时
- 说明：当前 `ThreadConfigPatch` **没有 provider 字段**，因此本 Phase 不把“thread config/set 切 provider”写成现状能力

---

## 任务 6：子 Agent instructions 归一

当前问题：
- 当前实现中 `orchestration_launch_agent` 只把 `in.Prompt` 写入 `LaunchRequest.Prompt`，不填 `Instructions`（`orchestration_tools.go:127-149`）
- 后续由 `start_session.go:18-20` 的 `FirstNonEmpty` 把 `Prompt` 折叠成 `Instructions`
- 子 agent 不继承主 agent 的 PromptRegistry 输出

**改法（单一路径）**：
- 子 agent 启动**必须**走 `PromptAssemblyService.AssembleStart()`，不保留“只继承 BaseInstructions”分支
- 扩展 `thread.LaunchAgentRequest` / `contract.LaunchRequest`，显式承载子 agent 所需的 `DisplayName + BaseInstructions + DeveloperInstructions`（或 `PromptAssemblySnapshot`）
- orchestration tool 输入也要从模糊 `instructions` 升级为 `base_instructions + developer_instructions`，避免 remote/local 语义继续分叉
- orchestration 层只负责透传 assembly 产物，不再依赖 `FirstNonEmpty(Prompt, BaseInstructions)` 旧折叠路径

---

## 验收

- [ ] `BaseInstructions` 不再污染 thread name / store / resume / Fork / Recover / `toRef()` / `SetName()`
- [ ] `PromptAssemblyService` 接口 + `StartInput/TurnInput/PromptAssemblySnapshot` 定义完成
- [ ] Resume/Recovery/Restart 显式依赖 `PromptAssemblySnapshot`，不再只靠污染后的 `Prompt`
- [ ] Codex thread/start 正确接收 assembly 产物
- [ ] Claude launch `--system-prompt` 正确接收 assembly 产物
- [ ] Codex turn/start UserContext 前置注入
- [ ] Claude turn UserContext 前置注入
- [ ] Provider 切换时 section cache 清空
- [ ] 子 Agent 走 PromptAssemblyService
- [ ] 回归测试通过（binding/rpc_types/service_handlers/resume/fork/recover/toRef）

---

## 数据流总图

```text
PromptAssemblyService.AssembleStart()
  ├─ BaseInstructions ──→ dto.Instructions ──┬─→ codex: threadStartParams.baseInstructions
  │                                          └─→ claude: --system-prompt (前半)
  └─ DeveloperInstructions ──→ Config ──┬─→ codex: threadStartParams.developerInstructions
                                        └─→ claude: --system-prompt (后半)

PromptAssemblyService.AssembleTurn()
  └─ UserContextText ──→ 前置 synthetic text input ──┬─→ codex: 前置 synthetic text input（在 skill prompt 之后）
                                           └─→ claude: prepend to buildTurnText()
```

---

## 源码锚点

| 文件 | 行 | 内容 |
|------|-----|------|
| internal/module/thread/start_session.go | 18-20 | FirstNonEmpty 污染源 |
| internal/module/thread/start_session.go | 146-152 | provider DTO 组装（注入点） |
| internal/module/thread/lifecycle.go | 56,80 | Prompt 扩散到 launch/store |
| internal/module/thread/start_session_helpers.go | 9-30 | DeveloperInstructions → Config |
| internal/provider/codexapp/support.go | 248-261 | codex buildThreadStartParams |
| internal/provider/codexapp/session_turn.go | 37-49,76-85 | codex turn 注入 |
| internal/provider/codexapp/module.go | 231-248 | skill prompt 先例 |
| internal/provider/claudecli/transport_config.go | 99-147 | claude --system-prompt 拼装 |
| internal/provider/claudecli/session_turn.go | 173-202 | claude turn prepareTurnLocked |
| internal/provider/claudecli/driver.go | 106-127,166-179 | claude launch 链路 |
| internal/provider/unified/client.go | 30-68 | 统一 driver 分发 |
| internal/contract/provider.go | 10-39 | provider 接口定义 |
