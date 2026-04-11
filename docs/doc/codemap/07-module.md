# 07 业务模块层代码地图

> 扫描范围：`internal/module/dashboard/`、`lspgui/`、`skill/`、`thread/`、`turn/`、`uistate/`（含 `timeline/`）  
> 说明：地图主体聚焦生产代码（`*_test.go` 不展开），但目录内测试已用于辅助确认边界行为与实现意图。

---

## 1. 模块概述：业务模块层的整体设计

这一层位于 **contract / store / platform** 之上，是项目里把“底层能力”组织成“可被 RPC、前端、编排层直接消费的业务能力”的一层。整体上可以分成 3 类：

1. **读模型 / 运维查询类**
   - `dashboard`：面向运维/诊断的查询聚合层。
   - `lspgui`：面向 GUI 的轻量 LSP 文件/搜索入口。

2. **核心业务编排类**
   - `skill`：技能元数据、技能文件系统、技能匹配、受限命令执行。
   - `thread`：线程生命周期、绑定关系、历史、配置、恢复、归档。
   - `turn`：回合输入组装、技能选择、Turn 启停/中断/强制完成、追踪。

3. **前端投影 / UI 状态类**
   - `uistate`：把 agent/thread/turn/tool 事件投影成 UIState、Sidebar、Patch、Timeline、Preferences、Projects。

### 1.1 总体分层关系

```text
RPC / Frontend / Orchestration
          │
          ▼
   internal/module/*
          │
 ┌────────┼────────┐
 ▼        ▼        ▼
contract store   platform
```

### 1.2 本层的共性实现模式

- **`module.go`**：通过 Fx 暴露 `Service` 与 RPC handlers，并按需注册事件订阅。
- **`service.go`**：承载模块核心状态与主流程。
- **`rpc.go`**：把业务服务映射到 jrpc2 handler。
- **事件驱动**：`thread` 与 `skill` 负责产生领域/UI事件；`uistate` 是最主要的事件消费与投影模块。
- **provider-neutral**：`thread` / `turn` 主要依赖 `internal/contract` 中的抽象接口，而不是直接依赖某个 provider 实现。

### 1.3 模块间主线关系

```text
dashboard ──读取──> orchestration / stores / skill
lspgui    ──封装──> project-root 内安全文件读取 / grep
skill     ──提供──> skills-root 扫描 / project-root 文件操作 / 安全命令执行 / SkillsChanged
thread    ──协调──> binding + thread store / session starter / orchestration / turn cleanup
turn      ──封装──> Session / SessionResolver / ApprovalResponder / tracker / MCP manifest
uistate   ──投影──> thread + bindings + preferences + sharedfile + event bus + timeline
```

---

## 2. 各子模块详述

## 2.1 dashboard（仪表盘）

### 职责

- 聚合 orchestration、日志、任务追踪、提示词、共享文件、技能信息。
- 提供 **运维视角** 的查询型 RPC：agent 详情、系统信息、日志、DAG、任意 DB Query、页面化 dashboard 数据。
- 本质上是一个 **只读聚合服务**，没有事件驱动写模型。

### 核心类型

- `Service`（`contract.go`）
- `Dashboard` / `DashboardPage` / `AgentDetail` / `SystemInfo`（`types.go` / `ui_page.go`）
- `LogFilter` / `LogEntry`（`types.go`）

### 关键流程

1. **仪表盘页面查询**
   - `ui/dashboard/get` → `GetDashboardPage` → `dashboardPageLoaders`
   - 按页面名分发到 `agents/tasks/skills/commands/memory`
   - 使用 `errgroup` 并发加载页内数据

2. **Agent 详情查询**
   - `dashboard/agent/detail` → `GetAgentDetail`
   - 并发调用 `orchestration.Snapshot` 与 `orchestration.GetReport`
   - 基于 `ActiveTurnID` 衍生 `TurnHistory`

3. **统一日志查询**
   - `dashboard/logs` → `GetLogs`
   - `resolveLogSource` 判断 `all/system/ai`
   - 读取 `systemlog` 与 `ailog`，统一映射为 `LogEntry`
   - 按时间倒序稳定排序、截断 `limit`

4. **DAG 查询**
   - `dashboard/dags` / `dashboard/dagDetail`
   - 直接透传到 `contract.OrchestrationService`

### 文件地图

| 文件 | 作用 |
|---|---|
| `agent_status.go` | 查询 agent status store。 |
| `ai_logs.go` | AI 日志分类查询、状态统计、最近日志。 |
| `contract.go` | Dashboard Service 对外接口。 |
| `detail.go` | DAG 列表/详情、Agent turn history 辅助逻辑。 |
| `factory.go` | 响应包装、日志过滤字段、日志行映射、参数转 filter。 |
| `logs.go` | 统一日志过滤/排序、审计日志/总线日志包装。 |
| `module.go` | Fx 装配：注入 orchestration、stores、skill service。 |
| `rpc.go` | 暴露 dashboard RPC 路由。 |
| `service.go` | 主服务：系统信息、Agent 详情、日志、Query。 |
| `types.go` | Dashboard / AgentDetail / LogEntry 等 DTO。 |
| `ui_page.go` | dashboard page 聚合装配与并发加载。 |

### 依赖特点

- **contract**：`contract.OrchestrationService`
- **store**：`agentstatus / ailog / auditlog / buslog / commandcard / dbquery / prompt / sharedfile / systemlog / tasktrace`
- **platform**：`platform/rpc`、`platform/shared`
- **module**：依赖 `module/skill.Service` 用于技能页

### 备注

- `GetDashboard` 存在于 Service 中，但 RPC 主入口实际偏向 `GetDashboardPage` 与各个细分查询。
- 模块本身 **不订阅/不发布 event bus 事件**。

---

## 2.2 lspgui（LSP GUI）

### 职责

- 提供前端 GUI 可直接调用的 **文件读取 / 打开 / 文本搜索** 能力。
- 关键特点是 **路径沙箱**：一切路径都必须落在 project root 内。
- 当前只实现最基础文件与 grep 能力，structure / inspect / xref 多数仍是 stub。

### 核心类型

- `Service`（`contract.go`）
- `fileParams / grepParams / structureParams / inspectParams / xrefParams`（`types.go`）
- `searchResult / searchMatch / fileReadResult`（`types.go`）

### 关键流程

1. **路径解析**
   - `NewService` 从 `config.ProjectRoot` 或当前工作目录初始化 root
   - `resolvePath` → `canonicalPath` → `ensureWithinRoot`
   - 通过 `EvalSymlinks + ContainsPath` 防止越界

2. **文件读取**
   - `lsp/gui_file` 的 `read_file`
   - 限制最大 5MB，支持 `offset + limit` 切行

3. **文本搜索**
   - `lsp/gui_grep` 的 `text_search`
   - 使用 `shared.NewLineMatcher` 支持 regex / caseSensitive
   - 递归遍历目录，跳过 `.git/node_modules/vendor/build/...`
   - 单文件最大 1MB，总结果最多 50 条

4. **Stub 能力**
   - `ast_search`、`document_symbol`、`hover`、`references` 等返回 `status=not_implemented`

### 文件地图

| 文件 | 作用 |
|---|---|
| `contract.go` | LSP GUI Service 接口。 |
| `file.go` | `read_file/open_file/diagnostics`。 |
| `grep.go` | `text_search` 实现、glob 过滤、目录跳过策略。 |
| `module.go` | Fx 注册。 |
| `rpc.go` | 暴露 `lsp/gui_*` RPC。 |
| `service.go` | project root 归一化、路径沙箱。 |
| `stubs.go` | structure / inspect / xref / ast_search stub。 |
| `types.go` | 参数与结果类型。 |

### 依赖特点

- **contract/store**：无直接依赖
- **platform**：`platform/config`、`platform/rpc`、`platform/shared`
- **module**：无

### 备注

- `diagnostics` 当前只做路径校验并返回空数组。
- 这是一个 **GUI 辅助模块**，不是完整 LSP server 封装。

---

## 2.3 skill（技能系统）

### 职责

- 管理本地技能目录（默认 `CODEX_HOME/skills` 或 `~/.codex/skills`）。
- 解析 `SKILL.md` frontmatter，生成技能元数据、摘要、触发词与强触发词。
- 提供两类文件能力：
  - **skills root** 下的技能写入/导入/删除；
  - **project root** 下的本地文件读写/列目录（`skills/local/*`）。
- 提供 `skills/match/preview` 的技能命中预览。
- 提供受限 `command/exec` 执行环境，并发布 `uidto.SkillsChanged`。

### 核心类型

- `Service`（`contract.go`）
- `ExecResult` / `SkillInfo`（`types.go`）
- `UserInput` / `skillMatchPreviewParams`（`rpc_skill_types.go`）
- `skillRecord` / `autoMatchedSkill`（`skills_meta.go` / `skills_match.go`）

### 关键流程

1. **技能扫描 / 列表**
   - `skills/list` → `ListSkills` → `scanSkills`
   - 遍历 skills root 下所有 `SKILL.md`
   - `parseSkillInfo` 解析 `name/description/summary/trigger_words/force_words`
   - 若无 `summary`，则从正文中自动提取首段可读文本

2. **project root 本地文件操作**
   - `skills/local/read` / `skills/local/listFiles` / `skills/local/write`
   - 统一经 `resolveProjectPath` 做路径归一化与越界校验
   - `ReadLocal` 读取的是 **project root 内现有普通文件**，并返回生成摘要
   - `WriteLocal` 也要求目标文件已存在，会保留原文件权限位后回写内容

3. **skills root 技能写入 / 导入 / 删除**
   - `skills/local/importDir`
     - 支持 `path` 或 `paths`，并支持 glob 展开
     - 从 project root 复制目录到 skills root
     - 禁止 symlink
   - `skills/local/delete`
     - 先 `resolveSkill(name)`，再删除对应技能目录
   - `skills/config/write` / `skills/remote/write` / `skills/remote/export`
     - 最终都走 `writeSkill(name, content)`，写到 skills root 下 `SKILL.md`
   - `skills/summary/write`
     - 通过 `upsertSkillSummary` 更新 frontmatter `summary`

4. **远程读取与匹配预览**
   - `skills/remote/read` / `skills/remote/list`
     - 当前都直接调用 `ReadRemote(url)`，本质是 HTTP GET 指定 URL，并非远端目录 listing
   - `skills/match/preview`
     - `threadID` 为空时回退用 `agentID`
     - 先读取配置态技能（`readConfigState` / `ReadConfig`）
     - 再对 `text + input[]` 做本地词匹配：`force`、`explicit`、`trigger`
     - 显式 `@skill` 或 `[skill:skill]` 走更强匹配
     - 由于当前 `ReadConfig` 仍是 stub，configured 命中通常为空

5. **命令执行**
   - `command/exec` → `ExecCommand`
   - 阻断危险命令、shell 解释器、shell metacharacters
   - 对 `env/time/nice/timeout/find -exec/xargs` 等 wrapper 做递归危险命令分析
   - 仅允许白名单环境变量前缀透传
   - 使用 RPC 请求超时执行命令；若是只读命令，会自动在 stdout 前追加 “优先使用 LSP 工具” 提示

6. **技能变更事件**
   - 写入/导入/删除后调用 `publishSkillsChanged`
   - 100ms debounce 窗口内合并多次动作，去重 `action`
   - 发出 `uidto.SkillsChanged`

### 文件地图

| 文件 | 作用 |
|---|---|
| `contract.go` | Skill Service 接口。 |
| `events.go` | `SkillsChanged` 事件发射、debounce、动作合并。 |
| `exec.go` | 受限命令执行、环境变量白名单、只读命令提示。 |
| `exec_tokenizer.go` | shell token 拆分、命令替换扫描。 |
| `exec_tokenizer_safety.go` | wrapper 命令递归安全分析。 |
| `module.go` | Fx 注册与 dispatcher 绑定。 |
| `rpc.go` | 暴露 `command/exec` 与 `skills/*` RPC。 |
| `rpc_skill_types.go` | 技能类 RPC 参数。 |
| `rpc_types.go` | `command/exec` 参数兼容解码。 |
| `service.go` | skills root / project root / HTTP client / dispatcher 绑定。 |
| `skills_fs.go` | skills root 与 project root 的文件读写、导入、删除、远程读写、配置 stub。 |
| `skills_match.go` | MatchPreview、configured/local 自动匹配。 |
| `skills_meta.go` | `SKILL.md` frontmatter 解析、摘要生成、slug。 |
| `types.go` | `ExecResult`、`SkillInfo`。 |

### 依赖特点

- **contract/store**：无直接 store 依赖；配置读取当前仍是模块内 stub
- **platform**：`platform/bus`、`platform/config`、`platform/rpc`、`platform/shared`
- **dto**：`dto/ui`（`SkillsChanged`）、`dto/shared`
- **module**：无

### 备注

- `ReadConfig` 当前返回空技能绑定，是后续 provider/config 存储接入点。
- `module.go` 已留 TODO：未来可订阅 `thread.Started` 自动触发 auto-match。
- `skills/remote/list` 与 `skills/remote/read` 目前是同一路径；`skills/remote/export` 与 `skills/remote/write` 目前也是同一路径，`remote` 更多是 legacy RPC 命名。

---

## 2.4 thread（线程管理）

### 职责

- 管理 **public thread id / agent id / provider thread id / session UUID** 的绑定关系。
- 管理线程生命周期：`start / resume / fork / recover / stop / archive / unarchive / delete`。
- 读写线程历史、线程配置、模型切换、上下文压缩。
- 在线程停机前联动 `turn` 模块中断活动回合并清理 tracker。
- 发布 thread 领域事件：`Started / Stopped / MessagesPage / Compacted`。

### 核心类型

- `Service`（`contract.go`）
- `StartRequest/StartResult`、`ResumeRequest/ResumeResult`、`ForkResult`、`RecoverResult`
- `Ref`、`ReadHistoryResult`
- `threadState`（生命周期持久化/事件核心态）
- `bindingRegistration`（binding 合法性校验与持久化）
- `storedThreadConfig` / `offlineConfigSnapshot`

### 关键流程

#### A. 启动线程 `Start`

`normalizeStartRequest`
→ `resolveStartProvider/resolveStartApprovalPolicy`
→ `launchAgent`
→ `startSession`
→ `bindSessionGeneration`
→ `enrichFromSessionConfig`
→ `persistThreadState`

`persistThreadState` 内部会继续做：
- `ensurePublicThreadAvailable`
- `maybeRegisterThreadBinding`
- `upsertPublicThread`
- `rememberStartedThread`
- `publishThreadStarted`

关键点：
- provider 只允许 `codex/claude`
- `danger-full-access` sandbox 会把默认 approval policy 收敛为 `never`
- public thread id 在 start 路径默认复用 `agentID`
- session runtime config 可反填 model / cwd

#### B. 恢复线程 `Resume`

`resolveResumeRequest` 依赖 `lookupResumeState`，信息来源包括：
- thread store
- binding store
- `resolveBindingChain` 的多级解析（agent / persisted thread / remembered thread / provider thread）
- `SessionUUID`

随后流程为：
`launchAgent` → `resumeSession` → `bindSessionGeneration` → `persistThreadState`

关键点：
- 若 `SessionUUID` 与 `ProviderThreadID` 不同且看起来像真实 UUID，则优先用 `SessionUUID`
- persist binding 失败时仍会继续补发 `thread.Started`，避免 UI 因 binding immutable 失败而卡死

#### C. Fork / Recover

- `Fork`
  - 调 `session.ForkThread`
  - 新 thread id 同时作为新的 public thread id / agent id
  - 再执行 `launchAgent + resumeSession + bindSessionGeneration + persistThreadState`

- `Recover`
  - 先解析 binding 与 thread meta
  - 优先 `orchestration.Recover(agent)`；若恢复失败则回退到重新拉起 agent
  - 如果本地 session 不存在，再执行 `resumeSession`
  - 最后重新持久化 thread state

#### D. Stop / Archive / Delete / Unarchive

共同停机逻辑：
- `resolveThreadStopState`
- `turns.InterruptActiveTurn`
- `closeSessionForAgent`
- `orchestration.StopAgent`
- `turns.CleanupThread`

区别：
- `Stop`
  - 更新 thread status=`stopped`
  - 清空 binding `SessionUUID`
  - 发 `thread.Stopped(status=stopped)`
- `Archive`
  - 更新 thread status=`archived`
  - 设置 binding `archived=true`
  - 发 `thread.Stopped(status=archived)`
- `Delete`
  - 删除 binding 与 thread store 记录
  - 发 `thread.Stopped(status=deleted)`
- `Unarchive`
  - 只把 status 改回 `created` 并恢复 binding archived 标记
  - **不发事件**

#### E. 历史 / 配置 / compact / 低频命令

- `thread/read`
  - 是 V2 兼容的 thread-history wrapper
  - 若 session 可用，则走 `session.ListThreads`
  - 否则回退为仅含当前 thread id 的包装结果

- `thread/resolve`
  - 只是 `thread store` 的 `Get(threadID)` 包装
  - 不负责 provider/public thread id 之间的映射解析

- `thread/messages`
  - 先 resolve binding
  - 若 session 缺失，后台触发 `backgroundResumeIfNeeded`
  - 优先走 `session.ReadHistory`，否则回退到 `platform/historyjsonl`
  - 自动补齐 message `ID/AgentID/EventType`
  - `before` 支持消息 ID 或时间游标；分页结果为 **newest-first**
  - 发 `thread.MessagesPage`

- `thread/config/get` / `ReadRuntimeConfig`
  - live session 优先走 provider reader
  - 否则构造 offline config snapshot
  - 离线默认 provider=`codex`，tool routing=`legacy/openai_compatible`

- `thread/config/set`
  - RPC 当前只暴露 `model/effort` patch
  - `personality/approvals` 通过独立 RPC 走 `SendCommand -> session.Configure`

- `thread/model/set`
  - 需要 session 暴露 `AllowedModels`，先做 allow-list 校验

- `thread/compact/start`
  - 需要 capability gate `context_compact`
  - 压缩前后会估算 token 数，并发 `thread.Compacted`

- `thread/rollback` / `thread/undo` / `thread/backgroundTerminals/clean` / `thread/mcp/list` / `thread/skills/list` / `thread/realtime/*`
  - 路由已注册
  - 但当前仍通过 `SendCommand` 落到 `TODO(P9)` 占位错误

#### F. 绑定同步事件

- 线程模块订阅 `agentdto.AgentLaunched`
- 当事件中带有真实 provider UUID 时，用它回写 `binding.SessionUUID`
- 解决 Claude/Codex provider thread id 在恢复后的漂移问题

### 文件地图

| 文件 | 作用 |
|---|---|
| `archive.go` | 归档/取消归档。 |
| `binding_registration.go` | binding 合法性校验、持久化、验证失败回滚。 |
| `command.go` | `/config|get/set`、`/model`、`/personality`、`/approvals`、`/interrupt`、`/compact` 与低频命令占位。 |
| `compact_event.go` | 发布 `threaddto.Compacted`。 |
| `contract.go` | Thread Service 接口与请求/响应类型。 |
| `events.go` | 订阅 `AgentLaunched`，更新 `SessionUUID`。 |
| `factory.go` | `threadState`、thread event、offline config、binding chain 查找。 |
| `history.go` | `thread/read`、消息分页、runtime config、compact。 |
| `lifecycle.go` | `Start/Resume/Fork/Recover` 主流程。 |
| `lifecycle_helpers.go` | started thread 持久化、history target 解析、persisted history fallback。 |
| `module.go` | Fx 装配与订阅注册。 |
| `rpc.go` | `thread/*` RPC 路由。 |
| `rpc_types.go` | 线程 RPC 参数兼容解码。 |
| `service.go` | 主 service、List/Get/SetName/Delete、事件发射器。 |
| `session_generation.go` | 将 session generation 绑定到 orchestration。 |
| `start_session.go` | start/resume session 配置构造与请求归一化。 |
| `stop.go` | 停线程、关 session、清理 turn、清 binding sessionUUID。 |

### 依赖特点

- **contract**：`Session` 及 provider/session 相关错误抽象边界
- **store**：`binding`、`thread`
- **platform**：`bus`、`db`、`historyjsonl`、`rpc`、`shared`
- **module**：依赖 `module/turn.Service`
- **dto**：`dto/agent`、`dto/thread`、`dto/provider`

### 备注

- `thread/loaded/list` 当前等价于 `ListByStatus(statusCreated)`。
- `thread/debugMemory` 目前返回宿主 Go runtime `MemStats`，不是 provider 进程视角。
- public/provider/agent 三类线程标识的真实解析主要在 `resolveBindingChain`，不是 `thread/resolve` RPC 本身。

---

## 2.5 turn（回合管理）

### 职责

- 把前端/编排层输入组装成 `dto.TurnRequest`。
- 做输入规范化、显式技能合并、turn override、MCP manifest 构造。
- 启动 / Steer / 中断 / 强制完成 turn，并用本地 tracker 跟踪生命周期。
- 为 orchestration 提供 `OrchestrationTurnStarter` 适配。

### 核心类型

- `Service`（`contract.go`）
- `PrepareInput` / `TurnStatus`
- `turnTracker` / `trackedTurn` / `activeTurn`
- `turnInterruptEnvelope`
- `orchestrationTurnStarter`

### 关键流程

#### A. `turn/start`

`turn/start`
→ `withReadyTurnSession`（必要时轮询等待 session ready）
→ capability gate `CapMessageSend`
→ `applyTurnStartConfig`（当前仅处理 approval policy）
→ `PrepareTurn`
→ `StartTurn`

`PrepareTurn` 内部会做：
- `inputAssembler.Assemble`
  - 收集 `prompt/input/images/files`
  - 限制最多 256 个 item
  - 去重、截断
  - 白名单文件/图片扩展名
  - 拒绝可执行文件扩展名
- `skillResolver.Resolve`
  - 合并显式技能与 `CandidateSkills`
  - **但当前 RPC / orchestration 路径只真正接入了显式 `selectedSkills` 与 `input.type=skill`，没有直接接入 `module/skill` 的候选技能列表**
- `manifest.Build`
  - 基于 `CWD / ThreadCaps / binaryDir`
  - 探测 `mcp-lsp` / `mcp-orch` peer
- `buildOverrides`
  - 仅当 provider 支持 `CapTurnOverride`
  - `model` 还需额外具备 `CapModelSwitch`

`StartTurn` 内部：
- 生成/确保 `localID`
- tracker 记录 `preparing -> running`
- 调 `session.StartTurn`
- 挂上 `TurnHandle` watcher，等待 Done 后更新终态

#### B. `turn/steer`

- 通过 `tracker.ActiveByThread` 找到当前活跃 turn
- 校验 `expectedTurnId`
- 要求 session 实现 `Steer()`
- 当前 RPC 只接受 `prompt/input/selectedSkills/manualSkillSelection`，不接 `images/files/model/outputSchema`

#### C. `turn/interrupt` / `turn/forceComplete`

- `InterruptTurn`
  - 调 `session.Interrupt`
  - 若 tracker 观测到活跃 turn，则等待 settle
  - 返回 `confirmed/mode/stateBefore/stateAfter/waitedMs/activeObserved` 等 envelope 信息

- `ForceCompleteTurn`
  - 调 `session.ForceComplete`
  - 若本地 tracker 有活跃 turn，则等待进入终态

#### D. 线程停机协同

- `InterruptActiveTurn`
  - 被 thread 模块用于 stop/archive/delete 前的抢占中断
- `CleanupThread`
  - 把该线程下 tracker 中仍活跃的 turn 标成中断态

#### E. 编排层桥接

- `orchestrationTurnStarter.WaitForSessionReady(agentID, timeout)`
  - 轮询 `SessionProvider.GetSession(agentID)`
- `orchestrationTurnStarter.StartTurn(submission)`
  - 把 `TurnSubmission` 转为 `PrepareInput`
  - 用 `submission.ExpectedTurnID` 覆盖本地 turn id
  - 再直接调用 turn service

### 文件地图

| 文件 | 作用 |
|---|---|
| `assembler.go` | 输入 item 规范化、文件/图片白名单、去重、截断。 |
| `contract.go` | Turn Service、PrepareInput、TurnStatus。 |
| `factory.go` | PrepareInput 构造、interrupt result、legacy 参数解码。 |
| `interrupt_envelope.go` | 中断结果 envelope 与状态归一化。 |
| `interrupt_service.go` | turn interrupt 主流程。 |
| `manifest.go` | MCP manifest 构造与 peer 发现。 |
| `module.go` | Fx 注册。 |
| `orchestration_starter.go` | 编排层 TurnStarter 适配器。 |
| `rpc.go` | `turn/*` / `review/start` / `approval/respond` RPC。 |
| `rpc_helpers.go` | session ready wait、入参组装、capability gate。 |
| `rpc_types.go` | turn RPC 参数兼容解码。 |
| `service.go` | Prepare/Start/Steer/ForceComplete/Track/watcher。 |
| `skills.go` | 显式技能与 candidate skills 合并。 |
| `thread_cleanup.go` | 线程停机时的 turn 中断与清理。 |
| `tracker.go` | 本地 turn tracker。 |

### 依赖特点

- **contract**：`Session`、`TurnHandle`、`SessionResolver`、`ApprovalResponder`
- **store**：无直接依赖
- **platform**：`config`、`rpc`、`shared`
- **dto**：`dto/provider`、`dto/shared`
- **mcpserver**：`mcpserver/common`（peer discover）
- **module**：无直接模块依赖

### 备注

- 当前 turn 模块 **自身不直接发 event bus 事件**；uistate 消费的 `turn.* / tool.*` 事件主要来自 provider/session 层。
- `review/start` 仍是 `NotImplemented`。
- `PrepareInput` 的 `CandidateSkills / AgentID / BinaryDir` 能力在 service 层已存在，但 RPC 路径尚未全部接通。

---

## 2.6 uistate（UI 状态 + timeline）

### 职责

- 将 agent/thread/turn/tool/ui 事件投影成前端友好的 **UIState / Sidebar / Timeline / Patch**。
- 管理 UI Preferences（按 cwd scope）、项目列表、diff 快照、runtime config 读取、LSP prompt hint 覆写。
- 作为事件消费中心消费大量领域事件，同时向前端发布 UI 增量事件。

### 核心类型

- `UIState` / `Sidebar` / `Preferences` / `ProjectsState`
- `ThreadSummary` / `AgentSummary` / `TurnSummary`
- `ActivityStats` / `threadActivity`
- `WorkspacePanel` / `WorkspaceRunSummary`
- `timeline.Item`（子包）

### 关键流程

#### A. 初始化与快照读取

- `NewService` → `buildInitialState`
  - 从 `thread.Service.List()` 建立线程摘要
  - 从 `OrchestrationService.ListAgents()` 建立 agent 摘要
  - 若 agent 自带 threadID，则补齐 thread entry

- `ui/state/get`
  - 可携带 `cwd / threadId / includeDiff / knownDiffRevision`
  - `GetState` 流程：读取 scoped preferences → 应用到 state → 应用 diff snapshot → 注入 timeline snapshot → 用 binding store 纠正 agent provider/cwd/providerThreadID

- `ui/sidebar/get`
  - `sidebarLocked` 生成 Sidebar
  - `WorkspacePanel` 由 `workspaceByKey` 派生，但当前扫描范围内仍缺少对应写入者

#### B. 事件投影主循环

- `registerProjectionSubscriptions` 订阅：
  - `agentdto.StateChanged / AgentLaunched / AgentStopped / AgentRecovering / AgentFailed / AgentRuntimeReported`
  - `threaddto.Started / Stopped`
  - `turndto.TurnStarted / TurnInterrupted / TurnCompleted / TurnResumed / TurnInputReceived / TurnOutputDelta / ItemStarted / ItemCompleted`
  - `tooldto.ToolCallBegin / ToolCallEnd / ToolDiffUpdated / ToolApprovalRequested / ToolApprovalResolved`
  - `uidto.UITokensUpdated`

- timeline 子系统额外订阅：
  - `turndto.TurnStarted / TurnCompleted / TurnInterrupted / TurnInputReceived / TurnOutputDelta(reasoning)`
  - `turndto.PlanDelta / PlanUpdated`
  - `turndto.ItemStarted / ItemCompleted`
  - `tooldto.ToolCallBegin / ToolCallEnd / ToolApprovalRequested / ToolApprovalResolved`
  - `agentdto.AgentError / AgentFailed`

- `applyMutation`
  - 统一的：加锁 → 修改状态 → 构造 patch → 解锁 → 发 patch

- 关键投影差异：
  - `applyTurnOutputDelta` **只处理 `stream=message`**，用于更新 thread/agent 的 `LastMessage`
  - `stream=reasoning` 不进入主状态，而是仅由 timeline projector 处理为 `thinking` 项

#### C. 状态派生与 overlay

- `snapshot_helpers.go`
  - 使用 `turnDepth / commandDepth / editDepth / toolDepth / approvalDepth / collabDepth` 推导 `thinking/running/editing/waiting/...`

- `sidebar_compat.go`
  - `overlayTypeMCPStartup`：MCP 启动中，TTL 30s
  - `overlayTypeTerminalWait`：仅当 `ToolApprovalRequested.Kind == request_user_input` 时置位
  - 派生 `InterruptibleByThread`、`StatusHeadersByThread`、`StatusDetailsByThread`、`AgentRuntimeByID`

#### D. Patch / Diff / Timeline 增量

- `patch.go`
  - 生成 `UIThreadPatch`
  - 包含：状态、header/details、overlay、token usage、activity stats、diff、timeline delta、main agent 等
  - payload 超过 64KB 时退化成 `recover=true + refreshRequired=true`

- `diff_state.go`
  - 内部以 `agentID` 存 diff 文本/版本
  - 对外按 `threadID` 投影
  - 若调用方带 `knownDiffRevision` 且一致，则返回 `Unchanged=true`

- `patch_timeline.go`
  - 维护每个 thread 的上次 timeline patch 状态
  - 只下发 changed / removed / order 变化

- `patch.go` / `timeline/timeline.go`
  - `UIProjectionUpdated` 目前主要发生在：
    - 偏好变更触发的 `state/sidebar` revision 更新
    - timeline 更新触发的 `timeline` revision 更新
  - `UITimelineAppended` 只在 timeline `Append` 新项时发出；纯更新不会重复发 append 事件

- `timeline/timeline.go`
  - timeline 默认容量 200
  - 对重复 item 按 `lookupKey` 合并
  - 对 tool/item/approval 等运行中项采用 update/fallback 策略

#### E. Preferences / Projects / Runtime Config

- `preferences.go`
  - preference key 归一化：`activeThreadId/mainAgentId/viewPrefs.chat/threadPins.chat/...`
  - scope = `cwd`
  - `threadPins.chat` / `threadArchives.chat` 直接参与线程分组构造

- `projects.go`
  - `projects.state` 存在 preference 中
  - 支持 `get/setActive/add/remove`
  - active 默认 `.`

- `config_rpc.go`
  - `config/read`
    - 先给出全局默认配置
    - 再按当前 scope 的 `activeThreadId` 读取 thread runtime config 与 thread config 叠加
  - `config/lspPromptHint/read|write`
    - 默认值来自共享文件 `prompts/lsp-mandatory-prefix.md`
    - 覆盖值来自 scoped preference `config/lspPromptHint.override`

### 文件地图

| 文件 | 作用 |
|---|---|
| `module.go` | Fx 注册、binding adapter、投影订阅启动。 |
| `service.go` | 主 service、快照读取、preference 持久化、overlay/timeline/workspace 容器。 |
| `state.go` | UIState/Sidebar/Preferences 等核心结构与 clone/upsert/sort。 |
| `preferences.go` | scoped preferences、线程分组、值归一化。 |
| `projects.go` | 多项目列表与 active project 管理。 |
| `rpc.go` | `ui/state/*`、`ui/preferences/*`、`ui/projects/*` RPC。 |
| `config_rpc.go` | `config/read`、`config/lspPromptHint/*`。 |
| `factory.go` | `applyMutation`、状态归一化、patch 排序辅助。 |
| `patch.go` | `UIThreadPatch` / `UIPreferencesChanged` / `UIProjectionUpdated` 发射。 |
| `patch_timeline.go` | timeline 增量 patch 计算。 |
| `diff_state.go` | diff 文本与 revision 管理。 |
| `projector.go` | item/tool/token 事件投影。 |
| `projector_handlers.go` | agent/thread/turn 生命周期事件投影。 |
| `sidebar_compat.go` | Sidebar 派生字段与 overlay 表达。 |
| `snapshot_helpers.go` | `threadActivity`、派生状态、recent turn 管理。 |
| `timeline/timeline.go` | timeline 存储、去重、索引、容量控制。 |
| `timeline/merge.go` | timeline item 合并规则。 |
| `timeline/projector.go` | turn/tool/approval/reasoning 事件 → timeline item。 |
| `timeline/projector_parity.go` | user/plan/error/item/tool fallback 等补充投影。 |

### 依赖特点

- **contract**：`OrchestrationService`（初始化 agent 列表）
- **store**：`uipreference`、`binding`、`sharedfile`
- **platform**：`bus`、`config`、`db`、`rpc`、`shared`
- **module**：依赖 `module/thread.Service`，内部再依赖 `timeline` 子包
- **dto**：大量依赖 `agent/thread/turn/tool/ui` 事件 DTO

### 备注

- `workspaceByKey` / `WorkspacePanel` 当前在本模块里主要还是状态容器，当前扫描范围内还没看到对应事件写入逻辑。
- `GetState` / `GetSidebar` 最后都会用 binding store 做一次“DB 真相”纠偏，这一点很关键。
- 主 UI 状态与 timeline 对 `TurnOutputDelta` 的消费是分流的：message 流进主状态，reasoning 流进 timeline。

---

## 3. RPC 接口总览

## 3.1 dashboard

- `ui/dashboard/get`
- `dashboard/agentStatus`
- `dashboard/taskTraces`
- `dashboard/commandCards`
- `dashboard/prompts`
- `dashboard/sharedFiles`
- `dashboard/skills`
- `dashboard/agent/detail`
- `dashboard/system/info`
- `dashboard/query`
- `dashboard/aiLogs`
- `dashboard/aiLogs/recent`
- `dashboard/aiLogs/stats`
- `dashboard/auditLogs`
- `dashboard/busLogs`
- `dashboard/dags`
- `dashboard/dagDetail`
- `dashboard/logs`

## 3.2 lspgui

- `lsp/gui_file`
- `lsp/gui_grep`
- `lsp/gui_structure`
- `lsp/gui_inspect`
- `lsp/gui_xref`

## 3.3 skill

- `command/exec`
- `skills/list`
- `skills/local/read`
- `skills/local/listFiles`
- `skills/local/write`
- `skills/local/importDir`
- `skills/local/delete`
- `skills/remote/list`（当前与 `skills/remote/read` 同实现）
- `skills/remote/export`（当前与 `skills/remote/write` 同实现）
- `skills/remote/read`
- `skills/remote/write`
- `skills/config/read`
- `skills/config/write`
- `skills/summary/write`
- `skills/match/preview`

## 3.4 thread

### 生命周期 / 查询 / 配置
- `thread/start`
- `thread/stop`
- `thread/resume`
- `thread/fork`
- `thread/recover`
- `thread/archive`
- `thread/unarchive`
- `thread/delete`
- `thread/list`
- `thread/loaded/list`
- `thread/read`
- `thread/resolve`
- `thread/messages`
- `thread/name/set`
- `thread/config/get`
- `thread/config/set`
- `thread/model/set`
- `thread/personality/set`
- `thread/approvals/set`
- `thread/compact/start`
- `thread/debugMemory`

### 已注册但当前仍是占位 / TODO(P9) 的 RPC
- `thread/rollback`
- `thread/undo`
- `thread/backgroundTerminals/clean`
- `thread/mcp/list`
- `thread/skills/list`
- `thread/realtime/start`
- `thread/realtime/appendAudio`
- `thread/realtime/appendText`
- `thread/realtime/stop`

## 3.5 turn

- `turn/start`
- `turn/steer`
- `turn/interrupt`
- `turn/forceComplete`
- `review/start`（未实现）
- `approval/respond`

## 3.6 uistate

### UI 状态类
- `ui/state/get`
- `ui/sidebar/get`
- `ui/preferences/get`
- `ui/preferences/getAll`
- `ui/preferences/set`
- `ui/projects/get`
- `ui/projects/setActive`
- `ui/projects/add`
- `ui/projects/remove`

### 运行时配置类
- `config/read`
- `config/lspPromptHint/read`
- `config/lspPromptHint/write`

---

## 4. 事件机制：模块间通过 event bus 的交互方式

## 4.1 生产者 / 消费者总表

| 事件生产方 | 事件 | 主要消费者 | 用途 |
|---|---|---|---|
| `skill` | `uidto.SkillsChanged` | 前端事件面 / 外部订阅端 | 通知技能目录写入、导入、删除、摘要更新。 |
| provider / session 层 | `agentdto.AgentLaunched` | `thread`、`uistate` | thread 用于同步 `SessionUUID`；uistate 用于投影启动中 agent。 |
| provider / session 层 | `agentdto.StateChanged`、`AgentStopped`、`AgentRecovering`、`AgentFailed`、`AgentRuntimeReported` | `uistate` | 驱动 agent/thread 生命周期、runtime 信息与错误态。 |
| `thread` | `threaddto.Started` | `uistate`、事件面、hooks | 通知线程已就绪、provider/model/cwd 可投影。 |
| `thread` | `threaddto.Stopped` | `uistate`、事件面、hooks | 通知线程停止/归档/删除。 |
| `thread` | `threaddto.MessagesPage` | 事件面 / 外部订阅端 | 通知消息分页统计。 |
| `thread` | `threaddto.Compacted` | 事件面 / 外部订阅端 | 通知 compact 结果与 token 估算。 |
| provider / session 层 | `turndto.TurnStarted`、`TurnInterrupted`、`TurnCompleted`、`TurnResumed`、`TurnInputReceived`、`TurnOutputDelta` | `uistate`；timeline 消费其子集 | 主 UI 维护 active/recent turn、LastMessage；timeline 维护 `turn_start/assistant/thinking/...`。 |
| provider / session 层 | `turndto.ItemStarted`、`ItemCompleted` | `uistate`、`uistate.timeline` | 维护 command/edit activity 与 timeline item。 |
| provider / session 层 | `turndto.PlanDelta`、`PlanUpdated` | `uistate.timeline` | 维护 timeline 中的 `plan` 项。 |
| provider / session 层 | `tooldto.ToolCallBegin`、`ToolCallEnd`、`ToolApprovalRequested`、`ToolApprovalResolved` | `uistate`、`uistate.timeline` | 维护 tool activity / approval overlay / timeline。 |
| provider / session 层 | `tooldto.ToolDiffUpdated` | `uistate` | 更新 diff text / revision，并进入 patch。 |
| provider / session 层 | `agentdto.AgentError`、`AgentFailed` | `uistate.timeline`（`AgentFailed` 同时也进主状态） | 追加 timeline 错误项。 |
| provider / session 层 | `uidto.UITokensUpdated` | `uistate` | 更新 token usage 与 patch token stats。 |
| `uistate` | `uidto.UIThreadPatch` | 前端 | 线程增量 patch。 |
| `uistate` | `uidto.UIPreferencesChanged` | 前端 | preference 变更推送。 |
| `uistate` | `uidto.UIProjectionUpdated` | 前端 | state/sidebar/timeline projection revision 递增。 |
| `uistate.timeline` | `uidto.UITimelineAppended` | 前端 | timeline 新增项快速通知。 |

## 4.2 关键事件链路

### 链路 A：线程恢复后 UI 就绪

```text
thread.Start / Resume / Fork / Recover
  -> publish threaddto.Started
  -> uistate.applyThreadStarted
  -> 刷新 AgentSummary / ThreadSummary / 状态派生
  -> emit UIThreadPatch
```

### 链路 B：AgentLaunched 回写 SessionUUID

```text
provider/session 发布 agentdto.AgentLaunched(sessionID=uuid)
  -> thread.onAgentLaunched
  -> resolve binding by agent/thread
  -> bindingStore.UpdateSessionUUID
```

### 链路 C：tool / approval / diff 驱动 UI

```text
provider/session 发布 ToolCallBegin/End/Approval*/DiffUpdated
  -> uistate 更新 threadActivity / overlay / diff
  -> emit UIThreadPatch
  -> timeline 子系统并行投影 tool / approval item（DiffUpdated 仅主 UI 消费）
```

### 链路 D：turn 输出流分流到主状态与 timeline

```text
TurnOutputDelta(stream=message)
  -> uistate.applyTurnOutputDelta
  -> 更新 ThreadSummary/AgentSummary.LastMessage
  -> emit UIThreadPatch

TurnOutputDelta(stream=reasoning)
  -> timeline.reasoningDeltaHandler
  -> 追加/更新 thinking item
  -> emit UIThreadPatch + UIProjectionUpdated(timeline)
```

### 链路 E：技能目录变更通知

```text
skills/local|remote|config|summary write / import / delete
  -> skill.publishSkillsChanged
  -> debounce + merge actions
  -> uidto.SkillsChanged
```

### 链路 F：timeline 的补充事件

```text
PlanDelta / PlanUpdated / AgentError / AgentFailed / ItemStarted / ItemCompleted
  -> uistate.timeline projector
  -> 生成 plan / error / command / file item
  -> emit UIProjectionUpdated(timeline)
```

## 4.3 模块级结论

- **dashboard / lspgui**：纯查询模块，不参与 event bus。
- **skill**：只发 `SkillsChanged`，不订阅事件。
- **thread**：少量订阅（`AgentLaunched`），少量但关键的领域事件发射（Started/Stopped/MessagesPage/Compacted）。
- **turn**：本包内部不发 event bus 事件；它依赖 provider/session 发出 `turn.* / tool.*` 事件供下游消费。
- **uistate**：事件消费中心 + UI 事件生产中心；其中 timeline 是独立的二级投影子系统。

---

## 5. 依赖关系：对 contract / store / platform 的依赖

| 模块 | contract 依赖 | store 依赖 | platform 依赖 | 直接模块依赖 |
|---|---|---|---|---|
| `dashboard` | `OrchestrationService` | `agentstatus/ailog/auditlog/buslog/commandcard/dbquery/prompt/sharedfile/systemlog/tasktrace` | `rpc`、`shared` | `skill.Service` |
| `lspgui` | 无 | 无 | `config`、`rpc`、`shared` | 无 |
| `skill` | 无核心 contract 依赖 | 无 | `bus`、`config`、`rpc`、`shared` | 无 |
| `thread` | `Session`、provider errors（session/agent not found）等 | `binding`、`thread` | `bus`、`db`、`historyjsonl`、`rpc`、`shared` | `turn.Service` |
| `turn` | `Session`、`TurnHandle`、`SessionResolver`、`ApprovalResponder` | 无 | `config`、`rpc`、`shared` | 无 |
| `uistate` | `OrchestrationService`（初始化 agent 列表） | `uipreference`、`binding`、`sharedfile` | `bus`、`config`、`db`、`rpc`、`shared` | `thread.Service`、`timeline` 子包 |

### 5.1 依赖设计要点

1. **面向 contract 编程**
   - `thread` / `turn` 主要通过 `contract.Session`、`TurnHandle`、`SessionResolver` 与 provider 层解耦。
   - `dashboard` 与 `uistate` 通过 `contract.OrchestrationService` 解耦编排实现。

2. **store 主要集中在读模型与线程持久化**
   - `dashboard`：纯读 store 聚合。
   - `thread`：thread/binding 是核心真相源。
   - `uistate`：preferences/bindings/sharedfile 主要服务 UI 投影与配置读取。
   - `turn` / `lspgui` / `skill` 当前都不以 store 为中心。

3. **platform 是横切基础设施**
   - `platform/rpc`：所有模块的 RPC handler 装配点。
   - `platform/shared`：ID、路径、JSON clone、limit clamp、SafeGo 等通用工具。
   - `platform/bus`：typed emitter 与 resilient subscribe。
   - `platform/historyjsonl`：thread 模块的离线历史回退源。

---

## 6. 总结：业务模块层的职责边界

- `dashboard`：**运维查询聚合层**
- `lspgui`：**GUI 安全文件/LSP 轻入口**
- `skill`：**技能元数据 + 技能文件系统 + 安全执行**
- `thread`：**线程/agent/provider 绑定与生命周期核心**
- `turn`：**单次回合执行协调器**
- `uistate`：**事件驱动 UI 投影层**

## 审查补遗

- `internal/module/skill/rpc.go`
  - `skills/remote/list` 与 `skills/remote/read` 当前同指向 `ReadRemote(url)`；
  - `skills/remote/export` 与 `skills/remote/write` 当前同指向 `WriteRemote(name, content)`；
  - “remote” 更像 legacy RPC 命名，不是独立的远端目录管理面。
- `internal/module/thread/rpc.go` + `command.go`
  - `thread/rollback`、`thread/undo`、`thread/backgroundTerminals/clean`、`thread/mcp/list`、`thread/skills/list`、`thread/realtime/*` 已注册，但当前仍返回 `TODO(P9)` 占位错误。
- `internal/module/thread/history.go` + `internal/module/thread/rpc.go`
  - `thread/read` 是 V2 兼容的 thread-history 包装；`thread/resolve` 只是 thread store 读取，不负责 provider/public thread 映射解析。
- `internal/module/turn/rpc_helpers.go` + `internal/module/turn/service.go`
  - turn service 具备 `CandidateSkills / AgentID / BinaryDir` 等能力，但当前 RPC 路径只真正接通了显式 skills、输入项、部分 override；并未直接接入 `module/skill` 的候选技能列表。
- `internal/module/uistate/projector_handlers.go` + `internal/module/uistate/timeline/projector*.go`
  - `TurnOutputDelta` 的 `message` 流驱动主 UI `LastMessage`，`reasoning` 流只进入 timeline。
- `internal/module/thread/lifecycle.go`
  - `Recover` 路径在 `persistThreadState(...)->publishThreadStarted` 之后又显式调用了一次 `publishThreadStarted`；按当前源码会额外再发一次 `thread.Started`。
