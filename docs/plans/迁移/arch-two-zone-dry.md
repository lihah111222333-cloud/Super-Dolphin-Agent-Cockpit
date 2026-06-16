# Two-Zone DRY 架构验证

## 范围与方法

本次验证仅使用 LSP 能力完成：

- `lsp_xref.references`：统计真实符号消费点
- `lsp_grep.text_search`：搜索重复 handler 注册模式
- `lsp_file.read_file`：核对三件套文件存在性与定义位置

验证范围：

1. Zone A `platform/` 工厂复用度
2. Zone B `module/` 三件套完整性
3. 相似 handler 注册模式是否已抽工厂
4. card handler 工厂覆盖度
5. thread 命令工厂覆盖度
6. `store/` 三件套完整性

## 1. Zone A `platform/` 工厂复用度

### 结论

只有 `HandlerMapResult` 明确达到“被 >=3 个模块复用”。

`ResilientSubscribe[T]` 达到 3 个消费包，但其中只有 1 个是 `internal/module/` 业务模块。

`Route[T]` 和 `Projector` 都没有达到 >=3 个模块复用。

### 明细

| 构件 | 定义位置 | LSP 真实消费点 | 复用结论 |
| --- | --- | --- | --- |
| `HandlerMapResult` | `internal/platform/rpc/module.go:36` | `internal/sidecar/orch/orchestration/rpc.go:15,16`；`internal/module/skill/rpc.go:42,43`；`internal/module/thread/rpc.go:19,20`；`internal/module/turn/rpc.go:19,32`；`internal/module/workspace/rpc.go:13,14` | 5 个业务模块复用，满足 >=3 |
| `ResilientSubscribe[T]` | `internal/platform/bus/resilient.go:10` | `internal/sidecar/orch/orchestration/module.go:33,39`；`internal/platform/rpc/push.go:82,85,88`；`internal/ui/wails/bridge.go:54,57,60` | 3 个消费包，满足“包级复用”；若只按 `internal/module/` 业务模块计数，则仅 orchestration 1 个，不满足 |
| `Route[T]` | `internal/platform/bus/router.go:18` | `internal/platform/bus/projection.go:42` | 仅被 `Projector.Bind` 复用 1 次，不满足 |
| `Projector[S,E]` | `internal/platform/bus/projection.go:10` | 无外部 `references`；`NewProjector` 也无外部 `references` | 0 个外部消费者，不满足 |

### 判断

- `HandlerMapResult` 是当前 Zone A 里最成熟的共享工厂，已经稳定服务 5 个 `internal/module/*` 模块。
- `ResilientSubscribe[T]` 已经有横向复用，但主要发生在事件桥接层，不是业务模块层面的广泛复用。
- `Route[T]` / `Projector` 目前更像“预留抽象”而非已被验证的通用工厂。

## 2. Zone B `module/` 三件套

### 结论

`internal/module/` 下现有 5 个模块全部具备 `module.go + contract.go + service.go`，这一项通过。

### 明细

| 模块 | module.go | contract.go | service.go | 结论 |
| --- | --- | --- | --- | --- |
| orchestration | `internal/sidecar/orch/orchestration/module.go:1` | `internal/sidecar/orch/orchestration/contract.go:1` | `internal/sidecar/orch/orchestration/service.go:1` | 通过 |
| skill | `internal/module/skill/module.go:1` | `internal/module/skill/contract.go:1` | `internal/module/skill/service.go:1` | 通过 |
| thread | `internal/module/thread/module.go:1` | `internal/module/thread/contract.go:1` | `internal/module/thread/service.go:1` | 通过 |
| turn | `internal/module/turn/module.go:1` | `internal/module/turn/contract.go:1` | `internal/module/turn/service.go:1` | 通过 |
| workspace | `internal/module/workspace/module.go:1` | `internal/module/workspace/contract.go:1` | `internal/module/workspace/service.go:1` | 通过 |

## 3. 相似代码检测：handler 注册模式是否已抽成工厂

### 结论

整体是“部分抽工厂，未完全 DRY”。

- `thread` 抽象程度最高，已有明显局部工厂。
- `skill` 有局部工厂，但覆盖不完整。
- `workspace` 把每个 route 的 closure 提前命名了，但更像“拆分可读性”，不是高复用工厂。
- `orchestration` / `turn` 仍保留较多 inline handler 注册，重复 shape 还没有上提。

### LSP 证据

#### 3.1 `orchestration` 仍有成组重复的 inline `StrictHandler`

`lsp_grep` 对 `p agentIDParams` 的命中有 4 处：

- `internal/sidecar/orch/orchestration/rpc.go:40` `agent.stop`
- `internal/sidecar/orch/orchestration/rpc.go:46` `agent.snapshot`
- `internal/sidecar/orch/orchestration/rpc.go:49` `agent.getState`
- `internal/sidecar/orch/orchestration/rpc.go:52` `agent.getReport`

这些路由的形态都接近“取单个 ID 参数，然后直接转调 `svc`”，但当前没有抽成统一 helper。

#### 3.2 零参数 list 型 handler 跨模块重复出现

`lsp_grep` 对 `_ struct{}` 的命中有 5 处：

- `internal/sidecar/orch/orchestration/rpc.go:43`
- `internal/module/skill/rpc.go:44`
- `internal/module/skill/rpc.go:56`
- `internal/module/thread/rpc.go:42`
- `internal/module/thread/rpc.go:45`

说明“零参数只读路由”在多个模块重复存在，但没有公共工厂。

#### 3.3 `thread` 已抽出一组有效工厂

- `newThreadCall` 定义于 `internal/module/thread/rpc.go:99`，LSP `references` 命中 5 处：`34`、`49`、`84`、`106`、`182`
- `newThreadEffect` 定义于 `internal/module/thread/rpc.go:105`，LSP `references` 命中 4 处：`37`、`38`、`39`、`40`
- `newThreadCommandHandler` 定义于 `internal/module/thread/rpc.go:113`，LSP `references` 命中 8 处：`61`、`64`、`66`、`69`、`71`、`73`、`75`、`79`
- `newCapabilityThreadCommandHandler` 定义于 `internal/module/thread/rpc.go:125`，LSP `references` 命中 4 处：`89`、`91`、`93`、`95`

`thread` 是当前 handler DRY 最好的样板。

#### 3.4 `skill` 也有局部工厂，但覆盖面有限

- `cardByKeyHandler` 定义于 `internal/module/skill/rpc.go:12`，LSP `references` 命中 3 处：`45`、`48`、`50`
- `cardCreateHandler` 命中 1 处：`46`
- `cardUpdateHandler` 命中 1 处：`47`
- `cardRunHandler` 命中 1 处：`49`
- `namedContentHandler` 命中 3 处：`69`、`73`、`78`

这里已经开始抽 shape，但还没有把 card 族路由统一到单一工厂。

#### 3.5 `workspace` 更偏“命名化 closure”，不是共享工厂

`internal/module/workspace/rpc.go:15-21` 的 7 个路由全部转向 `handleCreateRun`、`handleGetRun`、`handleListRuns` 等命名函数。

这解决了 inline closure 过长问题，但每个 helper 只服务 1 个路由，不构成强复用工厂。

## 4. card handler 工厂覆盖度

### 结论

`cardByKeyHandler` 不是 `7/7`，而是 `3/7`。

### 证据

`lsp_grep` 对 `"command/card/` 的命中共 7 条，分别位于 `internal/module/skill/rpc.go:44-50`：

- `command/card/list`
- `command/card/get`
- `command/card/create`
- `command/card/update`
- `command/card/delete`
- `command/card/run`
- `command/card/versions`

`cardByKeyHandler` 的 LSP `references` 只有 3 处：

- `internal/module/skill/rpc.go:45` `command/card/get`
- `internal/module/skill/rpc.go:48` `command/card/delete`
- `internal/module/skill/rpc.go:50` `command/card/versions`

剩余 4 条路由的情况：

- `command/card/create` 使用 `cardCreateHandler`
- `command/card/update` 使用 `cardUpdateHandler`
- `command/card/run` 使用 `cardRunHandler`
- `command/card/list` 直接 inline `rpc.StrictHandler`

### 判断

- `cardByKeyHandler` 覆盖度 = `3/7`
- card 族路由存在局部抽象，但没有形成“单一 card handler 工厂”

## 5. `cmd/capCmd` 工厂：thread 模块覆盖度

### 结论

代码里已经没有字面量 `cmd` / `capCmd` helper；当前对应实现是：

- `newThreadCommandHandler`
- `newCapabilityThreadCommandHandler`

如果按“所有 `SendCommand` 路由”计，当前工厂覆盖度是 `12/15`。

如果按“只统计通用 string-args 命令壳”计，覆盖度是 `12/12`。

### 证据

`thread` 模块里实际发送命令的 helper 定义都在 `internal/module/thread/rpc.go`：

- `newThreadCommandHandler` at `113`
- `newCapabilityThreadCommandHandler` at `125`
- `newThreadConfigGetHandler` at `119`
- `newModelSetHandler` at `136`
- `newCompactStartHandler` at `146`

LSP `references` 计数：

- `newThreadCommandHandler` = 8 处：`61`、`64`、`66`、`69`、`71`、`73`、`75`、`79`
- `newCapabilityThreadCommandHandler` = 4 处：`89`、`91`、`93`、`95`
- `newThreadConfigGetHandler` = 1 处：`59`
- `newModelSetHandler` = 1 处：`62`
- `newCompactStartHandler` = 1 处：`67`

所以：

- 泛化命令工厂覆盖的 route 数 = `8 + 4 = 12`
- `SendCommand` 背后的总 route 数 = `12 + 1 + 1 + 1 = 15`
- 覆盖度 = `12/15 = 80%`

### 未纳入通用工厂的 3 条 route

- `thread/config/get` -> `newThreadConfigGetHandler`
- `thread/model/set` -> `newModelSetHandler`
- `thread/compact/start` -> `newCompactStartHandler`

这 3 条之所以单独存在，不是漏抽，而是因为它们都带有额外语义：

- `config/get` 是零参数命令
- `model/set` 需要做参数冲突归一
- `compact/start` 需要 capability gate，但参数 shape 不是纯 string 壳

### 判断

- `thread` 命令工厂已经是“部分统一，且主干统一”
- 但它还不是所有命令路由都能套进去的单一 mega-factory

## 6. `store/` 三件套：19 个 repo 是否齐全

### 结论

通过。

`internal/store/` 下 19 个 repo 目录全部具备 `module.go + contract.go + store.go`。

本次计数排除：

- 聚合根文件 `internal/store/module.go`
- 生成目录 `internal/store/sqlc/`

### 19 个 repo

| repo | module.go | contract.go | store.go | 结论 |
| --- | --- | --- | --- | --- |
| agentstatus | `internal/store/agentstatus/module.go:1` | `internal/store/agentstatus/contract.go:1` | `internal/store/agentstatus/store.go:1` | 通过 |
| ailog | `internal/store/ailog/module.go:1` | `internal/store/ailog/contract.go:1` | `internal/store/ailog/store.go:1` | 通过 |
| auditlog | `internal/store/auditlog/module.go:1` | `internal/store/auditlog/contract.go:1` | `internal/store/auditlog/store.go:1` | 通过 |
| binding | `internal/store/binding/module.go:1` | `internal/store/binding/contract.go:1` | `internal/store/binding/store.go:1` | 通过 |
| buslog | `internal/store/buslog/module.go:1` | `internal/store/buslog/contract.go:1` | `internal/store/buslog/store.go:1` | 通过 |
| commandcard | `internal/store/commandcard/module.go:1` | `internal/store/commandcard/contract.go:1` | `internal/store/commandcard/store.go:1` | 通过 |
| cwdlock | `internal/store/cwdlock/module.go:1` | `internal/store/cwdlock/contract.go:1` | `internal/store/cwdlock/store.go:1` | 通过 |
| dbquery | `internal/store/dbquery/module.go:1` | `internal/store/dbquery/contract.go:1` | `internal/store/dbquery/store.go:1` | 通过 |
| interaction | `internal/store/interaction/module.go:1` | `internal/store/interaction/contract.go:1` | `internal/store/interaction/store.go:1` | 通过 |
| prompt | `internal/store/prompt/module.go:1` | `internal/store/prompt/contract.go:1` | `internal/store/prompt/store.go:1` | 通过 |
| sharedfile | `internal/store/sharedfile/module.go:1` | `internal/store/sharedfile/contract.go:1` | `internal/store/sharedfile/store.go:1` | 通过 |
| systemlog | `internal/store/systemlog/module.go:1` | `internal/store/systemlog/contract.go:1` | `internal/store/systemlog/store.go:1` | 通过 |
| taskack | `internal/store/taskack/module.go:1` | `internal/store/taskack/contract.go:1` | `internal/store/taskack/store.go:1` | 通过 |
| taskdag | `internal/store/taskdag/module.go:1` | `internal/store/taskdag/contract.go:1` | `internal/store/taskdag/store.go:1` | 通过 |
| tasktrace | `internal/store/tasktrace/module.go:1` | `internal/store/tasktrace/contract.go:1` | `internal/store/tasktrace/store.go:1` | 通过 |
| thread | `internal/store/thread/module.go:1` | `internal/store/thread/contract.go:1` | `internal/store/thread/store.go:1` | 通过 |
| topologyapproval | `internal/store/topologyapproval/module.go:1` | `internal/store/topologyapproval/contract.go:1` | `internal/store/topologyapproval/store.go:1` | 通过 |
| uipreference | `internal/store/uipreference/module.go:1` | `internal/store/uipreference/contract.go:1` | `internal/store/uipreference/store.go:1` | 通过 |
| workspace | `internal/store/workspace/module.go:1` | `internal/store/workspace/contract.go:1` | `internal/store/workspace/store.go:1` | 通过 |

## 总结

### 通过项

- Zone B `internal/module/*` 三件套：`5/5`
- `store` 三件套：`19/19`
- Zone A 中 `HandlerMapResult`：`5` 个业务模块复用

### 部分通过项

- `ResilientSubscribe[T]`：3 个消费包复用，但业务模块复用面仍窄
- thread 命令工厂：主干已统一，但不是全量命令路由统一
- handler 注册去重：局部做得不错，跨模块仍未完全 DRY

### 未通过项

- `Route[T]` 未达到 >=3 个模块复用
- `Projector` 没有外部真实消费者
- `cardByKeyHandler` 不是 `7/7`，而是 `3/7`

