# P5 波次 2 审查 A

## 1. V2 方法覆盖

结论：当前“24 个方法”口径不成立。

- 当前工作树内相关 V2 文件实际行数为：
  - `go-agent-v2/internal/apiserver/methods_command.go`: `318` 行
  - `go-agent-v2/internal/apiserver/workspace_methods.go`: `168` 行
  - `go-agent-v2/internal/apiserver/methods_orchestration.go`: `242` 行
- 任务书给出的 `315/167/241` 已过时。
- 按指定三块 V2 surface 提取出的真实方法名是 `33` 个，不是 `24` 个。

### 1.1 `methods_command.go` 相关方法

`methods_command.go` 本身主要是 handler 实现；真实注册点在 `go-agent-v2/internal/apiserver/methods.go:157-166` 与 `go-agent-v2/internal/apiserver/methods.go:229-236`。

方法名共 `16` 个：

`app/list`, `command/exec`, `skills/list`, `skills/local/read`, `skills/local/listFiles`, `skills/local/write`, `skills/local/importDir`, `skills/local/delete`, `skills/remote/list`, `skills/remote/export`, `skills/remote/read`, `skills/remote/write`, `skills/config/read`, `skills/config/write`, `skills/summary/write`, `skills/match/preview`

对 R3 的影响：

- `p5-execution-plan.md:96-98` 当前只列了 `15` 个，少了 `app/list`。
- 任务描述把 R3 写成“command/card 方法”也与 V2 不符；`methods_command.go` 对应的是 `command/exec + skills/*`，不是 command card / prompt surface。

### 1.2 `workspace_methods.go` 方法

注册点在 `go-agent-v2/internal/apiserver/methods.go:255-259`。

方法名共 `5` 个：

`workspace/run/create`, `workspace/run/get`, `workspace/run/list`, `workspace/run/merge`, `workspace/run/abort`

对 R4 的影响：

- `p5-execution-plan.md:100-102` 的 `5` 个方法与 V2 完全一致。
- R4 在“方法覆盖”维度没有遗漏。

### 1.3 `methods_orchestration.go` 方法

注册点直接在 `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`。

方法名共 `12` 个：

`agent.launch`, `agent.submit`, `agent.submitPrompt`, `agent.stop`, `agent.list`, `agent.getReport`, `agent.rememberReportRequest`, `agent.reportEvent`, `agent.getState`, `agent.saveSubAgent`, `agent.deleteSubAgent`, `agent.persistSubAgentBinding`

对 R5 的影响：

- `methods_orchestration.go` 的真实基线是 `12` 个，不是“~8 个”。
- `p5-execution-plan.md:112-114` 把 R5 写成 `17` 个，是把以下 `5` 个 compat stub 一并并入了 orchestration：
  - `tasks/get`
  - `tasks/list`
  - `inbox-items`
  - `inbox-items/get`
  - `pending-automation-runs`
- 这 `5` 个方法不在 `methods_orchestration.go`，而在 `go-agent-v2/internal/apiserver/methods.go:320-346` 的 `registerDebugStubMethods()` 中注册。

### 1.4 覆盖结论

若以当前波次 2 执行计划为准：

- R3：`15/16`，缺 `app/list`
- R4：`5/5`
- R5：`12/12` 的 agent 方法已覆盖，但额外吸收了 `5` 个不属于 `methods_orchestration.go` 的 compat stub

若以“指定三块 V2 文件”为准：

- 总数应为 `33`，不是 `24`
- 当前任务书存在两个问题：
  - 低估方法总数
  - 混淆了 `methods_orchestration.go` 与 `methods.go` 中 compat stub 的归属

## 2. Service 差距

### 2.1 现有 contract 面

`internal/store/commandcard/contract.go` 的 `Store` 只有 `4` 个方法：

- `Get`
- `InsertVersion`
- `Upsert`
- `List`

`internal/store/prompt/contract.go` 的 `Store` 只有 `4` 个方法：

- `Get`
- `InsertVersion`
- `Upsert`
- `List`

`internal/store/workspace/contract.go` 的 `Store` 有 `8` 个方法：

- `UpsertRun`
- `GetRun`
- `ListRuns`
- `UpdateRunStatus`
- `TransitionRunStatus`
- `UpsertFile`
- `GetFile`
- `ListFiles`

`internal/sidecar/orch/orchestration/contract.go` 的 `Service` 只有 `6` 个方法：

- `LaunchAgent`
- `StopAgent`
- `SubmitTurn`
- `CompleteTurn`
- `Recover`
- `Snapshot`

### 2.2 R3: skill 模块与 commandcard/prompt store

结论：按 V2 `methods_command.go` 的真实职责，R3 不以 `commandcard/prompt` store 为主支撑面。

原因：

- `methods_command.go:94-141` 显示 V2 skill 路径的核心依赖是：
  - `SkillService`
  - `skills.Manager`
  - `AutoMatchCollector`
  - provider auto-match bridge
- `go-agent-v2/internal/apiserver/tool_provider_adapters.go:56-67` 才是 command card / prompt template store 的接入点。

因此：

- 如果 R3 的真实范围仍是 `command/exec + skills/*`，那么 `commandcard/prompt` store 不是主要阻塞点。
- 如果任务书已经把 R3 改成 command card / prompt surface，那么当前 store contract 只够做：
  - `get`
  - `list`
  - versioned `upsert`
- 当前 store contract 不提供：
  - `delete`
  - `set enabled/disabled`

结论化判断：

- “R3 需要的 store 方法是否已在 store contract 中”这个问题，前提本身未冻结。
- 按 V2 skill 基线，重点不是 store 缺口，而是缺一个新的 `skill.Service` 与 matcher 抽象。

### 2.3 R4: workspace 方法的 store 支撑

结论：`workspace.Store` 对 `workspace/run/create|get|list|merge|abort` 的持久化支撑基本齐全。

支撑关系：

- `create`：`UpsertRun` + 初始文件写入可用 `UpsertFile`
- `get`：`GetRun`
- `list`：`ListRuns`
- `merge`：`GetRun` + `ListFiles` + `GetFile` + `TransitionRunStatus`/`UpdateRunStatus` + `UpsertFile`
- `abort`：`TransitionRunStatus`/`UpdateRunStatus`

真实缺口不在 store contract，而在模块现状：

- 当前仓库不存在 `internal/module/workspace`
- 也不存在 `workspace.Service`
- 因此 R4 不是“扩充已有模块”，而是从零补齐 workspace 模块

### 2.4 R5: orchestration 方法与 Service 接口

结论：当前 `orchestration.Service` 明显不够。

直接可映射：

- `agent.launch` -> `LaunchAgent`
- `agent.stop` -> `StopAgent`

需要额外 adapter 才能勉强承接：

- `agent.submit` / `agent.submitPrompt` -> `SubmitTurn`
  - 但 V2 参数是 `agent_id/prompt/images/files`
  - 当前 `dto/turn.TurnSubmission` 是 `agentId/threadId/input/selectedSkills/...`
  - 不是同形 DTO
- `agent.getState` -> 可由 `Snapshot` 派生

当前完全无对应接口：

- `agent.list`
- `agent.getReport`
- `agent.rememberReportRequest`
- `agent.reportEvent`
- `agent.saveSubAgent`
- `agent.deleteSubAgent`
- `agent.persistSubAgentBinding`

额外说明：

- `tasks/get`、`tasks/list`、`inbox-items`、`inbox-items/get`、`pending-automation-runs` 当前是 compat stub，不应该被错误计入 `methods_orchestration.go` 的 service 缺口。
- 若这些方法在波次 2 要从 stub 升级为真实能力，需要单独定义 task / inbox read-model 接口，而不是直接塞进当前 `orchestration.Service`。

## 3. 依赖方向

### 3.1 R3 `module/skill`

结论：`rpc.go` 只依赖 `skill.Service` 是正确方向；“skill 只 import contract + dto + store，不 import provider”只说对了一半。

正确边界应为：

- `module/skill/rpc.go`
  - 只依赖 `skill.Service` + `platform/rpc`
- `module/skill/service.go`
  - 依赖模块内 loader/matcher
  - 依赖文件系统
  - 依赖一个 provider-facing 的抽象接口
  - 不 import concrete `provider/*`

当前问题：

- 现有 `internal/contract/provider.go` 并没有 skill auto-match contract
- V2 `methods_command.go:94-141` 明确存在 `AutoMatchCollector`
- `go-agent-v2/internal/skills/manager.go:32-39` 已经把它抽象成独立接口

因此：

- skill 模块需要额外的 matcher/collector 抽象
- 不能把 provider concrete import 继续留在 `rpc.go`
- 也不能简单认为“只靠 store contract 就够”

### 3.2 R4 `module/workspace/rpc.go`

结论：目标方向应当是“只注入 `workspace.Service`”，这一点成立。

但当前仓库没有 `workspace.Service`，所以这是目标态，不是现状。

### 3.3 R5 `module/orchestration/rpc.go`

结论：当前不能只注入现有 `orchestration.Service`。

原因：

- 当前 interface 只有 `6` 个方法
- R5 真实 agent surface 至少需要：
  - launch
  - stop
  - submit
  - list
  - report
  - state
  - remember report request
  - event ingest
  - sub-agent save/delete/binding

若坚持 `rpc.go` 只注入 `orchestration.Service`，必须先扩接口；否则只能再次把 `rpc.go` 做成 Server helper 聚合层。

## 4. 代码量

结论：`24 方法 <= 650 行` 的预算判断失真，不能直接成立。

先看口径：

- 若按指定三块 V2 文件真实方法数：`33`
- 若按当前 `p5-execution-plan.md`：`37`

则 `650` 行预算对应的平均每方法行数分别是：

- `650 / 33 = 19.7` 行
- `650 / 37 = 17.6` 行

与 V2 对比：

- 当前三块 V2 文件实际总行数约 `728` 行
- `650` 行只比 V2 少约 `10.7%`

判断：

- 如果只写薄 `rpc.go` wrapper，且 service/contract 早已存在，`650` 行有机会
- 但当前波次 2 不是这个前提：
  - R3 是全新 `skill` 模块
  - R4 的 `workspace` 模块当前不存在
  - R5 需要先扩 `orchestration.Service`

因此：

- `650` 行只适合做“transport glue 预算”
- 不适合作为“波次 2 端到端交付预算”

补充：

- `v3-module-migration-details.md` 对完整模块的估算是：
  - `skill`: `600-900` 行
  - `workspace`: `700-1000` 行
  - `orchestration`: `2200-3200` 行
- 这进一步说明当前预算只能覆盖很薄的一层，覆盖不了新模块骨架与 service 补齐。

## 5. HandlerMapResult

结论：三个 `rpc.go` 都应输出 `rpc.HandlerMapResult`，并且都需要通过 `fx.Provide` 进入 `group:"rpc_handlers"`。

现有模式证据：

- `internal/module/thread/rpc.go` 的 `NewThreadHandlers(...) rpc.HandlerMapResult`
- `internal/module/turn/rpc.go` 的 `NewTurnHandlers(...) rpc.HandlerMapResult`
- `internal/platform/rpc/module.go` 通过 `group:"rpc_handlers"` 聚合并注册

需要的改动：

- `internal/module/skill/module.go`
  - 必须 `fx.Provide(NewService, NewSkillHandlers, ...)`
- `internal/module/workspace/module.go`
  - 必须 `fx.Provide(NewService, NewWorkspaceHandlers, ...)`
- `internal/sidecar/orch/orchestration/module.go`
  - 当前只提供 `NewService`、`Service` 转型、`NewRunnerActor`
  - 必须新增 `NewOrchestrationHandlers`

还需要同步改动：

- `internal/app/modules.go` 当前只挂了 `thread.Module`、`turn.Module`、`orchestration.Module`
- 新增 `skill.Module` 与 `workspace.Module`，否则新 handler 不会进入 RPC 注册图

## 6. skill 设计

结论：`skill` 必须是完整模块，且需要额外的 loader/matcher；不应把 concrete provider 依赖继续塞进 `rpc.go`。

### 6.1 模块形态

建议保留：

- `module.go`
- `contract.go`
- `service.go`
- `rpc.go`
- `loader.go`
- `matcher.go`

这与 `v3-module-migration-details.md:190-205` 一致。

### 6.2 contract 设计判断

公共 contract 应聚焦业务 facade，不应暴露 transport/实现细节。

合理的公共能力至少包括：

- `List`
- `LocalRead`
- `LocalListFiles`
- `LocalWrite`
- `LocalImportDir`
- `LocalDelete`
- `RemoteRead`
- `RemoteWrite`
- `ConfigRead`
- `ConfigWrite`
- `SummaryWrite`
- `MatchPreview`

是否把 `AppList` 放进 `skill.Service`：

- 从 V2 文件归属看，它在 skill/command surface 内
- 从语义看，它更像 compat app-level stub
- 当前文档对其归属不一致
- 需要在冻结方法基线时一起定稿

是否把 `command/exec` 放进 `skill.Service`：

- 不建议
- 它是通用 shell/tooling 行为，不是 skill 领域行为
- 若继续放在 R3，应在任务书中明确这是“按旧文件分组迁移”，不是“按领域边界设计”

### 6.3 是否需要额外的 loader/matcher

结论：需要。

原因：

- loader 负责：
  - 本地技能目录扫描
  - frontmatter / summary / config 读写
  - 导入、覆盖、删除与索引刷新
- matcher 负责：
  - `skills/match/preview`
  - turn prepare 侧候选集与自动匹配
  - provider auto-match bridge

建议：

- loader/matcher 作为模块内部协作者
- `rpc.go` 不直接感知它们
- 若 turn 模块需要复用匹配能力，只暴露最小 cross-module interface，例如：
  - `ListSkillMatchCandidates`
  - `CollectAutoMatchedMatches`

### 6.4 commandcard/prompt 与 skill 的边界

结论：不建议把 command card / prompt template 直接并入 `skill` 模块。

原因：

- 现有 V2 `methods_command.go` 并不承载 command card / prompt template RPC
- V2 的 command card / prompt template store 接入点在 `tool_provider_adapters.go`
- 现有 `commandcard/prompt` store 更适合作为：
  - dashboard projection
  - tool-facing resource/query service

若任务书确实要在波次 2 引入 command/prompt surface，应单独更名，不要继续复用 `module/skill` 名称。

## 结论（Blocker / Improvement）

### Blocker

- 波次 2 的方法基线未冻结：
  - 用户任务描述是 `24`
  - 指定三块 V2 文件实际是 `33`
  - 当前执行计划写成 `37`
- R3 当前存在直接遗漏：`app/list`
- R5 把 `tasks/*`、`inbox-items*`、`pending-automation-runs` 误归到 `methods_orchestration.go`，属于归属错误
- `internal/module/workspace` 当前不存在，R4 不是“扩充已有模块”
- `orchestration.Service` 当前不能承接 R5
- `skill` 所需的 matcher/collector 抽象当前不存在于 V3 contract 中

### Improvement

- 先冻结波次 2 的 canonical method list，再开工
- 将 compat stub 与真实 orchestration surface 分开预算
- 先补 `skill.Service` / `workspace.Service` / `orchestration.Service`，再写 `rpc.go`
- 将 `command/exec` 与 `app/list` 的归属在任务书内明确写死
- `skill` 采用 `service + loader + matcher` 结构，不引入 concrete provider import

## 互辩：对 audit-B 的逐节批判

### 1. B 的 V2 复杂度抽查是否充分

结论：不充分。B 在 `p5-wave2-plan-audit-B.md:5-25` 只抽了每个文件“最长两个 handler”，这对 `methods_command.go` 不够。

用 LSP 列出的 `go-agent-v2/internal/apiserver/methods_command.go` 全函数与行数如下：

| 函数 | 行数 |
|---|---:|
| `commandExecTyped` | 53 |
| `newSkillsAutoMatchCollector` | 20 |
| `newSkillsManagerWithService` | 7 |
| `newSkillsManager` | 7 |
| `skillsManagerDelegate` | 12 |
| `normalizeSkillNamesForEvent` | 20 |
| `collectChangedSkillNames` | 38 |
| `notifySkillsChanged` | 12 |
| `skillsList` | 23 |
| `appList` | 3 |
| `skillsLocalReadTyped` | 3 |
| `skillsLocalListFilesTyped` | 3 |
| `skillWriteWithNotify` | 11 |
| `skillsLocalWriteTyped` | 3 |
| `skillsLocalImportDirTyped` | 3 |
| `skillsLocalDeleteTyped` | 3 |
| `skillsMatchPreviewTyped` | 3 |
| `skillsConfigReadTyped` | 3 |
| `skillsConfigWriteTyped` | 3 |
| `skillsSummaryWriteTyped` | 3 |
| `skillsRemoteReadTyped` | 3 |
| `skillsRemoteWriteTyped` | 3 |
| `getAgentSkills` | 3 |
| `listSkillMatchCandidates` | 24 |

对 B 的批判点：

- B 只抓了 `commandExecTyped` 和 `skillsList`，忽略了至少 `4` 个中等长度但直接影响模块边界的 helper：
  - `newSkillsAutoMatchCollector`：把 provider auto-match 桥接进 skill surface
  - `collectChangedSkillNames`：承担写后事件 payload 归一化
  - `notifySkillsChanged`：承担 RPC notify side effect
  - `listSkillMatchCandidates`：把 skill 目录扫描结果转成 turn prepare 候选集
- 这些 helper 虽然不是 RPC handler，但它们决定 `skill` 模块应否拆出 `matcher`、event publisher、候选集 facade。只看“最长两个 handler”会低估 `methods_command.go` 的真实风险点。
- B 对 `workspace_methods.go` 和 `methods_orchestration.go` 的“薄壳”判断大体成立，但在 `methods_command.go` 这个最关键文件上，抽查策略明显偏窄。

更严格的结论：

- 若审查目标是“handler 是否过厚”，B 的抽查勉强够用。
- 若审查目标是“波次 2 该下沉哪些逻辑到 module/service”，B 的抽查不足，漏掉了 auto-match、写后通知、skill candidate 三条重要链路。

### 2. B 对工厂模式的判断是否准确

结论：不准确，至少论据不成立。B 在 `p5-wave2-plan-audit-B.md:27-51` 把 `command/card` 说成 `list/get/upsert/delete/version/run/execute` 高异构 surface，但当前 V2 代码并没有给出这组 surface。

用 LSP 读取 V2 command/prompt 注册与 handler 代码：

- `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:116-169`
  - 只注册了 `command_list`、`command_get`、`prompt_list`、`prompt_get`
- `go-agent-v2/pkg/toolsdk/tools/resource.go:253-317`
  - `resourceCommandList`
  - `resourceCommandGet`
  - `resourcePromptList`
  - `resourcePromptGet`

这 `4` 个 handler 的实际形态是高度对称的：

- `list` 路由：
  - decode 可选 `keyword`
  - 调 `Store.List`
  - `resourceJSON`
- `get` 路由：
  - decode 必填 key
  - 调 `Store.Get`
  - nil check
  - `resourceJSON`

批判点：

- B 的“不要做 `cardHandler`，因为各方法 schema 差异明显”这一判断没有被 V2 当前注册代码支撑。
- 当前 V2 证据更像是“统一 CRUD 中的读面”，而不是高异构写面。
- 因此，如果范围真是 V2 现有 command/prompt read surface，那么一个轻量工厂或成对 helper 反而有明显价值。

更严格的判定：

- `重型 mega factory` 仍然不推荐，这一点 B 的方向没错。
- 但 B 用“方法强异构”去否定 `cardHandler` 的理由站不住，因为当前 V2 实际 surface 只有对称的 `list/get`。
- 若任务书坚持“8 个 command/card/* 方法”，那也是 scope 先漂移了，B 应该先证明这 `8` 个方法在 V2 的真实存在，而不是直接按假定异构面推演。

### 3. B 的 store 支撑判断是否完整

结论：大体正确，但表述仍偏粗。

对 `taskdag`，B 不是只看“文件存在”，这一点需要纠正。LSP 证据显示：

- `internal/store/taskdag/contract.go`
  - 有 `UpsertDAG`
  - 有 `ListDAGs`
  - 有 `GetDAG`
  - 有 `UpsertNode`
  - 有 `UpdateNodeStatus`
  - 有 `ListNodes`
  - 还有 wakeup / worker lease / tx 能力
- `internal/store/taskdag/store.go`
  - 上述 contract 方法都有实现

同时，V2 `task/dag/*` surface 实际需求可从下面两处核对：

- `go-agent-v2/pkg/toolsdk/tools/providers.go:169-176`
  - `DAGManager` 需要 `SaveDAG/ListDAGs/GetDAGDetail/SaveNode/UpdateNodeStatus/ListNodes`
- `go-agent-v2/pkg/toolsdk/tools/resource_dag.go:39-320`
  - `task_create_dag`
  - `task_get_dag`
  - `task_update_node`
  - `task_start_node`

对 B 的批判点：

- B 的方向是对的：`taskdag store` 不是 R5 blocker。
- 但它没有把一个关键细节说透：
  - V2 需要的是 `DAGManager` 风格聚合接口
  - 当前 V3 提供的是更底层的 `taskdag.Store`
  - 两者不是一一同名映射，例如 V2 要 `GetDAGDetail`，V3 需要用 `GetDAG + ListNodes` 组合
- 所以“store 足够”成立，但前提是先补一个 service/facade，把 store contract 组合成 V2 需要的读写面。

更严格的结论：

- B 没有漏看方法签名。
- 但它对 `taskdag.Store` 与 V2 `DAGManager` 之间的适配成本写得过轻，应该明确说是“store 足够，facade 不足”。

### 4. B 对 workspace 模块现状的判断是否准确

结论：准确，但仍然不够狠。

LSP 复核结果：

- 在 `internal/module` 下搜索 `package workspace`：无命中
- 在 `internal/module` 下搜索 `CreateRun(ctx`：无命中
- 在 `internal/module` 下搜索 `Workspace`：无有效模块命中
- 当前 `internal/module` 只有：
  - `thread`
  - `turn`
  - `orchestration`

因此 B 在 `p5-wave2-plan-audit-B.md:104-109` 的判断是对的：

- `internal/module/workspace/` 当前不存在
- R4 不是“补一个 rpc.go”
- 至少要新建 `module.go + contract.go + service.go + rpc.go`

但可以更进一步批判 B 的保守处：

- B 说清了“模块不存在”，但没有把这件事与 `R4 <= 150 行` 的预算正面撞穿。
- 结合 `docs/plans/迁移/v3-module-migration-details.md:327-355`，workspace 完整模块目标结构和代码量预估是 `700-1000` 行级，不可能靠 `<=150` 的 `rpc.go` 预算兜住。

更严格的结论：

- B 没有低估“是否存在模块”这个事实。
- 但它仍然低估了预算冲击，应该直接把 `R4 <=150` 判为失真预算，而不是只说“需要完整模块”。

### 5. B 对 orchestration.Service 映射是否遗漏了 `agent.list`

结论：没有遗漏，这一条 B 是准确的。

LSP 证据：

- `internal/sidecar/orch/orchestration/contract.go:10-17`
  - `Service` 只有 `LaunchAgent/StopAgent/SubmitTurn/CompleteTurn/Recover/Snapshot`
  - 没有 `ListAgents`
- `internal/sidecar/orch/orchestration/helpers.go:123-137`
  - 只有私有 `listAgents() []agentRuntime`

B 在 `p5-wave2-plan-audit-B.md:115-124` 已明确写出：

- `agent.list -> 没有 ListAgents`
- 私有 `listAgents()` 不能当稳定 contract

这一条不能批错，反而说明 B 在 orchestration 缺口识别上比它在 command/card 工厂和 `methods_command.go` 复杂度抽查上更扎实。

### 互辩结论

对 audit-B 的最终判定：

- `V2 复杂度`：部分成立，但 `methods_command.go` 抽查明显不充分，漏掉 auto-match / notify / candidate helper 风险点。
- `工厂模式`：论据不成立。V2 command/prompt 当前 surface 是对称 `list/get`，不是 B 描述的高异构写面。
- `store 支撑`：方向基本正确，`taskdag.Store` 也确实核到了方法签名；但应更明确区分“store 足够”和“facade 缺失”。
- `workspace 现状`：事实判断准确，但对 `<=150` 预算的批判力度不够。
- `orchestration.Service`：`agent.list` 缺口识别准确，无遗漏。

## 波次 2 前置修正落实

### 1. 修正 1：`orchestration.Service` 新增 `ListAgents`

已落地：

- `internal/sidecar/orch/orchestration/contract.go`
  - 新增 `ListAgents(ctx context.Context) ([]AgentSnapshot, error)`
- `internal/sidecar/orch/orchestration/service.go`
  - 新增公开方法 `ListAgents`
  - 从 `Snapshot` 提取 `snapshotLocked`

实现判断：

- 原实现中不存在 `snapshotLocked`
- 已按 `Snapshot` 现有逻辑提取锁内 helper，避免复制快照拼装逻辑

### 2. 修正 2：R4 workspace 预算上调

已落地：

- `docs/plans/迁移/p5-execution-plan.md`
  - `R4: module/workspace/rpc.go` 已从 `≤150` 改为 `≤300`

### 3. 修正 3：R3 skill 范围确认

LSP 证据：

- `go-agent-v2/internal/apiserver/methods.go:134-148`
  - `registerMethods()` 中依次调用 `registerCoreMethods`、`registerThreadTurnMethods`、`registerSkillMethods`、`registerConfigAccountMethods`...
- `go-agent-v2/internal/apiserver/methods.go:229-237`
  - `registerSkillMethods()` 只注册 `skills/*`

`registerSkillMethods()` 实际注册的 `14` 个方法，全部应归 R3：

- `skills/list`
- `skills/local/read`
- `skills/local/listFiles`
- `skills/local/write`
- `skills/local/importDir`
- `skills/local/delete`
- `skills/remote/list`
- `skills/remote/export`
- `skills/remote/read`
- `skills/remote/write`
- `skills/config/read`
- `skills/config/write`
- `skills/summary/write`
- `skills/match/preview`

不在 `registerSkillMethods()` 内，但与 `methods_command.go` 同文件分组相关的方法：

- `app/list`：位于 `registerCoreMethods`
- `command/exec`：位于 `registerCoreMethods`

归属判断：

- R3 确定范围：上述 `14` 个 `skills/*`
- 后续波次：`registerSkillMethods()` 内无剩余方法
- `app/list` 与 `command/exec` 不应再被混写成“command/card”范围，必须在执行计划中单独冻结归属

### 4. 修正 4：`command/exec` 依赖验证

LSP 证据：

- `go-agent-v2/internal/apiserver/methods_command.go:40-92`
  - `commandExecTyped` 直接使用 `exec.CommandContext`
  - 使用 `CurrentProjectCwd(s)` 作为默认工作目录
  - 使用 env allowlist、超时、输出截断、read-command LSP hint 注入
  - 全程没有 provider session / `SendCommand` / `SessionResolver`

结论：

- `command/exec` 不需要 `contract.SessionResolver`
- 它也不是 store 驱动逻辑，因此“只注入 store 即可”不成立
- 若仍将 `command/exec` 归入 R3，则 `module/skill` 需要额外的本地执行抽象，例如 `CommandExecutor` 或等价 facade
- 更稳的边界是把 `command/exec` 留在 `platform/rpc` 或单独 command facade，而不是塞进 skill store 路径

### 5. 改进 1：`cardHandler` CRUD 工厂设计

不在本轮落代码，只记录设计。

```go
// 建议在 R3 实现时使用的 CRUD 工厂
func cardCRUD[Req any](storeFn func(ctx context.Context, req Req) (any, error)) handler.Func {
    return rpc.StrictHandler(storeFn)
}
```

更贴近 key-based surface 的设计：

```go
func cardByKey(svc skill.Service, method string) handler.Func {
    return rpc.StrictHandler(func(ctx context.Context, p cardKeyParams) (any, error) {
        return svc.CardOperation(ctx, p.Key, method)
    })
}
```

约束：

- 只适合对称 `list/get` 或 key-based surface
- 不应把异构写面压成 mega params

### 6. 改进 2：R5 DAG 方法下沉备注

已落地：

- `internal/sidecar/orch/orchestration/contract.go`
  - 新增 `TODO(P5-R5)` 注释，明确 `CreateDAG/GetDAG/ListDAGs/UpdateNode` 属于 service 层，不应停留在 rpc glue

### 7. 改进 3：skill auto-match 归属备注

应归入 `module/skill/service.go`，不在 rpc 层：

- `newSkillsAutoMatchCollector`
- `collectChangedSkillNames`
- `notifySkillsChanged`
- `listSkillMatchCandidates`

结论：

- R3 实现时 `skill.Service` 必须包含 auto-match 能力
- `rpc.go` 只负责 strict bind、调用 service、返回 response/notify envelope
