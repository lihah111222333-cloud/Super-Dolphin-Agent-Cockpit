# V3 代码守卫规格

> 基线来源：
> - `go-agent-v2/scripts/code_size_guard.go`
> - `go-agent-v2/internal/guards/guardlib/guardlib.go`
> - `go-agent-v2/internal/guards/guardlib/package_lines.go`
> - `go-agent-v2/internal/guards/package_lines_guard_test.go`
> - `go-agent-v2/internal/guards/guard_manifest_contract_guard_test.go`
> - `go-agent-v2/internal/guards/store_manifest_guard_test.go`

V3 直接继承 V2 的测量口径，不重新定义统计语义：
- 文件行数和函数行数都按“有效行”计算，忽略空行、`//` 注释和 `/* ... */` 注释。
- 嵌套深度只统计 `if`、`for`、`range`、`switch`、`type-switch`、`select`。
- 圈复杂度按 `if`、`for`、`range`、`case`、`comm`、`&&`、`||` 计算。
- V2 的 `scripts/code_size_guard.go` 负责文件/函数/嵌套/复杂度/命名/包文件数/死键；包有效行数守卫独立落在 `guardlib/package_lines.go` + `internal/guards/package_lines_guard_test.go`。
- V2 的文件、函数、嵌套、复杂度 allowlist 都是“只减不增”；脚本会在检测到缩减后自动回写并删除过期死键。

## 第 1 章：V3 守卫常量

| 守卫维度 | V2 上限 | V3 上限 | 收紧理由 |
|---|---:|---:|---|
| 单文件有效行数 | 500 | 400 | V3 从零开始建模块，单文件不再为历史兼容承担聚合职责。 |
| 单函数有效行数 | 150 | 80 | V3 已把装配、协议、状态机、SQL 样板拆给框架，函数长度必须明显下降。 |
| 嵌套深度 | 5 | 4 | 进一步压低认知复杂度，迫使早返回和子函数提取。 |
| 圈复杂度 | 13 | 10 | V3 的分支密度必须低于 V2，复杂分支应改成表驱动或状态机。 |
| 标识符下划线数 | 3 | 3 | 该规则已足够严格，继续收紧只会制造无意义重命名。 |
| 包内文件数 | 仅对少数目录冻结上限 | 默认 15 | 与仓库当前 guardlib 常量一致；V3 仍保留默认硬限，防止包膨胀。 |
| 包有效行数 | 冻结基线，无统一硬限 | 默认 3000 | V2 通过 `refactor_baseline.json` 冻结包行数；V3 改成默认包预算，不再接受无限增长。 |

补充说明：
- V2 的包有效行数守卫不是脚本常量，而是 `guardlib/package_lines.go` 中的冻结基线机制：总产品代码、factory 预算、单包 `FrozenMax` 都只允许下降。
- V3 取消 `factory` 特殊预算，改为显式包预算和 `platform/shared` 目录预算。
- V3 的 `3000` 是默认包有效行数上限；只有迁移主文档明确标注的高复杂边界包，才允许在子文档中声明 `3200-3500` 的迁移期显式例外，并且必须配套更早的拆分触发阈值和架构测试。
- 当前明确允许突破默认 `3000` 的迁移期例外只有 `module/orchestration`、`module/ida`、`tool/lsp`。

### 1.1 核心包放宽守卫

以下 6 个核心包因功能密度高、跨 Phase 持续增长，适用放宽后的守卫上限：

| 适用包 | 包文件数 | 单文件有效行数 | 包有效行数 | 函数/嵌套/CC |
|--------|------:|------:|------:|---|
| `module/memory` | 30 | 600 | 10000 | 不变（80/4/10） |
| `module/prompt` | 30 | 600 | 10000 | 不变 |
| `module/thread` | 30 | 600 | 10000 | 不变 |
| `module/turn`   | 30 | 600 | 10000 | 不变 |
| `provider/claudecli` | 30 | 600 | 10000 | 不变 |
| `provider/codexapp`  | 30 | 600 | 10000 | 不变 |

> 其他包仍遵守默认守卫（文件 ≤400、包文件数 ≤15、包行数 ≤3000）。

## 第 2 章：V3 守卫类型完整清单

### 2.1 继承自 V2 的守卫

| 守卫 | V2 对应 | 规则 |
|---|---|---|
| 文件行数守卫 | `ViolationFile` | 单个 `.go` 文件按有效行计数，超过包级预算即失败。 |
| 函数行数守卫 | `ViolationFunc` | 非测试函数/方法超过函数预算即失败。 |
| 嵌套深度守卫 | `ViolationNesting` | 控制流嵌套深度超过阈值即失败。 |
| 圈复杂度守卫 | `ViolationCC` | 函数圈复杂度超过阈值即失败。 |
| 标识符规范守卫 | `ViolationIdentifier` | 标识符下划线数量超过 3 即失败。 |
| 包文件数守卫 | `ViolationPackageCount` | 单包 `.go` 文件数量超过预算即失败。 |
| 包行数守卫 | `ViolationPackageLines` | 单包有效行数超过预算或超出冻结基线即失败。 |
| 死键守卫 | `ViolationDeadKey` | allowlist/冻结项找不到真实文件或函数即失败。 |

### 2.2 V3 新增守卫

| 守卫 | 规则 | 建议落点 |
|---|---|---|
| `fx` import 范围守卫 | `fx` 只允许出现在 `internal/app`、`cmd/*`、`module.go` 以及装配入口，不允许出现在业务实现文件。 | `internal/archtest/fx_graph_test.go` |
| `sqlc` import 边界守卫 | `internal/store/sqlc/` 或 `sqlcgen` 只允许被 `store/*` import，生成目录视为只读。 | `internal/archtest/sqlc_boundary_test.go` |
| `jrpc2` handler 严格模式守卫 | 所有公共方法必须使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`。 | `platform/rpc` 本地测试 + `internal/archtest/dependency_direction_test.go` |
| `stateless` 业务枚举隔离守卫 | `platform/statemachine/` 只承接技术骨架，禁止 import `module/*` 或业务枚举。 | `internal/archtest/dependency_direction_test.go` |
| `platform/shared` 预算守卫 | `internal/platform/shared/` 总行数 `<= 2000`，单文件 `<= 500`。 | `internal/archtest/shared_budget_test.go` |
| 依赖方向守卫 | 按主文档 §2.5 的 11 条 import 规则校验所有边界。 | `internal/archtest/dependency_direction_test.go` |
| MCP 家族交叉 import 守卫 | `cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 互不 import；`internal/mcpserver/common` 作为共享协议层不计入家族身份。 | `internal/archtest/mcp_family_isolation_test.go` |
| `context.WithTimeout` 散落守卫 | 只有 `internal/platform/config/timeouts.go` 可以直接出现 `context.WithTimeout`。 | `internal/archtest/timeout_locality_test.go` |

### 2.3 依赖方向 11 条规则

1. `internal/contract` 和 `internal/dto` 不能 import `fx`、`jrpc2`、`pgx`、`wails`。
2. `internal/module/*` 不能在业务实现里 import `fx`；`fx.Module` 只允许出现在 `module.go`。
3. `internal/provider/claudecli` 和 `internal/provider/codexapp` 不能直接 import `internal/store/*`。
4. `internal/platform/*` 不能 import `internal/module/*`。
5. `internal/store/*` 只能依赖 `internal/platform/db`、`internal/store/sqlcgen`、`internal/contract`、`internal/dto`。
6. `internal/tool/*` 不能直接改 UI state；只能通过 typed event 或 `module/*` facade。
7. `cmd/mcp-lsp` 不能 import `internal/tool/ida` 或 `internal/tool/orchestration`，也不能 import `internal/app`、`internal/ui/*`。
8. `cmd/mcp-orch` 不能 import `internal/tool/lsp` 或 `internal/tool/ida`，也不能 import `internal/app`、`internal/ui/*`。
9. `cmd/mcp-ida` 不能 import `internal/tool/lsp` 或 `internal/tool/orchestration`，也不能 import `internal/app`、`internal/ui/*`。
10. 只有 `internal/app`、`internal/platform/*/module.go`、`internal/store/*/module.go`、`internal/module/*/module.go` 和 `cmd/*` 可以 import `fx` 模块清单。
11. 所有 timeout 常量统一定义在 `internal/platform/config/timeouts.go`。

补充口径：

- `cmd/mcp-*` 不得 import 其他 `cmd/*` 下的代码；MCP binary 之间只允许通过进程协议协作，不允许源码级复用。
- `internal/module/*` 不得 import `cmd/mcp-*`；核心层只能被 MCP binary 下游复用，不能反向依赖入口层。
- MCP schema、manifest 组装和 handler 壳只允许出现在 `cmd/mcp-*`；核心层不得承载这些协议面定义。

## 第 3 章：V3 专项行为守卫清单

V2 的 D1-D7 维度已在 `guard_manifest_contract_guard_test.go` 中冻结为正式枚举；`store_manifest_guard_test.go` 证明 V2 已把 store 守卫显式映射到这些维度。V3 继续保留该维度体系，但把落点换成新的边界。

| V2 守卫维度 | V3 等价守卫 | 归属模块 |
|---|---|---|
| D1_response | RPC golden response contract | `platform/rpc` + `module/*/rpc.go` |
| D2_side_effect | Store 副作用守卫 | `store/*` |
| D3_state_machine | `stateless` 全矩阵守卫 | `module/orchestration` |
| D4_event_mapping | typed event 路由守卫 | `platform/bus` + `module/uistate` |
| D5_protocol | `jrpc2` schema contract | `platform/rpc` |
| D6_concurrency | 并发安全守卫 | `store/*` + `module/orchestration` |
| D7_error_path | 错误路径守卫 | `store/*` + `module/turn` |

补充要求：
- `module/ida`、`tool/lsp`、`cmd/mcp-*` 仍需各自保留 D4/D5/D6/D7 本地守卫，但不再复制另一套维度命名。
- `platform/bus` 和 `provider/*` 的事件守卫必须锁定 route、payload shape 和订阅取消行为，不能只测 happy path。
- `module/workspace`、`module/dashboard`、`tool/orchestration` 这类 facade 层必须保留响应 shape 与错误 envelope 守卫，避免接口漂移。

## 第 4 章：V3 `internal/archtest/` 测试清单

`internal/archtest/` 只承接跨包、静态、结构性守卫。业务语义的 D1-D7 测试仍留在各自模块/包内。

```text
internal/archtest/
├── dependency_direction_test.go    — 依赖方向 11 条规则
├── fx_graph_test.go                — fx.ValidateApp + fx import 范围
├── shared_budget_test.go           — platform/shared 行数预算
├── sqlc_boundary_test.go           — sqlc import 边界
├── mcp_family_isolation_test.go    — `cmd/mcp-*` 家族交叉 import
├── timeout_locality_test.go        — context.WithTimeout 散落
├── code_size_guard_test.go         — 文件/函数/嵌套/CC 守卫
└── identifier_guard_test.go        — 标识符规范
```

实现说明：
- `code_size_guard_test.go` 除文件/函数/嵌套/复杂度外，还应顺带检查默认包文件数预算和“默认 allowlist 为空”。
- `fx_graph_test.go` 同时承担 `fx.ValidateApp` 图校验和 `fx` import 范围校验，不再拆出第二套名称接近的测试文件。
- `dependency_direction_test.go` 负责规则 1-10；`timeout_locality_test.go` 单独负责规则 11，避免把时间预算问题埋进通用 import 扫描逻辑。
- `shared_budget_test.go` 是 `platform/shared` 的特例守卫，不能并入通用 `code_size_guard_test.go`，否则目录总预算会被稀释。

## 第 5 章：Allowlist 策略

- V3 原则上不设 allowlist。默认状态下，`fileAllowlist`、`funcAllowlist`、`nestingAllowlist`、`complexityAllowlist` 都应为空。
- 如果确实存在必须超标的文件或函数，例如协议镜像或外部接口兼容层，必须在 PR 中写明超标原因、当前冻结值、拆分计划和审批人。
- allowlist 只允许冻结当前值，只减不增；任何上调都视为新增技术债，直接拒绝。
- allowlist 一旦存在，死键守卫必须开启；找不到真实文件或函数的冻结项一律失败。
- 业务包不得用 allowlist 规避拆分；迁移期例外只接受协议表、生成适配层或极少数必须保形的兼容文件。
- `cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 与 `internal/mcpserver/common` 默认纳入同一组守卫；新写 hand-written 代码必须直接满足单文件 `<=400`、函数 `<=80`、CC `<=10`、包非测试文件 `<=15`。
- 从 V2 直接复制到 MCP 服务的协议镜像 / 兼容文件允许迁移期临时豁免，但必须显式写入 allowlist、冻结当前值、标注来源文件与删除条件；任何新逻辑不得继续堆进豁免文件。
