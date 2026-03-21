# 验证：turn 族 1:1 对齐修复（start/steer/interrupt/forceComplete）

基于当前代码树，对 `align-turn-start.md` / `align-turn-interrupt.md` 的相关修复点复核如下。

## 结论

- ✅ `turn/start` 的 session-ready 前提现在是透明的，`CapabilityGate` 不会把 session/resolver 错误吞成 capability 不支持。
  - `turn/start` / `turn/steer` 先走 `CapabilityThreadHandler(...)`，即 `ThreadScope + CapabilityGate + StrictHandler`。`internal/module/turn/rpc_helpers.go:102-120`，`internal/platform/rpc/handler.go:129-130`
  - `CapabilityGate` 在 resolver 出错时走 `capabilityResolverError(...)`，返回 `CodeInvalidState` 并保留 `threadId` / `detail`；只有 `caps.Has(cap)==false` 才返回 `CodeCapabilityGate`。`internal/platform/rpc/handler.go:80-120`
  - capability resolver 本身会显式解析 thread session；session 缺失时直接返回 `thread session is not available`。`internal/platform/rpc/handler.go:22-39`

- ✅ `turn/start` 输入面已扩展。
  - `turnStartParams` 现在除 `prompt/images/files` 外，还支持 `input`、`selected_skills`、`manual_skill_selection`、`cwd`、`approval_policy`、`model`、`effort`、`output_schema`。`internal/module/turn/rpc_types.go:8-22`
  - `buildPrepareInput(...)` 已把这些字段透传到 `PrepareInput`，包括把 `input[]` 中的 `skill` 收敛进 `Skills`。`internal/module/turn/rpc_helpers.go:16-30,33-70,179-206`
  - `PrepareInput` / provider request 仍承载 `Skills` / `ManualSkillSelection` / `CWD` / `OutputSchema`。`internal/module/turn/contract.go:29-44`，`internal/dto/provider/turn.go:9-18`
  - codex provider 会再编码回 `turn/start` 的 `input + selectedSkills + manualSkillSelection + model + effort + outputSchema`。`internal/provider/codexapp/session.go:377-400`

- ✅ `turn/interrupt` 成功返回值已对齐为 `{"ok":true}`。
  - handler 成功分支固定返回 `turnInterruptResult{OK:true}`。`internal/module/turn/rpc_helpers.go:136-144`
  - result type 序列化为 `json:"ok"`。`internal/module/turn/rpc_types.go:152-154`

- ✅ `forceComplete` 现在有独立语义，不再复用 `interrupt`。
  - service 侧有独立 `ForceCompleteTurn(...)`，构造 `dto.ForceCompleteRequest`、置 `force_completing` 状态，并调用 `session.ForceComplete(...)`，不是 `session.Interrupt(...)`。`internal/module/turn/service.go:143-170`
  - session contract 也有独立 `ForceComplete(...)`。`internal/contract/provider.go:22-30`
  - codex provider 走独立 RPC `turn/forceComplete`，并在成功后触发 `reason=force_complete` 的本地 completion 语义。`internal/provider/codexapp/session.go:149-160,321-338`
  - claude provider 也实现了独立 `ForceComplete(...)` / `forceCompleteTurn(...)` 路径；底层仍用 `SIGINT`，但不再复用 `Interrupt(...)` 入口。`internal/provider/claudecli/session_config.go:95-125`

## 补充观察

- ⚠️ `turn/steer` 仍未做 1:1 输入面对齐。
  - RPC 参数仍只有 `thread_id + prompt`。`internal/module/turn/rpc_types.go:68-81`
  - service 语义仍是 `PrepareTurn(...Prompt...)` 后重新 `StartTurn(...)`。`internal/module/turn/service.go:109-115`
  - 现有测试也明确把它当成“用 prompt 开一个新 turn”。`internal/module/turn/service_test.go:179-202`
