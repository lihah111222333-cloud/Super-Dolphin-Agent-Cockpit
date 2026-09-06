# Terminal event bridge candidate（2026-09-04）

Status: Active / `LOCAL_NON_AUTHORITATIVE` / `NOT_VERIFIED` / `NO-GO` for production

`terminaleventbridge.Bridge` 只接收完整 provider binding 的 terminal observation，并要求
`runtimeeventwriter.BoundIngress` 与 `BindingVerifierTx`；缺少事务校验器、非原子 ingress、坏
context、late generation、source gap 或不完整 event metadata 均 fail-fast。Bridge 不从 DTO、
message id、session 名称或当前进程状态推导身份。

提交顺序固定为：

```text
validate observation
  -> same-transaction binding verification
  -> runtimeeventwriter.AppendBound
  -> canonical EventStore materialization
  -> ValidateMaterializedResult
```

只有完整 materialized receipt/event/frame/cursor/source watermark 结果才允许返回；缺失、冲突
或 commit-unknown 一律映射为 non-authorizing unknown。Bridge 不启动 provider、不发送通知、不
写 terminal outcome 或 UI/report projection，也未注册 Fx、HTTP、MCP tool 或 public registry。

同包合同覆盖 atomic verifier-before-append、late-generation/gap 分类、伪造 materialized
receipt 拒绝、bounded field/UTC 校验和 non-atomic/context-only dependency 拒绝。本轮未重跑
这些测试；它们只定位本机 candidate 行为，不能证明 provider session-incarnation receipt、
跨进程 recovery、terminal projection 或发布资格。
