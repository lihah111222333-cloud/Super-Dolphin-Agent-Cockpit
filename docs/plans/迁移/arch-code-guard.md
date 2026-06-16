# 架构合规：代码守卫全量验证

审计时间：2026-03-21

## 审计方法

- 取证以 LSP 为主：`read_file` 用于确认文件总行数，`document_symbol` 用于确认函数起止范围，`text_search` 用于确认 `fx` 与 `context.WithTimeout` 命中点。
- `archtest` 状态通过 `go test -v ./internal/archtest -run '^Test...$'` 逐项确认。
- 文件/函数行数、嵌套深度、圈复杂度按当前仓库实现口径统计：`internal/archtest/guardlib.go`。该口径只扫描 `internal/`、`cmd/`、`scripts/` 下非测试 `.go` 文件，并忽略空行、`//` 注释、`/* ... */` 注释。
- 本报告未使用 `grep/find/cat/sed/awk`。

说明：
- LSP 能直接给出的是“物理总行数”和“函数物理范围”。
- 守卫真正判定使用的是“有效行数”。因此下面同时给出二者，避免把 400 行物理边界与 400 行有效边界混淆。

## 1. 逐文件行数统计：所有 `>350` 行文件

LSP `read_file` 确认当前只有 2 个文件超过 350 有效行；没有文件超过 400 有效行红线。

| 文件 | LSP 物理总行 | 守卫有效行 | 结论 |
| --- | ---: | ---: | --- |
| `internal/module/thread/lifecycle.go` | 400 | 385 | 超过 350；未超过 400 红线；物理行数已到边界 |
| `internal/sidecar/orch/orchestration/service.go` | 395 | 358 | 超过 350；未超过 400 红线 |

结论：
- `>350` 文件数：2
- `>400` 文件数：0

## 2. 逐函数行数：所有 `>60` 行函数

LSP `document_symbol` 确认当前只有 2 个函数超过 60 有效行；没有函数超过 80 有效行红线。

| 函数 | LSP 范围 | 物理跨度 | 有效行 | 嵌套 | CC | 结论 |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| `NewTurnHandlers` | `internal/module/turn/rpc.go:14-96` | 83 | 77 | 1 | 10 | 超过 60；未超过 80；复杂度已到上限 |
| `NewOrchestrationHandlers` | `internal/sidecar/orch/orchestration/rpc.go:15-77` | 63 | 63 | 1 | 5 | 超过 60；未超过 80 |

结论：
- `>60` 函数数：2
- `>80` 函数数：0

## 3. 嵌套深度抽查：10 个最复杂函数

抽样口径：按 `guardlib` 的圈复杂度降序排序，复杂度相同则按嵌套深度、有效行数排序；函数边界由 LSP `document_symbol` 交叉确认。

| 排名 | 函数 | 位置 | 有效行 | 嵌套 | CC | 结果 |
| --- | --- | --- | ---: | ---: | ---: | --- |
| 1 | `main` | `scripts/check_p1_turn_wrappers.go:25-61` | 35 | 3 | 10 | 通过 |
| 2 | `upsertSkillSummary` | `internal/module/skill/skills_meta.go:285-312` | 28 | 3 | 10 | 通过 |
| 3 | `main` | `scripts/refactor/rename_codexsdk_to_agentsdk.go:64-125` | 53 | 2 | 10 | 通过 |
| 4 | `bindApprovalLifecycle` | `internal/platform/rpc/module.go:74-104` | 31 | 2 | 10 | 通过 |
| 5 | `visitSkillEntry` | `internal/module/skill/skills_meta.go:42-65` | 24 | 2 | 10 | 通过 |
| 6 | `reconcileReadyStateLocked` | `internal/sidecar/orch/orchestration/helpers.go:119-138` | 20 | 2 | 10 | 通过 |
| 7 | `inputItemsFromSubmitParams` | `internal/sidecar/orch/orchestration/rpc.go:106-125` | 20 | 2 | 10 | 通过 |
| 8 | `validMethod` | `scripts/extract_jsonrpc_methods.go:187-202` | 16 | 2 | 10 | 通过 |
| 9 | `NewTurnHandlers` | `internal/module/turn/rpc.go:14-96` | 77 | 1 | 10 | 通过，但距离函数长度红线只差 3 行 |
| 10 | `collectStringConsts` | `scripts/extract_jsonrpc_methods.go:124-145` | 22 | 4 | 9 | 通过；嵌套深度达到上限但未越线 |

抽查结论：
- 样本内没有函数嵌套深度超过 4。
- 样本内最大嵌套深度为 4，命中 `collectStringConsts`。
- 样本内 9/10 函数的 CC 已经达到 10 的上限，虽然未违规，但复杂度余量很小。

## 4. `archtest` 8 个测试当前状态

顶层测试结果：8/8 通过，没有顶层失败项。

| 测试 | 当前状态 | 备注 |
| --- | --- | --- |
| `TestCodeSizeGuard` | 通过 | 无跳过项 |
| `TestDependencyDirection` | 通过 | 含 4 个子测试跳过：`rule6_tool_cannot_import_ui_state_directly`、`rule7_mcpserver_lsp_family`、`rule8_mcpserver_orch_family`、`rule9_mcpserver_ida_family`，原因均为对应目录尚未创建 |
| `TestWave3DependencyDirection` | 通过 | `rule11`、`rule12` 均通过 |
| `TestFxValidateApp` | 通过 | `fx.ValidateApp(app.Module)` 通过 |
| `TestMCPFamilyIsolation` | 通过 | `mcp_lsp`、`mcp_orch`、`mcp_ida` 三个子测试均通过 |
| `TestSharedBudget` | 通过 | 无跳过项 |
| `TestSqlcBoundary` | 通过 | 无跳过项 |
| `TestTimeoutLocality` | 通过 | 无跳过项 |

补充说明：
- 当前仓库的 `fx` import 作用域校验实际落在 `TestDependencyDirection/rule10_fx_import_scope`。
- `fx_graph_test.go` 当前只做 `fx.ValidateApp`，没有单独承载 `fx` import 范围核查。

## 5. `fx` import 作用域

LSP `text_search` 与 AST 枚举交叉确认后，生产代码一共有 41 个 `go.uber.org/fx` import 命中，全部位于允许范围内；违规文件数为 0。

允许范围命中如下：

- `cmd/` 目录，3 个文件：`cmd/mcp-ida/fx.go`、`cmd/mcp-lsp/fx.go`、`cmd/mcp-orch/fx.go`
- `internal/app/` 目录，3 个文件：`internal/app/app.go`、`internal/app/modules.go`、`internal/app/runner.go`
- `module.go` 文件，35 个文件：
  - `internal/sidecar/orch/orchestration/module.go`
  - `internal/module/skill/module.go`
  - `internal/module/thread/module.go`
  - `internal/module/turn/module.go`
  - `internal/module/workspace/module.go`
  - `internal/platform/bus/module.go`
  - `internal/platform/config/module.go`
  - `internal/platform/db/module.go`
  - `internal/platform/rpc/module.go`
  - `internal/platform/runner/module.go`
  - `internal/platform/statemachine/module.go`
  - `internal/provider/claudecli/module.go`
  - `internal/provider/codexapp/module.go`
  - `internal/provider/unified/module.go`
  - `internal/store/agentstatus/module.go`
  - `internal/store/ailog/module.go`
  - `internal/store/auditlog/module.go`
  - `internal/store/binding/module.go`
  - `internal/store/buslog/module.go`
  - `internal/store/commandcard/module.go`
  - `internal/store/cwdlock/module.go`
  - `internal/store/dbquery/module.go`
  - `internal/store/interaction/module.go`
  - `internal/store/module.go`
  - `internal/store/prompt/module.go`
  - `internal/store/sharedfile/module.go`
  - `internal/store/systemlog/module.go`
  - `internal/store/taskack/module.go`
  - `internal/store/taskdag/module.go`
  - `internal/store/tasktrace/module.go`
  - `internal/store/thread/module.go`
  - `internal/store/topologyapproval/module.go`
  - `internal/store/uipreference/module.go`
  - `internal/store/workspace/module.go`
  - `internal/ui/wails/module.go`

结论：
- `fx` 只出现在 `cmd/`、`internal/app/`、`module.go`
- 违规数：0

## 6. `timeout locality`：`context.WithTimeout` 只在 `platform/` 中

按与守卫一致的生产代码口径扫描后，`context.WithTimeout` 只在 `internal/platform/config/timeouts.go` 中出现，共 3 处；平台目录外违规数为 0。

| 文件 | 行号 | 结论 |
| --- | --- | --- |
| `internal/platform/config/timeouts.go` | `22` | 合规 |
| `internal/platform/config/timeouts.go` | `26` | 合规 |
| `internal/platform/config/timeouts.go` | `30` | 合规 |

补充说明：
- LSP 全量文本搜索还会命中 `internal/archtest/timeout_locality_test.go` 中的报错字符串，以及 `internal/provider/claudecli/thread_identity_test.go` 中的测试代码；这些都不属于生产代码口径，不计入守卫结果。
- 当前 `TestTimeoutLocality` 的实现允许 `internal/transport/retry/` 作为额外白名单，但仓库当前生产代码中不存在该路径命中，因此按你要求的更严格口径“只在 `platform/` 中”仍然成立。

## 总结

当前代码守卫总体状态为通过：

- 文件行数：2 个文件超过 350，有效行没有超过 400 红线
- 函数行数：2 个函数超过 60，有效行没有超过 80 红线
- 嵌套深度：抽查样本全部通过，没有超过 4
- `archtest`：8/8 顶层测试通过
- `fx` import：作用域合规，违规数 0
- `context.WithTimeout`：生产代码只出现在 `internal/platform/config/timeouts.go`

需要优先关注的贴边项：

- `internal/module/thread/lifecycle.go`：有效行 385，LSP 物理总行 400，已经是文件级最靠近红线的文件
- `internal/module/turn/rpc.go:14-96` 的 `NewTurnHandlers`：有效行 77，距离 80 行红线只差 3 行
- 复杂度抽样中 9/10 函数已达到 CC=10 上限，后续新增分支时应优先拆分而不是继续堆叠
