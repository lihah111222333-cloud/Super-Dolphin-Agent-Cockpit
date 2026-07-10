# LSP Worktree Bootstrap 与两项前端 P1 修复设计

## 状态

- 设计日期：2026-07-10
- 基准提交：`0d8a77bdf00adc1220fc2490cd33e1dbd69ce1a4`
- 用户批准：采用仓库内可复用的 worktree LSP bootstrap；不把 Codex Desktop 外部自动 hook 纳入交付范围。
- 交付结构：一份设计规格，拆成三份可独立执行的实现计划。

## 背景与已证实根因

### LSP 工具完全缺失

当前 Codex worktree 中 `codex mcp get lsp` 返回 `No MCP server named 'lsp' found`。同一机器的主检出目录可以解析 `lsp`，差异来自：

1. worktree 没有本地 `.codex/config.toml`；该目录被 `.gitignore` 忽略，不会随 Git worktree 复制。
2. 全局 `~/.codex/config.toml` 没有 `mcp_servers.lsp`。
3. worktree 没有当前提交构建的 `bin/mcp-lsp`。
4. ChatGPT Desktop 启动的当前 Codex task 不经过 Super-Dolphin 内部的 `BuildPoolSpawnCmd` 配置注入链。
5. 已启动 task 的 MCP 工具快照不能热添加 server，必须创建新 task 验收。

`cmd/mcp-lsp` 已注册 `file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit`、`completion` 短工具名；相关定向测试通过。因此修复点是 worktree 配置发现和启动准备，而不是 LSP tool factory。

### “继承对话”没有调用 canonical fork

当前前端把可见 timeline 和共享文件拼成摘要，然后执行：

```text
thread/start(defer_spawn=true, base_instructions=摘要)
  -> turn/start("请基于上文摘要……")
```

后端已经提供 `thread/fork`，并负责 provider 原生历史、prompt snapshot、模型、cwd、provider identity、binding 和创建状态的复制与持久化。当前前端实现与用户界面的“继承对话”语义不一致，现有前端测试还锁定了错误链路。

### Event subscription readiness 没有进入 bootstrap 成功条件

`runtimeSlice.bootstrap()` fire-and-forget 调用 `initializeEvents()`，随后仅依据配置和 snapshot RPC 成功便写入 `bootstrapStatus: "ready"`。订阅返回 `ready=false` 或 reject 时，只清理部分句柄或记录 warning，不阻止应用进入 ready。

开发 shim 的 WebSocket 重连成功也不会生产 store 监听的 `wails:loaded`，导致已有 reconnect 恢复逻辑在真实 shim 链路中不可达。

## 目标

1. 每个 Super-Dolphin Git worktree 都能通过仓库命令生成本地 LSP MCP 配置，并在新 Codex task 中暴露完整短工具名及 Go、JavaScript/TypeScript 语义能力。
2. “继承对话”使用后端 canonical `thread/fork`，保留完整 provider 历史和 prompt snapshot。
3. bridge 与 reconnect 事件订阅都成功前，前端 bootstrap 不得进入 ready。
4. 所有缺失配置、未知响应、半成功订阅和 provider capability 错误都 fail-fast、用户可见且可测试。

## 非目标

1. 不修改 ChatGPT/Codex Desktop 的仓库外 worktree 自动 hook。
2. 不把 LSP 配置写入全局 `~/.codex/config.toml`。
3. 不让 setup 命令静默复用主检出目录的 `mcp-lsp`；二进制必须与当前 worktree 源码一致。
4. 不迁移既有摘要式创建出来的普通 thread。
5. 不为不支持 `CapThreadFork` 的 provider 构造摘要式 fallback。
6. 不增加无限后台重试或第二套 bootstrap 错误状态机。
7. 不在本轮顺带修复 observability、workflow、sidebar、app-update 或 RPC audit 的 P2 候选。

## 总体交付拓扑

```text
Plan 1: Worktree LSP bootstrap
  -> 构建当前 worktree sidecar
  -> 生成/校验本地 Codex MCP 配置
  -> 重启新 Codex task
  -> 用真实 LSP 工具链验收

Plan 2: Canonical thread/fork ─┐
                               ├─ 可在 Plan 1 验收后并行
Plan 3: Event readiness ───────┘
```

三份计划是独立提交边界，不是无条件独立调度单元。Plan 1 分成 implementation complete 与 new-task runtime accepted 两个门槛；只有后者完成后才能启动 Plan 2/3。生成实现计划时必须先列出共享文件，任何重叠文件串行处理，其余任务才可并行，并在最终前端全量验证处汇合。

## 设计一：仓库内 Worktree LSP Bootstrap

### 组件边界

新增跨平台 Go 命令 `cmd/codex-worktree-setup`。命令提供三个显式子命令：

- `configure`：只校验并写入当前 worktree 的 Codex MCP 配置。
- `ready`：无条件从当前 worktree 重建 `mcp-lsp`，再执行 configure 和 language-server preflight。
- `verify`：只读验证 config、binary、runtime env 和必需 language server，并通过 Go 与前端 JavaScript 文件的真实 `file(diagnostics)` 调用确认两类语言服务器可响应；不修改文件。

Make target `codex-worktree-ready` 只是 Unix/macOS convenience wrapper，调用 `go run ./cmd/codex-worktree-setup ready`。Windows 直接运行同一条 `go run` 命令，不依赖 Make。命令不启动 Codex task。

命令输入：

- `--worktree`：默认使用 `git rev-parse --show-toplevel`。
- `--binary`：默认 `<worktree>/bin/mcp-lsp`；Windows 使用 `<worktree>/bin/mcp-lsp.exe`。
- `--config`：默认 `<worktree>/.codex/config.toml`。

命令输出：

- 原子写入的 project-local Codex 配置。
- 人类可读的 worktree、binary、config 路径摘要。
- 明确提示必须新建 Codex task 才能加载 MCP server。

### 二进制策略

`configure` 不构建、不下载，也不搜索其他 checkout；`ready` 的名称明确授权重建当前 worktree sidecar：

1. binary 必须是绝对路径。
2. canonicalize worktree 与 binary 后，binary 必须仍位于当前 worktree；symlink 或 junction 指向其他 checkout 时 fail-fast。显式 `--binary` 只用于选择当前 worktree 内的其他构建目录。
3. binary 必须存在、是普通文件并可执行。
4. configure 遇到默认 binary 缺失时，错误信息指向 `go run ./cmd/codex-worktree-setup ready`。
5. ready 无条件执行当前 worktree 的 Go build；只有 ready 路径承诺 binary 与当前源码一致。

命令不自动安装 language server。ready/verify 必须检查 `gopls` 与 `typescript-language-server` 在即将写入 Codex server 的有效 `PATH` 中可执行；缺失时 fail-fast，并分别提示仓库现有 installer contract 使用的 `go install golang.org/x/tools/gopls@latest` 与 `npm install -g typescript-language-server typescript`。不得用其他语言服务器存在来替代这两个验收目标。

### 配置所有权与原子写入

`.codex/config.toml` 仍保持 ignored，不提交任何机器路径。配置器只拥有带标记的 LSP block：

```toml
# BEGIN SUPER-DOLPHIN MANAGED LSP
[mcp_servers.lsp]
type = "stdio"
command = "/absolute/worktree/bin/mcp-lsp"
cwd = "/absolute/worktree"

[mcp_servers.lsp.env]
SUPER_DOLPHIN_RUNTIME_MODE = "dev"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = "/absolute/worktree"
GO_AGENT_LSP_ROOT = "/absolute/worktree"
GO_AGENT_LSP_ROOTS = "[\"/absolute/worktree\"]"
# END SUPER-DOLPHIN MANAGED LSP
```

写入规则：

1. 文件不存在时创建；Unix 权限设为 `0600`，Windows 不放宽文件的继承 ACL。
2. managed block 始终放在文件末尾；已存在时先删除旧 block，再在末尾写入新 block。
3. marker 重复、嵌套、不配对或顺序反转时 fail-fast。
4. 未受管的 `[mcp_servers.lsp]` 或任何 `mcp_servers.lsp` 子树已存在时 fail-fast，不能覆盖用户配置。
5. managed block 之外的字节、注释、顺序和本地设置保持不变；末尾保留且仅保留一个换行后再追加 managed block。
6. 写入前后均用现有 TOML 库解析，非法输入或生成结果立即失败。
7. 先写同目录临时文件，设置权限并同步后原子 rename。
8. managed block 由 `BurntSushi/toml` typed struct 编码；`GO_AGENT_LSP_ROOTS` 先用 `encoding/json` 生成，不能手工拼接路径转义。

### 接入现有工作流

更新以下入口，使创建 worktree 后的准备步骤可发现且一致：

- README 的开发环境与 worktree 段落。
- `.agents/skills/使用git工作区/SKILL.md` 的 worktree 创建后验证步骤。
- 只更新 canonical repo-local worktree skill，并运行现有 mirror 一致性检查；除非检查证明必须同步，不修改 provider mirror 或 runtime 实现。

仓库不承诺 Codex Desktop 自动调用此命令；使用者必须在创建新 task 前运行 `go run ./cmd/codex-worktree-setup ready`，Unix/macOS 可以使用等价 Make target。

### LSP 验收

验收分四层：

1. 当前 shell：`codex mcp get lsp` 和 `codex mcp list` 能解析当前 worktree 配置。
2. 启动级集成测试：真实 binary 使用生成的四个 runtime/root env 完成 MCP initialize、tools/list，以及 Go/JavaScript 两次 `file(diagnostics)`，而不是启动即退出或只验证可执行位。
3. tools/list 必须包含 `file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit`、`completion` 七个短名。
4. 新建 Codex task：实际调用 file/read、file/diagnostics、grep、structure、inspect、xref、completion；patch_edit 只在临时 fixture 上执行并清理。所有路径都必须属于当前 worktree。

只通过 unit test、只看到 server 名称、或复用旧 task 都不算完成。

## 设计二：Canonical `thread/fork`

### API 合同

前端 API 单一事实源增加 `THREAD_FORK = "thread/fork"`，并通过 backend factory 与 `sessionApi` 暴露：

```text
forkThread({ threadId })
```

请求只允许源 `threadId`；不得携带 cwd、provider、model、baseInstructions 或 launch preferences。

响应 validator 至少强制：

- `thread.id` 是非空新 ID。
- `thread.forkedFrom` 与源 ID 一致。
- `kickoff_state` 或 `kickoffState` 至少一个存在；两者同时存在时必须相等。
- kickoff state 必须是明确支持的 `created_only`；未知或缺失值 fail-fast。

### 用户动作数据流

```text
用户确认“继承对话”
  -> thread/fork(sourceThreadId)
  -> 严格解析 fork response
  -> 合并 RPC response 与可能先到的 thread.Started/ui patch
  -> 选择新 thread
  -> kickoffState == created_only
  -> turn/start(FORK_KICKOFF_PROMPT + filecontent inputs)
```

kickoff prompt 固定为“请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。”，不再包含 timeline 摘要。选中的共享文件继续作为 `filecontent` input 发送，不写入 base instructions 或 prompt snapshot。

### 状态与乱序

后端可能在 RPC response 前发布 `thread.Started`。前端合并规则为：

1. 事件 patch 中的 name、status、provider、cwd 和 generation 是权威值。
2. RPC response 只补齐尚未出现的 fork identity 和临时字段。
3. 同一新 thread 不得重复插入列表。
4. response-first 和 event-first 两种顺序必须收敛到相同 state。

### 错误语义

- fork RPC 失败：保持 draft 打开，恢复 `submitting=false`，不切换 active thread，不调用 turn/start。
- provider 不支持 fork：展示 capability error，不 fallback。
- fork 成功、kickoff 失败：保留 canonical fork，标记需要用户操作并允许重新发送；不能删除已经成功创建的 thread。
- 源 session 未加载：显示后端错误；“先恢复再 fork”若需要，作为独立产品变更处理。

Go fork 主流程不重写。只允许补充 typed kickoff constant 或成功事件契约测试，不把前端修复扩成 backend 重构。

## 设计三：Event Subscription Readiness

### 状态机

运行时订阅增加明确的内部初始化状态：

```text
idle -> initializing -> ready
                    \-> failed
idle/initializing/ready/failed -> destroyed
```

store 维护：

- `eventInitializationPromise`
- `eventInitializationGeneration`
- `bridgeUnsubscribe`
- `reconnectUnsubscribe`
- 当前 attempt 的 pending subscription 集合

这些是内部生命周期字段，不新增第二套用户状态；用户仍只看到现有 `bootstrapStatus/error`。

### 原子 single-flight 订阅

`initializeEvents()` 返回共享 Promise：

1. 已 ready 且两个 unsubscribe 都存在时立即成功。
2. initializing 时返回相同 Promise，不创建重复订阅。
3. 新 attempt 同时建立 bridge 与 reconnect subscription。
4. 两个 `ready` 都为 true 才原子写入 unsubscribe 并进入 ready。
5. 任一 false/reject 时清理本 attempt 已创建的全部订阅、清空 Promise，并以精确错误拒绝。
6. `destroy()` unsubscribe pending/已提交订阅并增加 generation，使迟到 Promise settle 无效；不宣称取消普通 Promise。
7. bridge 成功但 reconnect 失败后，下一次显式 retry 必须能完整重试，不能被半成功句柄短路。

### Bootstrap 顺序

`bootstrap()` 在自己的 try/catch 内先 `await initializeEvents()`，再执行配置和 snapshot RPC。这样 snapshot 获取期间实时 listener 已存在，不丢失早到事件。

现有 sequence/generation 规则继续负责防止旧 snapshot 覆盖新事件；增加 event-before-snapshot 测试。如果测试证明现有规则不足，只做最小 generation 修正，不引入新的全局事件版本协议。

订阅失败复用现有 `handleBootstrapError`：状态为 failed、错误用户可见、依赖 ready/cwd 的动作保持禁用。

### 开发 shim 重连

`frontend-app/public/wails/runtime.js` 记录 socket 是否经历失败或已连接后断开：

- 首次正常连接不发送 reconnect event。
- 失败/断开后的下一次成功 open，向本地 listener 发送一次 `wails:loaded`。
- 同一 reconnect cycle 只发送一次。
- unsubscribe 后不得调用旧 callback。

`ready=true` 仍表示 listener 已注册，不宣称 transport 已连接。

### 用户恢复入口

bootstrap failed feedback 提供“重新连接”按钮：

- 点击调用既有 `bootstrap()`。
- loading 时禁用，避免重复 attempt。
- 失败后可再次点击。
- 不增加静默、无限或指数退避重试。

## 测试策略

### LSP Bootstrap

- 临时 Git 主目录和 linked worktree 集成测试。
- binary 缺失、不可执行、位于错误路径、symlink/junction 越界、非法 TOML、marker 非法、未受管 LSP 子树冲突的 RED 测试。
- managed block 首次创建、幂等替换、保留其他字节、路径含空格和 Windows 分隔符测试。
- 四个 runtime/root env 的 TOML、JSON 与启动级解析测试。
- `gopls`/`typescript-language-server` 缺失时的 fail-fast preflight 测试。
- 当前 worktree `codex mcp get lsp` smoke。
- 新 task 中真实短工具链验收。

### Canonical Fork

- backend API method/payload/response validator 测试。
- `sessionApi.fork()` facade 边界测试。
- store action 不调用 `thread/start` 的 RED 测试。
- `created_only` 后恰好一次 kickoff turn。
- shared file 使用 `filecontent`，不进入 base instructions。
- fork 失败、kickoff 部分成功、capability error。
- response-first/event-first 收敛测试。
- 后端 fork event/persistence 现有测试与最小补充测试。

### Event Readiness

- bootstrap 在两个 readiness 完成前保持 loading，且不启动 snapshot RPC。
- 任一 false/reject 导致 failed，并清理另一成功订阅。
- 并发 initialize 只订阅一次。
- 半成功后 retry、pending destroy、迟到 ready、reject cleanup。
- shim 首连、断线重连、多轮重连、unsubscribe 测试。
- 用户重连按钮的 loading/failed/success 行为。

### 完整验证

每个前端计划完成后运行：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

fork 计划追加：

```bash
go test ./internal/module/thread -run 'Test.*Fork' -count=1
go test ./internal/module/uistate -run 'Test.*ThreadStarted|Test.*Patch' -count=1
```

LSP 计划追加受影响 Go 包、配置器集成测试，以及新 task 的真实 MCP/LSP smoke。所有成功声明前必须运行 LSP diagnostics；Error、Warning、Information、Hint 均需处理或明确记录 blocker。

## 文档与交接

实现完成后更新：

- README 的 Codex worktree 准备步骤。
- 前端代码地图中的 fork 与 runtime bootstrap 链路。
- repo-local worktree skill 及其 canonical/runtime mirror 一致性测试。
- 三份实现计划的完成状态与实际验证证据。

不提交 `.codex/config.toml`、`bin/mcp-lsp`、`node_modules`、`dist` 或任何机器绝对路径。

## 验收标准

1. 新 linked worktree 从无 `.codex/config.toml`、无 binary 的 RED 状态开始，可通过 `go run ./cmd/codex-worktree-setup ready` 准备完成；Unix/macOS 可使用等价 Make target。
2. 新 Codex task 中 tools/list 包含七个短名，并完成规定的全部真实调用与 diagnostics。
3. “继承对话”不会调用 `thread/start`，新 thread 保留 canonical fork identity 与完整 provider 历史。
4. 事件任一订阅未 ready 时，bootstrap 不得为 ready；用户能看到错误并显式重试。
5. 聚焦测试、完整前端 lint/test/build、相关 Go 测试全部通过。
6. Git tracked/staged diff 不包含 `.codex/config.toml`、`bin/mcp-lsp`、机器绝对路径或静默 fallback；这两个 ignored 本地产物是 Plan 1 的预期输出。

## 实现计划拆分

批准本规格后生成三份计划：

1. `docs/plans/2026-07-10-codex-worktree-lsp-bootstrap.md`
2. `docs/plans/2026-07-10-canonical-thread-fork.md`
3. `docs/plans/2026-07-10-event-subscription-readiness.md`

每份计划使用 TDD 小步、精确文件与命令、独立提交边界。Plan 1 的 implementation complete 不足以放行；只有 new-task runtime accepted 完成后，Plan 2 与 Plan 3 才进入实现。生成计划时先枚举共享文件，重叠文件必须串行处理。
