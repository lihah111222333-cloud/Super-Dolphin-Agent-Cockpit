# P18 Phase 4.5：Provider 归一前置解耦

> 预计：5-7 天（若只做 hot-path start/turn 可压到 4-5 天；含 durability / recovery / 子 agent 合同收口按 5-7 天排）| 依赖：Phase 3 | 必须在 Phase 4 之前完成
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
- `displayName` 一旦派生，必须同时写入 `threadStateFields.Name`（`thread.Started.Name` 事件可见名）与 display-only 持久槽位；否则旧入口只传 `prompt` 时仍会出现“launch/store 已修好，但 Started 事件名称为空”的半修状态
- legacy `prompt` 不再双向污染 instructions；只允许单向兼容映射
- bool/camelCase/snake_case 兼容解码要改成 **presence-aware**：current 优先、legacy 只在 current 未出现时兜底；双写冲突直接报错或告警，不允许显式 `false` 被 legacy `true` 覆盖
- Start/Resume/Fork/Recover 的事件与持久化必须保持同一 `displayName` 语义；禁止再出现 `thread.Started` 二次发布把字段冲空的缩水事件

---

## 任务 2：PromptAssemblyService 接线与补全

> 审查补记（2026-04-14）：
> - `internal/contract/prompt.go` 已经定义了 `PromptAssemblyService`、`StartInput/TurnInput`、`StartAssembly/TurnAssembly/PromptAssemblySnapshot`；`internal/module/prompt/types.go` 主要是别名桥接，不再是“从零起草 contract”的状态；
> - `internal/module/prompt/assembler.go` 的 `AssembleStart()` 已能产出 `Snapshot`，`AssembleTurn()` 也不再是空实现，而是会渲染 **registered dynamic sections**；
> - 但生产链路里 `thread/provider/orchestration` 仍**没有接线**到 `AssembleStart()/AssembleTurn()`，且现有 turn 组装还没有落实计划中的 `claudeMd + currentDate + runtime extras` 两阶段语义；
> - 因此本任务的真实范围是 **接线 + 校正现有 contract 描述 + 把 turn 组装补齐到计划语义 + 落 durability contract**。

```go
package contract

type PromptAssemblyService interface {
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
    Provider              string // v1 过渡字段；长期应收口为 capability/profile，而不是产品名分支
    CWD                   string
    Language              string
}

type TurnInput struct {
    ThreadID    string
    Provider    string // v1 过渡字段；长期应收口为 capability/profile，而不是产品名分支
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
    DisplayName           string
    BaseInstructions      string // → provider system prompt
    DeveloperInstructions string // → provider system tail
    Snapshot              PromptAssemblySnapshot
}

type TurnAssembly struct {
    UserContextText string // v1 只统一 UserContext 前置位；skill prompt / attachment hint / output schema 仍由 turn 组装链消费同一契约后落地
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

**现状落点**：`internal/contract/prompt.go` 已承载 interface + payload contract，`internal/module/prompt/types.go` 主要提供 alias，`internal/module/prompt/assembler.go` / `service.go` 负责实现
**目标落点**：保持 contract 单点定义；若后续仍坚持 `internal/dto/*` 承载跨 layer payload，需要做**迁移/收口**而不是在 provider 内重复再定义一套
**不放**：provider 内部，也不允许 provider / `cmd/mcp-*` 反向 import `internal/module/prompt`

### Snapshot 持久化契约

- `PromptAssemblyService.AssembleStart()` **必须直接产出** `StartAssembly.Snapshot`
- `thread/start_session` 负责把 snapshot 写入 `thread_states.prompt_snapshot`；provider 只消费 assembly 产物，不拥有 durability contract
- 建议 schema：`thread_states.prompt_snapshot JSONB DEFAULT NULL`
- snapshot payload 以 `PromptAssemblySnapshot{DisplayName, BaseInstructions, DeveloperInstructions, Provider, Version, Hash}` 为 canonical shape；禁止复用 `thread.Prompt`，也禁止偷塞进 `config_override`
- `Resume/Restart/Recovery` 统一先读 `PromptAssemblySnapshot`，校验 `Version + Hash + Provider`；校验失败时才走 compat fallback
- 子 Agent 透传也以 `DisplayName + BaseInstructions + DeveloperInstructions + Snapshot` 为标准形态，不再依赖旧 `Prompt` 折叠
- **审查结论**：当前仓库并不存在现成的 prompt snapshot 持久层；`threadStore` 只有 `Prompt/Model/Cwd/ConfigOverride`，`bindingStore` 无 prompt snapshot 字段，`codexapp.session.RuntimeConfigSnapshot()` 不会输出 `baseInstructions/developerInstructions`，而 claude live session 虽持有 `s.instructions + s.config.DeveloperInstructions`，但 cold restart 跨进程后同样会丢。该持久化层必须在本 Phase 明确补齐
- `ReadRuntimeConfig` / offline config 也要合并该 snapshot，否则 live/offline 的 `baseInstructions/developerInstructions` 仍会继续分叉

### Resume 持久化方案

- **Claude CLI Resume / Restart**：live session 内存里还有 `s.instructions` 与 `s.config.DeveloperInstructions`，同进程 restart 可直接复用；但 cold restart / recovery 只剩 thread/provider identity 与 runtime config，instructions 不会从 provider 自己回流，所以必须依赖 snapshot
- **Codex App Resume**：`thread/resume` 当前只传 `threadId + cwd + model`（`driver.go:203-214`），wire format 不带 `baseInstructions`，也不会回补 `developerInstructions`；只靠 provider runtime snapshot 无法重建 PromptAssembly
- **统一方案**：以 `thread_states.prompt_snapshot` 作为 resume durability source of truth，线程恢复先读 snapshot，再重建 `PromptAssembly`
- **恢复流程**：① `thread/start_session` 在首次 `AssembleStart()` 后落盘 `prompt_snapshot`；② `Resume/Recover` 读取 `thread_states.prompt_snapshot`；③ 用 snapshot 重建 `PromptAssembly{DisplayName, BaseInstructions, DeveloperInstructions, Snapshot}`；④ 按 provider 注入：codex 映射到 `baseInstructions/developerInstructions`，claude 重新走 `composeLaunchSystemPrompt()`；⑤ 仅 live session 命中时允许以内存态短路，cold path 缺 snapshot 只能走 compat fallback + durability 告警，不能再依赖 `thread.Prompt`

### 职责边界

| 组件 | 负责 | 不负责 |
|------|------|--------|
| `PromptRegistry` | section 注册、system prompt 渲染、cache | provider DTO 映射 |
| `PromptContext` | `gitStatus` / `claudeMd` / currentDate 等运行时上下文聚合 | lifecycle 兼容字段迁移 |
| `PromptAssemblyService` | 调 `PromptRegistry + PromptContext` 产出 `StartAssembly/TurnAssembly/Snapshot` | provider transport 细节、把 displayName 回写成 instructions |
| `thread/start_session` | 在 provider DTO 前注入 assembly 产物，并持久化 `prompt_snapshot` | 直接拼装 prompt 内容 |
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
- Resume 必须先读 `thread_states.prompt_snapshot`，再重建 `PromptAssembly` 并注入 provider；不能期待 app-server/runtime snapshot 自动回补 instructions
- live session 可以把“内存里已有 assembly”作为优化，但 cold resume / recovery replay 一律以 snapshot 为准

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
- claude restart 用 `s.instructions`（`session_log_watcher_integration.go:231-236`），这只覆盖 live session restart
- cold restart / recovery 仍需读取 `thread_states.prompt_snapshot`，重建 `PromptAssembly` 后再做 provider-specific 映射
- snapshot 以**结构化字段 + hash/version/provider** 为主，不长期持久化完整拼装后的 provider 最终字符串，避免安全泄露与 provider 串味

## 审计补记：Claude / Codex 当前统一阻塞（2026-04-14）

- **launch prompt wire format 仍分叉**：Claude `composeLaunchSystemPrompt()` 还会把 `approval/sandbox/summary/effort/personality` 拼进单字符串 `--system-prompt`；Codex `threadStartParams` 则把这些字段保持为结构化 RPC 参数。
- **developerInstructions alias 仍不对齐**：Claude `configFromMap()` 同时接受 `developer_instructions` / `developerInstructions`；Codex `buildThreadStartParams()` 当前只读 camelCase `developerInstructions`，仍依赖线程层双写兜底。
- **turn 入口顺序差异仍是现状事实**：Claude 当前基线顺序是 `附件提示 -> user text -> skill section -> output_schema`；Codex 当前基线顺序是 `skill prompt synthetic input -> 用户结构化 inputs`。两边都还没有 `UserContextText` 注入位。
- **resume/restart live 能力不对称**：Claude live restart 会复用内存态 `s.instructions + next.config`；Codex `ResumeSession()` / `resumeThreadAfterRecovery()` 只发 `threadId/cwd/model` 到 `thread/resume`，没有 instructions 回补。
- **snapshot 现状不对称且都不够**：Claude `RuntimeConfigSnapshot()` 会暴露 `baseInstructions/developerInstructions`，Codex `RuntimeConfigSnapshot()` 完全不带 instructions；但两者都不是跨进程 durable `prompt_snapshot`，仍不足以覆盖 cold resume/recovery。
- **PromptAssemblyService 已存在但未接线**：contract + assembler 已落地，生产代码里 `thread/provider/orchestration` 仍没有真实调用链，说明本 Phase 的重点是接线与 durability，而不是再发明一套新接口。

---

## 任务 5：缓存失效补充

Phase 3 缓存失效点需补充：
- **Provider 切换**：codex↔claude 时必须清空 section cache（cache key 不含 provider）
- **session cache/barrier**：provider session 缓存 entry 必须携带 provider/providerThread 元数据；binding/provider 不匹配时立即拒绝复用并失效旧缓存
- **auto-compact**：落在 V3 的 compact cleanup hook；自动 compact 完成后立即触发 invalidate
- **REPL partial compact**：落在 turn loop partial-compact cleanup hook；partial compact 成功后立即触发 invalidate

触发点统一放在：
- `start_session` 解析 provider 后、构造 provider DTO 前
- `/resume` 恢复后发现 persisted provider 与当前 provider 不一致时
- 说明：当前 `ThreadConfigPatch` **没有 provider 字段**，因此本 Phase 不把“thread config/set 切 provider”写成现状能力

### Config 归一前置规则
- 在线程边界增加共享 `NormalizeStartConfig(...)`，统一解析 `approvalPolicy/approval_policy/approvals`、`developerInstructions/developer_instructions`、`modelProvider/model_provider`、`sandbox`
- canonical key 与 typed field 优先；legacy alias 只做兼容输入，不允许不同 alias 取值冲突后在 provider 内各自解释
- provider 不再各自兜底默认值来掩盖归一失败

### unified contract / identity 收口（同 Phase 4.5）
- 在 `internal/contract` 增加 optional capability interfaces，统一承载 `Steer / ReadConfig / AllowedModels / CompactThread / RuntimeConfigSnapshot`
- 显式建模 `SessionIdentity`，至少区分：`publicThreadID / providerThreadID / sessionID`
- provider-specific 差异先收口到 capability / identity 层，再谈 registry hardcode、event alias 形式优化

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

## 推荐执行顺序（审查修订）

1. **子任务 A：thread 语义解耦 + displayName 贯通**
   - 范围：Batch 1-4，外加 `threadStateFields.Name` / `thread.Started.Name` 对齐，以及 `thread` 模块接入 `PromptAssemblyService.AssembleStart()`。
   - 依赖：无。
   - 原因：先切断 `Prompt/BaseInstructions` 污染，否则后续 provider / snapshot 改造会被旧折叠链重新返污。

2. **子任务 B：snapshot durability + resume/recover/restart**
   - 范围：`PromptAssemblySnapshot` 持久化层、读取校验、`ReadRuntimeConfig`/offline config 合并、codex/claude 恢复链。
   - 依赖：A 完成后才能保证写入的是干净的 `displayName/base/dev instructions` 语义。

3. **子任务 C：provider wire mapping + orchestration/sub-agent + cache invalidation**
   - 范围：codex/claude start/turn/steer 顺序、remote/local launch contract、provider switch invalidate、回归测试。
   - 依赖：A 必需；涉及 cold resume / recovery replay 的场景依赖 B，纯 hot-path `thread/start` / `turn/start` 映射可与 B 后半并行。

---

## 验收

- [ ] `BaseInstructions` 不再污染 thread name / store / resume / Fork / Recover / `toRef()` / `SetName()`
- [ ] `PromptAssemblyService` 现有 contract/impl 真正接入 `thread/start` + turn 入口；`AssembleTurn()` 从“仅渲染 dynamic sections”升级为符合计划语义的 `claudeMd + currentDate + runtime extras` 两阶段产物
- [ ] `AssembleStart()` 直接产出 `StartAssembly.Snapshot`，并写入 thread/session runtime snapshot 层
- [ ] Resume/Recovery/Restart 显式依赖 `PromptAssemblySnapshot`（含 `Version + Hash + Provider` 校验），不再只靠污染后的 `Prompt`
- [ ] Resume/Recover 不再重复发布 `thread.Started`；Recover 不再发送会冲空 UI 字段的缩水 Started 事件
- [ ] `thread.Started.Name` 在 `Name` 缺省、仅 legacy `prompt` 提供 displayName 的场景下仍能正确输出展示名
- [ ] Codex thread/start 正确接收 assembly 产物
- [ ] Claude launch `--system-prompt` 正确接收 assembly 产物
- [ ] Codex turn/start UserContext 前置注入
- [ ] Claude turn UserContext 前置注入
- [ ] Provider 切换时 section cache 清空
- [ ] unified optional capability 与 `SessionIdentity` 合同落位
- [ ] 子 Agent 走 PromptAssemblyService
- [ ] 回归测试通过（binding/rpc_types/service_handlers/resume/fork/recover/toRef）
- [ ] presence-aware bool alias 冲突与 config alias 归一测试通过

---

## 数据流总图

```text
PromptAssemblyService.AssembleStart()
  ├─ BaseInstructions ──→ dto.Instructions ──┬─→ codex: threadStartParams.baseInstructions
  │                                          └─→ claude: --system-prompt (前半)
  └─ DeveloperInstructions ──→ Config ──┬─→ codex: threadStartParams.developerInstructions
                                        └─→ claude: --system-prompt (后半)

PromptAssemblyService.AssembleTurn()
  └─ UserContextText ──→ thread/turn adapter 消费 ──┬─→ codex: 前置 synthetic text input（在 skill prompt 之后）
                                           └─→ claude: provider-local prepend block（不回写普通 user inputs）
```

---

## 源码锚点

| 文件 | 行 | 内容 |
|------|-----|------|
| internal/module/thread/contract.go | 34-50 | StartRequest |
| internal/module/thread/contract.go | 96-103 | LaunchAgentRequest |
| internal/module/thread/start_session.go | 18-20 | FirstNonEmpty 污染源 |
| internal/module/thread/start_session.go | 146-152 | provider DTO 组装（注入点） |
| internal/module/thread/start_session_helpers.go | 9-30 | DeveloperInstructions → Config |
| internal/module/thread/lifecycle.go | 56-80 | Start: Prompt → launch/persist |
| internal/module/thread/lifecycle.go | 116-116 | Resume: state.Prompt → launchAgent |
| internal/module/thread/lifecycle.go | 340-372 | launchAgent / buildLaunchRequest |
| internal/module/thread/lifecycle_helpers.go | 146-152 | toRef display name fallback |
| internal/module/thread/service.go | 135-166 | SetName 只改 displayName 槽位 |
| internal/module/thread/rpc.go | 51-53 | thread/name/set handler |
| internal/module/thread/rpc_types.go | 54-91 | legacy 字段兼容 |
| internal/ui/wails/binding.go | 60-66 | Wails LaunchAgent 旧入口兼容 |
| internal/provider/codexapp/support.go | 248-261 | codex buildThreadStartParams（当前只读 camelCase developerInstructions） |
| internal/provider/codexapp/support.go | 305-312 | codex startRemoteThreadWithDynamicTools |
| internal/provider/codexapp/session.go | 141-155 | codex RuntimeConfigSnapshot（不含 instructions） |
| internal/provider/codexapp/session_turn.go | 37-49 | codex buildTurnStartParams |
| internal/provider/codexapp/session_turn.go | 51-63 | codex buildTurnSteerParams |
| internal/provider/codexapp/session_turn.go | 76-85 | codex turnInputsFromRequest |
| internal/provider/codexapp/module.go | 231-248 | skill prompt 先例 |
| internal/provider/codexapp/driver.go | 203-214 | codex ResumeSession → thread/resume |
| internal/provider/codexapp/recovery.go | 164-182 | codex recovery resumeThreadAfterRecovery |
| internal/provider/claudecli/config.go | 39-47 | claude configFromMap alias 兼容 |
| internal/provider/claudecli/session.go | 118-149 | claude RuntimeConfigSnapshot（含 base/dev instructions） |
| internal/provider/claudecli/transport_config.go | 99-147 | claude --system-prompt 拼装 |
| internal/provider/claudecli/session_turn.go | 167-210 | claude turn prepareTurnLocked / buildSteerPayload |
| internal/provider/claudecli/session_log_watcher_integration.go | 230-253 | claude restart instructions 恢复 |
| internal/provider/claudecli/driver.go | 106-127 | claude StartSession launch 入口 |
| internal/provider/claudecli/driver.go | 149-191 | claude start / prepareSessionStart |
| internal/provider/unified/client.go | 30-68 | 统一 driver 分发 |
| internal/contract/provider.go | 10-39 | provider 接口定义 |
| internal/contract/orchestration.go | 46-55 | orchestration LaunchRequest |
| internal/sidecar/orch/orchestration/rpc.go | 131-142 | launchRequestFromParams |
| internal/sidecar/orch/orchestration/launcher.go | 141-178 | remote launcher → thread/start |
| internal/sidecar/orch/tools/orchestration_tools.go | 33-53 | orchestration_launch_agent MCP 入口 |
| internal/sidecar/orch/tools/orchestration_tools.go | 127-150 | 子 agent launch request builder |

---

## 实施参考

> 基于 `lsp_xref(references)` 对 `internal/module/thread/` 内 `Prompt / BaseInstructions` 的生产态引用追踪整理。

### 实施 Checklist（按批次）

#### Batch 1：`rpc_types.go` — 语义解耦（BaseInstructions→Name 字段清理）
- **需改文件**
  - `internal/module/thread/rpc_types.go`
  - `internal/module/thread/rpc_types_test.go`
- **改动点**
  - 将 legacy `prompt/instructions` 解码改成 **presence-aware**：当前字段优先，legacy alias 仅在当前字段未出现时兜底。
  - `legacy.Prompt` 只参与 `displayName/Name` 兼容，不再写入 `BaseInstructions`。
  - 显式 `instructions/baseInstructions` 与 legacy `prompt/instructions` 冲突时，返回错误或至少告警，禁止静默覆盖。
- **预期测试**
  - `legacy prompt only`：成功产出 `displayName`，`BaseInstructions` 保持空。
  - `explicit baseInstructions + legacy prompt`：provider instructions 只取显式 `baseInstructions`。
  - `camelCase/snake_case/legacy alias` 同时出现且值冲突：命中冲突保护。
- **风险**
  - 旧 RPC/Wails 调用若长期依赖 `prompt -> instructions` 隐式折叠，改完后可能出现空 displayName 或行为回归。
  - presence-aware 解码处理不严会把显式 `false/empty` 误判为“未提供”，导致 legacy 值回灌。

#### Batch 2：`start_session.go` — 消除 Prompt/BaseInstructions 交叉
- **需改文件**
  - `internal/module/thread/start_session.go`
  - `internal/module/thread/start_session_test.go`（若现有测试文件拆分，则落到对应 `*_test.go`）
- **改动点**
  - 删除 `normalizeStartRequest()` 内 `req.BaseInstructions -> req.Prompt` 回填；改为显式派生 `displayName := FirstNonEmpty(req.Name, req.Prompt)`。
  - `startSession()` 组装 provider DTO 时，`Instructions` 只接 `BaseInstructions/assembly`，不再 `FirstNonEmpty(req.BaseInstructions, req.Prompt)`。
  - `lookupResumeState()` 不再把 `thread.Prompt` 当作 instructions 来源；恢复路径改读 persisted `displayName` + `PromptAssemblySnapshot`。
- **预期测试**
  - `normalizeStartRequest` 不再因为 `BaseInstructions` 非空而污染 `Prompt/Name`。
  - `startSession` 产出的 provider DTO 不再从 `Prompt` 兜底 `Instructions`。
  - `lookupResumeState` 在 snapshot 存在时只恢复 displayName + snapshot；snapshot 缺失时走显式 compat degrade，而不是重新污染 instructions。
- **风险**
  - 兼容 displayName 派生链若遗漏，启动/恢复链路可能出现空 launch name。
  - snapshot 缺失或损坏时，恢复链路容易在 compat fallback 中重新引入 `Prompt -> Instructions` 污染。

#### Batch 3：`lifecycle.go` — Start/Resume/Fork/Recover 四条链路统一
- **需改文件**
  - `internal/module/thread/lifecycle.go`
  - `internal/module/thread/lifecycle_test.go`
  - `internal/module/thread/service_handlers_test.go`（或覆盖 Start/Resume/Fork/Recover 的现有测试文件）
- **改动点**
  - `Start()` 的 `launchAgent(...)`、`threadStateFields` 写入统一改用 `displayName`，不再使用污染后的 `Prompt`。
  - `Start()/Resume()/Fork()/Recover()` 构造 `threadStateFields` 时同步设置 `Name = displayName`；否则 `newThreadEvent(thread.Started)` 仍会把名称发空。
  - `Resume()/Fork()/Recover()` 全部从 persisted `displayName` 读取 launch name；instructions 统一改走 snapshot 校验恢复。
  - `lookupThreadMeta()` / 相关 meta 结构从 `Prompt` 语义切到 `DisplayName` 语义，禁止为 Fork/Recover 继续输送污染字段。
  - 收口 Start/Resume/Fork/Recover 的事件与持久化语义，避免链路间 displayName 含义不一致。
- **预期测试**
  - Start/Resume/Fork/Recover 全链路 roundtrip 后，launch name / persisted displayName / UI 可见名称保持一致。
  - Fork/Recover 不再把旧 system prompt 当作子线程或恢复线程名称。
  - `thread.Started` 事件在恢复链路上不再因为缩写/二次发布导致名称丢失或冲空。
- **风险**
  - 旧 thread store 中残留的 `Prompt` 污染值可能在升级后表现为脏 displayName，需要 compat 清洗或 degrade 策略。
  - 四条生命周期链路同时改动，容易造成 launch、persist、event 三处语义再次分叉。

#### Batch 4：`factory.go` + `lifecycle_helpers.go` — 辅助函数对齐
- **需改文件**
  - `internal/module/thread/factory.go`
  - `internal/module/thread/lifecycle_helpers.go`
  - 对应 helper/store 测试文件
- **改动点**
  - `newThreadState()` 将原 `Prompt` 承载语义收敛为 display-only 槽位（或等价 `DisplayName` 语义），禁止继续携带 instructions。
  - `upsertPublicThread()` 持久化 displayName，而不是把 instructions 文本落到 `thread.Prompt`。
  - `toRef()` 只消费 persisted displayName；compat fallback 最多回退到 threadID/agentID，不再展示 instructions。
- **预期测试**
  - `newThreadState` / `upsertPublicThread` / `toRef` 对同一 displayName 语义保持一致。
  - 旧记录缺 displayName 时，UI/ref fallback 不泄露 system prompt，只退到安全标识。
  - thread store 读写后，Resume/Fork/Recover 看到的名称与 UI 引用一致。
- **风险**
  - 现阶段未新增独立 `Name` schema 时，字段名仍叫 `Prompt`，后续维护者容易再次误用。
  - store helper 与 UI helper 若只改其一，会出现“落盘已修正、展示仍泄露 prompt”或反过来的半修状态。

#### Batch 5：Provider wire format mapping（`claudecli` / `codexapp`）
- **需改文件**
  - `(可选) internal/dto/provider/session.go`（仅当要把 `DeveloperInstructions` 从 `Config` 提升为 typed DTO 字段时再改；v1 不作为阻塞）
  - `internal/provider/codexapp/support.go`
  - `internal/provider/codexapp/session_turn.go`
  - `internal/provider/claudecli/transport_config.go`
  - `internal/provider/claudecli/session_turn.go`
- **改动点**
  - **v1 范围收口**：沿用 `dto.StartSessionRequest.Instructions` 作为 `BaseInstructions` alias，`DeveloperInstructions` 继续走 `Config`；先把生命周期语义与 wire mapping 做对，再决定是否做 DTO 重命名。
  - codex `thread/start`：`StartAssembly.BaseInstructions -> dto.Instructions -> threadStartParams.baseInstructions`，`DeveloperInstructions -> developerInstructions`；禁止再从 `Prompt/displayName` 回填。
  - codex `turn/start` / `turn/steer`：`TurnAssembly.UserContextText` 映射为 **前置 synthetic text input**，并保持在 `skill prompt` 之后。
  - claude `thread/start`：`StartAssembly` 收敛成单个 `--system-prompt`；launch wire format 不再吃 displayName。
  - claude `turn` / `steer`：`TurnAssembly.UserContextText` 只作为 **provider-local prepend block** 消费，不回写普通 `dto.TurnRequest.Inputs`。
- **预期测试**
  - codex / claude 各自的 `thread/start` payload golden：`DisplayName / BaseInstructions / DeveloperInstructions / Snapshot` 落点正确。
  - codex / claude 的 `turn/start` 与 `turn/steer` 顺序测试：`skill prompt -> UserContext -> user inputs`（codex）与 `UserContext prepend -> attachment hint -> user text -> skill section -> output_schema`（claude）保持稳定。
  - provider mapping 集成测试：provider 不再自行从 runtime 状态重组 UserContext/SystemPrompt。
- **风险**
  - provider-specific 输入顺序极易漂移，尤其是 codex `selectedSkills` 与 claude `steer` 复用 helper 两条链。
  - 若 provider DTO 或 adapter 层仍保留 `FirstNonEmpty(Prompt, BaseInstructions)` 旧兜底，前四个 Batch 的解耦会被重新污染。

### 污染点覆盖矩阵（16 个位置 → 5 个 Batch）

| Batch | 覆盖污染点 | 说明 |
|------|------------|------|
| Batch 1 | `rpc_types.go:82` | 先切断 legacy `prompt/instructions -> BaseInstructions` 入口污染。 |
| Batch 2 | `start_session.go:20,151,352` | 清掉 start / provider DTO / resumeState 三个交叉回灌点。 |
| Batch 3 | `lifecycle.go:56,80,116,141,192,219,245,275,325` | 统一 Start/Resume/Fork/Recover/lookupThreadMeta 的 launch + persist + meta 语义。 |
| Batch 4 | `factory.go:51`、`lifecycle_helpers.go:111,147` | 收口 threadState / thread store / UI fallback 三个 helper 污染点。 |
| Batch 5 | provider boundary guard（无新增污染点编号） | 不新增 thread 污染点，但负责把前四批的解耦结果正确映射到 `codexapp/claudecli` wire format，防止 provider 侧二次返污。 |

> 覆盖口径：污染地图中的 **16 个位置已全部落到 Batch 1-4**；Batch 5 是 provider 边界收口批次，用来确保解耦结果不会在 DTO / adapter 层重新被 `Prompt` 兜底污染。

### 直接 Prompt/BaseInstructions 交叉点

- `internal/module/thread/rpc_types.go:82` | legacy 兼容折叠（Prompt → BaseInstructions） | `fillLegacyFields()` 在 `BaseInstructions` 为空时会回退到 `legacy.Instructions / legacy.Prompt`，使 legacy display text 重新进入 provider instructions 通道 | 改成 presence-aware 解码：`legacy.Prompt` 只参与 `displayName` 兼容，`instructions/baseInstructions` 只写入 `BaseInstructions`
- `internal/module/thread/start_session.go:20` | BaseInstructions 回填 Prompt | `normalizeStartRequest()` 把 `req.BaseInstructions` 回填到 `req.Prompt`，是 thread/name/store/resume/UI 污染链的源头 | 删除该回填；显式派生 `displayName := FirstNonEmpty(req.Name, req.Prompt)`，并让其只服务 lifecycle 展示语义
- `internal/module/thread/start_session.go:151` | Prompt 回填 provider Instructions | `startSession()` 仍用 `FirstNonEmpty(req.BaseInstructions, req.Prompt)` 组装 provider DTO，导致 displayName/legacy prompt 可再次混入 system prompt | provider DTO 的 `Instructions` 只接收 assembly/base instructions；兼容 fallback 前移到 RPC 解码/assembly 层，不再从 `Prompt` 兜底

### Prompt 污染传播链

- `internal/module/thread/lifecycle.go:56` | 污染后的 Prompt 进入 launch name | `Start()` 直接 `launchAgent(..., req.Prompt, ...)`，会把 system prompt 传播到 orchestration launch request | 改为传递 `displayName` / `StartAssembly.DisplayName`
- `internal/module/thread/lifecycle.go:80` | 污染后的 Prompt 写入 thread state | `Start()` 把 `req.Prompt` 写入 `threadStateFields.Prompt`，污染开始落盘 | 兼容期先把该槽位收敛为 persisted displayName；后续如有 schema 迁移再拆出独立 `Name`
- `internal/module/thread/factory.go:51` | threadState 继续携带污染字段 | `newThreadState()` 无差别复制 `fields.Prompt` 到 `state.Prompt`，使 Start/Resume/Fork/Recover 共用同一污染载体 | 将该字段语义改成 `DisplayName`（或等价 display-only 槽位），禁止承载 instructions
- `internal/module/thread/lifecycle_helpers.go:111` | 污染字段写入 thread store | `upsertPublicThread()` 把 `state.Prompt` 持久化为 `thread.Prompt`，后续 resume/fork/recover/UI 都会读回 | 线程持久化层只保存 display name；instructions 改由 `PromptAssemblySnapshot`/runtime snapshot 保存
- `internal/module/thread/start_session.go:352` | 落盘 Prompt 回灌 resumeState | `lookupResumeState()` 从 `thread.Prompt` 回填 `state.Prompt`，把已污染展示槽重新当成恢复输入 | Resume 应优先读取 persisted displayName + prompt snapshot，而不是把 `thread.Prompt` 当 instructions 来源
- `internal/module/thread/lifecycle.go:116` | Resume relaunch 复用污染 Prompt | `Resume()` 用 `state.Prompt` 重新 `launchAgent()`，system prompt 会在恢复时再次变成 launch name | Resume launch 统一改读 persisted displayName
- `internal/module/thread/lifecycle.go:141` | Resume 再次持久化污染 Prompt | `Resume()` 把 `state.Prompt` 写回新 `threadState`，导致每次恢复都加固污染 | Resume 持久化只写 display name；instructions 走 snapshot 校验恢复
- `internal/module/thread/lifecycle.go:192` | Fork launch 继承污染 Prompt | `Fork()` 用 `meta.Prompt` 启动新 agent，子线程展示名会继承旧 system prompt | Fork 仅继承 display name，不继承 instructions 文本到 launch name
- `internal/module/thread/lifecycle.go:219` | Fork 持久化继承污染 Prompt | `Fork()` 把 `meta.Prompt` 写入 fork 后的新 thread state | Fork 持久化沿用 display-only 语义，instructions 重新由 assembly/snapshot 提供
- `internal/module/thread/lifecycle.go:245` | Recover launch 继承污染 Prompt | `Recover()` 用 `meta.Prompt` 调 `recoverAgent()`，恢复链路继续把 prompt 当显示名 | Recover launch 只带 persisted displayName
- `internal/module/thread/lifecycle.go:275` | Recover 持久化继承污染 Prompt | `Recover()` 把 `meta.Prompt` 继续写回 thread state，恢复后污染仍在 | Recover 持久化与 Start/Resume/Fork 保持同一 display-only 语义
- `internal/module/thread/lifecycle.go:325` | thread store Prompt 回灌 meta | `lookupThreadMeta()` 读 `thread.Prompt` 形成 `meta.Prompt`，为 Fork/Recover 提供污染输入 | `threadMeta` 改为显式 `DisplayName`，并与 snapshot 分层
- `internal/module/thread/lifecycle_helpers.go:147` | UI/toRef 展示被污染 | `toRef()` 以 `thread.Prompt` 作为名称 fallback，UI 列表/引用可能直接展示 system prompt | `toRef()` 只消费 persisted displayName；compat fallback 才退到 threadID，不再展示 instructions

---

## 改造影响面分析

### 核心交叉点：签名 + 双向调用链

#### 1. `internal/module/thread/rpc_types.go:54-91` `(*startParams).fillLegacyFields`

- **签名**：`func (p *startParams) fillLegacyFields(data []byte) error`
- **双向调用链**
  - incoming：`(*startParams).UnmarshalJSON()`
  - outgoing：`shared.FirstNonEmpty()`、`strings.TrimSpace()`、`json.Unmarshal()`
  - 实际链路展开：RPC payload → `UnmarshalJSON()` → `fillLegacyFields()` → `newStartHandler()` → `service.Start()`
- **影响判断**
  - 这是 **入口兼容折叠点**：把 legacy `prompt/instructions` 归并进 `BaseInstructions`。
  - 这一层如果不先改，后面的 `normalizeStartRequest()` / `startSession()` 就算改干净，也会继续收到被 legacy prompt 污染过的 `BaseInstructions`。
  - 该点可以 **相对独立** 改，但必须同批补齐 presence-aware/冲突测试，否则容易把“显式空值”误判成“缺省值”。

#### 2. `internal/module/thread/start_session.go:18-29` `normalizeStartRequest`

- **签名**：`func normalizeStartRequest(req StartRequest) (StartRequest, string, error)`
- **双向调用链**
  - incoming：`(*service).Start()`
  - outgoing：`trimStartRequest()`、`resolveStartConfig()`、`shared.NewID()`、`shared.FirstNonEmpty()`
  - 实际链路展开：`newStartHandler()` → `service.Start()` → `normalizeStartRequest()` → `launchAgent()` / `startSession()`
- **影响判断**
  - 这是 **thread 生命周期内的根污染源**：`BaseInstructions -> Prompt` 回填发生在 launch/store/provider DTO 分叉之前。
  - 该点 **不能单独删除**；如果只去掉 `req.Prompt = FirstNonEmpty(req.Prompt, req.BaseInstructions)`，而没有同步引入 `displayName := FirstNonEmpty(req.Name, req.Prompt)`，Start 链路会出现空 launch name / 空展示名回归。

#### 3. `internal/module/thread/start_session.go:136-154` `(*service).startSession`

- **签名**：`func (s *service) startSession(ctx context.Context, req StartRequest, agentID string) (contract.Session, error)`
- **双向调用链**
  - incoming：`(*service).Start()`
  - outgoing：`s.starter.StartSession()`、`buildStartSessionConfig()`、`shared.FirstNonEmpty()`
  - 实际链路展开：`service.Start()` → `startSession()` → `unified.Client.StartSession()` → `{claudecli|codexapp}.StartSession()` → Claude `composeLaunchSystemPrompt()` / Codex `buildThreadStartParams()`
- **影响判断**
  - 这是 **provider 边界汇聚点**：thread 侧的 `Prompt/BaseInstructions` 语义最终在这里折叠进 `dto.StartSessionRequest.Instructions`。
  - 该点可以作为 **边界收口批次** 独立改，但前提是 Batch 1 已先把 legacy `prompt -> BaseInstructions` 兼容策略重新定义清楚；否则旧 prompt-only 调用会突然失去 provider instructions。

### 13 个传播链位置：改造粒度判断

#### A. 一处改动可覆盖一簇下游（枢纽点）

| 位置 | 枢纽类型 | 一处改动覆盖范围 |
|------|----------|------------------|
| `internal/module/thread/factory.go:51` | carrier 构造枢纽 | `newThreadState()` 被 `Start/Resume/Fork/Recover` 共用，字段语义一旦从 `Prompt` 收敛为 `DisplayName`，4 条生命周期链都会跟着统一到同一载体。 |
| `internal/module/thread/lifecycle_helpers.go:111` | 持久化枢纽 | `upsertPublicThread()` 是 thread store 的统一写入口，修正一次即可覆盖所有通过 `persistThreadState()` 落盘的链路。 |
| `internal/module/thread/start_session.go:352` | resume 读取枢纽 | `lookupResumeState()` 同时被 `resolveResumeRequest()` 与 `hydrateResumeSessionRequest()` 调用，修正一次即可覆盖 Resume 正常路径与恢复/补水路径。 |
| `internal/module/thread/lifecycle.go:325` | fork/recover 读取枢纽 | `lookupThreadMeta()` 同时服务 `Fork()` 与 `Recover()`，切到 `DisplayName` 语义后，两条链对 store 的读取语义会一起收敛。 |

#### B. 必须逐点改的显式调用位点

| 位置 | 是否需逐点修改 | 原因 |
|------|----------------|------|
| `lifecycle.go:56` | 是 | `launchAgent(..., req.Prompt, ...)` 是显式参数位，必须改成 `displayName`。 |
| `lifecycle.go:80` | 是（机械） | `threadStateFields.Prompt` 的赋值点需要明确换成 display-only 值；不能指望下游自动纠正。 |
| `lifecycle.go:116` | 是 | Resume relaunch 参数位独立存在，不会被 `lookupResumeState()` 的修复自动覆盖。 |
| `lifecycle.go:141` | 是（机械） | Resume 重新构造 `threadStateFields` 时仍显式写入污染字段。 |
| `lifecycle.go:192` | 是 | Fork launch 参数位独立存在。 |
| `lifecycle.go:219` | 是（机械） | Fork 新线程持久化时仍显式写入 `meta.Prompt`。 |
| `lifecycle.go:245` | 是 | Recover launch 参数位独立存在。 |
| `lifecycle.go:275` | 是（机械） | Recover 持久化时仍显式写入 `meta.Prompt`。 |
| `lifecycle_helpers.go:147` | 是 | `toRef()` 是 UI/read-model 侧单独的 fallback 汇点，不改就会继续泄露旧 `thread.Prompt`。 |

> 结论：13 个传播点里，**4 个是枢纽点**（`factory.go:51`、`lifecycle_helpers.go:111`、`start_session.go:352`、`lifecycle.go:325`），其余 **9 个都需要逐点改**，其中 `80/141/219/275` 属于“机械型逐点改”，`56/116/192/245/147` 属于“语义型逐点改”。

### Provider 端消费链分析

#### Claude CLI

- `StartSession()` 签名：`func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error)`
- 调用链：`thread.startSession()` → `unified.Client.StartSession()` → `claudecli.(*driver).StartSession()` → `startSpec.instructions = req.Instructions` → `prepareSessionStart()` → `launchCLI()` → `buildCLIArgs()` → `composeLaunchSystemPrompt(instructions, cfg)`
- 关键观察：
  - Claude provider **并不消费 thread.Prompt**；它只消费 `dto.StartSessionRequest.Instructions` 与 `req.Config` 中的 `developerInstructions`。
  - `composeLaunchSystemPrompt()` 会把 `instructions + developerInstructions + meta(approvals/sandbox/summary/effort/personality)` 拼成单个 `--system-prompt`。
  - `ResumeSession()` 不接 `Instructions`；跨进程恢复要依赖 thread 层持久化的 snapshot/contract，而不是 Claude driver 自己重新推导。
  - `session.RuntimeConfigSnapshot()` 已能回放 `baseInstructions/developerInstructions`，说明 Claude 侧的“runtime 可读性”比 Codex 更完整，但 thread 层当前并未把这套 snapshot 作为 durability contract 使用。

#### Codex App

- `StartSession()` 签名：`func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error)`
- 调用链：`thread.startSession()` → `unified.Client.StartSession()` → `codexapp.(*driver).StartSession()` → `startDynamicSession()` → `startRemoteThreadWithDynamicTools()` → `buildThreadStartParams(req)` → `thread/start`
- 关键观察：
  - Codex provider 同样 **不消费 thread.Prompt**；它消费的是 `req.Instructions` 与 `req.Config["developerInstructions"]`。
  - `buildThreadStartParams()` 已有天然 split wire format：`BaseInstructions <- req.Instructions`，`DeveloperInstructions <- req.Config`。
  - `ResumeSession()` / `resumeRemoteThread()` 只发送 `threadId/cwd/model`，**不带 base/developer instructions**。
  - `session.RuntimeConfigSnapshot()` 当前只稳定返回 approvalPolicy + runtimeConfig，**不会回放 `baseInstructions/developerInstructions`**；这意味着 Codex 路径若要支持真正的 Resume/Recover durability，要么扩 `RuntimeConfigSnapshot()`，要么由 thread 层单独持久化 `PromptAssemblySnapshot`。

#### Provider 侧结论

- **thread/start mapping**：Codex 与 Claude 都可以在 thread 边界收口后继续复用现有 provider 接口，属于“边界适配即可”的改动。
- **Resume/Recover durability**：Claude 侧已有较完整 runtime snapshot；Codex 侧没有，因此 **Resume/Recover 的完整闭环不能只改 thread 模块**，必须同时补 Codex 的 snapshot/durability 方案。

### 改造影响面矩阵

| 文件 | 改动类型 | 上游依赖 | 下游影响 | 可否独立改 | 测试覆盖建议 |
|------|----------|----------|----------|------------|--------------|
| `internal/module/thread/rpc_types.go` | legacy 参数解码重构 | RPC JSON、Wails/旧客户端兼容字段 | 影响 `StartRequest.BaseInstructions/Prompt` 初值 | **部分**：可先改入口，但要保留 compat | `rpc_types_test`：legacy prompt/baseInstructions 优先级、snake/camelCase 冲突、显式空值用例 |
| `internal/module/thread/rpc.go` | StartRequest 透传契约更新 | `startParams` 解码结果 | 影响 `service.Start()` 收到的 display/instructions 载荷 | **是**：如果字段名不变则改动很小 | `service_handlers_test`：handler 透传 displayName/baseInstructions/developerInstructions |
| `internal/module/thread/start_session.go` | 核心污染源切断 + provider DTO 收口 + resume 读取改造 | `rpc_types.go`、`StartRequest` 语义 | 影响 Start/Resume/provider StartSession 边界 | **否**：必须与 displayName 方案一起落地 | `start_session_guard_test`、新增 normalize/startSession/lookupResumeState 单测 |
| `internal/module/thread/lifecycle.go` | 生命周期四链路语义重排 | `start_session.go`、`lookupThreadMeta()`、`lookupResumeState()` | 影响 launch/persist/event/Fork/Recover | **否**：多条主链路同时耦合 | `resume_test`、`fork_isolation_test`、`recover`/`lifecycle` roundtrip 测试 |
| `internal/module/thread/factory.go` | `threadState` 载体语义收敛 | `lifecycle.go` 所有 `newThreadState()` 调用点 | 影响 Start/Resume/Fork/Recover 的共享状态承载 | **部分**：是枢纽，但仍需调用点配合 | `factory`/`binding` 相关测试：carrier 字段不再承载 instructions |
| `internal/module/thread/lifecycle_helpers.go` | store 写入 + UI fallback 收敛 | `persistThreadState()`、thread store 读模型 | 影响落盘 displayName、`toRef()` 展示、thread/read | **部分**：helper 可先改，但 UI/生命周期需同步验证 | `history_test`、`read_view_test`、`toRef`/Get/List 覆盖 |
| `internal/provider/unified/client.go` | 边界透传校验 | thread `startSession()` | 影响 provider driver 入口一致性 | **是**：通常无需实质改动，只需契约确认 | `unified/client_test`：driver 选择 + request 透传不变 |
| `internal/provider/claudecli/driver.go` | Start/Resume 边界适配 | `dto.StartSessionRequest`、thread assembly | 影响 Claude launch/restart/resume 起点 | **部分**：Start 可独立，Resume durability 仍依赖 snapshot | `session_restart_status_test`、Start/Resume 集成测试 |
| `internal/provider/claudecli/transport_config.go` | `--system-prompt` 组装约束 | `driver.StartSession()`、`configFromMap()` | 影响 Claude 最终 launch prompt 顺序 | **是**：在 DTO 语义稳定后可单独收口 | 新增/更新 golden：`composeLaunchSystemPrompt()` 顺序与去重 |
| `internal/provider/claudecli/session.go` | runtime snapshot durability 对齐 | `driver.newStartedSession()` | 影响 thread/read runtime config、未来 Resume 快照 | **部分**：本身可独立增强，但 thread 层要消费 | `RuntimeConfigSnapshot` 单测：base/developer instructions 仍可读 |
| `internal/provider/codexapp/driver.go` | Start/Resume 边界适配 | `dto.StartSessionRequest/ResumeSessionRequest` | 影响 Codex start/resume 的 payload 入口 | **部分**：Start 可独立，Resume 不完整 | `driver_toolbridge_test`、resume payload 用例 |
| `internal/provider/codexapp/support.go` | `thread/start` payload split mapping | `driver.StartSession()`、dynamic tools 路径 | 影响 `baseInstructions/developerInstructions` 最终落点 | **是**：边界收口点明确 | `buildThreadStartParams` 单测、`thread/start` payload golden |
| `internal/provider/codexapp/session.go` | runtime snapshot durability 补强 | `driver.StartSession()`、thread/read | 影响 Resume/Recover 能否回放 instructions | **否**：若要真正支撑 Phase 4.5 durability，必须与 thread snapshot 方案一起设计 | `RuntimeConfigSnapshot` 回放 base/developer instructions、resume/recover roundtrip |
| `internal/dto/provider/session.go` | DTO 契约审视（必要时加 snapshot/显式字段） | thread module/provider module 双边 | 影响所有 provider Start/Resume 边界 | **部分**：若只保留 `Instructions+Config` 可不改；若引入 snapshot 则需双边同步 | contract test：dto 映射、json 兼容、provider interface 回归 |

### 实施顺序建议（按耦合度）

1. **先切入口**：`rpc_types.go`
2. **再切 thread 根污染源**：`start_session.go`
3. **随后统一生命周期主链**：`lifecycle.go` + `factory.go` + `lifecycle_helpers.go`
4. **最后收 provider 边界**：`claudecli` / `codexapp`
5. **若要做完整 Resume/Recover durability**：额外补 `PromptAssemblySnapshot` 持久化，并同步补 Codex runtime snapshot 能力
