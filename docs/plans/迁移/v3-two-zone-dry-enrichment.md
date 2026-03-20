# V3 Two-Zone DRY 丰满方案

> 约束回放：
> - Zone A：跨模块共享，承载在 `internal/platform/*` 与框架层；`internal/platform/shared/` 只收“Rule of Two + 预算内”的纯 helper。
> - Zone B：模块内 DRY，承载在 `internal/module/*` 自治模块模式。
> - 预算：`internal/platform/shared/` 单文件 `<=500` 行，目录总量 `<=2000` 行。

## 1. Zone A 完整清单

`internal/platform/shared/` 只应该放“纯技术型、无业务语义、被 2 个以上模块稳定复用”的 helper。推荐首版清单如下。

| 文件 | 职责 | 行数预算 | 进入条件 |
| --- | --- | ---: | --- |
| `retry.go` | 退避、限次重试、瞬态错误判断 | 180 | orchestration / ida / codexapp 至少 2 处稳定复用 |
| `validation.go` | 空值、枚举、limit、cursor、path 参数基础校验 | 220 | RPC/tool/store 都复用的纯校验 |
| `idgen.go` | 短 ID、trace ID、随机 key 生成 | 80 | 不引入业务前缀 |
| `pathscope.go` | 路径归一化、root containment、scope CWD 解析 | 220 | workspace / dashboard / coderun / ida 共用 |
| `fileops.go` | 原子写、哈希、目录 walk 安全封装 | 260 | workspace 与 ida 或 dashboard 至少双处使用 |
| `jsonutil.go` | `json.RawMessage` clone、map clone、lossless normalize | 140 | dashboard / uistate / registry 复用 |
| `cursor.go` | 通用分页 cursor encode/decode、limit clamp | 150 | dashboard / store / tool 复用 |
| `truncate.go` | 输出、审计 payload、预览文本截断 | 120 | coderun / ida / dashboard 复用 |
| `hash.go` | 轻量 hash、内容摘要、版本指纹 | 80 | store/workspace/tool 共用 |
| `errors.go` | 纯错误包装辅助，不含业务码表 | 120 | 仅补 stdlib 缺口，不替代模块错误定义 |

预算合计：`1570` 行。保留机动空间：`430` 行。

不应进入 `platform/shared/` 的内容：
- `jrpc2` middleware、request context、validated binder。这些属于 `platform/rpc`。
- `pgx` 事务、`sqlc` `Querier` 封装。这些属于 `platform/db`。
- typed event 定义和订阅桥接。这些属于 `platform/bus`。
- 状态机 builder、graph export、matrix harness。这些属于 `platform/statemachine`。
- tool schema、availability、family compose。这些属于 `tool/registry`。

`pkg/factory/` 的去向：
- `handler.go` 被 `platform/rpc` 吸收。
- `fsm.go` 被 `platform/statemachine` 吸收。
- `schema.go` 被 `tool/registry` 吸收。
- `retry.go` 若满足复用门槛，迁到 `platform/shared/retry.go`。
- `module/core` / `module/debug` 在 V3 中已废弃；initialize/approval/log/debug 职责分别并入 `platform/rpc`、`module/turn`、`platform/bus` 等已有边界。

## 2. Zone A 框架承接映射

V2 的 Zone A 不是简单搬目录，而是被框架和平台层重新承接。

| V2 Zone A 项 | V3 承接方式 | 类型 |
| --- | --- | --- |
| `pkg/factory/handler.go` | `jrpc2 handler.New` + `handler.Map` + `platform/rpc/middleware.go` | 框架直接替代 |
| `withRequiredThreadID(...)` | typed request `Validate()` + `platform/rpc` validated binder | 框架 + helper |
| `capabilityGuard(...)` | `platform/rpc` middleware + provider capability error builder | helper |
| `pkg/factory/fsm.go` | `stateless` + `platform/statemachine/factory.go` | 框架直接替代 |
| `effectiveState()` 二次投影 | 单一主状态机 + `uistate`/dashboard 只读 projection | 架构替代 |
| `SafeGo` / 手写 supervisor | `platform/runner` + `run.Group` | 框架直接替代 |
| `BaseStore` + 重复 tx 模式 | `sqlc` + `platform/db/tx.go` + `Queries.WithTx` | 框架 + helper |
| `pkg/factory/schema.go` | `tool/registry/schemas.go` | 平台模块承接 |
| 字符串 topic + `map[string]any` bus | `kelindar/event` typed event + `platform/bus` | 框架直接替代 |
| 零散 nil-guard 构造 | `fx` object graph + `fx.ValidateApp` | 框架直接替代 |

仍需手写 helper 的区域：
- path/root containment：框架不解决，需要 `platform/shared/pathscope.go`
- 输出截断、审计截断：框架不解决，需要 `platform/shared/truncate.go`
- backoff/retry：`run.Group` 只负责生命周期，不负责策略，需要 `platform/shared/retry.go`
- file atomic write/hash：框架不解决，需要 `platform/shared/fileops.go` / `hash.go`

不该手写、要强制交给框架的区域：
- DI 构造和生命周期。
- RPC 参数绑定和统一注册。
- 主状态机迁移表。
- actor 宿主与一停全停语义。
- 静态 SQL 生成和事务 querier 切换。
- 进程内 typed event 分发。

## 3. Zone B 模块自治模式

### 3.1 标准文件结构

所有 `internal/module/*` 统一采用以下骨架。

```text
internal/module/<name>/
  module.go
  contract.go
  service.go
  rpc.go           # 仅当该模块暴露 jrpc2 handler
  events.go        # 仅当该模块发布/订阅 typed event
  helpers.go       # 仅当模块内重复模式 >= 2 处
  patterns.go      # 仅当模块有内部策略矩阵/构造模板
```

规则：
- `module.go`：只出现 `fx.Module`、`fx.Provide`、`fx.Invoke`、`fx.Annotate`。
- `contract.go`：只放对外接口、输入输出 DTO、模块内窄接口。
- `service.go`：只放主业务服务，不承接 RPC transport 细节。
- `rpc.go`：只装配 `handler.Map` 片段，不写业务。
- `events.go`：只定义模块事件桥和订阅绑定。
- `helpers.go` / `patterns.go`：只做模块内收敛，不作为上提前站。

### 3.2 需要额外 `helpers.go` / `patterns.go` 的模块

| 模块 | 额外文件 | 原因 |
| --- | --- | --- |
| `module/thread` | `archive.go` `config.go` `helpers.go` | 归档、绑定、config 三块规则独立 |
| `module/turn` | `prepare.go` `runtime.go` `tracker.go` `review.go` | 输入整形、运行时事件、tracker、review 四段不可再混写 |
| `module/skill` | `loader.go` `matcher.go` `helpers.go` | loader 与 matcher 是两套规则 |
| `module/orchestration` | `phase1_watcher.go` `runner_actor.go` `recover.go` `patterns.go` | actor/恢复/phase1 是三类不同行为 |
| `module/workspace` | `merge.go` `helpers.go` | merge 算法复杂且有文件系统重复样板 |
| `module/uistate` | `projection.go` `runtime.go` `patterns.go` | 高扇入投影，必须分 runtime 与 projection |
| `module/coderun` | `tool.go` `audit.go` | tool facade 与审计策略分离 |
| `module/ida` | `lifecycle.go` `gateway.go` `helpers.go` | gateway 生命周期和工具 surface 都复杂 |
| `module/dashboard` | `projection.go` `rpc.go` | 聚合投影与 transport 装配应分开 |

### 3.3 不需要额外 patterns 文件的模块

这些模块只要维持最小骨架即可：
- `platform/config`
- `platform/runner`
- `provider/claudecli`
- `provider/codexapp`
- `tool/code`

原因不是“简单”，而是重复样板主要由框架承接，过早拆文件只会制造假抽象。

## 4. Rule of Two 候选清单

下表列出跨模块复用的候选抽象及当前处理建议。

| 候选抽象 | 当前所在 | 候选归宿 | 当前状态 | 提升条件 |
| --- | --- | --- | --- | --- |
| 路径 containment / scope CWD | workspace / dashboard / coderun | `platform/shared/pathscope.go` | 立即提升 | 已有 3+ 稳定消费者 |
| 通用 retry/backoff | orchestration / ida / codexapp | `platform/shared/retry.go` | 立即提升 | 已有 3+ 消费者 |
| 文件原子写 / hash | workspace | `platform/shared/fileops.go` | 暂留模块内 | ida 或 dashboard 第二消费者落地后提升 |
| 审计截断 | coderun | `platform/shared/truncate.go` | 暂留模块内 | ida shell / dashboard 复用时提升 |
| JSON clone/normalize | uistate / dashboard | `platform/shared/jsonutil.go` | 候选 | 第二个明确调用点落盘即提升 |
| 分页 cursor | dashboard / store | `platform/shared/cursor.go` | 候选 | tool/orch 再复用后提升 |
| Request `Validate()` 辅助 | rpc | `platform/rpc` | 保持平台专属 | 不进入 shared |
| 状态机 graph/matrix harness | orchestration / ida | `platform/statemachine` | 立即放平台专包 | 不是 shared |
| Tool schema enum builder | tool/registry | `tool/registry` | 不提升 | 只有 registry 使用 |
| 危险命令 gate | coderun | 模块内 | 暂留模块内 | ida shell worker 复用后再议 |
| 线程别名归一化 | thread / uistate / dashboard | `platform/shared/alias.go` | 候选 | 先冻结 thread 主键语义，再决定是否上提 |
| Event -> RPC notify bridge | bus / rpc | `platform/bus/bridge_rpc.go` | 立即放平台专包 | 属于平台集成，不进 shared |

提升规则必须满足四个条件：
1. 至少两个独立模块已经落地并复用。
2. 复用点行为稳定，不在快速迭代期。
3. 抽象不 import `internal/module/*`。
4. 抽象放入共享层后不会扩大公共 API 面。

## 5. 防巨石包预算

### 5.1 `internal/platform/shared/` 预算分配表

| 文件 | 预算 | 超额后动作 |
| --- | ---: | --- |
| `retry.go` | 180 | 拆成 `retry.go` + `backoff.go` |
| `validation.go` | 220 | 超 220 说明已混入业务校验，必须回退到模块或专包 |
| `idgen.go` | 80 | 不得扩成“ID policy 中心” |
| `pathscope.go` | 220 | 超额则拆出 `pathscope_windows.go` / `scopecwd.go` |
| `fileops.go` | 260 | 超额则拆到 `platform/fileops/` 专包 |
| `jsonutil.go` | 140 | 不得塞入 DTO 映射 |
| `cursor.go` | 150 | 超额则拆到 `platform/paging/` |
| `truncate.go` | 120 | 不得混入日志格式化 |
| `hash.go` | 80 | 不得混入版本策略 |
| `errors.go` | 120 | 不得混入业务 code 表 |

目录总预算：`<=2000` 行。

### 5.2 守门规则

- 超出单文件预算：先判断是否掺入业务语义；如果是，回退到模块内。如果不是，拆成更专门的 `platform/<area>/` 子包。
- 超出目录总预算：禁止继续往 `shared/` 塞文件，必须新建专包。
- 新 helper 进入 `shared/` 之前，提交 MR 必须写明两个复用点和未上提成本。

### 5.3 预算红线

以下现象一旦出现，说明 `shared/` 正在巨石化：
- 文件名开始出现 `manager.go`、`service.go`、`provider.go`、`router.go`。
- helper 需要 import `internal/module/*`。
- helper 需要知道业务枚举、业务错误码、业务状态名。
- helper 必须通过 interface 回调业务动作。

## 6. 架构守护测试

`internal/archtest/` 只承接跨包、静态、结构性守卫。模块内 D1-D7 行为守卫仍留在各自包测试中，不挤进 `archtest/`。

```text
internal/archtest/
├── dependency_direction_test.go    — 依赖方向 11 条规则
├── fx_graph_test.go                — fx.ValidateApp + fx import 范围
├── shared_budget_test.go           — platform/shared 行数预算
├── sqlc_boundary_test.go           — sqlc import 边界
├── mcp_family_isolation_test.go    — 三家族交叉 import
├── timeout_locality_test.go        — context.WithTimeout 散落
├── code_size_guard_test.go         — 文件/函数/嵌套/CC 守卫
└── identifier_guard_test.go        — 标识符规范
```

补充约束：
- `fx_graph_test.go` 同时覆盖装配图校验和 `fx` import 范围，不再拆成命名接近的重复测试。
- `code_size_guard_test.go` 除文件/函数/嵌套/复杂度外，还应顺带检查默认包文件数预算和“默认 allowlist 为空”。
- `shared_budget_test.go` 单独负责 `platform/shared` 总预算 `<=2000` 和单文件预算 `<=500`，不并入通用 code-size 守卫。
- `timeout_locality_test.go` 固定 `context.WithTimeout` 只允许出现在 `platform/config/timeouts.go`。

## 执行结论

- Zone A 的核心不是新建“大工厂包”，而是把 V2 的跨包样板拆到六个框架和明确的平台专包里。
- `internal/platform/shared/` 只能装“最后剩下、且已稳定复用”的纯 helper，不接受预判式抽象。
- Zone B 的核心不是多文件，而是单模块单真相：一块业务的重复只在自己的包里收敛。
- `pkg/factory` 在 V3 只能作为过渡遗留，不应成为最终落点。
