# 架构合规：测试覆盖全景

## 范围与方法

- 包清单用 `go list ./...` 枚举，避免漏掉“零测试”包。
- `_test.go` 存在性、`func Test*` 数量、`archtest` 守卫覆盖范围用 LSP `text_search` 与 `read_file` 复核。
- 第 3 节和第 4 节的“几个测试函数”按顶层 `func Test*` 计数，不含 `t.Run` 子用例。
- “核心模块”优先看生产包；`cmd/*` 与 `scripts/*` 作为补充观察单列，不并入主缺口排序。

## 1. 逐模块测试文件清单

### 1.1 有 `_test.go` 的核心生产包

| 包 | 生产 Go 文件数 | `_test.go` |
| --- | ---: | --- |
| `internal/archtest` | 1 | `code_size_guard_test.go`, `dependency_direction_test.go`, `dependency_direction_wave3_test.go`, `fx_graph_test.go`, `mcp_family_isolation_test.go`, `shared_budget_test.go`, `sqlc_boundary_test.go`, `timeout_locality_test.go` |
| `internal/sidecar/orch/orchestration` | 14 | `execution_test.go`, `submission_test.go`, `turn_lifecycle_test.go` |
| `internal/module/skill` | 12 | `exec_test.go`, `skills_fs_test.go`, `skills_match_test.go` |
| `internal/module/turn` | 11 | `orchestration_starter_test.go`, `service_test.go` |
| `internal/module/workspace` | 7 | `service_test.go` |
| `internal/platform/bus` | 9 | `bus_test.go`, `subscription_test.go`, `typed_test.go` |
| `internal/platform/rpc` | 15 | `approval_test.go`, `handler_test.go` |
| `internal/provider/claudecli` | 14 | `thread_identity_test.go` |
| `internal/provider/unified` | 7 | `client_test.go`, `contract_test.go`, `manifest_test.go`, `registry_test.go` |

### 1.2 仅有守卫/脚本测试的包

| 包 | 生产 Go 文件数 | `_test.go` |
| --- | ---: | --- |
| `internal/guards` | 0 | `code_size_guard_test.go` |
| `scripts` | 0 | `check_p1_turn_wrappers_guard_test.go`, `extract_jsonrpc_methods_guard_test.go`, `extract_jsonrpc_methods_main_guardrail_test.go` |
| `scripts/refactor` | 0 | `rename_codexsdk_to_agentsdk_error_path_guard_test.go`, `rename_codexsdk_to_agentsdk_guard_strength_test.go`, `rename_codexsdk_to_agentsdk_guard_test.go`, `rename_codexsdk_to_agentsdk_guardrail_extra_test.go`, `rename_codexsdk_to_agentsdk_main_guardrail_test.go`, `rename_codexsdk_to_agentsdk_side_effect_guard_test.go` |

### 1.3 零测试核心生产包

- `internal/app`
- `internal/contract`
- `internal/dto/*`: `agent`, `provider`, `shared`, `task`, `tool`, `turn`, `ui`, `workspace`
- `internal/mcpserver/common`
- `internal/module/thread`
- `internal/platform/*`: `config`, `db`, `runner`, `shared`, `statemachine`
- `internal/provider/codexapp`
- `internal/store` 与全部子包：`agentstatus`, `ailog`, `auditlog`, `binding`, `buslog`, `commandcard`, `cwdlock`, `dbquery`, `interaction`, `prompt`, `sharedfile`, `sqlc`, `systemlog`, `taskack`, `taskdag`, `tasktrace`, `thread`, `topologyapproval`, `uipreference`, `workspace`
- `internal/ui/wails`
- `pkg/logger`
- `internal/contract`
- `internal/provider/unified`

### 1.4 补充观察

- `cmd/*` 入口包全部零测试：`cmd/agent-terminal`, `cmd/mcp-ida`, `cmd/mcp-lsp`, `cmd/mcp-orch`。
- 当前覆盖较好的核心包主要集中在 `internal/sidecar/orch/orchestration`, `internal/module/skill`, `internal/module/turn`, `internal/platform/bus`, `internal/provider/unified`。

## 2. `archtest` 8 个守卫测试

| 测试名 | 文件 | 覆盖范围 |
| --- | --- | --- |
| `TestCodeSizeGuard` | `internal/archtest/code_size_guard_test.go` | 调 `archtest.CheckAll(...)` 扫描 `internal`, `cmd`, `scripts`，拦截代码体量超预算违规。 |
| `TestDependencyDirection` | `internal/archtest/dependency_direction_test.go` | 10 条静态导入边界规则：`contract/dto` 不得引框架依赖；`internal/module` 实现文件不得直接引 `fx`；`claudecli/codexapp` 不得依赖 `store`；provider 外部依赖只允许白名单；`internal/store/*` 只能依赖 `platform/db`、`store/sqlc`、`contract`、`dto`；`fx` 导入只能出现在 `cmd/*`、`internal/app/*` 或 `module.go`。其中 `rule6` 与 `rule7-9` 只有对应目录存在时才生效。 |
| `TestWave3DependencyDirection` | `internal/archtest/dependency_direction_wave3_test.go` | Wave3 新边界：`internal/module/turn` 不得依赖 `internal/provider/*`；`internal/provider/unified` 不得依赖具体 provider `claudecli` / `codexapp`。 |
| `TestFxValidateApp` | `internal/archtest/fx_graph_test.go` | 对 `app.Module` 做 `fx.ValidateApp(...)`，验证装配图可解析。 |
| `TestMCPFamilyIsolation` | `internal/archtest/mcp_family_isolation_test.go` | 用 `go list` 依赖图校验 `cmd/mcp-lsp`, `cmd/mcp-orch`, `cmd/mcp-ida` 三个 MCP 家族不跨用彼此禁用的 tool 依赖。 |
| `TestSharedBudget` | `internal/archtest/shared_budget_test.go` | 约束 `internal/platform/shared` 单文件有效行数不超过 500、目录总量不超过 2000，且不得反向依赖 `internal/module/*`。 |
| `TestSqlcBoundary` | `internal/archtest/sqlc_boundary_test.go` | 约束只有 `internal/store/*` 可以导入 `internal/store/sqlc`。 |
| `TestTimeoutLocality` | `internal/archtest/timeout_locality_test.go` | 禁止在白名单外直接使用 `context.WithTimeout`；允许点集中在 `internal/platform/config/timeouts.go` 与 `internal/transport/retry/`。 |

补充说明：

- `TestDependencyDirection` 里的 `rule6_tool_cannot_import_ui_state_directly` 当前会跳过，因为仓库没有 `internal/tool`。
- `TestDependencyDirection` 里的 `rule7_mcpserver_lsp_family`、`rule8_mcpserver_orch_family`、`rule9_mcpserver_ida_family` 当前也会跳过，因为 `internal/mcpserver` 现只有 `common` 子包。
- 因此，`archtest` 的“8 个守卫测试”已经存在，但其中部分规则在当前目录布局下是“已定义、条件生效”，不是所有规则都在当前仓库里实际命中生产代码。

## 3. 模块级单测数量

| 模块 | 生产 Go 文件数 | `_test.go` 文件数 | `func Test*` 数量 | 结论 |
| --- | ---: | ---: | ---: | --- |
| `internal/sidecar/orch/orchestration` | 14 | 3 | 12 | 覆盖面在模块层里最好，已覆盖队列、事件、提交参数、turn 生命周期。 |
| `internal/module/skill` | 12 | 3 | 9 | 已覆盖命令执行、skill FS、匹配预览。 |
| `internal/module/workspace` | 7 | 1 | 4 | 只覆盖 run key 校验与 merge 状态迁移，仍偏薄。 |
| `internal/module/turn` | 11 | 2 | 9 | 已覆盖 prepare、starter、interrupt、force-complete 等主路径。 |
| `internal/module/thread` | 9 | 0 | 0 | 线程生命周期面完全零测试。 |

## 4. provider 测试现状

| provider 包 | 有测试吗 | 生产 Go 文件数 | `_test.go` 文件数 | `func Test*` 数量 | 观察 |
| --- | --- | ---: | ---: | ---: | --- |
| `internal/provider/claudecli` | 有 | 14 | 1 | 2 | 只覆盖 thread ID 解析与等待；没有 session/start/resume/history/config/interrupt 覆盖。 |
| `internal/provider/codexapp` | 没有 | 14 | 0 | 0 | concrete provider 完全零测试。 |
| `internal/provider/unified` | 有 | 7 | 4 | 22 | 覆盖 registry、client、manifest、session interface contract，是 provider 侧最完整的一块。 |

## 5. 契约测试：V2 `schema_contract_test` vs V3

- 以当前 V3 仓库为准，LSP 没有命中 `schema_contract_test`、`SchemaContract` 或同名的独立 schema 契约测试。
- V3 最接近“契约测试”的是 `internal/provider/unified/contract_test.go`：共有 10 个 `TestSessionContract_*`，验证的是 `contract.Session` 接口行为契约，不是 schema 契约。
- V3 当前与 schema 有关的测试只看到两类“字段透传”覆盖：
  - `internal/module/turn/orchestration_starter_test.go` 断言 `OutputSchema` 被带入 turn 启动请求。
  - `internal/sidecar/orch/orchestration/submission_test.go` 断言 `output_schema` / `outputSchema` JSON 入参能被反序列化进 `submitParams`。
- 结论：如果以 V2 的 `schema_contract_test` 为基线，V3 目前没有对应物；现有覆盖只证明接口契约和 schema 字段透传，没有验证 schema 本身的结构约束、兼容性和回归面。

## 6. 测试缺口清单

### P0

1. `internal/module/thread`
   - 9 个生产文件，0 个 `_test.go`，0 个 `func Test*`。
   - 目录已经分出 `archive`, `command`, `history`, `lifecycle`, `rpc`, `service` 等核心面，迁移风险高但完全无单测兜底。
2. `internal/provider/codexapp`
   - 14 个生产文件，0 个 `_test.go`，0 个 `func Test*`。
   - 作为 concrete provider，目前没有 start/resume/transport/history/rollout 任何验证。
3. `internal/store/*`
   - `internal/store` 根包加 20 个子包全部零测试；其中 `internal/store/sqlc` 单包就有 25 个生产 Go 文件。
   - 持久化边界体量最大，但没有仓储层、SQL 映射、状态流转和错误路径覆盖。

### P1

1. `internal/module/workspace`
   - 7 个生产文件，只有 1 个测试文件、4 个测试函数。
   - 现有覆盖集中在 merge 状态迁移；`build run`、workspace/source 文件同步、删除策略、dry-run、冲突细节仍缺测试。
2. `internal/provider/claudecli`
   - 14 个生产文件，只有 1 个测试文件、2 个测试函数。
   - 现在只测 thread ID resolve；主 provider 行为面几乎没有覆盖。
3. `internal/platform/rpc`
   - 15 个生产文件，2 个测试文件、5 个测试函数。
   - 目前只覆盖 approval request ID 去重和 capability resolver；handler 主流程、错误映射、事件桥接仍薄。
4. `internal/contract` + `internal/dto/provider`
   - 分别有 3 个和 8 个生产文件，零测试。
   - 这正是 V2 `schema_contract_test` 在 V3 缺失后最明显的空洞位置。

### P2

1. `internal/app` 与 `cmd/*`
   - 装配入口没有功能回归测试；当前只有 `archtest` 的 `fx.ValidateApp` 守卫，不覆盖业务行为。
2. `internal/mcpserver/common`
   - 3 个生产文件，零测试；作为 MCP 共用层，没有参数/协议/错误路径覆盖。
3. `internal/platform/config`, `internal/platform/db`, `internal/platform/shared`, `internal/platform/runner`, `internal/platform/statemachine`
   - 都是零测试；其中 `shared` 虽有 `archtest` 预算守卫，但没有功能性单测。
4. 已删除的 legacy factory stub 层
   - 删除前只有一个 `TestFactoryPackageExists` 占位测试，现已由平台层承接，不再作为独立包维护。

## 7. 结论

- 当前测试重心明显偏向 `archtest`、`module/orchestration`、`module/skill`、`module/turn`、`provider/unified`。
- 最大的真实缺口不在“有没有守卫”，而在 `thread`、`codexapp`、`store/*`、`workspace`、`claudecli` 这些迁移核心边界的行为测试不足。
- 如果要补测试以支撑迁移验收，建议顺序是：`thread` -> `codexapp` -> `store/sqlc + store/*` -> `workspace` -> `claudecli` -> `contract/dto provider schema`。
