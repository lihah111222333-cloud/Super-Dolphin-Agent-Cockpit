# 架构合规：Import 方向全量扫描

> 2026-04-24 debt banner / authoritative pointer：本页是 **2026-03-21** 的历史扫描记录，不再作为 live debt 的权威基线。P22 umbrella 的 P0/P1a/P1b/P1c/P2/P3/P4 代码主批已收口，`archtest` 持续守卫回归；observability log / metric / trace 锚点的剩余 slice 追踪见 [`docs/plans/迁移/p22/observability-contract.md`](p22/observability-contract.md)。当前依赖方向 / hidden-contract 的 authoritative 入口是 [`docs/plans/迁移/p22/README.md`](p22/README.md) 与 [`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md`](p22/P4_DependencyDirectionAndHiddenContracts.md)；若本页与 `P22/P4` 有冲突，以后者为准，并只在本页同步 debt banner / authoritative pointer，不反向覆盖 active 计划页。

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

- `internal/sidecar/orch/orchestration`
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
| `internal/ui/wails/rpc.go` | 10 | `internal/module/uistate` | Wails RPC handler 直接注入 `uistate.Service`，违反“只经 `rpc.Server.Dispatch` / contract facade”约束 |
| `internal/ui/wails/scope_catalog.go` | 10 | `internal/module/uistate` | Wails scope 解析直接消费 `uistate.ProjectsState`，形成 UI 侧对 owner state shape 的隐藏契约 |

补充说明：

- `internal/ui/wails/module.go` 当前已从直接依赖 orchestration concrete 收敛为依赖 `contract.OrchestrationService`，不再是本规则的包方向违规点。
- `NewActiveAgentCounter` 仍在 UI 侧按 agent state 负面枚举重算 active 语义；这属于 hidden contract 债务，已归入 `docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md`。
- 对 `internal/ui/wails/**/*.go` 做 `text_search("internal/store")` 无命中；当前 import 违规集中在 `module/uistate` 方向。

## 最终结论

- 本文是一次历史扫描记录，不再覆盖全部 live debt；P22/P4 是当前依赖方向与隐藏契约的 authoritative 追踪入口。
- 当前已知 import / hidden-contract 问题至少包括：
  1. `contract/dto` 还没有收敛到“仅标准库”的纯边界层。
  2. `ui/wails` 仍直连 `module/uistate`。
  3. `provider/claudecli` 仍反向依赖 `module/*`。
  4. `platform/toolbridge` 仍直连 provider concrete 与业务 store。
  5. `internal/sidecar/orch/orchestration` 仍暴露 `Module` / `handler.Map` / hidden side-channel contract。
- 其余方向约束的状态以最新 archtest 与 `docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md` 为准，不能再从本文旧扫描结果外推“全仓无违规”。
