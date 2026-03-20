# P4 波次 3 独立深度审查

> 审查日期：2026-03-20
> 审查员：第二独立审查 Agent

## 1. 编译全绿

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过
- 实测输出：`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.076s`

## 2. V2 能力覆盖矩阵

- 统计结果：45 项公开函数/公开可调用入口中，`覆盖 2`、`骨架 11`、`遗漏 32`。
- 总体判断：T5/T6 当前是“可编译骨架”，不是 V2 unified + turn 的等价迁移；缺口集中在 `turn runtime`、`interrupt settle`、`tracker summary`、`user timeline`、`steer`、`provider fallback/orphan matching`。

| V2 函数 | V3 归宿 | 状态（覆盖/骨架/遗漏） |
|---|---|---|
| `codexadapter.New` | `internal/module/turn/service.go::NewService` | 骨架 |
| `(*Adapter).FuzzyFileSearch` | 无 | 遗漏 |
| `(*Adapter).TurnInterrupt` | `internal/module/turn/service.go::(*service).InterruptTurn` | 骨架 |
| `(*Adapter).TurnForceComplete` | 无 | 遗漏 |
| `(OrphanProcessMatcher).Match` | 无 | 遗漏 |
| `NewProviderRegistry` | `internal/provider/unified/registry.go::NewRegistry` | 骨架 |
| `NewDefaultProviderRegistry` | 无 | 遗漏 |
| `(*staticProviderRegistry).Resolve` | `internal/provider/unified/registry.go::(*Registry).Resolve` | 骨架 |
| `(*staticProviderRegistry).Default` | 无 | 遗漏 |
| `ResolveProviderFactory` | 无 | 遗漏 |
| `NewCodexProviderFactory` | `internal/provider/codexapp/module.go::NewDriverFactory` | 覆盖 |
| `(codexProviderFactory).ClientFactories` | `internal/provider/codexapp/module.go::NewDriverFactory -> contract.DriverFactory.Create` | 骨架 |
| `(codexProviderFactory).OrphanProcessMatchers` | 无 | 遗漏 |
| `NewClaudeProviderFactory` | `internal/provider/claudecli/module.go::NewDriverFactory` | 覆盖 |
| `(claudeProviderFactory).ClientFactories` | `internal/provider/claudecli/module.go::NewDriverFactory -> contract.DriverFactory.Create` | 骨架 |
| `(claudeProviderFactory).OrphanProcessMatchers` | 无 | 遗漏 |
| `EnsureTurnSteerResultTurnID` | 无 | 遗漏 |
| `PrepareTurnStartSubmission` | `internal/module/turn/service.go::(*service).PrepareTurn` + `assembler.go` + `skills.go` + `manifest.go` | 骨架 |
| `PrepareTurnSteerSubmission` | 无 | 遗漏 |
| `ResolveTurnSteerAlignment` | 无 | 遗漏 |
| `TurnSteerFromInputAligned` | 无 | 遗漏 |
| `AppendTurnStartUserTimeline` | 无 | 遗漏 |
| `ThreadTimelineAlreadyShowsInjectedPrompt` | 无 | 遗漏 |
| `CollectAutoMatchedSkillMatchesForThread` | `internal/module/turn/skills.go::(*skillResolver).Resolve/autoMatch` | 骨架 |
| `ParseTurnInputs` | `internal/module/turn/assembler.go::(*inputAssembler).Assemble` | 骨架 |
| `BuildUserTimelineAttachments` | 无 | 遗漏 |
| `ComposeUserTimelineTextForTurn` | 无 | 遗漏 |
| `EnsureThreadReadyForTurn` | 无 | 遗漏 |
| `ReadThreadRuntimeStateByHooks` | 无 | 遗漏 |
| `WaitInterruptOutcome` | 无 | 遗漏 |
| `SendInterruptCommand` | `internal/module/turn/service.go::(*service).InterruptTurn -> contract.Session.Interrupt` | 骨架 |
| `NotifyTurnCompleted` | 无 | 遗漏 |
| `TurnInterrupt` | `internal/module/turn/service.go::(*service).InterruptTurn` | 骨架 |
| `TurnForceComplete` | 无 | 遗漏 |
| `NormalizeTrackedTurnStatus` | 无 | 遗漏 |
| `ExtractTrackedString` | 无 | 遗漏 |
| `ExtractTrackedTurnID` | 无 | 遗漏 |
| `ExtractTrackedTurnStatus` | 无 | 遗漏 |
| `ExtractTrackedTurnReason` | 无 | 遗漏 |
| `TrackedTurnSummaryCacheKey` | 无 | 遗漏 |
| `RememberTrackedTurnSummary` | 无 | 遗漏 |
| `LookupTrackedTurnSummary` | 无 | 遗漏 |
| `TrackedTurnSummaryFromPayload` | 无 | 遗漏 |
| `InjectTrackedTurnSummary` | 无 | 遗漏 |
| `MergeTrackedTurnCompletionPayload` | 无 | 遗漏 |

补充判读：

- `PrepareTurnStartSubmission` 只保留了最小路径：`PrepareTurn` 负责生成 `dto.TurnRequest`，但 V2 中 `timeline attachments`、`selected/auto matched skill count`、多种 input 形态、guardrail 与 prompt compose 细节没有迁入。
- `CollectAutoMatchedSkillMatchesForThread` 仅存在意图性残影。`internal/module/turn/skills.go:19-31` 先把全部 `refs` 写入 `seen`，第二轮 `autoMatch` 永远无法追加新技能，实际行为等价于“只返回显式 skills”。
- `TurnInterrupt` 只剩 `session.Interrupt` 的薄封装；`service.go:105` 明确写明 settle wait 留到 P5，因此 V2 的 wait/notify/force-complete 语义当前不在场。
- `provider registry` 的默认 provider/fallback/orphan matcher 能力没有一起迁入；当前 `Registry.Resolve` 仅支持严格 provider 名称命中。

## 3. 工厂模式

- `internal/module/turn/service.go` 不是纯编排器。它除了调用 `assembler/skills/manifest/tracker` 外，还内嵌了 `context/session 校验`、`thread id 解析`、`local turn id 生成`、`override 决策`、`后台 watcher` 等具体逻辑。
- `assembler.go`、`skills.go`、`manifest.go`、`tracker.go` 已拆分成独立文件，单元规模小，理论上可以在包内独立测试。
- 但这些组件没有通过接口或单独构造函数暴露给 `NewService`，`internal/module/turn/service.go:26-31` 直接硬编码具体实现；因此“可单测”成立，“可替换/可注入”不成立。
- 构造统一入口满足要求：`NewService` 是 turn 包内唯一导出构造函数，内部统一组装 `assembler/skills/manifest/tracker`。
- 未发现单个方法超过 50 行。范围内最大的方法是：
  - `internal/module/turn/service.go::(*service).StartTurn`，30 行
  - `internal/module/turn/service.go::(*service).PrepareTurn`，24 行
  - `internal/provider/unified/event_map.go::(*EventDispatcher).Dispatch`，24 行
- 结论：没有“上帝函数”，但 `service.go` 仍承担了一部分本可继续下沉的策略逻辑。

## 4. contract 一致性

- `internal/module/turn/contract.go` 的 `Service` 接口与 `internal/module/turn/service.go` 的实现方法签名一致：
  - `PrepareTurn(ctx context.Context, session contract.Session, input PrepareInput) (dto.TurnRequest, error)`
  - `StartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error)`
  - `InterruptTurn(ctx context.Context, session contract.Session, source string) error`
  - `TrackTurn(ctx context.Context, localID string) (TurnStatus, error)`
- `internal/contract/provider.go` 的 `Driver` / `Session` 接口与当前调用点一致：
  - `internal/provider/unified/client.go` 只调用 `Driver.StartSession` / `Driver.ResumeSession`
  - `internal/provider/unified/session.go` 只调用 `Session.Close` / `Session.ForceStop`
  - `internal/module/turn/service.go` 只调用 `Session.ThreadID` / `Capabilities` / `StartTurn` / `Interrupt`
- 未发现签名漂移或调用侧超出 contract 的情况。
- 未发现 `var _ Service = (*service)(nil)` 这类编译期断言；当前一致性依赖 `go build` 间接保证，缺少局部护栏。

## 5. fx 装配

- `internal/provider/unified/module.go` 的 `fx.Provide` 已覆盖统一层全部导出构造函数：
  - `NewEventDispatcher`
  - `NewRegistry`
  - `NewClient`
  - `NewSessionManager`
- `internal/module/turn/module.go` 的 `fx.Provide` 已覆盖 `NewService`。
- `RegistryParams` 的 `group:"drivers"` 能被 concrete provider 满足：
  - `internal/provider/claudecli/module.go:23` 通过 `fx.Annotate(NewDriverFactory, fx.ResultTags(\`group:"drivers"\`))`
  - `internal/provider/codexapp/module.go:23` 同样输出 `group:"drivers"`
- 在审查范围内，没有发现“导出构造函数存在但未注册到 fx”的情况。
- 但 `inputAssembler`、`skillResolver`、`manifestBuilder`、`turnTracker` 不是 fx 节点，只能通过 `NewService` 内部硬装配获得，无法单独注入替身。

## 6. 依赖方向

- `internal/provider/unified/*.go` 的 import 列表干净，仅依赖：
  - 标准库
  - `internal/contract`
  - `internal/dto/provider`
  - `go.uber.org/fx`
  - `github.com/kelindar/event`
- `internal/module/turn/*.go` 的 import 列表干净，仅依赖：
  - 标准库
  - `internal/contract`
  - `internal/dto/provider`
  - `internal/dto/shared`
- 通过全文检索确认：
  - `internal/provider/unified` 不 import `internal/provider/claudecli`
  - `internal/provider/unified` 不 import `internal/provider/codexapp`
  - `internal/module/turn` 不 import `internal/provider/`
- 隐式依赖检查：
  - `internal/provider/unified/event_map.go:50` 存在 `func(ev any)`，但该 `any` 来自 `dto.EventTranslator` 的 publish callback 约定，随后立即做 `event.Event` 类型断言，不是用 `any/interface{}` 绕过包依赖。
  - `internal/module/turn` 范围内未发现 `any` 或 `interface{}` 作为跨层语义通道。
- 结论：依赖方向符合方案要求；当前缺口主要是“能力没迁够”，不是“依赖方向错了”。

## 7. 并发安全

### SessionManager

- `internal/provider/unified/session.go` 的锁粒度正确：
  - `Register` 在锁内替换 session，在锁外 `ForceStop` 被替换实例，避免长耗时停留在临界区。
  - `Get` 使用 `RLock`。
  - `Remove` 使用 `Lock`。
  - `CloseAll` 通过 `drain()` 复制并清空 map，再在锁外做 `Close/ForceStop`，避免把 IO/进程等待放进临界区。
- 未发现明显 data race。

### turnTracker

- `internal/module/turn/tracker.go` 的锁使用也正确：
  - 写路径 `Start/BindProviderID/Update/Complete` 全部走 `Lock`
  - 读路径 `Get/GetByProviderID` 走 `RLock`
  - 返回值是 `TurnStatus` 拷贝，不泄漏内部指针
- 未发现明显 data race。

### 风险点

- `internal/module/turn/service.go:87` 启动 `go s.watchTurn(handle)`，而 `watchTurn` 在 `service.go:121-135` 只阻塞等待 `handle.Done()`；没有 timeout、context cancel、watchdog、service shutdown hook。若 driver 永不关闭 `Done()`，goroutine 会永久泄漏。
- `internal/module/turn/tracker.go:23-115` 没有任何删除、TTL、prune、summary cache 上限控制。完成态 turn 永久保存在 `map[string]*trackedTurn` 中，长生命周期进程会持续累积状态。
- 与 V2 对比，缺失 `DefaultTurnWatchdogTimeout`、stall timer、summary TTL 等机制；因此“锁安全”成立，但“生命周期安全”明显退化。

## 8. 代码量

目标区间来自 `docs/plans/迁移/p4-execution-plan.md:183-200`。

| 包 | 文件数 | 总行数 | 目标区间 | 评估 |
|---|---:|---:|---:|---|
| `internal/provider/unified` | 5 | 306 | 1,400 - 2,000 | 明显低于目标。当前只有 `registry/client/session manager/event dispatcher/module`，未达到计划中的 `capability fallback`、更完整 session facade、其他统一层配套能力。 |
| `internal/module/turn` | 7 | 516 | 900 - 1,300 | 明显低于目标。当前只覆盖基础 `prepare/start/interrupt/track`，缺 `runtime/steer/user timeline/summary/force-complete` 等大块能力。 |

补充量化：

- 两包合计：`12 文件 / 822 行`
- 对照执行计划：`provider/unified + module/turn` 目标合计 `2,300 - 3,300 行`
- 当前完成度按代码量仅约 `24.9% - 35.7%`

## 9. 结论

### 通过项

- 编译、`vet`、`archtest` 全绿。
- `turn` 与 `unified` 的依赖方向符合设计：`turn` 不依赖 concrete provider，`unified` 不依赖 concrete provider 实现。
- `fx` 装配链路是闭合的：`unified` 的构造函数全部已注册，`turn` 的 `NewService` 已注册，concrete provider 已通过 `group:"drivers"` 接入 registry。
- `Service` / `Driver` / `Session` contract 与调用实现保持一致。
- 没有超过 50 行的单体“上帝函数”。

### 需修正项

- `internal/module/turn/skills.go:19-31` 的 auto-match 逻辑实际上不可达；当前并没有完成 V2 的自动技能匹配迁移。
- `internal/module/turn/service.go:87-135` 的后台 watcher 缺少超时/关闭控制，存在 goroutine 泄漏风险。
- `internal/module/turn/tracker.go:23-115` 缺少 completed turn 清理、TTL 或上限控制，存在状态无界增长风险。
- `internal/module/turn/service.go:91-106` 明确把 interrupt settle 留到 P5，导致 V2 的 `WaitInterruptOutcome` / `NotifyTurnCompleted` / `TurnForceComplete` 语义整体缺失。
- `internal/provider/unified/registry.go:15-36` 只有严格命中式 resolve，没有 V2 `Default` / `ResolveProviderFactory` 的默认 provider/fallback 语义。
- `internal/module/turn` 缺少 `var _ Service = (*service)(nil)` 编译期断言。

### V2 能力差距（可接受/需补齐）

可接受：

- `NewCodexProviderFactory` / `NewClaudeProviderFactory` 已由 `internal/provider/*/module.go::NewDriverFactory` + `contract.DriverFactory` + `group:"drivers"` 架构替换；不必按 V2 runner registry 形状原样回迁。

需补齐：

- `turn runtime`：`EnsureThreadReadyForTurn` 对应的恢复/拉起/历史线程 resume 能力当前缺失。
- `turn steer`：`EnsureTurnSteerResultTurnID`、`ResolveTurnSteerAlignment`、`TurnSteerFromInputAligned` 整体缺失。
- `turn prepare / timeline`：`AppendTurnStartUserTimeline`、`ThreadTimelineAlreadyShowsInjectedPrompt`、`BuildUserTimelineAttachments`、`ComposeUserTimelineTextForTurn` 缺失；`ParseTurnInputs` 只有极简版。
- `turn interrupt`：`ReadThreadRuntimeStateByHooks`、`WaitInterruptOutcome`、`NotifyTurnCompleted`、`TurnForceComplete` 缺失或仅剩薄封装。
- `turn tracker`：summary cache、payload extract/merge、status normalize 等大块能力缺失。
- `provider/unified`：默认 provider fallback 缺失；若系统仍保留 orphan-process 恢复路径，则 orphan matcher 也需要明确迁移归宿。
- `codexadapter.FuzzyFileSearch` 当前未发现 V3 归宿；需要显式决定“迁移到其他模块”还是“正式删除并更新能力声明”。
