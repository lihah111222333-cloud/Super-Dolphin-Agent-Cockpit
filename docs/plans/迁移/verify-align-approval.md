# approval 对齐修复核验

基线：
- 读取 `docs/plans/迁移/align-approval.md`
- 只用 LSP 取证当前仓库实现

## 总表

| 项目 | 结论 | 取证 |
| --- | --- | --- |
| requestId 空值折叠是否修复 | ✅ | `internal/platform/rpc/approval.go:134-139` 在 `RequestID == nil` 或 `<= 0` 时分配新的 `nextRequestID`，随后用新值生成 pending key；`internal/platform/rpc/approval_test.go:9-30` 也覆盖了“同 `callID`、无 `requestID`”不再折叠的场景。 |
| dispatcher 锁外写竞态是否修复 | ✅ | `internal/platform/rpc/approval.go:131-156` 在持锁的 `registerPending` 内把 `dispatcher` 写入 `pending` 结构体并完成注册；当前读取点在 `internal/platform/rpc/approval_events.go:34-42`，未再看到后续锁外补写 `pending.dispatcher` 的路径。 |
| RestorePending 是否在新连接触发 | ✅ | `internal/platform/rpc/module.go:79-86` 注册了 `server.OnConnect(...)`，连接到来时直接调 `approvals.RestorePending(...)`；`internal/platform/rpc/server.go:115-117` 在连接建立后调用 `notifyConnected`，`internal/platform/rpc/server.go:157-160` 会执行该 hook。 |
| Cleanup 超时是否从 Nanosecond 改为合理值 | ✅ | 当前停机清理路径在 `internal/platform/rpc/module.go:107-117`，grace 已是 `5 * time.Second`，并传给 `approvals.Cleanup(grace)`；`internal/platform/rpc/approval_lifecycle.go:10-20` 按传入 timeout 做 cutoff，不再是 `time.Nanosecond`。 |
| callback method V2 兼容是否修复 | ⚠️ | `internal/platform/rpc/approval_events.go:45-55,57-74` 已兼容 legacy/V2 method family：保留 `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`skill/requestApproval`，并把 `tool.approval.requested` / `request_user_input` 族归一到 V2 command-exec callback；但 `internal/platform/rpc/approval_events.go:54` 默认仍回落到 `approval/request`，不是 V2 家族 method。 |
| respond snake_case 兼容层是否存在 | ✅ | `internal/module/turn/rpc_types.go:112-149` 以 `call_id` / `request_id` 为主字段，并在 `UnmarshalJSON` 里兼容 `callId` / `requestId`；`internal/module/turn/rpc_helpers.go:164-175` 的 `approval/respond` handler 直接消费该结构并调用 `approver.Respond(...)`。 |

## 逐项说明

### 1. requestId 空值折叠

结论：`✅`

证据：
- `internal/platform/rpc/approval.go:134-139`
- `internal/platform/rpc/approval_test.go:9-30`

判断：
- 旧问题的根因是空 `requestID` 参与 key 生成时容易把多个 pending 折到同一个键。
- 当前实现会先补唯一 `requestID`，再生成 `pendingStorageKey`，并且已有单测验证“重复 `callID` + 空 `requestID`”不会再拿到同一条 pending。

### 2. dispatcher 锁外写竞态

结论：`✅`

证据：
- `internal/platform/rpc/approval.go:131-156`
- `internal/platform/rpc/approval_events.go:34-42`
- `internal/platform/rpc/approval_test.go:32-43`

判断：
- `dispatcher` 现在作为 `registerPending(...)` 的入参，在持锁区内随 `pending` 一次性初始化。
- 当前代码里能命中的 `pending.dispatcher` 赋值点就是 `internal/platform/rpc/approval.go:151`，未见“注册后再补写 dispatcher”的旧窗口。

### 3. RestorePending 新连接触发

结论：`✅`

证据：
- `internal/platform/rpc/module.go:79-86`
- `internal/platform/rpc/server.go:115-117`
- `internal/platform/rpc/server.go:148-160`
- `internal/platform/rpc/server.go:180-190`

判断：
- `bindApprovalLifecycle(...)` 不再只在启动时扫 `snapshotActive()`。
- 现在每次新 RPC 连接建立后，`notifyConnected(...)` 都会触发 `OnConnect` hook，从而执行 `RestorePending(...)`。
- `OnConnect(...)` 还会回放已存在 active server，因此“已有连接 + 后注册 hook”也能补发。

### 4. Cleanup 超时值

结论：`✅`

证据：
- `internal/platform/rpc/module.go:107-117`
- `internal/platform/rpc/approval_lifecycle.go:10-20`

判断：
- 目前停机清理用的是 `5 * time.Second` grace，而不是一上来就用 `time.Nanosecond` 把所有 pending 立即打成超时。
- 这项修复成立。
- 但它仍然只是 stop-path cleanup，不等于 `align-approval.md` 里提到的 V2 `per-request 5 min TTL` 语义回归。

### 5. callback method V2 兼容

结论：`⚠️`

证据：
- `internal/platform/rpc/approval_events.go:45-55`
- `internal/platform/rpc/approval_events.go:57-74`

判断：
- 兼容面已明显扩大：`legacy tool/approval/request`、`tool.approval.requested`、`request_user_input` 族都做了归一化，`fileChange` / `skill` V2 family 也已纳入。
- 但默认 fallback 仍是 `approval/request`，不是 V2 的 `item/.../requestApproval` / `skill/requestApproval` 家族。
- 所以更准确的判断是“V2 family 兼容已补齐大部分，但还不是无条件 1:1 默认对齐”。

### 6. respond snake_case 兼容层

结论：`✅`

证据：
- `internal/module/turn/rpc_types.go:112-149`
- `internal/module/turn/rpc_helpers.go:164-175`

判断：
- `approval/respond` 入参当前原生接受 snake_case 字段名，同时保留 camelCase 兼容入口。
- 这一项作为 transport ingress 兼容层已经落地。
- 仅从当前核验项来看，可以判定为已修复。
