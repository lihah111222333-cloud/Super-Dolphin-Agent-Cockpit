# P5 波次 1 前置审查 B（接口+工厂）

## 1. 编译+守卫
- `go build ./...` 通过。
- `go vet ./...` 通过。
- `go test ./internal/archtest/... -count=1 -timeout 120s` 通过，输出为 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 3.481s`。

## 2. ApprovalResponder 接口
- `ApprovalResponder` 已定义在 `internal/contract/approval.go:5`，位置正确。
- 方法签名为 `Respond(callID string, requestID *int64, decision ApprovalDecision) error`，与要求一致。
- `ApprovalDecision` 的定义位于 `internal/contract/approval.go:10`，不是 `platform/rpc` 包内的原生类型定义。
- `internal/platform/rpc/approval.go:28` 曾导出 `type ApprovalDecision = contract.ApprovalDecision`。这不是新定义，但会让 `platform/rpc` 的导出面继续暴露该类型名。

## 3. ApprovalManager 实现
- `internal/platform/rpc/approval.go:26` 存在 `var _ contract.ApprovalResponder = (*ApprovalManager)(nil)` 断言。
- `internal/platform/rpc/approval.go:107` 的 `Respond` 签名与 contract 接口一致。
- 实现路径为 `approvalCallID(callID, requestID)` 归一化标识后查找 pending，再通过 `finishPending` 收口，接口行为完整。

## 4. fx 注册
- `internal/platform/rpc/module.go:14` 到 `internal/platform/rpc/module.go:19` 已通过 `fx.Provide(func(m *ApprovalManager) contract.ApprovalResponder { return m })` 暴露 contract 接口。
- `internal/app/modules.go:20` 到 `internal/app/modules.go:31` 同时装配 `rpc.Module` 与 `turn.Module`。
- 因此，`module/turn` 后续若将 `NewService` 或其他 provider 参数改为 `contract.ApprovalResponder`，fx 图可以解析。
- 当前 `module/turn` 还没有实际注入该接口；结论基于现有 provider 图推断。

## 5. ThreadHandler 工厂
- `internal/platform/rpc/handler.go:75` 新增 `ThreadHandler[Req, Resp]`，实现为 `Wrap(ThreadScope())(StrictHandler(fn))`。
- `internal/platform/rpc/handler.go:80` 新增 `CapabilityThreadHandler[Req, Resp]`，实现为 `Wrap(ThreadScope(), CapabilityGate(cap, resolver))(StrictHandler(fn))`。
- 这两个工厂与现有 `ThreadScope`、`StrictHandler` 完全同构，兼容性正常。
- 限制是工厂只覆盖默认 `ThreadScope()` 字段集，不覆盖 `ThreadScope(fields...)` 的自定义字段变体；需要非默认字段时仍需显式组合。

## 6. import 方向
- `internal/contract/approval.go` 无 import，确认没有反向依赖 `platform/rpc`。
- `internal/platform/rpc/approval.go` 仅单向 import `internal/contract`，方向正确。
- `ApprovalDecision` 的唯一定义在 contract 层；但 `internal/platform/rpc/approval.go:28` 的导出别名会让 `platform/rpc` 侧继续暴露该类型名，contract 边界未完全收紧。

## 7. 行数
- 本次相关修改/新建代码文件均未超过 400 行。
- 已检查文件包括 `internal/contract/approval.go` 13 行、`internal/platform/rpc/module.go` 46 行、`internal/platform/rpc/handler.go` 116 行、`internal/platform/rpc/approval.go` 292 行、`internal/module/thread/lifecycle.go` 399 行等。
- 当前最大值为 `internal/module/thread/lifecycle.go`，399 行。

## 8. 工厂质量
- 在一个假想的 25 方法 `handler.Map` 中，若 25 个方法都需要 thread-scoped strict decode，旧写法需要重复 25 次 `Wrap(ThreadScope())(StrictHandler(fn))`；新工厂可缩减为 25 次 `ThreadHandler(fn)`，重复样板被压缩到 1 个工厂实现。
- 对 capability-gated 方法同理：`CapabilityThreadHandler` 消除了每个 entry 手写 `ThreadScope + CapabilityGate + StrictHandler` 的三段式组合。
- `internal/platform/rpc/strict.go:20` 的 `RawHandler` 仍然存在，因此 raw passthrough 场景与新工厂可以并存；需要原始 `*jrpc2.Request` 的方法不会被工厂绑死。
- 当前仓库内尚未发现 `ThreadHandler` 或 `CapabilityThreadHandler` 的调用点，所以“消除重复”已具备机制，但还没有落地替换证据。

## 结论（Blocker / Improvement）
- Blocker：`internal/platform/rpc/approval.go:28` 仍导出 `ApprovalDecision` 别名，导致 `ApprovalDecision` 仍出现在 `platform/rpc` 的导出面上；这与“ApprovalDecision 不在 platform/rpc 包”的目标不完全一致。建议删除该导出别名，并在 `rpc` 包内部显式使用 `contract.ApprovalDecision` 或改为私有别名。
- Improvement：`ThreadHandler` / `CapabilityThreadHandler` 的设计合理，但尚未被任何 `handler.Map` 使用；后续应至少在一个多方法 RPC 模块中落地替换，验证工厂对样板代码和可读性的实效。
