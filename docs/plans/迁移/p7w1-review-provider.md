# P7 波次 1 互审（Provider 视角）

范围：
- D1+D2+D4：approval-turn Agent
- D5+D11+D12：dto-store Agent

方法：
- 仅基于 LSP `text_search` / `references` / `read_file` / `call_hierarchy` 结果
- 只写 provider 视角能证实的问题，不做猜测

## 一、对 D1+D2+D4 的批判

### 1. “pending kept for restore” 仍是死路径，恢复并没有真正接线

证据：
- `internal/platform/rpc/approval.go:207-218` 在 recoverable callback error 时明确记录 “pending request kept for restore”。
- 但 `internal/platform/rpc/approval_lifecycle.go:23-34` 的 `RestorePending`、`internal/platform/rpc/approval_lifecycle.go:10-21` 的 `Cleanup`、`internal/platform/rpc/approval_lifecycle.go:36-43` 的 `PendingSnapshot`，LSP `references` 都是 0 命中。

问题：
- 这意味着 callback 中断后，pending approval 只是在内存里留下来，并没有实际 restore/catch-up 入口。
- 从 provider 侧看，`codexapp/session_approval.go:31-35` 还会继续等待 `ApprovalManager.RequestApproval` 的结果；一旦 callback 中断，这条链路只能等外部 `approval/respond` 或上层超时，不能靠 restore 自愈。

结论：
- D1 只补了索引结构，没补完整生命周期；“可恢复 pending approval” 目前是半接线状态。

### 2. callback method family 仍然不完整，V2 的 file-change / skill 分支没有迁进来

证据：
- 当前 V3 只有 `internal/platform/rpc/approval_events.go:14-17` 定义：
  - `approval/request`
  - `item/commandExecution/requestApproval`
  - legacy `tool/approval/request`
  - legacy `tool.approval.requested`
- 对 `internal` 做 LSP `text_search`：
  - `item/fileChange/requestApproval`：0 命中
  - `skill/requestApproval`：0 命中
- 对 V2 做 LSP `text_search`，`/Users/mima0000/Desktop/wj/go-agent-v2/internal/apiserver/server_approval.go:14-17` 明确还有：
  - `item/fileChange/requestApproval`
  - `skill/requestApproval`

问题：
- 如果这轮宣称“callback method V2 不兼容”已修，那这个结论站不住。
- 当前 `callbackMethod` 在 `internal/platform/rpc/approval_events.go:42-52` 最多只能归一到 command-execution 或默认 `approval/request`，方法族仍然缩水。

结论：
- D4 只修了一条 method family，不是完整兼容修复。

### 3. `request_user_input` 仍然没有真正桥接成可消费的统一流程

证据：
- `internal/platform/rpc/approval.go:101-106` 的 `RequestUserInput` 只是把 `Kind` 改成 `request_user_input`，然后直接复用 `RequestApproval`。
- `internal/platform/rpc/approval_events.go:42-52` 只是根据 `Kind`/method 选 callback method。
- 对当前仓库做 LSP `text_search`：
  - `RequestUserInput(`：只命中 `internal/platform/rpc/approval.go`
  - `questions`：0 非文档命中
  - `request_user_input`：非文档代码只命中 `approval.go` / `approval_events.go`

问题：
- 现在没有任何 V3 代码定义或消费 `questions/options` 这类 request-user-input 载荷。
- `callbackParams` 在 `internal/platform/rpc/approval_events.go:73-94` 只是 clone 原始 `Payload` 再拼通用字段，仓内也没有对应 producer。
- 这和 “request_user_input 桥接” 不是一回事，最多只是给 future payload 留了一个通道。

结论：
- D4 的 `request_user_input` 仍停留在 method 归一层，没有形成可用的业务闭环。

### 4. 默认 callback method 仍然没有仓内 consumer，端到端可用性没有被证明

证据：
- `internal/platform/rpc/approval_events.go:14` 默认 method 是 `approval/request`。
- `internal/platform/rpc/push.go:42-58` 的 `PushBridge.CallbackClient` 会直接 `server.Callback(ctx, method, params)`。
- 对整个仓库做 LSP `text_search`，`approval/request` 只有常量定义和文档命中，没有任何当前仓内 receiver/handler 实现。

问题：
- 这意味着默认路径是否可用，完全依赖仓外 client；当前仓内没有任何自证。
- 对 provider 侧这很关键，因为 approval callback 一旦失败，当前 restore 又没接线，问题会直接回落到 pending 卡住。

结论：
- “callback method 已修复” 这个结论缺乏仓内闭环证据。

## 二、对 D5+D11+D12 的批判

### 1. “统一 store 错误” 只覆盖了 3 个 store，远不到统一

证据：
- LSP `references` 到 `internal/platform/db/errors.go:47-61` 的 `WrapStoreError`，只有：
  - `internal/store/binding/store.go:104-106`
  - `internal/store/thread/store.go:145-147`
  - `internal/store/workspace/store.go:152-154`
- 反例：
  - `internal/store/commandcard/store.go:16-28,57-80` 仍直接 `return err`
  - `internal/store/sharedfile/store.go:17-56` 仍直接 `return err`

问题：
- 这不是“统一 store 错误”，只是对少数 store 补了 wrapper。
- 上层代码仍会同时面对 `*db.StoreError` 和原始 `pgx/sqlc` 错误，错误域没有真正收敛。

结论：
- D5 的目标被高估了，现状更像 “局部包裹”，不是统一治理。

### 2. store error 包装对 `codexapp` session/recovery 基本没有实际收益

证据：
- `internal/provider/codexapp/transport.go:243-258` 在 RPC error 返回时，统一降格成 `fmt.Errorf("rpc error %d: %s", ...)`。
- `internal/provider/codexapp/session_recovery.go:54-63` 的 `shouldReconnect` 直接把所有 `"rpc error "` 前缀视为不可重连错误。

问题：
- 即使 thread/binding/workspace store 现在返回了 `*db.StoreError`，跨 RPC 之后 provider 侧也只剩字符串。
- 从 provider 视角，D5 并没有让 `codexapp` session/recovery 获得更好的错误判别能力。
- 换句话说，store error 包装目前只在 server 进程内有意义，对 provider 客户端层几乎无效。

结论：
- 如果目标之一是改善 provider 侧恢复行为，D5 现在没有形成有效收益。

### 3. snake_case 改造仍然不完整，DTO 层还在继续输出 camelCase

证据：
- `internal/dto/provider/session.go:5-18` 仍有 `agentId` / `threadId`
- `internal/dto/provider/turn.go:9-50` 仍有 `localId` / `threadId` / `manualSkillSelection` / `outputSchema` / `providerId` / `newThreadId`
- `internal/dto/turn/model.go:11-19` 仍有 `agentId` / `threadId` / `expectedTurnId` / `selectedSkills` / `manualSkillSelection` / `outputSchema`

问题：
- D11 如果是“lowerCamelCase vs snake_case 全量治理”，那现在显然没完成。
- 这会直接反噬 provider 兼容层：`internal/provider/codexapp/event_map.go:150-173` 还得继续同时兼容 `threadId` 和 `thread_id`。
- 也就是说，DTO 组并没有把 wire shape 统一掉，provider translator 只能继续双栈解析。

结论：
- D11 当前状态仍是 mixed contract，不是统一 contract。

### 4. EventHeader 重构没有把 provider/event translator 的重复构造消掉

证据：
- 对 `internal/dto/shared/event.go:42-44` 的 `EventHeader` 做 LSP `references`，仍然能看到手工构造点：
  - `internal/provider/claudecli/event_map.go:105-124`
  - `internal/provider/codexapp/event_map.go:150-173`
  - `internal/platform/rpc/approval_events.go:96-114`
  - `internal/sidecar/orch/orchestration/events.go:73-81`
- 这些位置都还在手写 `EventHeader -> ThreadHeader -> AgentHeader -> TurnHeader` 的嵌套字面量。

问题：
- 从 provider 视角，这意味着 D12 并没有降低 translator 的耦合度。
- 目前 `EventHeader` 只有 `Timestamp`，所以不会立刻编译炸；但一旦 header 再加字段，这些 translator/emitters 全都要手补。
- 这也解释了为什么 provider 侧仍然要手工决定时间来源：
  - `internal/provider/codexapp/event_map.go:155` 用 `eventTime(payload)`
  - `internal/provider/claudecli/event_map.go:109` 用 `time.Now()`
  - `internal/platform/rpc/approval_events.go:102` 也用 `time.Now()`

结论：
- D12 没有真正把 header 构造抽象起来，只是把类型定义整理了一遍。

## 三、Provider 侧结论

### 对 provider event_map 的影响

结论：
- D12 对 provider event_map 没有造成编译层面的直接破坏。
- 但它也没有真正减轻 provider translator 的重复 header 组装负担；`claudecli/event_map.go` 和 `codexapp/event_map.go` 仍然是手工拼 header。
- D11 未完成导致 `codexapp/event_map.go:151` 这种双 key 解析继续存在，provider 侧并没有因为 DTO 重构而变简单。

### 对 codexapp session/recovery 的影响

结论：
- D5 的 store error wrapper 对 `codexapp` recovery 几乎没有直接影响。
- 原因不是 store error 不重要，而是 provider transport 在 `internal/provider/codexapp/transport.go:253-255` 已经把远端错误压成普通 RPC 字符串，`internal/provider/codexapp/session_recovery.go:54-63` 也只按字符串做 recoverability 判断。
- 因此，若要让 store error 真影响 recovery，必须同步改 transport error model，而不是只改 store 包装。
