# P5 波次 1 前置审查 A（contracts）

## 1. 编译+守卫
- `go build ./...` 通过。
- `go vet ./...` 通过。
- `go test ./internal/archtest/... -count=1 -timeout 120s` 通过：`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 3.234s`。
- 补充验证：`go test ./internal/module/thread ./internal/module/turn ./internal/platform/rpc -count=1` 通过；`internal/module/turn` 为 `ok`，`thread`/`rpc` 当前无测试文件。

## 2. B1 thread.Service
- `internal/module/thread/contract.go` 已新增：
- `Start(ctx context.Context, req StartRequest) (StartResult, error)`
- `Resume(ctx context.Context, req ResumeRequest) error`
- `Fork(ctx context.Context, threadID string) (ForkResult, error)`
- `Recover(ctx context.Context, threadID string) error`
- `StartRequest`、`ResumeRequest`、`StartResult`、`ForkResult` 类型均已定义。
- `internal/module/thread/service.go` 本身未直接放入上述方法实现；实际实现位于 `internal/module/thread/lifecycle.go`，且 `internal/module/thread/service.go` 的 `var _ Service = (*service)(nil)` 仍成立，`go build` 已证明 `service` 已完整满足扩充后的接口。
- `internal/module/thread/lifecycle.go` 已提供 `Start`、`Resume`、`Fork`、`Recover` 的具体实现，均不是空桩。
- provider 依赖获取方式合规：
- session 启动/恢复走 `SessionStarter`
- session 查询走 `SessionProvider`
- `module/thread` 未直接 import `internal/provider/*`
- 结论：B1 合约扩充已落地，但实现文件从 `service.go` 拆到了 `lifecycle.go`。

## 3. B2 turn.Service
- `internal/module/turn/contract.go` 已新增 `SteerTurn` 与 `ForceCompleteTurn`。
- `internal/module/turn/service.go` 中：
- `SteerTurn` 通过 `PrepareTurn(...Prompt...) + StartTurn(...)` 发起新 turn，符合“Steer = 新 turn”预期。
- `ForceCompleteTurn` 通过 `session.Interrupt(... Source: "force_complete")` 触发中断，并在有 tracked turn 时执行 `MarkInterruptRequested` + `Complete(..., true, "")`，属于“Interrupt + 本地状态推进”的实现，不是独立 provider API。
- `internal/module/turn/service_test.go` 已补到行为测试：
- `TestSteerTurnStartsPromptAsNewTurn`
- `TestForceCompleteTurnMarksTrackedTurnCompleted`
- 结论：B2 已实现，语义基本符合预期。

## 4. B3 ApprovalManager
- `func (m *ApprovalManager) Respond` 已改为 `Respond(callID string, requestID *int64, decision ApprovalDecision) error`。
- `internal/contract/approval.go` 中的 `ApprovalResponder` 接口已同步为三参数签名。
- LSP 搜索到的直接调用点均已同步：
- `internal/platform/rpc/approval.go` 的 `AutoApprove` 传 `nil`
- `internal/platform/rpc/approval.go` 的 `dispatchApproval` 传 `m.lookupRequestID(callID)`
- `approvalCallID(callID, requestID)` 已支持通过 `requestID` 回退生成 call id。
- 未发现遗留的旧两参数 `Respond` 调用点。

## 5. import 方向
- `module/thread/*.go` 未发现 `internal/provider/*` import。
- `module/turn/*.go` 未发现 `internal/provider/*` import。
- `SessionProvider` 定义在 `internal/module/thread/service.go`，位置合规，且只暴露 `GetSession(agentID string) (contract.Session, error)`。
- `OrchestrationFacade` 定义在 `internal/module/thread/lifecycle.go`，形式上位于 `module/thread` 包内。
- 但 `internal/module/thread/lifecycle.go` 与 `internal/module/thread/module.go` 仍直接 import `internal/sidecar/orch/orchestration`，且 `OrchestrationFacade` 的 `LaunchAgent` 参数直接暴露 `orchestration.LaunchRequest`，`buildLaunchRequest` 也直接构造该类型。
- 结论：对 `provider/*` 的 import 方向合规；对 `orchestration` 的解耦不完全合规，`thread` 仍然显式依赖 `module/orchestration` 类型。

## 6. 行数
- 按当前工作区中与本轮 B1/B2/B3 直接相关的 13 个 Go 文件统计，所有文件均 `<= 400` 行。
- 最大文件是 `internal/module/thread/lifecycle.go`，`399` 行，贴边但未超限。
- 通过 LSP 函数范围扫描，已检查这些改动文件中的函数/方法长度；未发现 `> 80` 行的函数。
- 本轮观察到的最长函数仍明显低于阈值，测试函数也未超限。

## 7. 编译期断言
- `internal/module/thread/service.go` 中 `var _ Service = (*service)(nil)` 仍存在且可通过编译。
- 补充：`internal/platform/rpc/approval.go` 中 `var _ contract.ApprovalResponder = (*ApprovalManager)(nil)` 也保持成立，说明 B3 的接口扩充已闭合。

## 8. 窄接口
- `SessionProvider` 足够窄，只暴露 session 查询能力，没有把 provider registry 或 session manager 全量泄漏到 `thread` 模块。
- `SessionStarter` 也基本合理，只暴露 `StartSession` / `ResumeSession`。
- `OrchestrationFacade` 的方法集相对 `internal/sidecar/orch/orchestration/contract.go` 中完整 `Service`（`LaunchAgent` / `StopAgent` / `SubmitTurn` / `CompleteTurn` / `Recover` / `Snapshot`）已经收窄到 3 个方法，方向正确。
- 但它仍暴露了 `orchestration.LaunchRequest`，导致 `thread` 需要认识 `module/orchestration` 的请求类型；这说明接口“方法数变窄”了，但“类型边界”没有彻底收窄。
- 结论：`SessionProvider` 合格；`OrchestrationFacade` 只做到了半收窄，仍有类型泄漏。

## 结论（Blocker / Improvement）
- Blocker：`internal/module/thread/lifecycle.go` 和 `internal/module/thread/module.go` 仍直接依赖 `internal/sidecar/orch/orchestration`，`OrchestrationFacade` 也直接暴露 `orchestration.LaunchRequest`。如果本轮目标是把 thread 侧 contracts 扩充为“模块内定义、模块外适配”的窄接口，这一项尚未完成，建议改为 `thread` 包内自定义 request DTO 或更小的构造参数，避免 `thread -> orchestration` 类型依赖。
- Improvement：`ForceCompleteTurn` 当前是“`Interrupt` 成功后立即把 tracker 标成 completed”的乐观实现；若后续 handle 以 `context.Canceled` 或其它 error 结束，状态可能再被 `watchTurn` 改写为 `interrupted/failed`。建议明确 `force_complete` 的最终状态语义，避免本地状态短暂失真。
