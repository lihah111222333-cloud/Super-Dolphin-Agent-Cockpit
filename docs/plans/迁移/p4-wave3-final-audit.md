# P4 波次 3 终审报告

> 审查日期：2026-03-20
> 审查员：独立终审 Agent（无历史偏见）

## 1. 基础验证
| 项 | 结果 | 备注 |
| --- | --- | --- |
| `go build ./...` | 通过 | 全仓编译通过 |
| `go vet ./...` | 通过 | 未发现静态分析问题 |
| `go test ./internal/archtest/... -count=1 -timeout 120s` | 通过 | `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.076s` |

## 2. 代码质量
### 文件行数表
| 文件 | 行数 | 结果 |
| --- | ---: | --- |
| `internal/provider/unified/registry.go` | 53 | 通过 |
| `internal/provider/unified/client.go` | 68 | 通过 |
| `internal/provider/unified/session.go` | 100 | 通过 |
| `internal/provider/unified/event_map.go` | 67 | 通过 |
| `internal/provider/unified/module.go` | 23 | 通过 |
| `internal/module/turn/module.go` | 8 | 通过 |
| `internal/module/turn/contract.go` | 38 | 通过 |
| `internal/module/turn/service.go` | 203 | 通过 |
| `internal/module/turn/assembler.go` | 45 | 通过 |
| `internal/module/turn/skills.go` | 116 | 通过 |
| `internal/module/turn/manifest.go` | 15 | 通过 |
| `internal/module/turn/tracker.go` | 148 | 通过 |

结论：全部目标文件均不超过 400 行。

### Top 5 最长函数
| 排名 | 函数 | 行数 | 结果 |
| --- | --- | ---: | --- |
| 1 | [`(*skillResolver).Resolve`](/Volumes/bot/super-agent-v3/internal/module/turn/skills.go#L11) | 37 | 通过 |
| 2 | [`(*service).watchTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L125) | 32 | 通过 |
| 3 | [`(*service).StartTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L61) | 31 | 通过 |
| 4 | [`(*service).PrepareTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L36) | 24 | 通过 |
| 5 | [`(*EventDispatcher).Dispatch`](/Volumes/bot/super-agent-v3/internal/provider/unified/event_map.go#L43) | 24 | 通过 |

结论：未发现超过 80 行的函数。

### import 方向
- `provider/unified` 未发现 `claudecli` 或 `codexapp` 反向 import。
- `module/turn` 未发现 `internal/provider/*` import。
- `module/turn` 对 `internal/dto/provider` 的依赖属于 DTO/契约层依赖，不构成实现层反向耦合。

### fx 范围
- `provider/unified` 仅 [`internal/provider/unified/module.go`](/Volumes/bot/super-agent-v3/internal/provider/unified/module.go#L1) 使用 `fx`。
- `module/turn` 仅 [`internal/module/turn/module.go`](/Volumes/bot/super-agent-v3/internal/module/turn/module.go#L1) 使用 `fx`。
- 本项通过。

### 编译期断言
- 在 `internal/provider/unified` 与 `internal/module/turn` 范围内，未发现 `var _ Interface = (*impl)(nil)` 形式的编译期断言。
- 典型缺口包括 `service` 对 `Service`、`Client`/`Registry`/`SessionManager` 对相关契约未做静态绑定。
- 结论：缺失，建议补齐。

## 3. 架构
### Registry
- [`Resolve`](/Volumes/bot/super-agent-v3/internal/provider/unified/registry.go#L27) 对 `r == nil` 和 `factory.Create == nil` 做了防护，这一层通过。
- 阻断问题：[`Resolve`](/Volumes/bot/super-agent-v3/internal/provider/unified/registry.go#L35) 直接返回 `factory.Create()`，未校验返回的 `Driver` 是否为 `nil`。随后 [`Client.open`](/Volumes/bot/super-agent-v3/internal/provider/unified/client.go#L54) 会对该结果直接执行方法调用，存在 nil driver 触发运行时故障的路径。
- 结论：部分通过，需补 `factory.Create() == nil` 的错误返回。

### Client facade
- [`StartSession`](/Volumes/bot/super-agent-v3/internal/provider/unified/client.go#L29) / [`ResumeSession`](/Volumes/bot/super-agent-v3/internal/provider/unified/client.go#L38) 仅做选路并委托给统一的 [`open`](/Volumes/bot/super-agent-v3/internal/provider/unified/client.go#L47)。
- `open` 的逻辑为 resolve、日志、驱动委托、session 注册；未混入 provider 业务规则，facade 边界基本干净。
- 风险补充：`run(driver)` 若返回 `(nil, nil)`，当前实现也会原样返回；建议在 facade 侧补充 nil session 防护。

### 工厂模式
- `turn` 已按职责拆分为 [`service.go`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L1)、[`assembler.go`](/Volumes/bot/super-agent-v3/internal/module/turn/assembler.go#L1)、[`skills.go`](/Volumes/bot/super-agent-v3/internal/module/turn/skills.go#L1)、[`manifest.go`](/Volumes/bot/super-agent-v3/internal/module/turn/manifest.go#L1)、[`tracker.go`](/Volumes/bot/super-agent-v3/internal/module/turn/tracker.go#L1)。
- 未发现 3 处及以上、应抽未抽的重复实现。
- 保留意见：[`NewService`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L23) 直接硬编码创建协作者，而非通过构造注入；这不构成架构阻断，但降低可替换性与测试可控性。

## 4. 运行时安全
### 并发安全
- [`turnTracker`](/Volumes/bot/super-agent-v3/internal/module/turn/tracker.go#L11) 的 `Start/BindProviderID/Update/Complete/Cleanup` 使用互斥锁写，`Get/GetByProviderID` 使用读锁，锁粒度正确。
- [`SessionManager`](/Volumes/bot/super-agent-v3/internal/provider/unified/session.go#L14) 在替换 session 与批量关闭场景中，均将外部调用放到锁外执行，避免锁内阻塞或重入。
- 本项通过。

### goroutine 泄漏
- [`watchTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L125) 具备三条退出路径：`ctx.Done()`、`trackerTTL` 定时器、`handle.Done()`，从 goroutine 数量角度看是有边界的。
- 阻断问题：在 `ctx.Done()` 或 `timer.C` 分支上，代码直接返回，不写入任何终态，也不清理 tracker；而 [`Cleanup`](/Volumes/bot/super-agent-v3/internal/module/turn/tracker.go#L91) 只清理终态 turn。结果是 turn 可能永久停留在 `running`，形成状态泄漏而不是 goroutine 泄漏。
- 阻断问题：[`watchTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L141) 绑定的是调用方 `ctx`。若调用方使用请求级 context，RPC 返回后 watcher 可能先于 turn 自身结束，导致 tracker 永远失真。
- 结论：goroutine 退出路径存在，但运行状态回收不完整，需修正。

### turn ID
- [`TurnStatus`](/Volumes/bot/super-agent-v3/internal/module/turn/contract.go#L32) 同时保留 `LocalID` 与 `ProviderID`，双标识存在。
- [`BindProviderID`](/Volumes/bot/super-agent-v3/internal/module/turn/tracker.go#L42) 与 [`GetByProviderID`](/Volumes/bot/super-agent-v3/internal/module/turn/tracker.go#L117) 已提供。
- 本项通过。

## 5. 语义
### 事件归属
- `module/turn` 范围内未发现 `event.Publish`、`event.Dispatcher` 或 `RawProviderEvent` 依赖，说明该模块当前没有越权发布 provider/raw event。
- [`provider/unified/event_map.go`](/Volumes/bot/super-agent-v3/internal/provider/unified/event_map.go#L43) 明确承担 raw provider event 到 typed event 的翻译后发布职责，边界清晰。
- 风险补充：`module/turn` 当前也没有消费这些 turn typed event。tracker 状态主要依赖 [`watchTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L125)，未形成“provider translator 发布，turn 消费并更新状态”的闭环。

### V2 对照
- 对照 V2 [`turn_prepare_core.go` 前 50 行](/Volumes/bot/super-agent-v3/go-agent-v2/pkg/agentsdk/service/runtime/turn_prepare_core.go#L1) 可见输入 guardrail、允许扩展名和大小限制属于核心准备逻辑；对照 [`ParseTurnInputs`](/Volumes/bot/super-agent-v3/go-agent-v2/pkg/agentsdk/service/runtime/turn_prepare_core.go#L277) 可见其还负责输入上限裁剪、结构化解析、去重和多输入类型归一化。
- V3 [`PrepareTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L36) + [`inputAssembler.Assemble`](/Volumes/bot/super-agent-v3/internal/module/turn/assembler.go#L11) 当前仅覆盖 `prompt/images/files` 的扁平拼装，未覆盖 guardrail、去重、`mention/localimage/filecontent`、attachment 生成等核心行为。
- 阻断问题：[`autoMatch`](/Volumes/bot/super-agent-v3/internal/module/turn/skills.go#L100) 仍为 `TODO(P5)`，而 V2 [`prepareTurnSubmissionCommon`](/Volumes/bot/super-agent-v3/go-agent-v2/pkg/agentsdk/service/runtime/turn_prepare_core.go#L146) 已包含 auto-matched skill 合并流程。
- 对照 V2 [`turn_interrupt_core.go` 前 50 行](/Volumes/bot/super-agent-v3/go-agent-v2/pkg/agentsdk/service/interrupt/turn_interrupt_core.go#L1) 可见中断 settle timeout 策略和 runtime state hook 是核心语义；对照 V2 [`turnInterrupt`](/Volumes/bot/super-agent-v3/go-agent-v2/pkg/agentsdk/service/interrupt/turn_interrupt_core.go#L215) 可见其会取消运行中的 code runs、发送中断、等待 settle、回读状态并产生完成通知。
- 阻断问题：V3 [`InterruptTurn`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L93) 仅调用 `session.Interrupt` 后立即返回，且代码中直接标注 `TODO(P5): settle`，未覆盖 V2 的核心中断流程。
- 结论：V3 turn service 尚未覆盖 V2 prepare/interrupt 的核心行为面，语义对照不通过。

## 6. 结论
### 通过项
- 编译、`go vet`、架构守卫测试全部通过。
- 所有目标文件均满足单文件 ≤ 400 行，所有函数均满足单函数 ≤ 80 行。
- import 方向符合约束，`fx` 仅存在于 `module.go`。
- `turnTracker` 与 `SessionManager` 的基本锁使用正确。
- `TurnStatus` 已具备 `LocalID`/`ProviderID` 双标识，`BindProviderID/GetByProviderID` 已存在。
- `provider.unified` 的事件翻译与发布边界清晰，`module/turn` 未越界发布事件。

### 需修正项（Blocker）
- [`internal/provider/unified/registry.go#L35`](/Volumes/bot/super-agent-v3/internal/provider/unified/registry.go#L35): `factory.Create()` 返回 `nil` 时未转错误，后续 [`internal/provider/unified/client.go#L54`](/Volumes/bot/super-agent-v3/internal/provider/unified/client.go#L54) 存在 nil driver 调用风险。
- [`internal/module/turn/service.go#L141`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L141) + [`internal/module/turn/tracker.go#L91`](/Volumes/bot/super-agent-v3/internal/module/turn/tracker.go#L91): `watchTurn` 在 `ctx.Done()`/TTL 分支上直接退出，不写终态；`Cleanup` 仅清理终态，导致 `running` 状态可能永久滞留。
- [`internal/module/turn/service.go#L93`](/Volumes/bot/super-agent-v3/internal/module/turn/service.go#L93): `InterruptTurn` 未实现 settle/wait/state 回读，V2 中断核心流程未迁移完成。
- [`internal/module/turn/assembler.go#L11`](/Volumes/bot/super-agent-v3/internal/module/turn/assembler.go#L11) + [`internal/module/turn/skills.go#L100`](/Volumes/bot/super-agent-v3/internal/module/turn/skills.go#L100): V3 `PrepareTurn` 未覆盖 V2 输入 guardrail、多输入类型归一化、去重、attachment 构建和 auto-match skill 逻辑。

### 建议项（Improvement）
- 为 `service`、`Client`、`Registry`、`SessionManager` 等实现补充编译期断言，降低接口漂移风险。
- 在 [`Client.open`](/Volumes/bot/super-agent-v3/internal/provider/unified/client.go#L47) 增加 nil session 防护，避免驱动返回 `(nil, nil)` 时将异常延后。
- 将 `service` 的协作者改为构造注入，减少 `NewService` 内部硬编码，便于测试替身与后续演进。
- 将 `GetByProviderID` 暴露到更上层的 turn 查询能力或事件消费流程中，避免 provider event 到 tracker 的映射能力停留在内部。

### 终审判定（通过/需修正）
需修正
