# P5 波次 0 审查 A

## 结论

当前 `R0` 不能判定为完成。

子任务状态：

- `R0a`：部分通过
- `R0b`：部分通过
- `R0c`：不通过
- `R0d`：部分通过

通过的是基础设施骨架；未通过的是“公开 RPC 已完成注册并形成 approval/push 闭环”的完成态。

## 范围

- `R0a`
  - `internal/platform/rpc/strict.go`
  - `internal/platform/rpc/codec.go`
  - `internal/platform/rpc/transport_ws.go`
  - `internal/platform/rpc/push.go`
- `R0b`
  - `internal/platform/rpc/module.go`
  - `internal/app/modules.go`
- `R0c`
  - `internal/platform/rpc/approval.go`
  - `internal/platform/rpc/approval_support.go`
- `R0d`
  - `internal/platform/rpc/handler.go`
- 依赖口径证据
  - `internal/platform/rpc/errors.go`
  - `internal/platform/rpc/server.go`
  - `internal/platform/rpc/registry.go`
  - `internal/contract/provider.go`
  - `internal/sidecar/orch/orchestration/contract.go`
  - `internal/dto/agent/state.go`
  - `docs/plans/迁移/p5-execution-plan.md`
  - `docs/契约/jrpc2-convention.md`

## 验证结果

- `go build ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过
  - 结果：`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.036s`
- `go test ./internal/platform/rpc/... -count=1`：通过，但结果为 `[no test files]`
- 空 `handler.Map` 最小实验：`jserver.NewLocal(handler.Map{}, nil)` 后调用 `rpc.serverInfo` 返回 `err=<nil> keys=2`

## Findings

### High

1. 整个 `R0` 的 Done 标准未达成，因为公开 RPC 注册链仍然是空的。

证据：

- `docs/plans/迁移/p5-execution-plan.md:141-147` 的 Done 标准要求：
  - 151 个方法全部完成注册
  - approval 状态机迁移完成
  - fx 图闭环，所有模块 `handler.Map` 自动注册
  - 不存在第二套手写注册链
- `internal/platform/rpc/module.go:28-39` 定义了 `group:"rpc_handlers"` 的 value-group 收集口。
- 全仓 LSP 搜索 `group:"rpc_handlers"` 仅命中 `internal/platform/rpc/module.go:31,39`。
- 全仓 LSP 搜索 `handler.Map{` 只命中空 map 初始化：
  - `internal/platform/rpc/registry.go:5-12`
  - `internal/platform/rpc/server.go:21-27`
  - `internal/platform/rpc/transport_ws.go:22-26`
- 全仓 LSP 搜索 `handler.Check(` 仅命中 `internal/platform/rpc/strict.go:12` 的工厂定义，没有任何公开方法调用点。

结论：

- `R0b/R0d` 的骨架已经存在。
- 但当前仓库里没有任何业务模块向 `rpc_handlers` 出值，也没有公开路由真正进入 `StrictHandler` / `ThreadScope` / `CapabilityGate` / `Logging` 新链路。
- 因此“151 个方法完成注册”“所有公开方法走严格绑定”都还不能成立。

2. `R0c` 的 approval 状态机仍未闭环，只是实现了一个孤立的 manager。

证据：

- `internal/platform/rpc/approval.go:74-152` 已实现 `RequestApproval` / `RequestUserInput` / `Respond` / `RestorePending`。
- `internal/platform/rpc/module.go:12-18` 已把 `NewApprovalManager` 注入 Fx。
- 但全仓 LSP 搜索下列 API 时，除定义外均无调用点：
  - `RequestApproval(`
  - `RequestUserInput(`
  - `Respond(`
  - `RestorePending(`
  - `AutoApprove(`
- `internal/platform/rpc/approval.go:16` 的 callback method 常量是 `tool/approval/request`；全仓没有对应 handler 或 route producer。
- `internal/contract/provider.go:47-50` 的 `ToolCallResponder` 只有 `RespondResult` / `RespondError`，没有 approval resolve 接口。
- `internal/sidecar/orch/orchestration/contract.go:10-17` 没有 `UserInputRequested` / `UserInputResolved` 一类状态推进接口。
- `internal/dto/agent/state.go:28-29,90-95` 虽然定义了 `user_input_requested` / `user_input_resolved` 触发器和 `awaiting_user_input -> turn_running` 转移，但全仓没有任何使用点。

结论：

- `R0c` 现在只能证明 approval manager 本身可编译。
- 不能证明 approval 结果能回流到 provider。
- 不能证明 `awaiting_user_input -> turn_running` 能被真正触发。
- 不能证明 pending restore、sub-agent auto-approve、typed respond 已进入主运行链。

3. 当前 `platform/rpc` 的业务错误码仍违反 `jrpc2` 契约。

证据：

- `docs/契约/jrpc2-convention.md:586-595` 明确要求业务错误码避开保留区间 `-32768..-32000`。
- `internal/platform/rpc/errors.go:5-10` 定义：
  - `CodeNotFound = -31001`
  - `CodeInvalidState = -31002`
  - `CodeConflict = -31003`
  - `CodeCapabilityGate = -31004`
  - `CodeApprovalTimeout = -31005`
- `internal/platform/rpc/handler.go:57-72` 的 `CapabilityGate` 直接返回 `CodeCapabilityGate`。
- `internal/platform/rpc/approval.go:137` / `internal/platform/rpc/approval_support.go:161-170` 会走 `ErrApprovalTimeout(...)`。

结论：

- 这不是单点问题，而是 `platform/rpc/errors.go` 的整体基线仍不符合迁移契约。
- 该问题同时影响 `R0c` 的 approval timeout surface 和 `R0d` 的 capability unsupported surface。

### Medium

4. `CapabilityGate` 的 resolver 形态对了，但 unsupported surface 仍不完整。

证据：

- `internal/platform/rpc/handler.go:57` 的签名已经是 `CapabilityGate(cap string, resolver CapabilityResolver)`。
- `internal/platform/rpc/handler.go:64-68` 的失败路径仅返回：
  - message: `capability not supported by active provider`
  - data: `{"capability": cap}`
- `go-agent-v2/internal/apiserver/methods.go:87-101` 的既有 unsupported 结果面是：
  - `{error:{code,provider,capability,reason}}`
- `docs/plans/迁移/p5-wave0-review-A.md:157-163,179-181` 要求 resolver 通过 assembly 注入，并把 unsupported surface 前置统一。

结论：

- resolver 参数已经补上，这是进展。
- 但当前 surface 缺 `provider` / `reason`，且错误码仍在保留区间。
- `R0d` 只能算“工厂形状正确，契约未完成”。

5. `R0c` 的任务预算 `<=250` 已失效。

证据：

- `docs/plans/迁移/p5-execution-plan.md:54` 将 `R0c` 定义为 `approval 状态机（≤250）`。
- `internal/platform/rpc/approval.go` 共 321 行。
- `internal/platform/rpc/approval_support.go` 共 256 行。
- 两个文件合计 577 行。

结论：

- 若按实现归属计入 `R0c`，当前规模明显超出原预算。
- 好的一面是：文件级硬约束仍然满足，`approval.go < 400`、`approval_support.go < 400`，且函数均小于 80 行。
- 坏的一面是：即便超预算，R0c 仍未补齐 provider/orchestration 端口与调用点，说明问题不是“仅仅拆小文件”。

6. approval/push 流程缺少本地测试，和执行计划的“先做 golden/contract”不一致。

证据：

- `go test ./internal/platform/rpc/... -count=1` 返回 `[no test files]`。
- 全仓 LSP 搜索 approval 相关 `*_test.go` 无命中。
- `docs/plans/迁移/p5-execution-plan.md:137` 要求：`push notify envelope、approval pending/resolve/reject 流程、workspace run 广播先做 golden/contract，再接 UI`。

结论：

- 当前 approval/push 只能靠静态审查与编译通过背书。
- 对并发、恢复、重复 requestId、recoverable callback error 等关键行为还没有自动化证明。

### Low

7. `ThreadScope(fields ...string)` 的自定义字段模式与报错文案不一致。

证据：

- `internal/platform/rpc/handler.go:30-33` 默认支持 `threadId` / `threadID` / `thread_id`。
- `internal/platform/rpc/handler.go:30-54` 允许调用方传入自定义字段列表。
- 失败路径固定返回 `threadId is required`。

结论：

- 默认三字段模式通过。
- 但自定义字段模式下，错误文案与真实接收字段可能不一致。

## 逐项判定

| 项目 | 结论 | 证据 |
| --- | --- | --- |
| `R0a/strict.go` | 通过 | `internal/platform/rpc/strict.go:11-17` 正确使用 `handler.Check(fn).AllowArray(false).SetStrict(true).Wrap()`。 |
| `R0a/codec.go` | 通过 | `internal/platform/rpc/codec.go:3-22` 只有 `PayloadEncoder` 与 `WrapSuccess` / `WrapError` 两个 helper，没有复制 V2 `server_payload.go` 的大块兼容逻辑。 |
| `R0a/transport_ws.go` | 通过 | `internal/platform/rpc/transport_ws.go:22-43` 用自定义 `wsChannel` 接入 `jrpc2.NewServer(...).Start(ch)`；`45-105` 完整实现 `channel.Channel`。 |
| `R0a/push.go` | 通过 | `internal/platform/rpc/push.go:25-47` 分别使用 `server.Notify` 与 `server.Callback`；`server.go:60-73` 强制 `AllowPush=true`。 |
| `R0b/module 接线` | 部分通过 | `internal/app/modules.go:20-35` 已接入 `turn/orchestration/unified/claudecli/codexapp`；`internal/archtest/fx_graph_test.go:11-15` 的 `fx.ValidateApp(app.Module)` 通过；但 `rpc_handlers` 仍无 producer。 |
| `R0c/approval manager` | 不通过 | manager 已存在并注入 Fx，但没有调用点、没有 route、没有 provider/orchestration 端口闭环。 |
| `R0d/middleware 工厂` | 部分通过 | `internal/platform/rpc/handler.go:14-27` 的 `Middleware` + `Wrap(...)` 设计正确；`ThreadScope` 三字段支持正确；`CapabilityGate` 仍未完成 surface 与错误码契约。 |

## 审查维度

| 维度 | 结论 | 证据 |
| --- | --- | --- |
| 1. 编译 | 通过 | `go build ./...` 通过。 |
| 2. 守卫 | 通过 | `go test ./internal/archtest/... -count=1 -timeout 120s` 通过；`internal/archtest/fx_graph_test.go:11-15` 证明 `fx.ValidateApp(app.Module)` 通过。 |
| 3. import 方向 | 通过 | 对 `internal/platform/rpc` 搜索 `internal/module/`、`internal/provider/` 无命中；`approval.go` / `approval_support.go` 也未反向依赖 module/provider。 |
| 4. 行数硬约束 | 通过 | `strict.go=23`、`codec.go=23`、`transport_ws.go=106`、`push.go=63`、`module.go=44`、`modules.go=39`、`handler.go=107`、`approval.go=321`、`approval_support.go=256`，均小于 400；函数均小于 80。 |
| 5. `jrpc2` 契约 | 部分通过 | 严格绑定、`Notify`/`Callback`、`AllowPush`、WS `channel.Channel` 适配都已落地；但业务错误码仍落在保留区间。 |
| 6. `ThreadScope` 多 field | 通过 | `internal/platform/rpc/handler.go:31-33` 默认支持 `threadId` / `threadID` / `thread_id`。 |
| 7. `CapabilityGate` | 不通过 | resolver 参数存在；但 unsupported surface 缺 `provider` / `reason`，错误码也不合约。 |
| 8. `fx` 闭环 | 部分通过 | 容器图可验证通过；空 `handler.Map` 启动安全；但自动注册闭环仍为空，没有业务 handler producer。 |
| 9. `codec` 薄度 | 通过 | `codec.go` 维持为 23 行薄 helper。 |
| 10. 工厂模式 | 通过（未接入） | `Wrap(mws ...Middleware)` 是可组合工厂；但当前没有实际公开路由消费。 |
| 11. approval 状态机 | 不通过 | manager 存在，但 provider resolve、状态恢复、restore 调用点、route exposure 都未闭环。 |
| 12. `R0` Done 标准 | 不通过 | 151 个方法未注册，approval 闭环未完成，`rpc_handlers` 自动注册链仍为空。 |

## 最终判断

当前 `R0` 更准确的状态是“基础设施预制完成一半”，不是“整轮完成”。

已经成立的部分：

- `StrictHandler`、`PushBridge`、`WS channel`、`codec helper`、`ApprovalManager`、`Middleware` 工厂都已落到当前树。
- Fx 图能通过校验。
- import 方向和规模控制没有失守。

尚未成立的部分：

- 公开 RPC 方法注册仍为空，Done 标准中的 151 方法注册不成立。
- `R0c` 没有把 approval 结果闭环到 provider 响应和 orchestration 状态恢复。
- `CapabilityGate` 与 approval timeout 的错误 surface 仍不符合 `jrpc2` 业务错误约束。
- approval/push 缺少 contract/golden 测试。

## 互辩：对 audit-B 的批判

以下互辩针对 `docs/plans/迁移/p5-wave0-audit-R0.md`，不推翻“整组 R0 仍未完成”的总体判断，只校正 B 的口径、证据强度和结论边界。

### 1. `R0c` 的预算口径：B 不是“用了已生效新口径之前的旧口径”，但行数统计有尾行口径差

- `audit-B` 在 `docs/plans/迁移/p5-wave0-audit-R0.md:109,227` 使用的是 `R0c <= 250`。
- 现有书面任务定义仍是 `docs/plans/迁移/p5-execution-plan.md:52-55`：
  - `R0c: approval 状态机（≤250）`
- 全仓 LSP 搜索未发现 `R0c <= 350` 或 `approval 状态机（≤350）` 的文档化修正。
- LSP 读取当前文件尾行：
  - `internal/platform/rpc/approval.go` 读取可见到第 `321` 行空行；最后有效代码落在第 `320` 行。
  - `internal/platform/rpc/approval_support.go` 读取可见到第 `256` 行空行；最后有效代码落在第 `255` 行。
- 因此：
  - 按最后有效代码行计：`320 + 255 = 575`
  - 按可见尾行计：`321 + 256 = 577`

判定：

- B 在“`575` 行”这个数字上采用的是“最后有效代码行”口径，存在 1-2 行的尾空行统计差，但不构成实质错误。
- 更关键的是：从仓内已落文档看，B 用 `<=250` 是在引用当前书面口径，不是使用了一个已被文档替换掉的旧口径。
- 即便外部互辩曾把目标口头放宽到 `<=350`，当前仓内未见该修正文档化；而且按 `575-577` 的实际体量，B 的“超预算”结论在 `<=350` 下依然成立。

### 2. `R0b 通过` 的标题偏宽，但 B 并没有忽略 `rpc_handlers` producer 为空这个事实

- `audit-B` 在 `docs/plans/迁移/p5-wave0-audit-R0.md:166` 明确写的是：
  - `R0b: fx 图闭环`
  - `结论：通过，但只是依赖图闭环，不是 RPC 路由闭环。`
- 同一节 `:187-191` 又明确写出：
  - 全仓 `group:"rpc_handlers"` 只命中 `internal/platform/rpc/module.go`
  - 当前没有任何业务模块向该 group 真正出值
- 这与当前代码证据一致：
  - `internal/platform/rpc/module.go:31,39` 是唯一的 `rpc_handlers` 命中

判定：

- B 在事实层没有漏掉“注册链是空的”这一点。
- B 的宽松之处不在事实，而在标题词汇。若按 `docs/plans/迁移/p5-execution-plan.md:53` 的窄任务名 `R0b: fx 图闭环（≤100）` 来看，给“通过”有依据。
- 但若把 `R0b` 放进整组 R0 的 Done 标准里看，`docs/plans/迁移/p5-execution-plan.md:145` 的“所有模块 `handler.Map` 自动注册”显然尚未达成，因此这个“通过”容易被读成过度乐观。
- 更严谨的写法应是：`R0b` 按 DI/Fx 图通过，按 RPC 注册闭环未完成。

### 3. B 对错误码的批判成立；仓内契约明文采用更宽的禁区，不是 `-32700..-32600`

- `docs/契约/jrpc2-convention.md:586-589` 原文写的是：
  - 业务层使用应用自定义 code
  - 必须避开保留区间 `-32768` 到 `-32000`
- 当前实现：
- `internal/platform/rpc/errors.go:5-10` 定义 `-31001..-31005`
  - `internal/platform/rpc/handler.go:65-67` 使用 `CodeCapabilityGate`
  - `internal/platform/rpc/approval.go:111,115,137`
  - `internal/platform/rpc/approval_support.go:138,145,164`

判定：

- 在“本仓迁移契约”这个裁判标准下，旧实现使用保留区间内业务码这一点明确违规。
- 因而 B 在这一点上不是事实错误。
- 若只按更窄的通用 JSON-RPC 标准错误集合去理解，可能会得到不同直觉；但仓内明确采用的是更宽的禁区，B 的批判应以仓内契约为准。

### 4. B 没有遗漏 `R0a` 的实质进展；它的问题是总述语气偏保守，不是事实失真

- `audit-B` 的 `R0a` 分节 `docs/plans/迁移/p5-wave0-audit-R0.md:141-162` 已明确列出完成项：
  - `internal/platform/rpc/server.go:60-73` 强制 `AllowPush=true`
  - `internal/platform/rpc/push.go:25-47` 提供 `NotifyClient` / `CallbackClient`
  - `internal/platform/rpc/transport_ws.go:22-105` 提供最小 WS adapter
  - `internal/platform/rpc/codec.go:1-22` 保持薄封装
  - 预算 `263` 行，在 `<=500` 内
- 同一节 `:159-162` 的判断原文已经区分了两层：
  - 作为“基础设施组件已写出”，`R0a` 成立
  - 作为“push/WS 已形成可验证运行链”，证据不足

判定：

- B 没有把“基础设施就绪”和“运行时闭环”混成一个结论。
- 它在 `R0a` 分节里的结论本身是分层的，事实层是成立的。
- 真正可批判的地方只是总述 `docs/plans/迁移/p5-wave0-audit-R0.md:85-102,267-289` 把 `R0a/R0c/R0d` 统一归到“定义存在、运行时未消费”，会让整篇报告的调性比其分项结论更保守。

### 5. B 对 `R0d <= 80` 的“超预算”判断缺少预算语义证成；它证明了结果体量大，却没有证明应按最终文件体量计

- 书面任务定义只有 `docs/plans/迁移/p5-execution-plan.md:55`：
  - `R0d: ThreadScope 多 field + StrictBind 兼容（≤80）`
- 该处没有说明 `<=80` 是：
  - 最终 touched-file 总行数
  - 还是增量修改行数
- 设计审查 `docs/plans/迁移/p5-wave0-review-A.md:135` 明确写着：
  - `当前实现 internal/platform/rpc/handler.go:27-45 只能接收单字段名`
  - 说明 `handler.go` 是既有文件上的增强，不是纯新建
- 当前 LSP 读取的文件体量是：
  - `internal/platform/rpc/strict.go` 可见到第 `23` 行空行，最后有效代码在第 `22` 行
  - `internal/platform/rpc/handler.go` 可见到第 `107` 行空行，最后有效代码在第 `106` 行
- `audit-B` 在 `docs/plans/迁移/p5-wave0-audit-R0.md:115` 使用的是最终文件体量口径：
  - `strict.go + handler.go = 128`

判定：

- 若按“最终 touched-file 总量”解释，B 的算术没有问题，`128` 这个量级成立。
- 但 B 没有证明这就是 `R0d <= 80` 的正确预算语义。
- 由于 `handler.go` 是既有文件增强项，`R0d` 是否超预算，不能仅凭最终文件总长就下硬结论；至少应说明为何这里不是按增量修改量计。
- 因此，B 在 `R0d` 预算问题上更准确的说法应是：
  - `若按最终 touched-file 总量计，当前超出 80`
  - `若按增量修改计，现有文档证据不足以直接判定超标`

## 互辩结论

对 `audit-B` 的最挑剔结论如下：

- B 关于 `R0c` 实际体量过大的判断成立，但“用了旧口径”这一指控不能成立，因为仓内书面口径仍是 `<=250`，且未见 `<=350` 的文档化修正。
- B 关于 `R0b` 的事实陈述基本准确，没有忽略 `rpc_handlers` producer 为空；它的问题是“通过”这个标题容易让人误读为 RPC 注册闭环已成立。
- B 关于错误码保留区间的批判成立，仓内契约明确禁止 `-32768..-32000` 整段范围内的业务码。
- B 并没有遗漏 `R0a` 的基础设施进展；在 `R0a` 分节里，它已经承认“组件已写出”。这一点不应再被反批成事实错误。
- B 最值得挑剔的地方是 `R0d <= 80` 的预算论证：它把最终文件体量当成默认预算基线，但没有证明这就是修改型任务的正确解释。
