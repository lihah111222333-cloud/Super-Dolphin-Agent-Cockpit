# contract + dto + app + archtest 总审查

审查方式：

- 只读；未改业务代码。
- 文件/符号核对使用 LSP：`text_search` / `workspace_symbol` / `references(compact)` / `call_hierarchy` / `read_file` / `document_symbol`。
- 运行验证仅做两类补充：`go test ./internal/archtest/...`，以及只读 Go AST 统计脚本用于方法数/handler 数计算。
- 未使用 `grep/find/cat/sed/awk`。

## 总结

当前 V3 后端骨架是可启动、可装配、`archtest` 全绿的，但这组横切面仍有 4 个明显缺口：

1. `contract/` 里有 3 个悬空接口没有实现，也几乎没有被引用：`ToolCallResponder`、`ThreadRepository`、`HandlerProvider`。
2. `dto/` 的 6 族事件面是完整的，但 `EventHeader` 体系并不满足“9 层零重复”的字面口径；header struct 实际为 12 个，且跨分支存在字段重复。
3. DTO 的复合字段 `json` tag 当前主流是 lowerCamelCase，不是 snake_case。
4. `internal/app/` 目前只有 headless `Run()`；没有 `RunDesktop()`，也没有 Wails/desktop 入口，和迁移计划 P6 仍有距离。

## 1. contract 接口清单

逐文件接口如下：

| 文件 | 接口 |
|---|---|
| `internal/contract/provider.go` | `Driver`, `Session`, `TurnHandle`, `ToolCallResponder` |
| `internal/contract/approval.go` | `ApprovalResponder` |
| `internal/contract/session_resolver.go` | `SessionResolver` |
| `internal/contract/repositories.go` | `ThreadRepository` |
| `internal/contract/rpc.go` | `HandlerProvider` |

补充：

- `DriverFactory`、`ApprovalDecision`、`ThreadRef`、`HandlerMap` 都不是接口。
- 证据：`internal/contract/provider.go:10-50`，`internal/contract/approval.go:7-16`，`internal/contract/session_resolver.go:5-7`，`internal/contract/repositories.go:5-13`，`internal/contract/rpc.go:3-7`。

## 2. contract 实现覆盖

按 LSP `implementation` 结果统计：

| 接口 | 实现情况 | 证据 |
|---|---|---|
| `Driver` | 有，至少 2 个生产实现 | `internal/provider/claudecli/driver.go:19`，`internal/provider/codexapp/driver.go:18` |
| `Session` | 有，至少 2 个生产实现 | `internal/provider/claudecli/session.go:19`，`internal/provider/codexapp/session.go:18` |
| `TurnHandle` | 有，至少 2 个生产实现 | `internal/provider/claudecli/session.go:39`，`internal/provider/codexapp/session.go:36` |
| `ApprovalResponder` | 有，1 个生产实现 | `internal/platform/rpc/approval.go:19` |
| `SessionResolver` | 有，1 个生产实现 | `internal/provider/unified/session_resolver.go:12` |
| `ToolCallResponder` | 无实现 | `implementation=0`；`references` 只有声明点 |
| `ThreadRepository` | 无实现 | `implementation=0`；`references` 只有声明点 |
| `HandlerProvider` | 无实现 | `implementation=0`；`references` 只有声明点 |

结论：

- 8 个 contract 接口里，5 个已有生产实现，3 个是悬空 contract。
- 这 3 个悬空接口不只是“未实现”，当前也基本未被消费，更像未收尾/已废弃的预留面。

## 3. dto 事件类型

按你指定的 6 族核对：

| 族 | 常量数 | 事件 struct 数 | 结论 |
|---|---:|---:|---|
| `agent` | 5 | 5 | 完整 |
| `turn` | 7 | 7 | 完整 |
| `tool` | 4 | 4 | 完整 |
| `task` | 4 | 4 | 完整 |
| `workspace` | 5 | 5 | 完整 |
| `ui` | 3 | 3 | 完整 |

说明：

- 常量定义集中在 `internal/dto/shared/event.go:5-38`。
- 对应事件 struct 分别位于：
  - `internal/dto/agent/event.go`
  - `internal/dto/turn/event.go`
  - `internal/dto/tool/event.go`
  - `internal/dto/task/event.go`
  - `internal/dto/workspace/event.go`
  - `internal/dto/ui/event.go`
- 每个事件 struct 都有对应的 `Type() uint32` 方法，数量一一对应。
- `internal/dto/provider/event.go` 只有 `RawProviderEvent` 与 `EventTranslator`，不属于这 6 族 typed event。

## 4. dto EventHeader 嵌入

当前 shared header 链路是正确的，但不满足“9 层零重复”的字面口径。

现状：

- 根：`EventHeader`
- agent/tool 最长链：
  - `ToolApprovalHeader -> ToolCallHeader -> TurnHeader -> AgentHeader -> EventHeader`
- agent session 分支：
  - `AgentSessionHeader -> AgentHeader -> EventHeader`
- task 分支：
  - `TaskWakeupHeader -> TaskNodeHeader -> TaskDAGHeader -> EventHeader`
- workspace 分支：
  - `WorkspaceRunHeader -> EventHeader`
- ui 分支：
  - `UITurnHeader -> UIProjectionHeader -> EventHeader`

结论：

- “嵌入链是否正确”：是，分支链路都自洽。
- “是否 9 层零重复”：否。
  - 当前 header struct 实际是 12 个：`EventHeader`、`AgentHeader`、`AgentSessionHeader`、`TurnHeader`、`ToolCallHeader`、`ToolApprovalHeader`、`TaskDAGHeader`、`TaskNodeHeader`、`TaskWakeupHeader`、`WorkspaceRunHeader`、`UIProjectionHeader`、`UITurnHeader`。
  - 跨分支存在字段名重复，不是“零重复”。
    - `ThreadID` 同时出现在 `AgentHeader` 与 `UIProjectionHeader`
    - `DagKey` 同时出现在 `TaskDAGHeader` 与 `WorkspaceRunHeader`

证据：`internal/dto/shared/event.go:41-114`。

## 5. dto JSON tag

抽查 5 个关键 DTO，结果都不是 snake_case，而是 lowerCamelCase 或单词小写：

| DTO | 字段样例 | 结果 |
|---|---|---|
| `provider.StartSessionRequest` | `agentId` / `cwd` | 非 snake_case |
| `provider.TurnRequest` | `localId` / `threadId` / `outputSchema` | 非 snake_case |
| `turn.TurnSubmission` | `agentId` / `expectedTurnId` / `selectedSkills` | 非 snake_case |
| `task.TaskNodeStatusChanged` | `assignedTo` / `oldStatus` / `activeTurnId` | 非 snake_case |
| `workspace.WorkspaceRunCreated` | `sourceRoot` / `workspacePath` / `createdBy` | 非 snake_case |

证据：

- `internal/dto/provider/session.go:5-19`
- `internal/dto/provider/turn.go:9-49`
- `internal/dto/turn/model.go:11-19`
- `internal/dto/task/event.go:14-21`
- `internal/dto/workspace/event.go:6-46`

结论：

- 如果目标是 snake_case wire contract，当前 DTO 明显不满足。
- 当前风格更接近 lowerCamelCase；这和 V2/V3 对齐文档里多次强调的 snake_case 兼容点存在偏差。

## 6. app fx 图完整性

`internal/app/modules.go` 当前引入的模块如下：

- `config.Module`
- `db.Module`
- `bus.Module`
- `rpc.Module`
- `platformrunner.Module`
- `statemachine.Module`
- `store.Module`
- `skill.Module`
- `thread.Module`
- `turn.Module`
- `orchestration.Module`
- `workspace.Module`
- `unified.Module`
- `claudecli.Module`
- `codexapp.Module`
- 以及两个额外 provider：`AsRPCRunner`、`newThreadOrchestrationFacade`

结论：

- 对“当前仓内已存在、且导出顶层 `Module` 的 V3 后端模块”来说，`app.Module` 基本完整，没有看到同层级已存在模块却忘记引入的情况。
- 但相对迁移计划 P5/P6，仍缺 `dashboard` / `uistate` / `ida` / `internal/ui/runtime` 一类模块，因为这些包当前就还不存在。
- `internal/store/*` 的大量子模块是通过 `store.Module` 统一聚合的，不算遗漏。

证据：`internal/app/modules.go:23-44`，`internal/store/module.go:28-49`。

## 7. app Run/RunDesktop

现状：

- `internal/app/app.go` 只有 `NewApp()` 和 `Run()`。
- `Run()` 是标准 headless 模式：`NewApp()` -> `app.Start()` -> 等待 `app.Done()` -> `app.Stop()`。
- 全仓搜索 `RunDesktop` 结果为 0；`desktop`/Wails 入口也未落在当前 V3 代码。
- `cmd/agent-terminal/main.go` 只是直接调用 `app.Run()`。

结论：

- headless：支持。
- desktop：当前不支持。
- 所以这一项不是“headless + desktop 双模式”，而是“只有 headless”。

证据：`internal/app/app.go:17-31`，`cmd/agent-terminal/main.go:10-14`。

## 8. app BindRuntime

`BindRuntime` 已和 `fx.Lifecycle` 集成，链路如下：

- `NewApp()` 通过 `fx.Invoke(BindRuntime)` 接入 runtime 绑定。
- `BindRuntime` 在 `OnStart` 中：
  - 建 `context.WithCancel`
  - 启 goroutine 调 `platformrunner.RunGroup(runCtx, p.Runners)`
  - 非 `context.Canceled` 错误时记日志并调用 `fx.Shutdowner`
- `OnStop` 中：
  - 调 `cancel()`
  - 等待 `done` 或 stop context 超时

补充：

- `BindRuntime` 的 incoming call hierarchy 指向 `NewApp()`。
- `BindRuntime` 的 outgoing call hierarchy 指向 `fx.Lifecycle.Append`、`platformrunner.RunGroup`、`fx.Shutdowner.Shutdown`。
- `Runner` 来源至少有两路：
  - `AsRPCRunner(server *rpc.Server)` 把 RPC server 放进 `group:"runners"`
  - `orchestration.Module` 里 `NewRunnerActor` 也放进 `group:"runners"`

结论：

- lifecycle 集成是完整的，不是裸 goroutine。
- 但 `archtest/fx_graph_test.go` 只验证 `app.Module`，没有直接验证 `NewApp()` 上的 `fx.Invoke(BindRuntime)`；这部分目前靠代码阅读与全包测试旁证，而不是专门的 fx validate。

证据：`internal/app/app.go:17-21`，`internal/app/runner.go:13-61`，`cmd/mcp-orch/orchestration/module.go:15-23`，`internal/app/modules.go:46-48`。

## 9. archtest guardlib

`internal/archtest/guardlib.go` 当前实际检查维度有 7 类：

1. 文件有效行数：`MaxFileLines = 400`
2. 函数有效行数：`MaxFuncLines = 80`
3. 最大嵌套深度：`MaxNestingDepth = 4`
4. 最大圈复杂度：`MaxCCComplexity = 10`
5. 标识符下划线数量：`MaxUnderscores = 3`
6. 包文件数预算：`MaxPackageFiles = 15`
7. 包总有效行数预算：`MaxPackageLines = 3000`

补充观察：

- `ViolationDeadKey` 常量存在，但 `guardlib.go` 当前没有任何路径会产出这类 violation。
- 也就是说 “dead key” 是预留类型，不是已生效维度。

证据：`internal/archtest/guardlib.go:17-37`，`internal/archtest/guardlib.go:253-317`。

## 10. archtest 8 个测试

8 个测试文件与职责如下：

| 文件 | 顶层测试 | 作用 |
|---|---|---|
| `code_size_guard_test.go` | `TestCodeSizeGuard` | 调 `archtest.CheckAll`，统一守卫文件/函数/嵌套/CC/包预算 |
| `dependency_direction_test.go` | `TestDependencyDirection` | 依赖方向主规则，共 10 个子规则 |
| `dependency_direction_wave3_test.go` | `TestWave3DependencyDirection` | Wave3 额外依赖规则，共 2 个子规则 |
| `fx_graph_test.go` | `TestFxValidateApp` | `fx.ValidateApp(app.Module)` |
| `sqlc_boundary_test.go` | `TestSqlcBoundary` | `internal/store/sqlc` 只能被 `internal/store/*` 使用 |
| `mcp_family_isolation_test.go` | `TestMCPFamilyIsolation` | `cmd/mcp-lsp` / `cmd/mcp-orch` / `cmd/mcp-ida` 三族依赖隔离 |
| `timeout_locality_test.go` | `TestTimeoutLocality` | `context.WithTimeout` 只能留在规定位置 |
| `shared_budget_test.go` | `TestSharedBudget` | `internal/platform/shared` 预算与依赖约束 |

所以你列出的 7 个名字之外，“还有”的第 8 个是：

- `dependency_direction_wave3_test.go`

## 11. archtest 当前通过性

实际运行结果：

```text
go test ./internal/archtest/...
ok  	github.com/anthropic-ai/super-agent-v3/internal/archtest	1.129s
```

结论：

- 当前 `internal/archtest` 全绿。

## 12. handler 总计

V3 全部 `handler.Map` key 总数是 80。

分布：

| 文件 | key 数 |
|---|---:|
| `cmd/mcp-orch/orchestration/rpc.go` | 15 |
| `internal/module/skill/rpc.go` | 22 |
| `internal/module/thread/rpc.go` | 29 |
| `internal/module/turn/rpc.go` | 6 |
| `internal/module/workspace/rpc.go` | 8 |
| 合计 | 80 |

说明：

- `platform/rpc` 只负责聚合与注册，不新增公开 key。
- 当前没有第 6 个 `rpc.HandlerMapResult` 提供点。

## 13. V2 方法总计 / 精确迁移覆盖率

这里要区分 3 个口径：

1. **迁移计划口径**
   `docs/plans/迁移/v3-migration-plan.md` 多处写的是 `151（含 23 noop）`。

2. **V2 当前代码快照口径**
   `go-agent-v2/internal/guards/rpc_registry_snapshot.json` 实际解析出来是 **154** 个方法。

3. **当前 V3 实装口径**
   当前 V3 `handler.Map` key 是 **80** 个，但其中只有 **64** 个与 V2 快照同名对齐。

精确覆盖率：

- **同名迁移覆盖率（推荐口径）**：`64 / 154 = 41.56%`
- **纯注册规模比**：`80 / 154 = 51.95%`

补充：

- V3 有 16 个“新增/改名后无 V2 同名对应”的 key，主要是：
  - `task/dag/*`
  - `task/node/update`
  - `command/card/*`
  - `workspace/run/file/get`
  - `workspace/run/files/list`
  - `workspace/run/status/update`
  - `agent.snapshot`
  - `orchestration/report`
- 这也是为什么 `80` 个 V3 key 里，真正和 V2 同名对齐的只有 `64` 个。

## 14. import 方向

结论分两层看：

1. **是否零业务依赖**：基本是。
   `contract/` 与 `dto/` 都没有依赖 `internal/module`、`internal/platform`、`internal/provider`、`internal/store` 这些业务/基础设施层。

2. **是否只依赖标准库**：不是。
   - `internal/contract/provider.go` 依赖了 `internal/dto/provider`
   - 多个 `dto/*` 子包依赖了 `internal/dto/shared`

具体证据：

- `internal/contract/provider.go:3-7` 导入 `internal/dto/provider`
- `internal/dto/agent/event.go:3`
- `internal/dto/task/event.go:3`
- `internal/dto/tool/event.go:3`
- `internal/dto/turn/event.go:3`
- `internal/dto/ui/event.go:3`
- `internal/dto/workspace/event.go:3`
- `internal/dto/provider/turn.go:3-7`
- `internal/dto/turn/model.go:3-7`

同时：

- `contract/` / `dto/` 都没有 `fx` / `jrpc2` / `pgx` / `wails` 依赖。
- 这和 `archtest` 里的 `rule1_contract_dto_no_framework_imports` 一致。

## 15. 迁移文档一致性

抽查 3 点：

### 抽查点 A：P5 方法总数

- 文档声明：`151（含 23 noop）`，见 `docs/plans/迁移/v3-migration-plan.md:31,45,1178-1224`
- 代码现状：`go-agent-v2/internal/guards/rpc_registry_snapshot.json` 实际是 154 项
- 结论：**文档口径已落后当前 V2 快照，差 3 个方法**

### 抽查点 B：P5 dashboard 归宿

- 文档声明：`dashboard_bindings.go -> internal/module/dashboard/rpc.go + internal/platform/rpc/*`，见 `docs/plans/迁移/v3-migration-plan.md:1469`
- 代码现状：当前仓内没有 `internal/module/dashboard`，也没有 dashboard RPC 模块
- 结论：**该迁移目标尚未落地**

### 抽查点 C：P6 desktop / Wails 入口

- 文档声明：P6 迁移目标包含 `internal/ui/runtime/*`、`internal/ui/dashboard/*`、`internal/app/*`，并要求 “Desktop app 能完整启动 V3 后端”，见 `docs/plans/迁移/v3-migration-plan.md:1236-1258`
- 代码现状：
  - 没有 `internal/ui/`
  - 没有 `RunDesktop()`
  - `cmd/agent-terminal/main.go` 只有 `app.Run()`
- 结论：**P6 desktop/Wails 目标尚未进入当前实现面**

补充可见的文档漂移：

- `v3-migration-plan.md:1492,1501` 把 `server_bootstrap.go` / `server_lifecycle.go` 指向 `internal/app/lifecycle.go`；当前 `internal/app/` 并没有 `lifecycle.go`，对应逻辑在 `runner.go`。

## 最终判断

- `contract`：接口面已经收敛，但还有 3 个未落地/未消费的死 contract。
- `dto`：typed event 体系完整；header 分支结构正确；但 JSON tag 风格和“9 层零重复”目标都未达标。
- `app`：FX 装配可用、runtime lifecycle 已接入，但仍是 headless-only。
- `archtest`：8 个测试都在位，当前全绿；`guardlib` 的检查面比“文件/函数/嵌套/CC”更宽，但 `ViolationDeadKey` 仍是未启用预留项。
- 迁移进度：如果按 **V2 当前快照同名方法** 计算，P5 RPC 面当前覆盖率是 **41.56%**；如果只按注册规模比，是 **51.95%**。这两个数字都明显低于“P5 已收官”的口径。
