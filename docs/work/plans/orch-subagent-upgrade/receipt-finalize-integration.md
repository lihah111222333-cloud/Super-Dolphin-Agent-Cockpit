# Receipt/Finalize 正式接线设计（基于当前源码）

## 事实与边界
`standalone.Execution.Launch` 先 reserve session，再按 provider 调 `launchCodex`/`launchClaude`，成功后才 `persistSession`；失败路径调用 `stopSession`，且取消请求上下文也必须回收进程。session 的 `activated` 只在 provider 返回可用身份并绑定 exit fence 后置位。`CaptureActiveProcessReceipt` 要求 session 已 activated 且 ready，读取当前 PID，调用注入的 `ProcessReceiptVerifier.Capture`，写入 PID/start-token/executable identity、threadID、launchSeq，并通过 `FinalizePreparedReceiptDigest`；它不启动、重启或终止进程。

`ProcessReceiptVerifier` 是只读端口：生产必须由 `NewProcessReceiptVerifier(ExecutableDigestVerifier)` 构造（缺摘要验证器直接 `ErrProductionNoGo`），`VerifyPrepared` 对同一 PID 做前后两次稳定身份 capture，并校验可执行文件摘要。Windows capture 当前明确 unsupported。`launchworkflow.Workflow` 只做 intent→reservation→incarnation→binding preparation；`Finalize` 要求外部 identity ref、active-process-receipt ref、identity digest 全部非空且与 incarnation 匹配，然后调用 identity repository 的 CAS finalize；绝不推断 active receipt。

## 接线裁决
* **Fx 边界/依赖注入**：在 `cmd/mcp-orch` standalone composition root 构造唯一生产 `ExecutableDigestVerifier` 与 `managedruntime.NewProcessReceiptVerifier`，把 verifier 作为显式参数注入 orchestration service（不得在 `Workflow` 内部 new，也不得用 PID 存活探测代替）。同一 Fx graph 提供 `*standalone.Execution` 与 `*launchworkflow.Workflow`；Execution 仍只拥有 CLI 句柄，Workflow 仍只拥有 identity owner。
* **真实启动后的时序**：先 `Workflow.Run` 得到 preparation；再 `Execution.Launch`。仅当 Launch 返回成功且 session 已 ready/activated 时调用 `CaptureActiveProcessReceipt`；随后以 receipt 的 canonical digest/ref 调 `Workflow.Finalize`。Launch 返回成功本身不能制造 active；capture 或 digest 失败必须保持 preparation 未 finalized，并将 outcome 标记 unknown。
* **active receipt 字段及来源**：PID、ProcessStartToken、ExecutableIdentity 来自 verifier 的内核 capture；ProviderThreadID 来自 session.threadID（Claude 为 session id，Codex 为 app-server thread id）；RuntimeGeneration/ProcessGeneration 来自 session.launchSeq；Binding/identity digest 来自 preparation/incarnation。`activeProcessReceiptRef` 必须指向持久化的完整 receipt（非 message_id、PID 字符串或路径）。
* **失败/unknown**：启动前校验失败是明确 failure；provider 已产生外部副作用但未达到 activated、capture 超时/unsupported、摘要或二次 capture 不一致均为 unknown（禁止自动重试同一 AgentID/生成号、禁止伪造 active）。只有 repository CAS 冲突可按其幂等规则读取并返回已相同终态；冲突终态不得覆盖。
* **重试/幂等**：以 command/idempotency key、reservation/preparation generation 和 launchSeq fencing；重试必须新 incarnation/代际。`Finalize` 重放仅允许相同 binding digest/receipt ref 的幂等成功；不同 receipt 或 digest 直接拒绝。
* **shutdown/revoke**：Finalize 前任何失败走 owner 的 `stopSession`，即使 context 已取消；停止后只有 activated 且 settled 才 reclaim AgentID，否则保留 tombstone。已 finalized 的 revoke/关闭必须走 identity owner 的显式撤销/CAS，再停止 CLI；不得用进程退出事件反推 active receipt。
* **Codex/Claude 差异**：Codex 通过 app-server handshake 后获得 thread；Claude 通过 `system/init` 与 attach-pending fence 获得 session id。两者都必须等 activated+ready 后 capture；receipt 的 ProviderThreadID 按各自真实 ID 写入，不能把 Claude session id 当 Codex thread 或反之。Windows 两者均受 capture unsupported 约束，必须 fail-closed/unknown。

## 必需 E2E
1. 真实 Codex CLI：Run→Launch→ready→capture→VerifyPrepared（双 capture+摘要）→Finalize，断言持久 receipt 字段与 identity owner CAS。
2. 真实 Claude CLI：覆盖 init/attach-pending 清除后同一链路。
3. provider 启动成功但 capture 失败/进程竞态重启：断言不 finalize、不伪造 active、保留 tombstone 并可安全 stop。
4. context cancel、shutdown/revoke 与重复 Finalize：断言进程回收、generation fencing、相同请求幂等、冲突请求拒绝。
5. Windows/不支持 capture：断言 `ErrProcessReceiptCaptureUnsupported` 或 unknown 投影而非成功。
6. Fx graph 级测试：缺 `ExecutableDigestVerifier` 或 verifier 未注入时 composition 失败（`ErrProductionNoGo`），禁止测试替身进入生产路径。
