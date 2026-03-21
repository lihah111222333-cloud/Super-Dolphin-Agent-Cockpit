# 架构合规：Import 方向全量扫描

审查日期：2026-03-21

## 审查方式

- 只读扫描；未改业务代码。
- 验证工具只使用 LSP：`text_search` 与 `read_file`。
- 包范围按仓内实际目录展开：`internal/contract`、`internal/dto/*`、`internal/module/*`、`internal/store/*`、`internal/platform/*`、`internal/provider/unified`、`internal/ui/wails`。
- import 判定按 `github.com/anthropic-ai/super-agent-v3/internal/...` 实际路径做精确匹配。
- 未使用 `grep/find/cat/sed/awk`。
- 规则 1 按你给定的严格口径执行：`contract/` 与 `dto/` 中任何非标准库 import 都计为违规，包括它们彼此之间或 dto 内部分层复用。

## 总结

- 本次共发现 10 处违规。
- 违规分布：规则 1 有 9 处，规则 6 有 1 处。
- 规则 2、3、4、5 在全包 `**/*.go` 扫描下均为 0 违规。

## 规则 1：`contract/ dto/` 零业务依赖（只允许标准库）

扫描包：

- `internal/contract`
- `internal/dto/agent`
- `internal/dto/provider`
- `internal/dto/shared`
- `internal/dto/task`
- `internal/dto/tool`
- `internal/dto/turn`
- `internal/dto/ui`
- `internal/dto/workspace`

违规清单：

| 文件 | 行 | import | 说明 |
| --- | ---: | --- | --- |
| `internal/contract/provider.go` | 6 | `internal/dto/provider` | `contract/` 出现非标准库依赖 |
| `internal/dto/agent/event.go` | 3 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/provider/turn.go` | 6 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/task/event.go` | 3 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/tool/event.go` | 3 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/turn/event.go` | 3 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/turn/model.go` | 6 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/ui/event.go` | 3 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |
| `internal/dto/workspace/event.go` | 3 | `internal/dto/shared` | `dto/` 出现非标准库依赖 |

备注：

- `internal/contract` 下其余文件只使用标准库。
- `internal/dto` 下未发现第三方依赖；违规全部来自 dto 内部对 `shared` 的复用。

## 规则 2：`module/*` 禁 import `provider/*`（除窄接口）

扫描包：

- `internal/module/orchestration`
- `internal/module/skill`
- `internal/module/thread`
- `internal/module/turn`
- `internal/module/workspace`

结果：

- 0 违规。

证据口径：

- 对 `internal/module/**/*.go` 做 `text_search("internal/provider")`，无命中。
- `module` 当前通过窄接口 `internal/contract` 接触 provider 能力，例如 `internal/module/turn/contract.go:7`、`internal/module/thread/command.go:10`、`internal/module/thread/lifecycle.go:10`、`internal/module/thread/service.go:11`。

## 规则 3：`store/*` 禁 import `module/ provider/ platform/rpc`

扫描包：

- `internal/store` 根包
- `internal/store/agentstatus`
- `internal/store/ailog`
- `internal/store/auditlog`
- `internal/store/binding`
- `internal/store/buslog`
- `internal/store/commandcard`
- `internal/store/cwdlock`
- `internal/store/dbquery`
- `internal/store/interaction`
- `internal/store/prompt`
- `internal/store/sharedfile`
- `internal/store/sqlc`
- `internal/store/systemlog`
- `internal/store/taskack`
- `internal/store/taskdag`
- `internal/store/tasktrace`
- `internal/store/thread`
- `internal/store/topologyapproval`
- `internal/store/uipreference`
- `internal/store/workspace`

结果：

- 0 违规。

证据口径：

- 对 `internal/store/**/*.go` 分别做 `text_search("internal/module")`、`text_search("internal/provider")`、`text_search("internal/platform/rpc")`，均无命中。
- 已确认 store 侧保留的基础设施依赖仍主要是 `internal/platform/db` 与 `internal/store/sqlc`，不在本规则禁区内。

## 规则 4：`platform/*` 禁 import `module/`（`rpc` 可 import `platform/bus`）

扫描包：

- `internal/platform/bus`
- `internal/platform/config`
- `internal/platform/db`
- `internal/platform/rpc`
- `internal/platform/runner`
- `internal/platform/shared`
- `internal/platform/statemachine`

结果：

- 0 违规。

证据口径：

- 对 `internal/platform/**/*.go` 做 `text_search("internal/module")`，无命中。
- 允许项已看到：`internal/platform/rpc/push.go:13` import `internal/platform/bus`，符合例外说明。

## 规则 5：`provider/unified` 禁 import 具体 driver（`claudecli` / `codexapp`）

扫描包：

- `internal/provider/unified`

结果：

- 0 违规。

证据口径：

- 对 `internal/provider/unified/**/*.go` 做 `text_search("internal/provider/claudecli")` 与 `text_search("internal/provider/codexapp")`，均无命中。
- `unified` 当前依赖的是 `internal/contract` 等抽象层，而不是具体 driver 实现。

## 规则 6：`ui/wails` 禁 import `store/ module/`（只通过 `rpc.Server.Dispatch`）

扫描包：

- `internal/ui/wails`

违规清单：

| 文件 | 行 | import | 说明 |
| --- | ---: | --- | --- |
| `internal/ui/wails/module.go` | 9 | `internal/module/orchestration` | `ui/wails` 直接依赖 `module/`，违反“只经 `rpc.Server.Dispatch`”约束 |

补充说明：

- `internal/ui/wails/module.go:30-33` 已通过 `server.Dispatch` 注入 `App.dispatch`，这条链路是合规的。
- 但同文件 `NewActiveAgentCounter` 仍直接接收 `orchestration.Service` 并调用 `ListAgents`（`internal/ui/wails/module.go:41-58`），因此 import 不是“未使用”，而是实质性跨层调用。
- 对 `internal/ui/wails/**/*.go` 做 `text_search("internal/store")` 无命中；当前违规只在 `module/` 方向。

## 最终结论

- 当前 import 方向的主要问题集中在两处：
  1. `contract/dto` 还没有收敛到“仅标准库”的纯边界层。
  2. `ui/wails` 仍有一条直连 `module/orchestration` 的跨层依赖。
- 其余四条方向约束，在本次 LSP 全包扫描下未发现违规点。
